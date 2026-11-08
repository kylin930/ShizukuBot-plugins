package hitokoto

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hitokoto"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

// memState 是测试用的内存状态。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func run(t *testing.T, state sdk.State, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{State: state, Event: sdk.Event{GroupID: "g1", UserID: "u1", Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func reply(t *testing.T, message string) []sdk.Reply { return run(t, nil, message) }

func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	old := apiURL
	apiURL = server.URL
	t.Cleanup(func() {
		server.Close()
		apiURL = old
	})
}

func TestHitokoto(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hitokoto":"人生如逆旅","from":"苏轼","from_who":"苏轼"}`))
	})

	got := reply(t, "一言")
	if len(got) != 1 {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "人生如逆旅") || !strings.Contains(got[0].Text, "苏轼《苏轼》") {
		t.Fatalf("reply = %q", got[0].Text)
	}
	if got[0].Kind != "keyboard" {
		t.Fatalf("kind = %q, want keyboard", got[0].Kind)
	}
	if !strings.Contains(got[0].Keyboard, `"data":"一言"`) {
		t.Fatalf("keyboard %q missing command 一言", got[0].Keyboard)
	}
}

func TestHitokotoBatchTen(t *testing.T) {
	var calls int32
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"hitokoto":"句子%d","from":"出处%d"}`, n, n)))
	})

	got := reply(t, "一言")
	if len(got) != 10 {
		t.Fatalf("replies = %d, want 10", len(got))
	}
	if got[0].Kind != "keyboard" {
		t.Fatalf("first kind = %q, want keyboard", got[0].Kind)
	}
	for i, r := range got {
		if !strings.Contains(r.Text, fmt.Sprintf("句子%d", i+1)) {
			t.Fatalf("reply %d = %q", i, r.Text)
		}
	}
}

func TestCategoryFilter(t *testing.T) {
	var gotCode atomic.Value
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotCode.Store(r.URL.Query().Get("c"))
		_, _ = w.Write([]byte(`{"hitokoto":"床前明月光","from":"李白"}`))
	})

	got := reply(t, "一言 诗词")
	if gotCode.Load() != "i" {
		t.Fatalf("c = %v, want i", gotCode.Load())
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "床前明月光") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestKeywordFilter(t *testing.T) {
	var calls int32
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("c") != "" {
			t.Errorf("unexpected category: %s", r.URL.RawQuery)
		}
		if atomic.AddInt32(&calls, 1) == 2 {
			_, _ = w.Write([]byte(`{"hitokoto":"人生如逆旅","from":"苏轼"}`))
			return
		}
		_, _ = w.Write([]byte(`{"hitokoto":"今天天气不错","from":"佚名"}`))
	})

	got := reply(t, "一言 苏轼")
	if len(got) != 1 || !strings.Contains(got[0].Text, "人生如逆旅") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestKeywordNoMatch(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hitokoto":"今天天气不错","from":"佚名"}`))
	})

	got := reply(t, "一言 不存在的词")
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有找到相关的一言") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestSeriesSelectTen(t *testing.T) {
	var calls int32
	var gotCodes atomic.Value
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotCodes.Store(r.URL.Query().Get("c"))
		n := atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"hitokoto":"句子` + string(rune('0'+n%10)) + `","from":"出处"}`))
	})

	state := memState{}
	oldNow := nowFunc
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = oldNow }()

	got := run(t, state, "系列一言")
	if len(got) != 1 || !strings.Contains(got[0].Text, "请输入系列一言序号") ||
		!strings.Contains(got[0].Text, "0. 动画") || !strings.Contains(got[0].Text, "2. 游戏") {
		t.Fatalf("list replies = %+v", got)
	}

	got = run(t, state, "2")
	if gotCodes.Load() != "c" {
		t.Fatalf("c = %v, want c", gotCodes.Load())
	}
	if len(got) != 11 || !strings.Contains(got[0].Text, "请欣赏系列一言: 游戏") {
		t.Fatalf("selection replies = %d %+v", len(got), got)
	}
}

func TestSeriesExpired(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hitokoto":"句子","from":"出处"}`))
	})
	state := memState{}
	oldNow := nowFunc
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = oldNow }()

	if got := run(t, state, "系列一言"); len(got) != 1 {
		t.Fatalf("list replies = %+v", got)
	}
	nowFunc = func() time.Time { return base.Add(seriesTimeout + time.Second) }
	got := run(t, state, "0")
	if len(got) != 1 || !strings.Contains(got[0].Text, "系列一言指令过期") {
		t.Fatalf("expired replies = %+v", got)
	}
}

func TestSeriesInvalidInput(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hitokoto":"句子","from":"出处"}`))
	})
	state := memState{}
	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Unix(1700000000, 0) }
	defer func() { nowFunc = oldNow }()

	if got := run(t, state, "系列一言"); len(got) != 1 {
		t.Fatalf("list replies = %+v", got)
	}
	if got := run(t, state, "99"); len(got) != 1 || !strings.Contains(got[0].Text, "序号非法!") {
		t.Fatalf("invalid index replies = %+v", got)
	}
	if got := run(t, state, "abc"); len(got) != 1 || !strings.Contains(got[0].Text, "请输入数字!") {
		t.Fatalf("non-number replies = %+v", got)
	}
}

func TestEmptyMessage(t *testing.T) {
	if got := reply(t, "今天天气不错"); len(got) != 0 {
		t.Fatalf("unexpected reply %+v", got)
	}
}

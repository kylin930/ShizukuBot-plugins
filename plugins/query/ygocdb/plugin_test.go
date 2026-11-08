package ygocdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	for _, name := range []string{"ygocdb"} {
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

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, message string) capture { return runWith(t, nil, message) }

func runWith(t *testing.T, state sdk.State, message string) capture {
	t.Helper()
	var got capture
	c := sdk.Call{State: state, Event: sdk.Event{GroupID: "g1", UserID: "u1", Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			switch r.Kind {
			case "image":
				got.images = append(got.images, r.URL)
			default:
				got.texts = append(got.texts, r.Text)
			}
			return nil
		}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// manyCardsJSON 生成 n 张卡片的搜索结果。
func manyCardsJSON(n int) string {
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"cn_name":"卡%d","jp_name":"","en_name":"Card %d","text":{"types":"龙族/通常","pdesc":"","desc":"描述%d"}}`, 1000+i, i, i, i))
	}
	return `{"result":[` + strings.Join(items, ",") + `]}`
}

func stub(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "青眼" {
			t.Errorf("search = %q", r.URL.Query().Get("search"))
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	oldURL, oldBase := searchURL, imageBase
	searchURL = server.URL + "/api/v0/?search="
	imageBase = "https://img.example/"
	t.Cleanup(func() { searchURL, imageBase = oldURL, oldBase })
}

const cardJSON = `{"result":[{"id":89631139,"cn_name":"青眼白龙","jp_name":"","en_name":"Blue-Eyes White Dragon","text":{"types":"龙族/通常","pdesc":"","desc":"以高攻击力著称的传说之龙。"}}]}`

func TestSearchText(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/yds 青眼")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "青眼白龙") ||
		!strings.Contains(got.texts[0], "卡片密码：89631139") ||
		!strings.Contains(got.texts[0], "Blue-Eyes White Dragon") {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 0 {
		t.Fatalf("images = %q", got.images)
	}
}

func TestSearchImage(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/ydp青眼")
	if len(got.images) != 1 || got.images[0] != "https://img.example/89631139.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestSearchBoth(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/ydb 青眼")
	if len(got.images) != 1 || len(got.texts) != 1 {
		t.Fatalf("images = %q, texts = %q", got.images, got.texts)
	}
}

func TestEmptyKeyword(t *testing.T) {
	got := run(t, "/ydp")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "空手假象") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestNoResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	t.Cleanup(server.Close)
	old := searchURL
	searchURL = server.URL + "/api/v0/?search="
	t.Cleanup(func() { searchURL = old })

	got := run(t, "/yds 青眼")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "没有找到") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "青眼白龙"); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

func TestMultiResultListAndSelect(t *testing.T) {
	stub(t, manyCardsJSON(15))
	state := memState{}

	got := runWith(t, state, "/yds 青眼")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "找到15张相关卡片") ||
		!strings.Contains(got.texts[0], "0.卡0") || !strings.Contains(got.texts[0], "9.卡9") ||
		strings.Contains(got.texts[0], "10.卡10") {
		t.Fatalf("list texts = %q", got.texts)
	}
	if !strings.Contains(got.texts[0], "取消") || !strings.Contains(got.texts[0], "下一页") {
		t.Fatalf("list missing hints: %q", got.texts[0])
	}

	// 第一页之外的序号应提示错误并重置计时。
	got = runWith(t, state, "12")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "请输入正确的序号") {
		t.Fatalf("invalid index texts = %q", got.texts)
	}

	got = runWith(t, state, "下一页")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "10.卡10") ||
		!strings.Contains(got.texts[0], "14.卡14") || strings.Contains(got.texts[0], "0.卡0") {
		t.Fatalf("page 2 texts = %q", got.texts)
	}

	got = runWith(t, state, "12")
	if len(got.images) != 1 || got.images[0] != imageBase+"1012.jpg" {
		t.Fatalf("select images = %q", got.images)
	}
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "卡12") {
		t.Fatalf("select texts = %q", got.texts)
	}
}

func TestNextPageWrap(t *testing.T) {
	stub(t, manyCardsJSON(15))
	state := memState{}
	if got := runWith(t, state, "/ydb 青眼"); len(got.texts) != 1 {
		t.Fatalf("list texts = %q", got.texts)
	}
	if got := runWith(t, state, "下一页"); len(got.texts) != 1 {
		t.Fatalf("page 2 texts = %q", got.texts)
	}
	got := runWith(t, state, "下一页")
	if len(got.texts) != 2 || !strings.Contains(got.texts[0], "已是最后一页，返回到第一页") ||
		!strings.Contains(got.texts[1], "0.卡0") {
		t.Fatalf("wrap texts = %q", got.texts)
	}
}

func TestMultiResultCancel(t *testing.T) {
	stub(t, manyCardsJSON(11))
	state := memState{}
	runWith(t, state, "/yds 青眼")

	got := runWith(t, state, "取消")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "用户取消,搜索结束") {
		t.Fatalf("cancel texts = %q", got.texts)
	}
	if got := runWith(t, state, "0"); len(got.texts) != 0 && len(got.images) != 0 {
		t.Fatalf("state not cleared: %+v", got)
	}
}

func TestMultiResultTimeout(t *testing.T) {
	stub(t, manyCardsJSON(11))
	state := memState{}
	oldNow := nowFunc
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = oldNow }()

	runWith(t, state, "/yds 青眼")
	nowFunc = func() time.Time { return base.Add(pendingTimeout + time.Second) }
	got := runWith(t, state, "0")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "等待超时,搜索结束") {
		t.Fatalf("timeout texts = %q", got.texts)
	}
}

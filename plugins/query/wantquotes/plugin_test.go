package wantquotes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	for _, name := range []string{"wantquotes"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

// memState 是内存版 sdk.State，测试不触碰真实存储。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return m[key], nil
}

func (m memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m[key] = value
	return nil
}

// capture 捕获回复文本。
type capture struct {
	text string
	all  []string
}

func (c *capture) reply(_ context.Context, r sdk.Reply) error {
	c.text = r.Text
	c.all = append(c.all, r.Text)
	return nil
}

func run(c sdk.Call) error { return Handle(context.Background(), c) }

// stub 启动假接口并替换 endpoint，t.Cleanup 时还原。
func stub(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	old := endpoint
	endpoint = server.URL
	t.Cleanup(func() {
		server.Close()
		endpoint = old
	})
	return server
}

func newCall(state sdk.State, config map[string]any, message, userID string, cap *capture) sdk.Call {
	return sdk.Call{
		Event:  sdk.Event{Message: message, UserID: userID, Reply: cap.reply},
		State:  state,
		Config: config,
	}
}

func TestHelp(t *testing.T) {
	cap := &capture{}
	if err := run(newCall(memState{}, nil, "据意查句", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "用法") || !strings.Contains(cap.text, "设置据意查句") {
		t.Fatalf("reply = %q", cap.text)
	}
}

func TestQuery(t *testing.T) {
	var gotQuery, gotType string
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotType = r.URL.Query().Get("type")
		if r.URL.Query().Get("unionid") != "uid" || r.URL.Query().Get("secret") != "sec" {
			t.Errorf("missing credentials: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"top_sim":0.9,"quotes":[
			{"quote":"海内存知己，天涯若比邻。","author":"王勃","work":"送杜少府之任蜀州"}
		]}`))
	})
	state := memState{}
	_ = sdk.SaveJSON(context.Background(), state, stateKey, storedKey{Key: "uid|sec"})

	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 知己", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "请下列选择查询名句的类型") || !strings.Contains(cap.text, "0. 现") {
		t.Fatalf("selection prompt = %q", cap.text)
	}
	if gotQuery != "" {
		t.Fatalf("query happened before selection: %q", gotQuery)
	}

	cap = &capture{}
	if err := run(newCall(state, nil, "0", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "知己" || gotType != "现" {
		t.Fatalf("query = %q, type = %q", gotQuery, gotType)
	}
	if !strings.Contains(cap.text, "海内存知己") {
		t.Fatalf("reply = %q", cap.text)
	}
	if !strings.Contains(cap.text, "王勃") || !strings.Contains(cap.text, "《送杜少府之任蜀州》") {
		t.Fatalf("reply = %q", cap.text)
	}
}

func TestTypeIndex(t *testing.T) {
	var gotType string
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		_, _ = w.Write([]byte(`{"quotes":[{"quote":"床前明月光","author":"李白","work":"静夜思"}]}`))
	})
	state := memState{}
	_ = sdk.SaveJSON(context.Background(), state, stateKey, storedKey{Key: "uid|sec"})
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 4 月亮", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if gotType != typeList[4] {
		t.Fatalf("type = %q, want %q", gotType, typeList[4])
	}
	if !strings.Contains(cap.text, "床前明月光") {
		t.Fatalf("reply = %q", cap.text)
	}
}

func TestQueryReturnsAllQuotes(t *testing.T) {
	quotes := make([]string, 0, 9)
	for i := 1; i <= 9; i++ {
		quotes = append(quotes, `{"quote":"句子`+strconv.Itoa(i)+`","author":"作者`+strconv.Itoa(i)+`","work":"作品"}`)
	}
	stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"quotes":[` + strings.Join(quotes, ",") + `]}`))
	})
	state := memState{}
	_ = sdk.SaveJSON(context.Background(), state, stateKey, storedKey{Key: "uid|sec"})
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 0 关键词", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if len(cap.all) != 10 {
		t.Fatalf("replies = %d %q", len(cap.all), cap.all)
	}
	for i := 1; i <= 9; i++ {
		if !strings.Contains(cap.all[i], "句子"+strconv.Itoa(i)) {
			t.Fatalf("reply %d = %q", i, cap.all[i])
		}
	}
}

func TestNoKey(t *testing.T) {
	state := memState{}
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 大海", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "请下列选择") {
		t.Fatalf("prompt = %q", cap.text)
	}
	cap = &capture{}
	if err := run(newCall(state, nil, "0", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "还没有配置") {
		t.Fatalf("reply = %q", cap.text)
	}
}

func TestSetKeyPermission(t *testing.T) {
	state := memState{}
	cap := &capture{}
	if err := run(newCall(state, nil, "设置据意查句 uid|sec", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "没有权限") {
		t.Fatalf("reply = %q", cap.text)
	}

	cap = &capture{}
	config := map[string]any{"admin_users": []any{"admin"}}
	if err := run(newCall(state, config, "设置据意查句 uid|sec", "admin", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "已保存") {
		t.Fatalf("reply = %q", cap.text)
	}
	key, err := loadKey(context.Background(), sdk.Call{State: state})
	if err != nil {
		t.Fatal(err)
	}
	if key != "uid|sec" {
		t.Fatalf("key = %q", key)
	}
}

func TestBadKeyFormat(t *testing.T) {
	state := memState{}
	_ = sdk.SaveJSON(context.Background(), state, stateKey, storedKey{Key: "broken"})
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 大海", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	cap = &capture{}
	if err := run(newCall(state, nil, "0", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "格式不正确") {
		t.Fatalf("reply = %q", cap.text)
	}
}

func TestSelectionInvalid(t *testing.T) {
	state := memState{}
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 大海", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	cap = &capture{}
	if err := run(newCall(state, nil, "99", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "序号非法!") {
		t.Fatalf("reply = %q", cap.text)
	}
	cap = &capture{}
	if err := run(newCall(state, nil, "0", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.text, "还没有配置") {
		t.Fatalf("selection was not kept: %q", cap.text)
	}
}

func TestSelectionTimeout(t *testing.T) {
	state := memState{}
	oldNow := nowFunc
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = oldNow }()

	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 大海", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	nowFunc = func() time.Time { return base.Add(selectionTimeout + time.Second) }
	cap = &capture{}
	if err := run(newCall(state, nil, "随便", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if len(cap.all) != 2 || !strings.Contains(cap.all[0], "时间太久啦") {
		t.Fatalf("timeout replies = %q", cap.all)
	}
	if !strings.Contains(cap.text, "还没有配置") {
		t.Fatalf("default query reply = %q", cap.text)
	}
}

func TestNonNumericSelectsDefault(t *testing.T) {
	var gotType string
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		_, _ = w.Write([]byte(`{"quotes":[{"quote":"海上生明月","author":"张九龄","work":"望月怀远"}]}`))
	})
	state := memState{}
	_ = sdk.SaveJSON(context.Background(), state, stateKey, storedKey{Key: "uid|sec"})
	cap := &capture{}
	if err := run(newCall(state, nil, "据意查句 月亮", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	cap = &capture{}
	if err := run(newCall(state, nil, "不知道", "u1", cap)); err != nil {
		t.Fatal(err)
	}
	if gotType != "现" || !strings.Contains(cap.text, "海上生明月") {
		t.Fatalf("type = %q, reply = %q", gotType, cap.text)
	}
}

func TestParseArg(t *testing.T) {
	if typ, keyword, ok := parseArg("大海"); !ok || typ != "现" || keyword != "大海" {
		t.Fatalf("parseArg = %q %q %v", typ, keyword, ok)
	}
	if typ, keyword, ok := parseArg("12 谚语"); !ok || typ != "谚" || keyword != "谚语" {
		t.Fatalf("parseArg = %q %q %v", typ, keyword, ok)
	}
	if _, _, ok := parseArg("3"); ok {
		t.Fatal("纯数字应视为查看用法")
	}
	if _, _, ok := parseArg("99 超出"); ok {
		t.Fatal("越界序号应判为非法")
	}
}

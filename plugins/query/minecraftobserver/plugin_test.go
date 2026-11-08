package minecraftobserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minecraftobserver"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

// memState 是内存版 sdk.State，供订阅测试使用。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

// useServer 把上游接口指向测试服务器。
func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	oldAPI := apiURL
	apiURL = ts.URL + "/%s"
	t.Cleanup(func() { apiURL = oldAPI })
}

// call 执行一次事件并收集回复。
func call(t *testing.T, message string) ([]sdk.Reply, error) {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{Event: sdk.Event{
		GroupID: "g1",
		UserID:  "u1",
		Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		},
	}}
	err := Handle(context.Background(), c)
	return got, err
}

func TestHelp(t *testing.T) {
	got, err := call(t, "mc服务器状态")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "缺省端口为 25565") ||
		!strings.Contains(got[0].Text, "mc服务器添加订阅") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestOnline(t *testing.T) {
	var gotPath string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
	"online": true,
	"version": "1.20.4",
	"protocol": {"name": "1.20.4"},
	"motd": {"clean": ["§a测试服务器", "欢迎加入"], "raw": ["ignored"]},
	"players": {"online": 3, "max": 20},
	"mods": [{"name": "Fabric API", "version": "0.15.0"}, {"name": "Sodium", "version": "0.5.8"}]
}`))
	})

	got, err := call(t, "MC服务器状态 mc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("replies = %+v", got)
	}
	text := got[0].Text
	for _, want := range []string{"测试服务器", "欢迎加入", "mc.example.com:25565",
		"状态：在线", "版本：1.20.4", "在线人数：3/20", "Fabric API", "Sodium"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reply %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "ignored") {
		t.Fatalf("reply should prefer clean motd: %q", text)
	}
	if gotPath != "/mc.example.com:25565" {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestExplicitPort(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mc.example.com:25566" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"online": true, "version": "1.19.2", "players": {"online": 0, "max": 10}}`))
	})
	got, err := call(t, "mc服务器状态 mc.example.com:25566")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "mc.example.com:25566") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestOffline(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"online": false}`))
	})
	got, err := call(t, "mc服务器状态 mc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "状态：离线") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestUpstreamError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	got, err := call(t, "mc服务器状态 mc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "服务器状态获取失败") ||
		!strings.Contains(got[0].Text, "500") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestInvalidAddress(t *testing.T) {
	got, err := call(t, "mc服务器状态 mc.example.com:70000")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "地址格式不正确") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestNormalizeAddr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"mc.example.com", "mc.example.com:25565"},
		{"mc.example.com:25566", "mc.example.com:25566"},
		{"minecraft://mc.example.com/", "mc.example.com:25565"},
		{"[::1]", "[::1]:25565"},
		{"[::1]:25566", "[::1]:25566"},
		{"::1", "[::1]:25565"},
	}
	for _, tc := range cases {
		got, err := normalizeAddr(tc.in)
		if err != nil {
			t.Fatalf("normalizeAddr(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIgnoreChat(t *testing.T) {
	got, err := call(t, "今天天气不错")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

// callIn 在指定会话、状态与配置下执行一次事件。
func callIn(t *testing.T, st sdk.State, cfg map[string]any, group, user, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{State: st, Config: cfg, Event: sdk.Event{
		GroupID: group,
		UserID:  user,
		Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("handle %q: %v", message, err)
	}
	return got
}

// joinReplies 拼接全部文本回复。
func joinReplies(got []sdk.Reply) string {
	parts := make([]string, 0, len(got))
	for _, r := range got {
		if r.Text != "" {
			parts = append(parts, r.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestSubscriptionLifecycle(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"online":true,"version":"1.20.4","players":{"online":3,"max":20}}`))
	})
	st := memState{}

	text := joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器添加订阅 mc.example.com"))
	for _, want := range []string{"订阅添加成功", "mc.example.com:25565", "在线人数：3/20"} {
		if !strings.Contains(text, want) {
			t.Fatalf("add reply %q missing %q", text, want)
		}
	}

	if text = joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅列表")); !strings.Contains(text, "mc.example.com:25565") {
		t.Fatalf("list reply = %q", text)
	}

	if text = joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器取消订阅 mc.example.com")); !strings.Contains(text, "取消订阅成功") {
		t.Fatalf("remove reply = %q", text)
	}

	if text = joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅列表")); !strings.Contains(text, "当前没有订阅哦") {
		t.Fatalf("list reply = %q", text)
	}
}

func TestSubscriptionAddOffline(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"online":false}`))
	})
	got := joinReplies(callIn(t, memState{}, nil, "g1", "u1", "mc服务器添加订阅 mc.example.com"))
	if !strings.Contains(got, "服务器信息初始化失败") {
		t.Fatalf("replies = %q", got)
	}
}

func TestSubscriptionPull(t *testing.T) {
	players := 3
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"online":true,"version":"1.20.4","players":{"online":%d,"max":20}}`, players)
	})
	st := memState{}
	callIn(t, st, nil, "g1", "u1", "mc服务器添加订阅 mc.example.com")

	players = 5
	text := joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅拉取"))
	for _, want := range []string{"状态变更通知", "在线人数", "3/20", "5/20"} {
		if !strings.Contains(text, want) {
			t.Fatalf("pull reply %q missing %q", text, want)
		}
	}

	if text = joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅拉取")); !strings.Contains(text, "没有检测到订阅服务器的状态变化") {
		t.Fatalf("pull reply = %q", text)
	}
}

func TestSubscriptionIconChange(t *testing.T) {
	icon := "data:image/png;base64,AAAA"
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"online":true,"version":"1.20.4","players":{"online":3,"max":20},"icon":%q}`, icon)
	})
	st := memState{}
	callIn(t, st, nil, "g1", "u1", "mc服务器添加订阅 mc.example.com")

	icon = "data:image/png;base64,BBBB"
	text := joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅拉取"))
	if !strings.Contains(text, "图标变更") {
		t.Fatalf("pull reply = %q", text)
	}
	if text = joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅拉取")); !strings.Contains(text, "没有检测到订阅服务器的状态变化") {
		t.Fatalf("unchanged reply = %q", text)
	}
}

func TestSubscriptionPendingFlush(t *testing.T) {
	players := 3
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"online":true,"version":"1.20.4","players":{"online":%d,"max":20}}`, players)
	})
	st := memState{}
	callIn(t, st, nil, "g2", "u2", "mc服务器添加订阅 mc.example.com")

	players = 7
	// 在另一个会话触发拉取，变化应进入 g2 的待推送队列。
	if text := joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器订阅拉取")); !strings.Contains(text, "没有检测到订阅服务器的状态变化") {
		t.Fatalf("pull reply = %q", text)
	}
	// g2 下次发言时补发。
	if text := joinReplies(callIn(t, st, nil, "g2", "u2", "你好")); !strings.Contains(text, "状态变更通知") || !strings.Contains(text, "7/20") {
		t.Fatalf("flush reply = %q", text)
	}
	// 补发后清空。
	if got := callIn(t, st, nil, "g2", "u2", "你好"); len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

func TestGlobalSubscriptionList(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"online":true,"version":"1.20.4","players":{"online":3,"max":20}}`))
	})
	st := memState{}
	callIn(t, st, nil, "g1", "u1", "mc服务器添加订阅 mc.example.com")

	if text := joinReplies(callIn(t, st, nil, "g1", "u1", "mc服务器全局订阅列表")); !strings.Contains(text, "没有权限") {
		t.Fatalf("replies = %q", text)
	}
	text := joinReplies(callIn(t, st, map[string]any{"admin_users": []any{"u1"}}, "g1", "u1", "mc服务器全局订阅列表"))
	for _, want := range []string{"[群 g1]", "mc.example.com:25565"} {
		if !strings.Contains(text, want) {
			t.Fatalf("replies = %q missing %q", text, want)
		}
	}
}

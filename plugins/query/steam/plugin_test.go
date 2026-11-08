package steam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	for _, name := range []string{"steam"} {
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

const testKey = "0123456789ABCDEF0123456789ABCDEF"

// useServer 把 Steam 接口指向测试服务器。
func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	oldSummary, oldResolve := summaryURL, resolveURL
	summaryURL = ts.URL + "/summary?key=%s&steamids=%s"
	resolveURL = ts.URL + "/resolve?key=%s&vanityurl=%s"
	t.Cleanup(func() { summaryURL, resolveURL = oldSummary, oldResolve })
}

// call 执行一次事件并收集回复。
func call(t *testing.T, c sdk.Call, message string) ([]sdk.Reply, error) {
	t.Helper()
	var got []sdk.Reply
	c.Event.Message = message
	c.Event.Reply = func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}
	err := Handle(context.Background(), c)
	return got, err
}

func adminConfig() map[string]any {
	return map[string]any{"admin_users": []any{"u1"}}
}

func groupCall(state memState, config map[string]any) sdk.Call {
	return sdk.Call{State: state, Config: config, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}
}

func stateWithKey() memState {
	st := memState{}
	st[stateKey] = json.RawMessage(`{"api_key":"` + testKey + `"}`)
	return st
}

func TestQueryWithoutKey(t *testing.T) {
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "steam 76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "尚未配置") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestBindKey(t *testing.T) {
	got, err := call(t, groupCall(memState{}, adminConfig()), "steam绑定apikey "+testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "仅在私聊") {
		t.Fatalf("group replies = %+v", got)
	}

	st := memState{}
	private := sdk.Call{State: st, Config: adminConfig(), Event: sdk.Event{UserID: "u1"}}
	got, err = call(t, private, "steam绑定apikey "+testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "已保存") {
		t.Fatalf("private replies = %+v", got)
	}
	if !strings.Contains(string(st[stateKey]), testKey+`"`) {
		t.Fatalf("state = %s", st[stateKey])
	}

	// 与上游一致，不再强制 32 位十六进制。
	got, _ = call(t, private, "steam绑定 api key bad-key")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已保存") {
		t.Fatalf("non-hex replies = %+v", got)
	}
	if !strings.Contains(string(st[stateKey]), "bad-key") {
		t.Fatalf("state = %s", st[stateKey])
	}

	got, _ = call(t, private, "steam绑定apikey")
	if len(got) != 1 || !strings.Contains(got[0].Text, "用法") {
		t.Fatalf("empty replies = %+v", got)
	}
}

func TestQueryWithVanity(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resolve":
			_, _ = w.Write([]byte(`{"response":{"success":1,"steamid":"76561197960287930"}}`))
		case "/summary":
			_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561197960287930",
"personaname":"Gabe","profileurl":"https://steamcommunity.com/profiles/76561197960287930/",
"personastate":1,"gameextrainfo":"Dota 2","gameid":"570","communityvisibilitystate":3}]}}`))
		default:
			http.NotFound(w, r)
		}
	})
	got, err := call(t, sdk.Call{State: stateWithKey(), Event: sdk.Event{}}, "steam gabelogannewell")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "Gabe") || !strings.Contains(got[0].Text, "Dota 2") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestAddListPullRemove(t *testing.T) {
	state := 1
	game := "Dota 2"
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561197960287930",
"personaname":"Gabe","profileurl":"https://steamcommunity.com/profiles/76561197960287930/",
"personastate":` + strconv.Itoa(state) + `,"gameextrainfo":"` + game + `","communityvisibilitystate":3}]}}`))
	})
	st := stateWithKey()
	c := groupCall(st, adminConfig())

	got, _ := call(t, c, "steam添加订阅 76561197960287930")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅 Gabe") {
		t.Fatalf("add replies = %+v", got)
	}
	got, _ = call(t, c, "steam查询订阅")
	if len(got) != 1 || !strings.Contains(got[0].Text, "Gabe") ||
		!strings.Contains(got[0].Text, "状态：在线") || !strings.Contains(got[0].Text, "正在玩：Dota 2") {
		t.Fatalf("list replies = %+v", got)
	}

	state, game = 0, ""
	got, _ = call(t, c, "拉取steam订阅")
	if len(got) != 1 || !strings.Contains(got[0].Text, "下线了") {
		t.Fatalf("pull replies = %+v", got)
	}

	got, _ = call(t, c, "steam推送")
	if len(got) != 1 || !strings.Contains(got[0].Text, "太频繁") {
		t.Fatalf("cooldown replies = %+v", got)
	}

	got, _ = call(t, c, "steam删除订阅 76561197960287930")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已取消") {
		t.Fatalf("remove replies = %+v", got)
	}
}

func TestViewKey(t *testing.T) {
	private := sdk.Call{State: stateWithKey(), Config: adminConfig(), Event: sdk.Event{UserID: "u1"}}
	got, err := call(t, private, "查看apikey")
	if err != nil {
		t.Fatal(err)
	}
	want := "apikey为: 0123****CDEF"
	if len(got) != 1 || got[0].Text != want {
		t.Fatalf("replies = %+v", got)
	}

	got, _ = call(t, groupCall(stateWithKey(), adminConfig()), "查看apikey")
	if len(got) != 1 || !strings.Contains(got[0].Text, "仅在私聊") {
		t.Fatalf("group replies = %+v", got)
	}

	nonAdmin := sdk.Call{State: stateWithKey(), Config: adminConfig(), Event: sdk.Event{UserID: "u2"}}
	got, _ = call(t, nonAdmin, "查看apikey")
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有权限") {
		t.Fatalf("non-admin replies = %+v", got)
	}

	got, _ = call(t, sdk.Call{State: memState{}, Config: adminConfig(), Event: sdk.Event{UserID: "u1"}}, "查看apikey")
	if len(got) != 1 || !strings.Contains(got[0].Text, "尚未配置") {
		t.Fatalf("empty replies = %+v", got)
	}
}

func TestMemberCanManageSubs(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561197960287930",
"personaname":"Gabe","personastate":1,"gameextrainfo":"Dota 2","communityvisibilitystate":3}]}}`))
	})
	st := stateWithKey()
	c := groupCall(st, adminConfig())
	c.Event.UserID = "u2"

	got, err := call(t, c, "steam添加订阅 76561197960287930")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅 Gabe") {
		t.Fatalf("add replies = %+v", got)
	}
	got, _ = call(t, c, "steam删除订阅 76561197960287930")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已取消") {
		t.Fatalf("remove replies = %+v", got)
	}
}

func TestIgnoreChat(t *testing.T) {
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "今天天气不错")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

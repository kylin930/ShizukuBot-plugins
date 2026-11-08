package bilibilipush

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
	for _, name := range []string{"bilibilipush"} {
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

const navBody = `{"code":0,"data":{"wbi_img":{
"img_url":"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
"sub_url":"https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}}}`

const cardBody = `{"code":0,"data":{"card":{"mid":"2","name":"碧诗","fans":1436728,
"sign":"直男过气网红"},"archive_count":44}}`

const liveBody = `{"code":0,"data":{"liveStatus":1,"title":"测试直播","roomid":1024,
"url":"https://live.bilibili.com/1024"}}`

// feedBody 生成包含一条动态的响应。
func feedBody(ts int64, title string) string {
	return `{"code":0,"message":"0","data":{"items":[{"id_str":"1267518804128170040",
"type":"DYNAMIC_TYPE_AV","modules":{"module_author":{"name":"碧诗","pub_ts":"` +
		strconv.FormatInt(ts, 10) + `","pub_time":"刚刚"},
"module_dynamic":{"desc":null,"major":{"archive":{"title":"` + title + `",
"jump_url":"//www.bilibili.com/video/BV1"}}}}}]}}`
}

// feedDetailBody 生成带正文首段与首图的动态响应。
func feedDetailBody(ts int64, title, summary, img string) string {
	return `{"code":0,"message":"0","data":{"items":[{"id_str":"1267518804128170040",
"type":"DYNAMIC_TYPE_DRAW","modules":{"module_author":{"name":"碧诗","pub_ts":"` +
		strconv.FormatInt(ts, 10) + `","pub_time":"刚刚"},
"module_dynamic":{"desc":{"text":"` + summary + `\n\n第二段不应出现"},"major":{
"archive":{"title":"` + title + `","jump_url":"//www.bilibili.com/video/BV1"},
"draw":{"items":[{"src":"` + img + `"}]}}}}}]}}`
}

// useServer 把全部接口指向测试服务器。
func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	oldCard, oldLive, oldSpace := cardURL, liveURL, spaceURL
	oldNav, oldWarm, oldWbi := navURL, warmURL, wbi
	cardURL = ts.URL + "/card?mid=%d"
	liveURL = ts.URL + "/live?mid=%d"
	spaceURL = ts.URL + "/space"
	navURL = ts.URL + "/nav"
	warmURL = ts.URL + "/warm"
	wbi = &wbiCache{}
	t.Cleanup(func() {
		cardURL, liveURL, spaceURL = oldCard, oldLive, oldSpace
		navURL, warmURL, wbi = oldNav, oldWarm, oldWbi
	})
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

func TestQueryUser(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		default:
			http.NotFound(w, r)
		}
	})
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "b站用户 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "碧诗") || !strings.Contains(got[0].Text, "143.7万") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryDynamics(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/warm", "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/space":
			if r.URL.Query().Get("w_rid") == "" || r.URL.Query().Get("host_mid") != "2" {
				t.Errorf("unsigned request: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(feedBody(1700000000, "我是奸商")))
		default:
			http.NotFound(w, r)
		}
	})
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "b站动态 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "投稿：我是奸商") {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "https://t.bilibili.com/1267518804128170040") {
		t.Fatalf("missing link: %q", got[0].Text)
	}
}

func TestQueryLive(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(liveBody))
	})
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "b站直播 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "直播中") || !strings.Contains(got[0].Text, "测试直播") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestAddSubRequiresAdmin(t *testing.T) {
	got, err := call(t, groupCall(memState{}, nil), "添加b站订阅 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有权限") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestAddListRemoveAndPull(t *testing.T) {
	liveOn := false
	ts := int64(1700000000)
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/live":
			if liveOn {
				_, _ = w.Write([]byte(liveBody))
			} else {
				_, _ = w.Write([]byte(`{"code":0,"data":{"liveStatus":0,"roomid":1024}}`))
			}
		case "/warm", "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/space":
			_, _ = w.Write([]byte(feedBody(ts, "测试投稿")))
		default:
			http.NotFound(w, r)
		}
	})
	st := memState{}
	c := groupCall(st, adminConfig())

	got, _ := call(t, c, "添加b站订阅 2")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅") {
		t.Fatalf("add replies = %+v", got)
	}
	got, _ = call(t, c, "b站推送列表")
	if len(got) != 1 || !strings.Contains(got[0].Text, "碧诗") ||
		!strings.Contains(got[0].Text, "动态：● 直播：●") {
		t.Fatalf("list replies = %+v", got)
	}

	// 基线之后的动态与开播都应被推送。
	ts += 60
	liveOn = true
	got, _ = call(t, c, "拉取b站推送")
	if len(got) != 1 {
		t.Fatalf("pull replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "正在直播") || !strings.Contains(got[0].Text, "更新了动态") {
		t.Fatalf("pull text = %q", got[0].Text)
	}

	// 立即再次拉取应被频控拦截。
	got, _ = call(t, c, "b站推送")
	if len(got) != 1 || !strings.Contains(got[0].Text, "太频繁") {
		t.Fatalf("cooldown replies = %+v", got)
	}

	got, _ = call(t, c, "取消b站订阅 2")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已取消") {
		t.Fatalf("remove replies = %+v", got)
	}
}

func TestPullWithoutSubs(t *testing.T) {
	got, err := call(t, groupCall(memState{}, adminConfig()), "b站推送")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "还没有订阅") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestCancelPartSub(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/live":
			_, _ = w.Write([]byte(`{"code":0,"data":{"liveStatus":0,"roomid":1024}}`))
		case "/warm", "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/space":
			_, _ = w.Write([]byte(feedBody(1700000000, "测试投稿")))
		default:
			http.NotFound(w, r)
		}
	})
	st := memState{}
	c := groupCall(st, adminConfig())
	if got, _ := call(t, c, "添加b站订阅 2"); len(got) != 1 || !strings.Contains(got[0].Text, "已订阅") {
		t.Fatalf("add replies = %+v", got)
	}

	got, _ := call(t, c, "取消b站动态订阅 2")
	if len(got) != 1 || got[0].Text != "已取消碧诗的动态订阅" {
		t.Fatalf("cancel dynamic replies = %+v", got)
	}
	if !strings.Contains(string(st[stateKey]), `"dynamic_off":true`) {
		t.Fatalf("state = %s", st[stateKey])
	}
	got, _ = call(t, c, "b站推送列表")
	if len(got) != 1 || !strings.Contains(got[0].Text, "动态：○ 直播：●") {
		t.Fatalf("list switches = %+v", got)
	}

	got, _ = call(t, c, "取消b站直播订阅 2")
	if len(got) != 1 || got[0].Text != "已取消碧诗的直播订阅" {
		t.Fatalf("cancel live replies = %+v", got)
	}
	if !strings.Contains(string(st[stateKey]), `"live_off":true`) {
		t.Fatalf("state = %s", st[stateKey])
	}

	got, _ = call(t, c, "取消b站动态订阅 9")
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有订阅") {
		t.Fatalf("missing replies = %+v", got)
	}

	// 两项都关闭后拉取不应再请求动态或直播。
	got, _ = call(t, c, "b站推送")
	if len(got) != 1 || !strings.Contains(got[0].Text, "本次没有新动态或直播") {
		t.Fatalf("pull replies = %+v", got)
	}

	// 重新添加会把两个开关复位，便于恢复订阅。
	got, _ = call(t, c, "添加b站订阅 2")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅") {
		t.Fatalf("re-add replies = %+v", got)
	}
}

func TestAtAllAndDynamicDetail(t *testing.T) {
	liveOn := false
	ts := int64(1700000000)
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/live":
			if liveOn {
				_, _ = w.Write([]byte(liveBody))
			} else {
				_, _ = w.Write([]byte(`{"code":0,"data":{"liveStatus":0,"roomid":1024}}`))
			}
		case "/warm", "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/space":
			_, _ = w.Write([]byte(feedDetailBody(ts, "绘图投稿", "这是动态正文的第一段", "https://i0.hdslb.com/test.jpg")))
		default:
			http.NotFound(w, r)
		}
	})
	st := memState{}
	c := groupCall(st, adminConfig())
	if got, _ := call(t, c, "添加b站订阅 2"); len(got) != 1 || !strings.Contains(got[0].Text, "已订阅") {
		t.Fatalf("add replies = %+v", got)
	}

	got, _ := call(t, c, "开启艾特全体")
	if len(got) != 1 || got[0].Text != "已开启艾特全体Oo" {
		t.Fatalf("at all replies = %+v", got)
	}
	if !strings.Contains(string(st[stateKey]), `"at_all":{"g1":true}`) {
		t.Fatalf("state = %s", st[stateKey])
	}

	ts += 60
	liveOn = true
	got, _ = call(t, c, "b站推送")
	if len(got) != 2 {
		t.Fatalf("pull replies = %+v", got)
	}
	if !strings.HasPrefix(got[0].Text, "【B站推送】\n@全体成员 ") {
		t.Fatalf("pull text = %q", got[0].Text)
	}
	if !strings.Contains(got[0].Text, "这是动态正文的第一段") || strings.Contains(got[0].Text, "第二段不应出现") {
		t.Fatalf("pull text = %q", got[0].Text)
	}
	if got[1].Kind != "image" || got[1].URL != "https://i0.hdslb.com/test.jpg" {
		t.Fatalf("image reply = %+v", got[1])
	}

	got, _ = call(t, c, "关闭艾特全体")
	if len(got) != 1 || got[0].Text != "已关闭艾特全体Oo" {
		t.Fatalf("close at all replies = %+v", got)
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

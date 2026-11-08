package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bilibili"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

const navBody = `{"code":0,"data":{"wbi_img":{
"img_url":"https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
"sub_url":"https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}}}`

const cardBody = `{"code":0,"data":{"card":{"mid":"2","name":"碧诗","sex":"男",
"birthday":"01-01","sign":"直男过气网红","fans":1436728,"attention":40,
"regtime":1600000000,"attentions":[100,200,300],"level_info":{"current_level":6}},
"archive_count":44}}`

const vtbBody = `{"mid":100,"uname":"测试VUP","video":12,"roomid":1024,"face":"http://i0.hdslb.com/face.jpg",
"sign":"测试签名","title":"官方认证标题","official":{"type":1,"title":"认证"},"rise":3,
"follower":25000,"guardNum":8,"areaRank":9}`

const vupListBody = `[{"mid":100,"uname":"测试VUP","roomid":1024,"face":"https://i0.hdslb.com/a.jpg",
"sign":"签名A","title":"标题A","official":true,"follower":25000,"video":12,"rise":3,"guardNum":8,"areaRank":9},
{"mid":200,"uname":"另一位VUP","roomid":2048,"follower":8000},
{"mid":999,"uname":"无关VUP","roomid":4096,"follower":100}]`

const medalBody = `{"code":0,"message":"0","data":{"list":[
{"target_name":"牌子UP","medal_info":{"target_id":300,"medal_name":"小电视","level":20,
"medal_color_start":16777215,"medal_color_end":16711680,"medal_color_border":255}},
{"target_name":"测试VUP","medal_info":{"target_id":100,"medal_name":"小电视","level":5}}]}}`

const searchBody = `{"code":0,"data":{"result":[{"mid":100,"uname":"测试VUP"}]}}`

const danmakuBody = `{"code":0,"message":"","data":{"total":9,"pageNum":0,"pageSize":5,
"hasMore":true,"data":{"records":[{"channel":{"uId":2,"uName":"测试主播","roomId":1024,
"title":"测试直播间"},"live":{"isFinish":true,"danmakusCount":100,
"startDate":1700000000000,"stopDate":1700007200000,"title":"测试直播",
"totalIncome":203.0,"watchCount":1000},"danmakus":[
{"uId":3,"uName":"观众A","type":0,"sendDate":1700000001000,"message":"你好"},
{"uId":4,"uName":"观众B","type":1,"sendDate":1700000002000,"message":"辣条","price":0.1}]}]}}}`

// jpegStub 具备 JPEG 文件头，可被 http.DetectContentType 识别。
var jpegStub = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00}

// memState 内存版 sdk.State，测试中替代宿主存储。
type memState struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

func newMemState() *memState {
	return &memState{data: map[string]json.RawMessage{}}
}

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

// useServer 把全部接口指向测试服务器，并在结束后还原。
func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	oldCard, oldVtb, oldList, oldMedal := cardURL, vtbURL, vtbListURL, medalURL
	oldDanmaku, oldSearch, oldNav, oldWarm := danmakuURL, searchURL, navURL, warmURL
	oldWbi := wbi
	cardURL = ts.URL + "/card?mid=%d"
	vtbURL = ts.URL + "/vtb/%d"
	vtbListURL = ts.URL + "/vup-list"
	medalURL = ts.URL + "/medal?target_id=%d"
	danmakuURL = ts.URL + "/danmaku?uId=%d&pageNum=%d"
	searchURL = ts.URL + "/search"
	navURL = ts.URL + "/nav"
	warmURL = ts.URL + "/warm"
	wbi = &wbiCache{}
	t.Cleanup(func() {
		cardURL, vtbURL, vtbListURL, medalURL = oldCard, oldVtb, oldList, oldMedal
		danmakuURL, searchURL, navURL, warmURL = oldDanmaku, oldSearch, oldNav, oldWarm
		wbi = oldWbi
	})
}

// stubRender 替换出图函数，captured 非空时记录渲染的 HTML。
func stubRender(t *testing.T, captured *string) {
	t.Helper()
	old := renderHTML
	renderHTML = func(_ context.Context, html []byte, _ sdk.RenderOptions) ([]byte, error) {
		if captured != nil {
			*captured = string(html)
		}
		return jpegStub, nil
	}
	t.Cleanup(func() { renderHTML = old })
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

func plainCall(config map[string]any) sdk.Call {
	return sdk.Call{Config: config, State: newMemState(), Event: sdk.Event{UserID: "u1"}}
}

func replyText(t *testing.T, got []sdk.Reply) string {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("replies = %+v", got)
	}
	return got[0].Text
}

func TestHelp(t *testing.T) {
	got, err := call(t, plainCall(nil), "b站成分帮助")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	for _, want := range []string{"查成分", "查弹幕", ">user info", "cookie"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help = %q", text)
		}
	}
}

func TestQueryUserByUID(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/card" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(cardBody))
	})
	got, err := call(t, plainCall(nil), ">user info 2")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	for _, want := range []string{"碧诗", "UID：2", "Lv6", "生日：01-01", "143.7万", "space.bilibili.com/2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user info = %q", text)
		}
	}
}

func TestQueryUserMissingArg(t *testing.T) {
	got, err := call(t, plainCall(nil), ">user info")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "用法") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryUserByNameWithoutCookie(t *testing.T) {
	got, err := call(t, plainCall(nil), ">user info 测试VUP")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "cookie") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryUserByNameWithWBI(t *testing.T) {
	var signed bool
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/warm":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "test"})
		case "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/search":
			query := r.URL.Query()
			if query.Get("w_rid") == "" || query.Get("wts") == "" ||
				query.Get("keyword") != "测试VUP" || query.Get("search_type") != "bili_user" {
				t.Errorf("search query = %q", r.URL.RawQuery)
			}
			signed = true
			_, _ = w.Write([]byte(searchBody))
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		default:
			http.NotFound(w, r)
		}
	})
	got, err := call(t, plainCall(map[string]any{"cookie": "SESSDATA=abc"}), ">user info 测试VUP")
	if err != nil {
		t.Fatal(err)
	}
	if !signed || !strings.Contains(replyText(t, got), "碧诗") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryUserUpstreamError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":-404,"message":"啥都木有"}`))
	})
	got, err := call(t, plainCall(nil), ">user info 404")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "查询失败") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryVup(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vtb/100" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(vtbBody))
	})
	got, err := call(t, plainCall(nil), ">vup info 100")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	for _, want := range []string{"测试VUP", "粉丝：2.5万", "24h涨粉：3", "舰队：8", "vtbs.moe/detail/100"} {
		if !strings.Contains(text, want) {
			t.Fatalf("vup info = %q", text)
		}
	}
}

func TestQueryVupUpstreamError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	got, err := call(t, plainCall(nil), ">vup info 100")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "查询失败") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestSetCookie(t *testing.T) {
	got, err := call(t, plainCall(nil), "设置b站cookie SESSDATA=abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "没有权限") {
		t.Fatalf("replies = %+v", got)
	}

	var searchCookie string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/warm":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "test"})
		case "/nav":
			_, _ = w.Write([]byte(navBody))
		case "/search":
			searchCookie = r.Header.Get("Cookie")
			_, _ = w.Write([]byte(searchBody))
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		default:
			http.NotFound(w, r)
		}
	})
	st := newMemState()
	c := sdk.Call{State: st, Config: map[string]any{"admin_users": []any{"u1"}},
		Event: sdk.Event{UserID: "u1"}}
	got, err = call(t, c, "设置b站cookie SESSDATA=abc")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	if !strings.Contains(text, "已保存") || strings.Contains(text, "SESSDATA=abc") {
		t.Fatalf("replies = %+v", got)
	}
	var stored cookieStore
	if err := sdk.LoadJSON(context.Background(), st, cookieStateKey, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Cookie != "SESSDATA=abc" {
		t.Fatalf("stored cookie = %q", stored.Cookie)
	}

	// 保存后的 Cookie 应用于昵称搜索，无需再改插件配置。
	got, err = call(t, c, ">user info 测试VUP")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "碧诗") || !strings.Contains(searchCookie, "SESSDATA=abc") {
		t.Fatalf("replies = %+v, cookie = %q", got, searchCookie)
	}
}

func TestIgnoreUnrelatedMessage(t *testing.T) {
	got, err := call(t, plainCall(nil), "今天天气不错")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "bilibili" {
		t.Fatalf("manifest = %+v", def.Manifest)
	}
	if !def.Manifest.Allows("state.read") || !def.Manifest.Allows("state.write") {
		t.Fatalf("manifest permissions = %+v", def.Manifest.Permissions)
	}
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryComponent(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/vup-list":
			_, _ = w.Write([]byte(vupListBody))
		case "/medal":
			_, _ = w.Write([]byte(medalBody))
		default:
			http.NotFound(w, r)
		}
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(map[string]any{"cookie": "SESSDATA=abc"}), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" || !strings.HasPrefix(got[0].URL, "data:image/jpeg;base64,") {
		t.Fatalf("replies = %+v", got)
	}
	for _, want := range []string{"碧诗", "UID 2", "143.7万", "管人痴成分", "66.67%（2/3）", "Lv6",
		"牌子UP", "小电视", "测试VUP", "另一位VUP", "注册时间"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestQueryComponentRowLimit(t *testing.T) {
	atts := make([]string, 0, 60)
	vups := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		mid := 1000 + i
		atts = append(atts, strconv.Itoa(mid))
		vups = append(vups, fmt.Sprintf(`{"mid":%d,"uname":"UP%d"}`, mid, i))
	}
	card := fmt.Sprintf(`{"code":0,"data":{"card":{"mid":"2","name":"碧诗","attentions":[%s]},`+
		`"archive_count":0}}`, strings.Join(atts, ","))
	list := "[" + strings.Join(vups, ",") + "]"
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(card))
		case "/vup-list":
			_, _ = w.Write([]byte(list))
		default:
			http.NotFound(w, r)
		}
	})
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML = old })

	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	if !strings.Contains(text, "UP49") || strings.Contains(text, "UP50") ||
		!strings.Contains(text, "仅展示前 50 位") {
		t.Fatalf("fallback = %q", text)
	}
}

func TestQueryComponentWithoutCookie(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vup-list" {
			_, _ = w.Write([]byte(vupListBody))
			return
		}
		_, _ = w.Write([]byte(cardBody))
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(html, "未配置 cookie") || !strings.Contains(html, "粉丝牌墙已跳过") {
		t.Fatalf("html = %q", html)
	}
}

func TestQueryComponentListError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/card" {
			_, _ = w.Write([]byte(cardBody))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(html, "无法统计") || !strings.Contains(html, "列表获取失败") {
		t.Fatalf("html = %q", html)
	}
}

func TestQueryComponentCookieExpired(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/vup-list":
			_, _ = w.Write([]byte(vupListBody))
		case "/medal":
			_, _ = w.Write([]byte(`{"code":-101,"message":"账号未登录"}`))
		default:
			http.NotFound(w, r)
		}
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(map[string]any{"cookie": "SESSDATA=expired"}), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(html, "Cookie 已失效") {
		t.Fatalf("html = %q", html)
	}
}

func TestQueryDanmaku(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/danmaku":
			if r.URL.Query().Get("uId") != "2" || r.URL.Query().Get("pageNum") != "0" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(danmakuBody))
		default:
			http.NotFound(w, r)
		}
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(nil), "查弹幕 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	for _, want := range []string{"碧诗", "测试直播", "1000", "你好", "辣条", "￥0.1"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestQueryDanmakuShowsAll(t *testing.T) {
	items := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		items = append(items, fmt.Sprintf(`{"uId":%d,"uName":"观众%d","type":0,"sendDate":1700000001000,"message":"弹幕%d"}`, i, i, i))
	}
	body := `{"code":0,"message":"","data":{"total":1,"pageNum":0,"pageSize":5,"data":{"records":[` +
		`{"channel":{"uId":2,"uName":"测试主播","roomId":1024,"title":"测试直播间"},` +
		`"live":{"isFinish":true,"danmakusCount":12,"startDate":1700000000000,"stopDate":1700007200000,"title":"测试直播"},` +
		`"danmakus":[` + strings.Join(items, ",") + `]}]}}}`
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/danmaku" {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(cardBody))
	})
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML = old })

	got, err := call(t, plainCall(nil), "查弹幕 2")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	if !strings.Contains(text, "观众11：弹幕11") || strings.Contains(text, "未展示") {
		t.Fatalf("fallback = %q", text)
	}
}

func TestQueryDanmakuInvalidPage(t *testing.T) {
	got, err := call(t, plainCall(nil), "查弹幕 2 -1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "页码必须是非负整数") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestQueryDanmakuUpstreamError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":500,"message":"服务异常"}`))
	})
	got, err := call(t, plainCall(nil), "查弹幕 2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "查询失败") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestRenderFailureFallsBackToText(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/vup-list":
			_, _ = w.Write([]byte(vupListBody))
		default:
			http.NotFound(w, r)
		}
	})
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML = old })

	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	for _, want := range []string{"【B站成分】碧诗", "管人痴成分：66.67%（2/3）", "测试VUP（UID 100）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fallback = %q", text)
		}
	}
}

func TestDanmakuRenderFailureFallsBackToText(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/danmaku" {
			_, _ = w.Write([]byte(danmakuBody))
			return
		}
		_, _ = w.Write([]byte(cardBody))
	})
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML = old })

	got, err := call(t, plainCall(nil), "查弹幕 2")
	if err != nil {
		t.Fatal(err)
	}
	text := replyText(t, got)
	for _, want := range []string{"【查弹幕】碧诗", "测试直播", "观众A：你好", "辣条 ￥0.1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fallback = %q", text)
		}
	}
}

func TestOversizedRenderFallsBackToText(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/vup-list":
			_, _ = w.Write([]byte(vupListBody))
		default:
			http.NotFound(w, r)
		}
	})
	renders := 0
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		renders++
		data := make([]byte, cardMaxBytes+1)
		copy(data, jpegStub)
		return data, nil
	}
	t.Cleanup(func() { renderHTML = old })

	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "【B站成分】碧诗") {
		t.Fatalf("replies = %+v", got)
	}
	if renders != 2 {
		t.Fatalf("renders = %d, want 2 (narrow retry)", renders)
	}
}

func TestComponentHTMLEscapesUntrustedText(t *testing.T) {
	html, err := buildComponentHTML(componentView{
		Name: `<script>alert("x")</script>`,
		UID:  1,
		Rows: []componentRow{{
			Uname:       `A&B <b>`,
			UID:         2,
			VUP:         true,
			MedalName:   `牌子"><img src=x>`,
			MedalLevel:  3,
			MedalStart:  "#ffffff",
			MedalEnd:    "#000000",
			MedalBorder: "#ff0000",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	if strings.Contains(page, `<script>alert`) || strings.Contains(page, `<img src=x>`) {
		t.Fatalf("html not escaped: %s", page)
	}
	for _, want := range []string{"&lt;script&gt;", "A&amp;B", "牌子&#34;&gt;&lt;img"} {
		if !strings.Contains(page, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestVupCachePersistsFullFields(t *testing.T) {
	listCalls := 0
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			_, _ = w.Write([]byte(cardBody))
		case "/vup-list":
			listCalls++
			_, _ = w.Write([]byte(vupListBody))
		default:
			http.NotFound(w, r)
		}
	})
	c := plainCall(map[string]any{"admin_users": []any{"u1"}})
	got, err := call(t, c, "更新vup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "共 3 位") || listCalls != 1 {
		t.Fatalf("replies = %+v, listCalls = %d", got, listCalls)
	}

	var store vupStore
	if err := sdk.LoadJSON(context.Background(), c.State, vupStateKey, &store); err != nil {
		t.Fatal(err)
	}
	if len(store.Entries) != 3 {
		t.Fatalf("entries = %+v", store.Entries)
	}
	first := store.Entries[0]
	if first.Face == "" || first.Sign != "签名A" || first.Title != "标题A" ||
		first.Follower != 25000 || first.Roomid != 1024 || string(first.Official) != "true" {
		t.Fatalf("entry = %+v", first)
	}

	// 列表缓存仍新鲜时，查成分不应再次请求 vtbs.moe。
	var html string
	stubRender(t, &html)
	got, err = call(t, c, "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	if listCalls != 1 || !strings.Contains(html, "另一位VUP") {
		t.Fatalf("listCalls = %d, html = %q", listCalls, html)
	}
}

func TestUpdateVupPermission(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(vupListBody))
	})
	got, err := call(t, plainCall(nil), "更新vup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "没有权限") {
		t.Fatalf("replies = %+v", got)
	}

	got, err = call(t, plainCall(map[string]any{"admin_users": []any{"u1"}}), "更新vup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyText(t, got), "共 3 位") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestComponentInlinesAvatar(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x00}
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/face.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		case "/card":
			face := "http://" + r.Host + "/face.png"
			body := strings.Replace(cardBody, `"sign":"直男过气网红"`, `"sign":"直男过气网红","face":"`+face+`"`, 1)
			_, _ = w.Write([]byte(body))
		case "/vup-list":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	})
	var html string
	stubRender(t, &html)
	got, err := call(t, plainCall(nil), "查成分 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(html, "data:image/png;base64,") {
		t.Fatalf("html missing inlined avatar: %s", html)
	}
}

func TestNormalizeImageURL(t *testing.T) {
	cases := map[string]string{
		"http://i0.hdslb.com/a.jpg":   "https://i0.hdslb.com/a.jpg",
		"//i0.hdslb.com/a.jpg":        "https://i0.hdslb.com/a.jpg",
		"https://i0.hdslb.com/a.jpg":  "https://i0.hdslb.com/a.jpg",
		"http://127.0.0.1:8080/a.jpg": "http://127.0.0.1:8080/a.jpg",
		"http://localhost:8080/a.jpg": "http://localhost:8080/a.jpg",
		"":                            "",
		"ftp://i0.hdslb.com/a.jpg":    "",
		"not a url":                   "",
	}
	for in, want := range cases {
		if got := normalizeImageURL(in); got != want {
			t.Fatalf("normalizeImageURL(%q) = %q, want %q", in, got, want)
		}
	}
}

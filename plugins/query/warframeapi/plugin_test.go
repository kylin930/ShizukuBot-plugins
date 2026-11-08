package warframeapi

import (
	"context"
	"encoding/json"
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
	for _, name := range []string{"warframeapi"} {
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

const sample = `{
  "cetusCycle": {"isDay": true, "timeLeft": "1h 20m"},
  "vallisCycle": {"isWarm": false, "timeLeft": "5m"},
  "cambionCycle": {"active": "vome", "timeLeft": "40m"},
  "alerts": [{
    "active": true,
    "eta": "2h",
    "mission": {"node": "E Prime", "type": "Extermination", "minEnemyLevel": 1, "maxEnemyLevel": 5,
      "reward": {"asString": "1000 现金"}}
  }],
  "arbitration": {"node": "Mot", "type": "Survival", "enemy": "Corpus", "expiry": "2030-01-01T00:00:00Z"},
  "dailyDeals": [{"item": "催化剂蓝图", "originalPrice": 100, "salePrice": 50, "total": 1000, "sold": 300, "eta": "3h"}]
}`

type capture struct {
	texts   []string
	replies []sdk.Reply
}

func run(t *testing.T, message string) capture {
	t.Helper()
	return runCall(t, sdk.Call{Event: sdk.Event{UserID: "u1"}}, message)
}

func runCall(t *testing.T, c sdk.Call, message string) capture {
	t.Helper()
	var got capture
	c.Event.Message = message
	c.Event.Reply = func(_ context.Context, r sdk.Reply) error {
		got.texts = append(got.texts, r.Text)
		got.replies = append(got.replies, r)
		return nil
	}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// wantKeyboard 断言回复带按钮，且按钮指令与触发指令一致。
func wantKeyboard(t *testing.T, r sdk.Reply, command string) {
	t.Helper()
	if r.Kind != "keyboard" {
		t.Fatalf("kind = %q, want keyboard", r.Kind)
	}
	if !strings.Contains(r.Keyboard, `"data":"`+command+`"`) {
		t.Fatalf("keyboard %q missing command %q", r.Keyboard, command)
	}
}

func stub(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sample))
	}))
	t.Cleanup(server.Close)
	old := worldStateURL
	worldStateURL = server.URL
	resetWorldCache()
	t.Cleanup(func() {
		worldStateURL = old
		resetWorldCache()
	})
}

func TestPlains(t *testing.T) {
	stub(t)
	cases := map[string]string{
		"地球平原时间":  "地球平原: 白天, 下次更新: 1h 20m",
		"金星平原时间":  "金星平原: 寒冷, 下次更新: 5m",
		"火卫二平原时间": "火卫二平原: vome, 下次更新: 40m",
	}
	for msg, want := range cases {
		got := run(t, msg)
		if len(got.texts) != 1 || got.texts[0] != want {
			t.Fatalf("%s => %q", msg, got.texts)
		}
		wantKeyboard(t, got.replies[0], msg)
	}
}

func TestMenuKeyboard(t *testing.T) {
	commands := []string{"地球平原时间", "金星平原时间", "火卫二平原时间", "wf警报", "wf仲裁", "wf每日特惠"}
	payload, err := menuKeyboard().JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if !strings.Contains(payload, `"data":"`+command+`"`) {
			t.Fatalf("keyboard %q missing command %q", payload, command)
		}
	}
}

func TestPlainNotExist(t *testing.T) {
	stub(t)
	got := run(t, "木星平原时间")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "平原不存在") {
		t.Fatalf("texts = %q", got.texts)
	}
	if got.replies[0].Kind != "text" {
		t.Fatalf("kind = %q, want text", got.replies[0].Kind)
	}
}

func TestAlerts(t *testing.T) {
	stub(t)
	got := run(t, "wf警报")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "E Prime") ||
		!strings.Contains(got.texts[0], "1000 现金") {
		t.Fatalf("texts = %q", got.texts)
	}
	wantKeyboard(t, got.replies[0], "wf警报")
}

func TestArbitration(t *testing.T) {
	stub(t)
	got := run(t, "wf仲裁")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "Mot") ||
		!strings.Contains(got.texts[0], "阵营: Corpus") {
		t.Fatalf("texts = %q", got.texts)
	}
	wantKeyboard(t, got.replies[0], "wf仲裁")
}

func TestDeals(t *testing.T) {
	stub(t)
	got := run(t, "wf每日特惠")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "催化剂蓝图") ||
		!strings.Contains(got.texts[0], "100→50") {
		t.Fatalf("texts = %q", got.texts)
	}
	wantKeyboard(t, got.replies[0], "wf每日特惠")
}

func TestWFTimeSyncSimulation(t *testing.T) {
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	const cycleSample = `{
	  "cetusCycle": {"isDay": true, "expiry": "2030-01-01T00:01:00Z"},
	  "vallisCycle": {"isWarm": true, "expiry": "2030-01-01T00:01:00Z"},
	  "cambionCycle": {"active": "fass", "expiry": "2030-01-01T00:01:00Z"}
	}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(cycleSample))
	}))
	t.Cleanup(server.Close)
	oldURL, oldNow := worldStateURL, nowFunc
	worldStateURL = server.URL
	nowFunc = func() time.Time { return base }
	resetWorldCache()
	t.Cleanup(func() {
		worldStateURL = oldURL
		nowFunc = oldNow
		resetWorldCache()
	})

	if got := run(t, "wf时间同步"); len(got.texts) != 1 {
		t.Fatalf("sync texts = %q", got.texts)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// 同步窗口内不再请求上游，并按昼夜时长推进状态。
	nowFunc = func() time.Time { return base.Add(2 * time.Minute) }
	got := run(t, "地球平原时间")
	want := "地球平原: 夜晚, 下次更新: 49m0s"
	if len(got.texts) != 1 || got.texts[0] != want {
		t.Fatalf("texts = %q, want %q", got.texts, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls)
	}
}

func TestWFTimeSync(t *testing.T) {
	stub(t)
	got := run(t, "wf时间同步")
	if len(got.texts) != 1 ||
		!strings.Contains(got.texts[0], "已拉取服务器时间并同步到本地模拟") ||
		!strings.Contains(got.texts[0], "本地时间: ") ||
		!strings.Contains(got.texts[0], "地球平原: 白天") {
		t.Fatalf("texts = %q", got.texts)
	}
}

const marketItemsBody = `{"payload":{"items":[
 {"url_name":"fire","item_name":"火焰"},
 {"url_name":"flamethrower","item_name":"火焰喷射器"},
 {"url_name":"battery","item_name":"奥罗金电池"}
]}}`

const flamethrowerOrdersBody = `{"payload":{"orders":[
 {"order_type":"sell","platinum":20,"mod_rank":5,"user":{"ingame_name":"SellerA","status":"ingame"}},
 {"order_type":"sell","platinum":5,"mod_rank":3,"user":{"ingame_name":"SellerB","status":"ingame"}},
 {"order_type":"buy","platinum":1,"mod_rank":5,"user":{"ingame_name":"Buyer","status":"ingame"}},
 {"order_type":"sell","platinum":1,"mod_rank":5,"user":{"ingame_name":"Offline","status":"offline"}}
]},"include":{"item":{"items_in_set":[
 {"url_name":"flamethrower","mod_max_rank":5,"en":{"item_name":"Flame Thrower"}}
]}}}`

const batteryOrdersBody = `{"payload":{"orders":[
 {"order_type":"sell","platinum":15,"mod_rank":0,"user":{"ingame_name":"CellSeller","status":"ingame"}},
 {"order_type":"sell","platinum":10,"mod_rank":0,"user":{"ingame_name":"CheapSeller","status":"ingame"}}
]},"include":{"item":{"items_in_set":[
 {"url_name":"battery","mod_max_rank":0,"en":{"item_name":"Orokin Cell"}}
]}}}`

// stubMarket 把 warframe.market 接口指向测试服务器。
func stubMarket(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items":
			_, _ = w.Write([]byte(marketItemsBody))
		case "/items/flamethrower/orders":
			_, _ = w.Write([]byte(flamethrowerOrdersBody))
		case "/items/battery/orders":
			_, _ = w.Write([]byte(batteryOrdersBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	oldItems, oldOrders := marketItemsURL, marketOrdersURL
	marketItemsURL = server.URL + "/items"
	marketOrdersURL = server.URL + "/items/%s/orders?include=item"
	t.Cleanup(func() { marketItemsURL, marketOrdersURL = oldItems, oldOrders })
}

func TestWMMultipleResultsAndSelect(t *testing.T) {
	stubMarket(t)
	st := memState{}
	c := sdk.Call{State: st, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}

	got := runCall(t, c, ".wm 火焰")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "包含多个结果") ||
		!strings.Contains(got.texts[0], "[0] 火焰") || !strings.Contains(got.texts[0], "[1] 火焰喷射器") {
		t.Fatalf("texts = %q", got.texts)
	}

	got = runCall(t, c, "1")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "(Rank:5/5) 20P - SellerA") ||
		strings.Contains(got.texts[0], "SellerB") {
		t.Fatalf("order texts = %q", got.texts)
	}

	got = runCall(t, c, "0")
	want := "/w SellerA Hi! I want to buy: Flame Thrower (Rank:5) for 20 platinum. (warframe.market)"
	if len(got.texts) != 1 || got.texts[0] != want {
		t.Fatalf("whisper = %q", got.texts)
	}

	// 会话结束后普通消息应保持静默。
	if got = runCall(t, c, "0"); len(got.texts) != 0 {
		t.Fatalf("unexpected reply %q", got.texts)
	}
}

func TestWMSingleResultAndCancel(t *testing.T) {
	stubMarket(t)
	st := memState{}
	c := sdk.Call{State: st, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}

	got := runCall(t, c, ".wm 奥罗金电池")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "[0] 10P - CheapSeller") ||
		strings.Contains(got.texts[0], "Rank:") {
		t.Fatalf("texts = %q", got.texts)
	}

	got = runCall(t, c, "0")
	want := "/w CheapSeller Hi! I want to buy: Orokin Cell for 10 platinum. (warframe.market)"
	if len(got.texts) != 1 || got.texts[0] != want {
		t.Fatalf("whisper = %q", got.texts)
	}

	// 再次进入选择态后可用 c 结束会话。
	runCall(t, c, ".wm 奥罗金电池")
	got = runCall(t, c, "c")
	if len(got.texts) != 1 || got.texts[0] != "会话已结束!" {
		t.Fatalf("cancel = %q", got.texts)
	}
}

func TestWMNoResult(t *testing.T) {
	stubMarket(t)
	c := sdk.Call{State: memState{}, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}
	got := runCall(t, c, ".wm 不存在的东西")
	if len(got.texts) != 1 || got.texts[0] != "无法查询到该物品" {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "wf"); len(got.texts) != 0 {
		t.Fatalf("unexpected reply %q", got.texts)
	}
}

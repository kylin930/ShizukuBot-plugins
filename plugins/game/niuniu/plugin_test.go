package niuniu

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func call(t *testing.T, st sdk.State, group, user, message string) string {
	t.Helper()
	got := ""
	e := sdk.Event{GroupID: group, UserID: user, Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		}}
	if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
		t.Fatal(err)
	}
	return got
}

func fixedRandom(t *testing.T, value float64) {
	t.Helper()
	old := unit
	unit = func() float64 { return value }
	t.Cleanup(func() { unit = old })
}

func fixedClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	clock := start
	old := nowFunc
	nowFunc = func() time.Time { return clock }
	t.Cleanup(func() { nowFunc = old })
	return &clock
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.Manifest.Allows("state.write") || !d.Manifest.Allows("state.read") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestRegisterAndView(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)
	if got := call(t, st, "g", "u1", "注册牛牛"); !strings.Contains(got, "注册成功") {
		t.Fatalf("register = %q", got)
	}
	if got := call(t, st, "g", "u1", "查看我的牛牛"); !strings.Contains(got, "15.00cm") || !strings.Contains(got, "4.50cm") {
		t.Fatalf("view = %q", got)
	}
	if got := call(t, st, "g", "u1", "注册牛牛"); !strings.Contains(got, "已经注册") {
		t.Fatalf("second register = %q", got)
	}
	if got := call(t, st, "g", "u2", "查看我的牛牛"); !strings.Contains(got, "还没有牛牛") {
		t.Fatalf("unregistered view = %q", got)
	}
}

func TestGlueCooldown(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)

	call(t, st, "g", "u1", "注册牛牛")
	if got := call(t, st, "g", "u1", "打胶"); !strings.Contains(got, "增加了0.50cm") {
		t.Fatalf("glue = %q", got)
	}
	if got := call(t, st, "g", "u1", "打胶"); !strings.Contains(got, "休息") {
		t.Fatalf("cd = %q", got)
	}
	*clock = start.Add(90 * time.Second)
	if got := call(t, st, "g", "u1", "打胶"); !strings.Contains(got, "增加了0.50cm") {
		t.Fatalf("glue after cd = %q", got)
	}
}

func TestJJRankingAndRedeem(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)

	call(t, st, "g", "u1", "注册牛牛")
	call(t, st, "g", "u2", "注册牛牛")
	call(t, st, "g", "u1", "打胶") // u1 15.5cm > u2 15cm

	for i := 0; i < redeemCount; i++ {
		if i > 0 {
			*clock = clock.Add(jjCD + time.Second)
		}
		got := call(t, st, "g", "u1", "jj@u2")
		if !strings.Contains(got, "击败") {
			t.Fatalf("jj #%d = %q", i+1, got)
		}
		if i == redeemCount-1 && !strings.Contains(got, "赎牛牛") {
			t.Fatalf("final jj = %q", got)
		}
	}
	if got := call(t, st, "g", "u2", "查看我的牛牛"); !strings.Contains(got, "被夺走") {
		t.Fatalf("view seized = %q", got)
	}
	if got := call(t, st, "g", "u2", "赎牛牛"); !strings.Contains(got, "再次确认") || !strings.Contains(got, "【是|否】") {
		t.Fatalf("redeem confirm = %q", got)
	}
	if got := call(t, st, "g", "u1", "是"); got != "" {
		t.Fatalf("non-initiator reply should be ignored, got %q", got)
	}
	if got := call(t, st, "g", "u2", "是"); !strings.Contains(got, "成功赎回") {
		t.Fatalf("redeem = %q", got)
	}
	if got := call(t, st, "g", "u1", "牛子长度排行"); !strings.Contains(got, "1. u1") {
		t.Fatalf("ranking = %q", got)
	}
}

func TestSellAuctionAndShop(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)

	call(t, st, "g", "u1", "注册牛牛")
	if got := call(t, st, "g", "u1", "出售牛牛"); !strings.Contains(got, "商品序号1") {
		t.Fatalf("sell = %q", got)
	}
	if got := call(t, st, "g", "u2", "牛牛拍卖行"); !strings.Contains(got, "商品序号: 1") {
		t.Fatalf("auction = %q", got)
	}
	call(t, st, "g", "u3", "注册牛牛")
	if got := call(t, st, "g", "u3", "购买道具 1-2"); !strings.Contains(got, "伟哥") {
		t.Fatalf("buy prop = %q", got)
	}
	if got := call(t, st, "g", "u3", "牛牛背包"); !strings.Contains(got, "伟哥 x2") {
		t.Fatalf("bag = %q", got)
	}
	call(t, st, "g", "u3", "出售牛牛")
	if got := call(t, st, "g", "u3", "牛牛拍卖行"); !strings.Contains(got, "请输入对应序号") {
		t.Fatalf("auction should open session = %q", got)
	}
	if got := call(t, st, "g", "u3", "1"); !strings.Contains(got, "购买成功") {
		t.Fatalf("buy auction in session = %q", got)
	}
}

func TestShopSessionPurchase(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)
	call(t, st, "g", "u1", "注册牛牛")
	if got := call(t, st, "g", "u1", "牛牛商店"); !strings.Contains(got, "输入对应序号") {
		t.Fatalf("shop should open session = %q", got)
	}
	if got := call(t, st, "g", "u1", "1-2"); !strings.Contains(got, "购买成功") {
		t.Fatalf("session purchase = %q", got)
	}
	if got := call(t, st, "g", "u1", "牛牛背包"); !strings.Contains(got, "伟哥 x2") {
		t.Fatalf("bag = %q", got)
	}
}

func TestSessionExpiry(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)
	call(t, st, "g", "u1", "注册牛牛")
	call(t, st, "g", "u1", "牛牛商店")

	*clock = start.Add(sessionWindow + time.Second)
	if got := call(t, st, "g", "u1", "1-2"); !strings.Contains(got, "超时") {
		t.Fatalf("expired session = %q", got)
	}
	if got := call(t, st, "g", "u1", "牛牛背包"); !strings.Contains(got, "空空如也") {
		t.Fatalf("expired purchase should not happen: %q", got)
	}
}

func TestIgnoresPrivateAndUnknown(t *testing.T) {
	st := memState{}
	if got := call(t, st, "", "u1", "注册牛牛"); got != "" {
		t.Fatalf("private reply = %q", got)
	}
	if got := call(t, st, "g", "u1", "随便聊聊"); got != "" {
		t.Fatalf("unknown reply = %q", got)
	}
}

func callReply(t *testing.T, st sdk.State, group, user, message string) sdk.Reply {
	t.Helper()
	got := sdk.Reply{}
	e := sdk.Event{GroupID: group, UserID: user, Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r
			return nil
		}}
	if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestKeyboardButtons(t *testing.T) {
	st := memState{}
	fixedRandom(t, 0.5)

	got := callReply(t, st, "g", "u1", "注册牛牛")
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "打胶") ||
		!strings.Contains(got.Keyboard, "牛牛商店") || !strings.Contains(got.Keyboard, "牛牛背包") {
		t.Fatalf("register reply = %+v", got)
	}
	if got := callReply(t, st, "g", "u1", "牛牛商店"); !strings.Contains(got.Keyboard, "购买道具 1-1") {
		t.Fatalf("shop keyboard = %+v", got)
	}
	call(t, st, "g", "u2", "注册牛牛")
	call(t, st, "g", "u2", "出售牛牛")
	if got := callReply(t, st, "g", "u2", "牛牛拍卖行"); !strings.Contains(got.Keyboard, "购买牛牛 1") {
		t.Fatalf("auction keyboard = %+v", got)
	}
}

func seedSeizedPlayer(t *testing.T, st memState, group, user string, now time.Time) {
	t.Helper()
	g := game{Users: map[string]*player{
		user: {Length: 0, PKLength: 12.5, PKCount: redeemCount, PKTime: now.Unix()},
	}}
	if err := sdk.SaveJSON(context.Background(), st, "niu:"+group, g); err != nil {
		t.Fatal(err)
	}
}

func TestRedeemConfirmationFlow(t *testing.T) {
	st := memState{}
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)
	seedSeizedPlayer(t, st, "g", "u2", start)

	if got := call(t, st, "g", "u2", "赎牛牛"); !strings.Contains(got, "再次确认") || !strings.Contains(got, "12.50cm") {
		t.Fatalf("confirm prompt = %q", got)
	}
	if got := call(t, st, "g", "u2", "否"); !strings.Contains(got, "取消成功") {
		t.Fatalf("cancel confirm = %q", got)
	}

	if got := call(t, st, "g", "u2", "赎牛牛"); !strings.Contains(got, "再次确认") {
		t.Fatalf("second prompt = %q", got)
	}
	if got := call(t, st, "g", "u2", "是"); !strings.Contains(got, "成功赎回") || !strings.Contains(got, "12.50cm") {
		t.Fatalf("confirmed redeem = %q", got)
	}

	// 超时：2 分钟后回复「是」应自动取消，牛牛仍处于被夺走状态。
	seedSeizedPlayer(t, st, "g", "u2", start)
	call(t, st, "g", "u2", "赎牛牛")
	*clock = start.Add(redeemConfirmWindow + time.Second)
	if got := call(t, st, "g", "u2", "是"); !strings.Contains(got, "操作超时") {
		t.Fatalf("timeout = %q", got)
	}
	if got := call(t, st, "g", "u2", "查看我的牛牛"); !strings.Contains(got, "被夺走") {
		t.Fatalf("still seized = %q", got)
	}
}

func TestApplyCancelCharge(t *testing.T) {
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)

	p := &player{Coins: 500}
	record, fee, ok := applyCancelCharge(p, nil, start)
	if !ok || fee != 0 || record.Count != 1 || p.Coins != 500 {
		t.Fatalf("first cancel = %+v/%d/%v coins=%d", record, fee, ok, p.Coins)
	}

	record, fee, ok = applyCancelCharge(p, record, start.Add(time.Hour))
	if !ok || fee != 50 || record.Count != 2 || p.Coins != 450 {
		t.Fatalf("second cancel = %+v/%d/%v coins=%d", record, fee, ok, p.Coins)
	}

	record, fee, ok = applyCancelCharge(p, record, start.Add(2*time.Hour))
	if !ok || fee != 100 || record.Count != 3 || p.Coins != 350 {
		t.Fatalf("third cancel = %+v/%d/%v coins=%d", record, fee, ok, p.Coins)
	}

	poor := &player{Coins: 49}
	old := &cancelRecord{TimeLimit: start.Unix(), Count: 1}
	record, fee, ok = applyCancelCharge(poor, old, start.Add(time.Hour))
	if ok || fee != 50 || record != old || poor.Coins != 49 || record.Count != 1 {
		t.Fatalf("insufficient = %+v/%d/%v coins=%d", record, fee, ok, poor.Coins)
	}

	expired := &cancelRecord{TimeLimit: start.Unix(), Count: 5}
	record, fee, ok = applyCancelCharge(poor, expired, start.Add(cancelWindow+time.Hour))
	if !ok || fee != 0 || record.Count != 1 || record.TimeLimit != start.Add(cancelWindow+time.Hour).Unix() {
		t.Fatalf("expired window = %+v/%d/%v", record, fee, ok)
	}
}

func TestCancelInsufficientAndCharged(t *testing.T) {
	st := memState{}
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	fixedClock(t, start)
	c := sdk.Call{Event: sdk.Event{GroupID: "g", UserID: "u1"}, State: st}

	g := game{Users: map[string]*player{"u1": {Length: 10, Coins: 10}}}
	if err := sdk.SaveJSON(context.Background(), st, "niu:g", g); err != nil {
		t.Fatal(err)
	}
	if err := saveCancelRecord(context.Background(), st, c, &cancelRecord{TimeLimit: start.Unix(), Count: 1}); err != nil {
		t.Fatal(err)
	}
	if got := call(t, st, "g", "u1", "注销牛牛"); !strings.Contains(got, "钱不够") || !strings.Contains(got, "50") {
		t.Fatalf("insufficient cancel = %q", got)
	}
	loaded := load(context.Background(), c)
	if loaded.Users["u1"] == nil {
		t.Fatal("insufficient cancel should keep player")
	}

	loaded.Users["u1"].Coins = 100
	if err := sdk.SaveJSON(context.Background(), st, "niu:g", loaded); err != nil {
		t.Fatal(err)
	}
	if got := call(t, st, "g", "u1", "注销牛牛"); !strings.Contains(got, "已注销") {
		t.Fatalf("charged cancel = %q", got)
	}
	record := loadCancelRecord(context.Background(), st, c)
	if record == nil || record.Count != 2 {
		t.Fatalf("cancel record = %+v", record)
	}
	after := load(context.Background(), c)
	if player := after.Users["u1"]; player == nil || player.Coins != 50 || player.Length != 0 {
		t.Fatalf("cancel should charge 50 and keep wallet, got %+v", player)
	}
}

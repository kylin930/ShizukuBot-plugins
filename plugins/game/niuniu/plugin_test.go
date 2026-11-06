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
	if got := call(t, st, "g", "u2", "赎牛牛"); !strings.Contains(got, "成功赎回") {
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
	if got := call(t, st, "g", "u3", "购买牛牛 1"); !strings.Contains(got, "购买成功") {
		t.Fatalf("buy auction = %q", got)
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

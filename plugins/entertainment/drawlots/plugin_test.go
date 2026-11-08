package drawlots

import (
	"context"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// freeze 固定当前日期，返回恢复函数。
func freeze(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

// replyText 用 fake Reply 捕获插件回复的文本。
func replyText(t *testing.T, message string) string {
	t.Helper()
	return replyCall(t, sdk.Call{Event: sdk.Event{UserID: "u1", Message: message}})
}

// replyCall 用给定的 Call 执行一次 Handle，补齐 fake Reply 并返回回复文本。
func replyCall(t *testing.T, c sdk.Call) string {
	t.Helper()
	var got string
	c.Event.Reply = func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// adminCall 构造管理员身份的 Call。
func adminCall(message string) sdk.Call {
	return sdk.Call{
		Event:  sdk.Event{UserID: "admin", Message: message},
		Config: map[string]any{"admin_users": []string{"admin"}},
	}
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDrawLot(t *testing.T) {
	freeze(t, time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local))
	got := replyText(t, "抽事业签")
	if !strings.Contains(got, "【事业签·") {
		t.Fatalf("reply = %q", got)
	}
	if !strings.Contains(got, "签文：") || !strings.Contains(got, "解签：") {
		t.Fatalf("reply = %q", got)
	}
}

func TestDrawLotFixedPerUserAndDay(t *testing.T) {
	day := time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local)
	freeze(t, day)
	first := replyText(t, "抽事业签")
	second := replyText(t, "抽事业签")
	if first != second {
		t.Fatalf("same user same day changed: %q != %q", first, second)
	}
	// 直接验证索引函数：同一天同一用户固定，次日重新计算。
	l := lots[1]
	if dailyIndex("u1", len(l.Entries), day) != dailyIndex("u1", len(l.Entries), day) {
		t.Fatal("daily index is not stable")
	}
	next := day.Add(24 * time.Hour)
	if got := dailyIndex("u1", len(l.Entries), next); got < 0 || got >= len(l.Entries) {
		t.Fatalf("next day index = %d", got)
	}
}

func TestDrawAllLots(t *testing.T) {
	for _, l := range lots {
		got := replyText(t, "抽"+l.Name+"签")
		if !strings.Contains(got, "【"+l.Name+"签·") {
			t.Fatalf("lot %q reply = %q", l.Name, got)
		}
	}
}

func TestDrawUnknownLot(t *testing.T) {
	got := replyText(t, "抽不存在签")
	if !strings.Contains(got, "才...才没有不存在签这种东西啦") {
		t.Fatalf("reply = %q", got)
	}
}

func TestListLots(t *testing.T) {
	for _, trigger := range []string{"抽签列表", "刷新抽签列表"} {
		got := replyText(t, trigger)
		if !strings.Contains(got, "签 名 [ 类 型 ]----签数") {
			t.Fatalf("%q reply = %q", trigger, got)
		}
		if !strings.Contains(got, "事业[文本]----3") {
			t.Fatalf("%q reply = %q", trigger, got)
		}
	}
}

func TestViewLot(t *testing.T) {
	// 非管理员不能看签。
	if got := replyText(t, "看事业签"); !strings.Contains(got, "没有权限") {
		t.Fatalf("non-admin view reply = %q", got)
	}
	got := replyCall(t, adminCall("看事业签"))
	if !strings.Contains(got, "【事业签】共 3 签") {
		t.Fatalf("reply = %q", got)
	}
	if !strings.Contains(got, "1. [上吉] 舟行顺水，帆借东风") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyCall(t, adminCall("看不存在签")); !strings.Contains(got, "才...才没有不存在签这种东西啦") {
		t.Fatalf("reply = %q", got)
	}
}

func TestAddAndDeleteNotice(t *testing.T) {
	// 非管理员沿用统一权限提示。
	if got := replyText(t, "加大吉签"); !strings.Contains(got, "没有权限") {
		t.Fatalf("non-admin add reply = %q", got)
	}
	if got := replyText(t, "删大吉签"); !strings.Contains(got, "没有权限") {
		t.Fatalf("non-admin delete reply = %q", got)
	}

	// 管理员保留入口，但明确说明内置文本签不支持增删，不伪造成功。
	if got := replyCall(t, adminCall("加大吉签")); !strings.Contains(got, "暂不支持添加自定义签文") || strings.Contains(got, "成功！") {
		t.Fatalf("admin add reply = %q", got)
	}
	if got := replyCall(t, adminCall("删事业签")); !strings.Contains(got, "暂不支持删除签文") || strings.Contains(got, "成功！") {
		t.Fatalf("admin delete reply = %q", got)
	}
	if got := replyCall(t, adminCall("删不存在签")); !strings.Contains(got, "才...才没有不存在签这种东西啦") {
		t.Fatalf("admin delete unknown reply = %q", got)
	}
	if got := replyCall(t, adminCall("加签")); !strings.Contains(got, "请使用正确的指令形式哦~") {
		t.Fatalf("admin add empty reply = %q", got)
	}
}

func TestNonMatch(t *testing.T) {
	for _, message := range []string{"抽签", "看签", "签", "今天天气不错"} {
		if got := replyText(t, message); got != "" {
			t.Fatalf("%q reply = %q", message, got)
		}
	}
}

func TestLotsNotEmpty(t *testing.T) {
	for _, l := range lots {
		if len(l.Entries) == 0 {
			t.Fatalf("lot %q has no entries", l.Name)
		}
	}
}

package curse

import (
	"context"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

func reply(t *testing.T, message string) sdk.Reply {
	t.Helper()
	return replyAs(t, "u1", message)
}

func replyAs(t *testing.T, userID, message string) sdk.Reply {
	t.Helper()
	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{UserID: userID, Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func noSleep(t *testing.T) {
	t.Helper()
	old := sleepFunc
	sleepFunc = func(context.Context) error { return nil }
	t.Cleanup(func() { sleepFunc = old })
}

func resetLimits(t *testing.T) {
	t.Helper()
	limitMu.Lock()
	limitHits = map[string][]time.Time{}
	limitMu.Unlock()
	t.Cleanup(func() {
		limitMu.Lock()
		limitHits = map[string][]time.Time{}
		limitMu.Unlock()
	})
}

func TestExplicitTriggers(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()
	noSleep(t)
	resetLimits(t)

	got := reply(t, "骂我")
	if got.Text != minLevel[0] {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "骂我") {
		t.Fatalf("kind = %q, keyboard = %q", got.Kind, got.Keyboard)
	}
	got = reply(t, "大力骂我")
	if got.Text != maxLevel[0] {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "大力骂我") {
		t.Fatalf("kind = %q, keyboard = %q", got.Kind, got.Keyboard)
	}
}

// TestUserRateLimit 显式触发按用户限频 5 次/10 秒，超出静默。
func TestUserRateLimit(t *testing.T) {
	resetLimits(t)
	noSleep(t)

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	for i := 0; i < limitBurst; i++ {
		if got := replyAs(t, "u1", "骂我"); got.Text == "" {
			t.Fatalf("第 %d 次被错误限频", i+1)
		}
	}
	if got := replyAs(t, "u1", "骂我"); got.Text != "" {
		t.Fatalf("超出限频仍回复 %q", got.Text)
	}
	if got := replyAs(t, "u2", "骂我"); got.Text == "" {
		t.Fatal("限频不应影响其他用户")
	}
	now = now.Add(limitWindow)
	if got := replyAs(t, "u1", "骂我"); got.Text == "" {
		t.Fatal("等待窗口后应恢复")
	}
}

// TestDelayOnlyForExplicit 只有「骂我」「大力骂我」带 1–2 秒延迟。
func TestDelayOnlyForExplicit(t *testing.T) {
	resetLimits(t)

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	calls := 0
	oldSleep := sleepFunc
	sleepFunc = func(context.Context) error {
		calls++
		return nil
	}
	t.Cleanup(func() { sleepFunc = oldSleep })

	reply(t, "骂我")
	if calls != 1 {
		t.Fatalf("骂我 sleep = %d", calls)
	}
	reply(t, "<@10000> 你妈")
	if calls != 1 {
		t.Fatalf("关键词回击不应 sleep，calls = %d", calls)
	}
}

func TestMentionedKeyword(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	got := reply(t, "<@10000> 你妈")
	if got.Text != maxLevel[0] {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "text" || got.Keyboard != "" {
		t.Fatalf("kind = %q, keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestKeywordWithoutMentionIgnored(t *testing.T) {
	if got := reply(t, "你妈"); got.Text != "" {
		t.Fatalf("unexpected reply %q", got.Text)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	for _, message := range []string{"你好", "骂", "大力", "今天天气不错"} {
		if got := reply(t, message); got.Text != "" {
			t.Fatalf("%q -> unexpected reply %q", message, got.Text)
		}
	}
}

func TestListsArePopulated(t *testing.T) {
	if len(minLevel) == 0 || len(maxLevel) == 0 {
		t.Fatal("empty curse lists")
	}
	for _, line := range append(append([]string{}, minLevel...), maxLevel...) {
		if strings.TrimSpace(line) == "" {
			t.Fatal("blank curse line")
		}
	}
}

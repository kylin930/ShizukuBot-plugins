package fortune

import (
	"context"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

func freeze(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func replyText(t *testing.T, event sdk.Event) string {
	t.Helper()
	var got string
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSameUserSameDay(t *testing.T) {
	freeze(t, time.Date(2026, 11, 7, 9, 30, 0, 0, time.Local))
	first := replyText(t, sdk.Event{UserID: "u1", Nickname: "小明", Message: "运势"})
	second := replyText(t, sdk.Event{UserID: "u1", Nickname: "小明", Message: "今日运势"})
	if first == "" {
		t.Fatal("empty reply")
	}
	if first != second {
		t.Fatalf("first = %q, second = %q", first, second)
	}
}

func TestFortuneUsesOracleText(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.Local)
	freeze(t, at)
	got := replyText(t, sdk.Event{UserID: "u1", Nickname: "小明", Message: "运势"})
	o := draw("u1", at)
	if !strings.Contains(got, "【"+o.Title+"】") {
		t.Fatalf("reply %q missing title %q", got, o.Title)
	}
	if !strings.Contains(got, o.Content) {
		t.Fatalf("reply %q missing content %q", got, o.Content)
	}
	if strings.Contains(got, "评分：") || strings.Contains(got, "幸运物：") {
		t.Fatalf("reply still uses old score model: %q", got)
	}
}

func TestTriggerVariants(t *testing.T) {
	freeze(t, time.Date(2026, 11, 7, 9, 30, 0, 0, time.Local))
	for _, msg := range []string{"运势", "今日运势", "抽签", "今日运势帮我看看"} {
		if got := replyText(t, sdk.Event{UserID: "u1", Message: msg}); got == "" {
			t.Fatalf("%q got empty reply", msg)
		}
	}
}

func TestNonMatch(t *testing.T) {
	freeze(t, time.Date(2026, 11, 7, 9, 30, 0, 0, time.Local))
	if got := replyText(t, sdk.Event{UserID: "u1", Message: "今天运势如何"}); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestOracleData(t *testing.T) {
	if len(oracles) < 10 {
		t.Fatalf("only %d oracle entries", len(oracles))
	}
	for i, o := range oracles {
		if strings.TrimSpace(o.Title) == "" || strings.TrimSpace(o.Content) == "" {
			t.Fatalf("entry %d is incomplete: %+v", i, o)
		}
	}
}

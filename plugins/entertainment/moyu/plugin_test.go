package moyu

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

func reply(t *testing.T, message string) (string, bool) {
	t.Helper()
	got, replied := "", false
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkday(t *testing.T) {
	freeze(t, time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)) // 周五
	got, replied := reply(t, "摸鱼提醒")
	if !replied {
		t.Fatal("no reply")
	}
	for _, want := range []string{"2026-11-06", "上午好，摸鱼人！", "距离周末还有:0天！"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply missing %q: %q", want, got)
		}
	}
}

func TestWeekend(t *testing.T) {
	freeze(t, time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)) // 周六
	got, _ := reply(t, "摸鱼提醒")
	if !strings.Contains(got, "好好享受周末吧！") {
		t.Fatalf("reply = %q", got)
	}
}

func TestNonMatch(t *testing.T) {
	if _, replied := reply(t, "提醒我摸鱼"); replied {
		t.Fatal("unexpected reply")
	}
}

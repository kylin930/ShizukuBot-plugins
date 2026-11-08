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

func TestHolidayCountdowns(t *testing.T) {
	freeze(t, time.Date(2026, 11, 8, 10, 0, 0, 0, time.Local))
	got, replied := reply(t, "摸鱼提醒")
	if !replied {
		t.Fatal("no reply")
	}
	for _, name := range []string{"元旦", "春节", "清明节", "劳动节", "端午节", "中秋节", "国庆节"} {
		if !strings.Contains(got, "距离"+name+"还有: ") {
			t.Fatalf("reply missing %s countdown: %q", name, got)
		}
	}
	// 2026-11-08 时最近的节日是 2027 元旦。
	if !strings.Contains(got, "距离元旦还有: ") {
		t.Fatalf("reply missing 元旦 countdown: %q", got)
	}
}

func TestHolidayStates(t *testing.T) {
	freeze(t, time.Date(2026, 12, 31, 12, 0, 0, 0, time.Local))
	if got := holidayText("元旦", nowFunc()); !strings.Contains(got, "距离元旦还有: 0.50天！") {
		t.Fatalf("countdown = %q", got)
	}

	freeze(t, time.Date(2027, 1, 2, 12, 0, 0, 0, time.Local))
	if got := holidayText("元旦", nowFunc()); got != "好好享受 元旦 假期吧!" {
		t.Fatalf("during holiday = %q", got)
	}

	freeze(t, time.Date(2027, 12, 31, 12, 0, 0, 0, time.Local))
	if got := holidayText("元旦", nowFunc()); got != "今年 元旦 假期已过" {
		t.Fatalf("after holiday = %q", got)
	}
}

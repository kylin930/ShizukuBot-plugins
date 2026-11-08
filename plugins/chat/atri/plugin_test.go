package atri

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

func setClock(hour int) func() {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 7, hour, 0, 0, 0, time.Local) }
	return func() { nowFunc = old }
}
func fixedPick() func() {
	old, oldChance := pickIndex, chanceIndex
	pickIndex = func(int) int { return 0 }
	chanceIndex = func(int) int { return 0 }
	return func() { pickIndex, chanceIndex = old, oldChance }
}

func replyOf(t *testing.T, event sdk.Event) string {
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

func atEvent(message string) sdk.Event {
	raw, _ := json.Marshal(map[string]any{"mentions": []map[string]any{{"is_you": true}}})
	return sdk.Event{Message: message, Raw: raw}
}

func TestGreetings(t *testing.T) {
	restorePick := fixedPick()
	defer restorePick()

	restore := setClock(8)
	if got := replyOf(t, sdk.Event{Message: "早上好"}); !strings.Contains(got, "早上好") {
		t.Fatalf("morning = %q", got)
	}
	restore()

	restore = setClock(12)
	if got := replyOf(t, sdk.Event{Message: "中午好"}); !strings.Contains(got, "午安") {
		t.Fatalf("noon = %q", got)
	}
	restore()

	restore = setClock(20)
	defer restore()
	if got := replyOf(t, sdk.Event{Message: "晚安"}); got == "" {
		t.Fatal("night reply empty")
	}
}

func TestBasicReplies(t *testing.T) {
	restore := fixedPick()
	defer restore()
	if got := replyOf(t, sdk.Event{Message: "萝卜子"}); got == "" {
		t.Fatal("empty robo reply")
	}
	if got := replyOf(t, sdk.Event{Message: "我好了"}); got != "不许好！" {
		t.Fatalf("reply = %q", got)
	}
	if got := replyOf(t, sdk.Event{Message: "？"}); got != "?" {
		t.Fatalf("reply = %q", got)
	}
	if got := replyOf(t, sdk.Event{Message: "这也太离谱了"}); got != "?" {
		t.Fatalf("reply = %q", got)
	}
}

func TestMentionOnlyTriggers(t *testing.T) {
	restore := fixedPick()
	defer restore()
	if got := replyOf(t, sdk.Event{Message: "高性能"}); got != "" {
		t.Fatalf("without mention got %q", got)
	}
	if got := replyOf(t, atEvent("高性能")); !strings.Contains(got, "高性能") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyOf(t, atEvent("草你妈")); got == "" {
		t.Fatal("insult reply empty")
	}
	if got := replyOf(t, atEvent("答应我")); got != "我无法回应你的请求" {
		t.Fatalf("reply = %q", got)
	}
}

// TestKeywordProbabilities 校验「离谱」3/5、「啊这」1/2 的触发概率。
func TestKeywordProbabilities(t *testing.T) {
	restorePick := fixedPick()
	defer restorePick()
	restoreClock := setClock(12)
	defer restoreClock()

	old := chanceIndex
	defer func() { chanceIndex = old }()

	chanceIndex = func(int) int { return 3 }
	if got := replyOf(t, sdk.Event{Message: "离谱"}); got != "" {
		t.Fatalf("离谱 should stay silent at 4/5, got %q", got)
	}
	chanceIndex = func(int) int { return 2 }
	if got := replyOf(t, sdk.Event{Message: "离谱"}); got == "" {
		t.Fatal("离谱 should reply at 3/5")
	}

	chanceIndex = func(int) int { return 1 }
	if got := replyOf(t, sdk.Event{Message: "啊这"}); got != "" {
		t.Fatalf("啊这 should stay silent at 1/2, got %q", got)
	}
	chanceIndex = func(int) int { return 0 }
	if got := replyOf(t, sdk.Event{Message: "啊这"}); got == "" {
		t.Fatal("啊这 should reply at 1/2")
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := replyOf(t, sdk.Event{Message: "今天天气不错"}); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestSleepWindowSilencesAllTriggers(t *testing.T) {
	restorePick := fixedPick()
	defer restorePick()

	for _, hour := range []int{1, 3, 5} {
		restore := setClock(hour)
		for _, message := range []string{"早上好", "晚安", "萝卜子", "我好了", "？", "离谱"} {
			if got := replyOf(t, atEvent(message)); got != "" {
				restore()
				t.Fatalf("hour %d message %q replied %q", hour, message, got)
			}
		}
		restore()
	}

	// 0 点与 6 点属于清醒时段，仍会回复。
	restore := setClock(0)
	if got := replyOf(t, sdk.Event{Message: "早上好"}); got == "" {
		restore()
		t.Fatal("hour 0 should still reply")
	}
	restore()

	restore = setClock(6)
	defer restore()
	if got := replyOf(t, sdk.Event{Message: "早上好"}); got == "" {
		t.Fatal("hour 6 should still reply")
	}
}

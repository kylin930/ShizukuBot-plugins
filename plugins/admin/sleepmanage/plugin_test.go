package sleepmanage

import (
	"context"
	"encoding/json"
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

func call(t *testing.T, st sdk.State, event sdk.Event) (string, bool) {
	t.Helper()
	got, replied := "", false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, State: st}); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func freeze(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func at(d time.Duration) time.Time {
	return time.Date(2026, 11, 6, 21, 0, 0, 0, time.Local).Add(d)
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.Manifest.Allows("state.write") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestGoodNightAndGoodMorning(t *testing.T) {
	st := memState{}

	freeze(t, at(2*time.Hour)) // 23:00
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "晚安"}); got != "晚安成功！你是今天第1个睡觉的" {
		t.Fatalf("首次晚安 = %q", got)
	}

	freeze(t, at(2*time.Hour+10*time.Minute)) // 23:10
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u2", Message: "晚安"}); got != "晚安成功！你是今天第2个睡觉的" {
		t.Fatalf("第二人晚安 = %q", got)
	}

	freeze(t, at(2*time.Hour+20*time.Minute)) // 23:20
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "晚安"}); got != "晚安成功！你的清醒时长为0时20分0秒,你是今天第2个睡觉的" {
		t.Fatalf("再次晚安 = %q", got)
	}

	freeze(t, time.Date(2026, 11, 7, 7, 0, 0, 0, time.Local))
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "早安"}); got != "早安成功！你的睡眠时长为7时40分0秒,你是今天第1个起床的" {
		t.Fatalf("早安 = %q", got)
	}

	freeze(t, time.Date(2026, 11, 7, 7, 10, 0, 0, time.Local))
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u2", Message: "早安"}); got != "早安成功！你的睡眠时长为8时0分0秒,你是今天第2个起床的" {
		t.Fatalf("第二人早安 = %q", got)
	}
}

func TestFirstMorningNoDuration(t *testing.T) {
	st := memState{}
	freeze(t, time.Date(2026, 11, 7, 8, 0, 0, 0, time.Local))
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "早安"}); got != "早安成功！你是今天第1个起床的" {
		t.Fatalf("reply = %q", got)
	}
}

func TestTimeWindows(t *testing.T) {
	st := memState{}
	freeze(t, time.Date(2026, 11, 6, 20, 0, 0, 0, time.Local))
	if _, replied := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "早安"}); replied {
		t.Fatal("早安 outside window replied")
	}
	freeze(t, time.Date(2026, 11, 6, 13, 0, 0, 0, time.Local))
	if _, replied := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "晚安"}); replied {
		t.Fatal("晚安 outside window replied")
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "", UserID: "u1", Message: "晚安"}); replied {
		t.Fatal("private message replied")
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "晚安啦"}); replied {
		t.Fatal("non-exact message replied")
	}
}

func TestEarlyMorningNightUsePreviousDayWindow(t *testing.T) {
	st := memState{}
	freeze(t, time.Date(2026, 11, 6, 23, 0, 0, 0, time.Local))
	call(t, st, sdk.Event{GroupID: "g", UserID: "u1", Message: "晚安"})
	freeze(t, time.Date(2026, 11, 7, 1, 0, 0, 0, time.Local))
	if got, _ := call(t, st, sdk.Event{GroupID: "g", UserID: "u2", Message: "晚安"}); got != "晚安成功！你是今天第2个睡觉的" {
		t.Fatalf("reply = %q", got)
	}
}

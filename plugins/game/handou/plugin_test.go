package handou

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func fixedPick(t *testing.T, value int) {
	t.Helper()
	old := pick
	pick = func(int) int { return value }
	t.Cleanup(func() { pick = old })
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
	if !d.Manifest.Allows("state.write") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestIdiomTable(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range idioms {
		if utf8.RuneCountInString(v.Word) != 4 {
			t.Fatalf("word %q is not four characters", v.Word)
		}
		if len(v.Pinyin) != 4 {
			t.Fatalf("pinyin of %q has %d parts", v.Word, len(v.Pinyin))
		}
		if v.Explain == "" {
			t.Fatalf("missing explanation for %q", v.Word)
		}
		if seen[v.Word] {
			t.Fatalf("duplicate idiom %q", v.Word)
		}
		seen[v.Word] = true
	}
	if len(idioms) < 60 {
		t.Fatalf("idiom table too small: %d", len(idioms))
	}
}

func TestFeedback(t *testing.T) {
	check := func(target, guess, want string) {
		t.Helper()
		if got := markText(feedback([]rune(target), []rune(guess))); got != want {
			t.Fatalf("feedback(%s, %s) = %s, want %s", target, guess, got, want)
		}
	}
	check("一心一意", "一心一意", "对 对 对 对")
	check("一心一意", "三心二意", "无 对 无 对")
	check("一心一意", "一一一一", "对 无 对 无")
}

func TestStartAndWin(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	got := call(t, st, "g", "u1", "团队猜成语")
	if !strings.Contains(got, "7次机会") || !strings.Contains(got, "首字拼音为：yi") {
		t.Fatalf("start = %q", got)
	}
	if got := call(t, st, "g", "u1", "一心一意"); !strings.Contains(got, "太棒了") || !strings.Contains(got, "解释") {
		t.Fatalf("win = %q", got)
	}
	if got := call(t, st, "g", "u1", "一心一意"); got != "" {
		t.Fatalf("finished game replied %q", got)
	}
}

func TestUnknownRepeatAndRunOut(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	call(t, st, "g", "u1", "团队猜成语")

	if got := call(t, st, "g", "u1", "天天向上"); !strings.Contains(got, "确定存在") {
		t.Fatalf("unknown = %q", got)
	}
	if got := call(t, st, "g", "u1", "三心二意"); !strings.Contains(got, "剩余次数: 6") {
		t.Fatalf("first guess = %q", got)
	}
	if got := call(t, st, "g", "u1", "三心二意"); !strings.Contains(got, "已经猜过") {
		t.Fatalf("repeat = %q", got)
	}

	var got string
	for _, w := range []string{"画蛇添足", "守株待兔", "亡羊补牢", "掩耳盗铃", "刻舟求剑", "杯弓蛇影"} {
		got = call(t, st, "g", "u1", w)
	}
	if !strings.Contains(got, "游戏结束") || !strings.Contains(got, "一心一意") {
		t.Fatalf("run out = %q", got)
	}
}

func TestPersonalModeAndTimeout(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)

	call(t, st, "g", "u1", "个人猜成语")
	if got := call(t, st, "g", "u2", "一心一意"); got != "" {
		t.Fatalf("other user replied %q", got)
	}
	*clock = start.Add(3 * time.Minute)
	if got := call(t, st, "g", "u1", "一心一意"); !strings.Contains(got, "超时") || !strings.Contains(got, "一心一意") {
		t.Fatalf("timeout = %q", got)
	}
}

func TestStartValidation(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	call(t, st, "g", "u1", "团队猜成语")
	if got := call(t, st, "g", "u1", "个人猜成语"); !strings.Contains(got, "已经有正在进行") {
		t.Fatalf("second start = %q", got)
	}
	if got := call(t, st, "g", "u1", "你好"); got != "" {
		t.Fatalf("chat replied %q", got)
	}
	if got := call(t, st, "", "u1", "团队猜成语"); got != "" {
		t.Fatalf("private start = %q", got)
	}
}

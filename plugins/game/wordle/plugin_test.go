package wordle

import (
	"context"
	"encoding/json"
	"regexp"
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

func TestWordList(t *testing.T) {
	seen := map[string]bool{}
	total := 0
	for _, list := range [][]string{words5, words6, words7} {
		for _, w := range list {
			if !regexp.MustCompile(`^[a-z]{5,7}$`).MatchString(w) {
				t.Fatalf("bad word %q", w)
			}
			if seen[w] {
				t.Fatalf("duplicate word %q", w)
			}
			seen[w] = true
			total++
		}
	}
	if total < 200 {
		t.Fatalf("word list has only %d words", total)
	}
}

func TestFeedback(t *testing.T) {
	check := func(target, guess string, want string) {
		t.Helper()
		if got := spaced(marks(feedback(target, guess))); got != want {
			t.Fatalf("feedback(%s, %s) = %s, want %s", target, guess, got, want)
		}
	}
	check("about", "about", "√ √ √ √ √")
	check("crane", "nacre", "△ △ △ △ √")
	check("about", "aaaaa", "√ × × × ×")
}

func TestStartAndWin(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	got := call(t, st, "g", "u1", "团队猜单词")
	if !strings.Contains(got, "6次机会") || !strings.Contains(got, "长度为5") {
		t.Fatalf("start = %q", got)
	}
	if got := call(t, st, "g", "u1", "about"); !strings.Contains(got, "太棒了") {
		t.Fatalf("win = %q", got)
	}
	if got := call(t, st, "g", "u1", "about"); got != "" {
		t.Fatalf("finished game replied %q", got)
	}
}

func TestWrongGuessAndRunOut(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)

	call(t, st, "g", "u1", "团队猜单词")
	got := call(t, st, "g", "u1", "above")
	if !strings.Contains(got, "剩余次数: 5") || !strings.Contains(got, "√") {
		t.Fatalf("wrong guess = %q", got)
	}
	for _, w := range []string{"actor", "adapt", "admit", "adopt", "adult"} {
		got = call(t, st, "g", "u1", w)
	}
	if !strings.Contains(got, "游戏结束") {
		t.Fatalf("run out = %q", got)
	}
}

func TestPersonalModeAndTimeout(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	clock := fixedClock(t, start)

	call(t, st, "g", "u1", "个人猜单词")
	if got := call(t, st, "g", "u2", "about"); got != "" {
		t.Fatalf("other user replied %q", got)
	}
	*clock = start.Add(3 * time.Minute)
	if got := call(t, st, "g", "u1", "about"); !strings.Contains(got, "超时") || !strings.Contains(got, "about") {
		t.Fatalf("timeout = %q", got)
	}
}

func TestStartValidation(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	call(t, st, "g", "u1", "团队猜单词")
	if got := call(t, st, "g", "u1", "个人猜单词"); !strings.Contains(got, "已经有正在进行") {
		t.Fatalf("second start = %q", got)
	}
	if got := call(t, st, "g", "u1", "animal"); !strings.Contains(got, "长度错误") {
		t.Fatalf("length = %q", got)
	}
	if got := call(t, st, "", "u1", "团队猜单词"); got != "" {
		t.Fatalf("private start = %q", got)
	}
}

package wordle

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// TestMain 默认关闭后台提醒，避免测试结束后仍有计时器触发。
func TestMain(m *testing.M) {
	newReminder = func(time.Duration, func()) *time.Timer { return nil }
	os.Exit(m.Run())
}

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

// TestDictionaries 校验 CET-4 题库与 dict 词典均已按阶内嵌且互相覆盖。
func TestDictionaries(t *testing.T) {
	for _, n := range []int{5, 6, 7} {
		words := wordsOfLength(n)
		dict := dictOfLength(n)
		if len(words) < 500 {
			t.Fatalf("%d 阶题库过小: %d", n, len(words))
		}
		if len(dict) < len(words) {
			t.Fatalf("%d 阶词典过小: %d", n, len(dict))
		}
		for _, w := range words {
			if !inDict(n, w) {
				t.Fatalf("题库单词 %q 不在 %d 阶词典中", w, n)
			}
		}
	}
}

// TestGuessMustBeInDict 校验不存在于词典的单词不会消耗次数。
func TestGuessMustBeInDict(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	call(t, st, "g", "u1", "团队猜单词")
	if got := call(t, st, "g", "u1", "zzzzz"); !strings.Contains(got, "你确定存在这样的单词吗") {
		t.Fatalf("unknown word = %q", got)
	}
	if got := call(t, st, "g", "u1", "above"); !strings.Contains(got, "剩余次数: 5") {
		t.Fatalf("valid wrong guess = %q", got)
	}
}

// TestReminder 校验 105 秒提醒的延迟、文案以及结束后不再提醒。
func TestReminder(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	fixedClock(t, time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local))

	var delay time.Duration
	var fire func()
	old := newReminder
	newReminder = func(d time.Duration, f func()) *time.Timer {
		delay, fire = d, f
		return nil
	}
	t.Cleanup(func() { newReminder = old })

	var replies []string
	send := func(message string) {
		e := sdk.Event{GroupID: "g", UserID: "u1", Message: message,
			Reply: func(_ context.Context, r sdk.Reply) error {
				replies = append(replies, r.Text)
				return nil
			}}
		if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
			t.Fatal(err)
		}
	}

	send("团队猜单词")
	if delay != 105*time.Second || fire == nil {
		t.Fatalf("reminder delay = %v, fire nil = %v", delay, fire == nil)
	}
	fire()
	if len(replies) != 2 || !strings.Contains(replies[1], "你还有15s作答时间") {
		t.Fatalf("reminder replies = %q", replies)
	}

	send("about")
	before := len(replies)
	fire()
	if len(replies) != before {
		t.Fatalf("stale reminder should be ignored: %q", replies[before:])
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

func callReply(t *testing.T, st sdk.State, group, user, message string) sdk.Reply {
	t.Helper()
	got := sdk.Reply{}
	e := sdk.Event{GroupID: group, UserID: user, Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r
			return nil
		}}
	if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRestartButtons(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)

	got := callReply(t, st, "g", "u1", "团队猜单词")
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "再来一局") ||
		!strings.Contains(got.Keyboard, "结束猜单词") {
		t.Fatalf("start reply = %+v", got)
	}
	call(t, st, "g", "u1", "about")
	if got := call(t, st, "g", "u1", "再来一局"); !strings.Contains(got, "6次机会") {
		t.Fatalf("restart = %q", got)
	}
}

func TestRestartPersonalOwner(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)

	call(t, st, "g", "u1", "个人七阶猜单词")
	call(t, st, "g", "u1", "结束")
	if got := call(t, st, "g", "u2", "再来一局"); got != "" {
		t.Fatalf("other user restarted: %q", got)
	}
	if got := call(t, st, "g", "u1", "再来一局"); !strings.Contains(got, "长度为7") {
		t.Fatalf("owner restart = %q", got)
	}
}

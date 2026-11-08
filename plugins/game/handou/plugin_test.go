package handou

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"shizukubot/sdk"
)

// TestMain 让所有测试默认离线：词库下载失败时回退内置词库。
func TestMain(m *testing.M) {
	fetchIdiomJSON = func(context.Context) ([]byte, error) { return nil, errors.New("offline") }
	newReminder = func(time.Duration, func()) *time.Timer { return nil }
	os.Exit(m.Run())
}

// useExternalIdioms 注入一份临时词库并重置惰性加载标记。
func useExternalIdioms(t *testing.T, raw string) {
	t.Helper()
	oldFetch, oldTried, oldCache, oldKeys, oldDetails := fetchIdiomJSON, idiomTried, idiomCache, idiomKeys, idiomDetails
	fetchIdiomJSON = func(context.Context) ([]byte, error) { return []byte(raw), nil }
	idiomMu.Lock()
	idiomTried, idiomCache, idiomKeys, idiomDetails = false, nil, nil, nil
	idiomMu.Unlock()
	t.Cleanup(func() {
		idiomMu.Lock()
		fetchIdiomJSON, idiomTried, idiomCache, idiomKeys, idiomDetails = oldFetch, oldTried, oldCache, oldKeys, oldDetails
		idiomMu.Unlock()
	})
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

// TestExternalIdiomLoad 验证惰性下载的词库能识别内置表之外的成语。
func TestExternalIdiomLoad(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	useExternalIdioms(t, `{
		"百发百中":{"word":"百发百中","chars":["百","发","百","中"],"pinyin":["bai","fa","bai","zhong"],"baobian":"褒义","explanation":"形容射箭或射击非常准。","derivation":"《战国策·西周策》","synonyms":["百步穿杨","弹无虚发"]},
		"坏数据":{"word":"坏数据","chars":["坏","数"],"pinyin":["huai"],"explanation":"pinyin 长度不一致应被过滤"}
	}`)

	got := call(t, st, "g", "u1", "团队猜成语")
	if !strings.Contains(got, "首字拼音为：bai") {
		t.Fatalf("start = %q", got)
	}
	got = call(t, st, "g", "u1", "百发百中")
	for _, want := range []string{"太棒了", "百发百中", "褒义词", "词源", "《战国策·西周策》", "近义词", "百步穿杨"} {
		if !strings.Contains(got, want) {
			t.Fatalf("external idiom answer %q missing %q", got, want)
		}
	}
	if _, ok := lookup("坏数据"); ok {
		t.Fatal("invalid record should be filtered")
	}
}

// TestIdiomFallback 验证下载失败时仍能使用内置词库开局。
func TestIdiomFallback(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	got := call(t, st, "g", "u1", "团队猜成语")
	if !strings.Contains(got, "首字拼音为：yi") {
		t.Fatalf("fallback start = %q", got)
	}
}

// TestHotStats 验证热门汉字/热门成语 Top10 基于猜词统计输出。
func TestHotStats(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	call(t, st, "g", "u1", "团队猜成语")
	call(t, st, "g", "u1", "三心二意")
	call(t, st, "g", "u1", "一心一意")

	got := call(t, st, "g", "u1", "猜成语热门汉字")
	if !strings.Contains(got, "热门汉字") || !strings.Contains(got, "一:2") || !strings.Contains(got, "心:2") {
		t.Fatalf("hot chars = %q", got)
	}
	got = call(t, st, "g", "u1", "猜成语热门成语")
	if !strings.Contains(got, "热门成语") || !strings.Contains(got, "一心一意:1") || !strings.Contains(got, "三心二意:1") {
		t.Fatalf("hot idioms = %q", got)
	}
}

// TestHotStatsEmpty 无统计时给出明确提示。
func TestHotStatsEmpty(t *testing.T) {
	st := memState{}
	if got := call(t, st, "g", "u1", "猜成语热门成语"); !strings.Contains(got, "还没有统计数据") {
		t.Fatalf("empty hot = %q", got)
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

// TestReminder 校验 105 秒提醒的延迟、文案以及结束后不再提醒。
func TestReminder(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	fixedClock(t, start)

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

	send("团队猜成语")
	if delay != 105*time.Second || fire == nil {
		t.Fatalf("reminder delay = %v, fire nil = %v", delay, fire == nil)
	}
	fire()
	if len(replies) != 2 || !strings.Contains(replies[1], "你还有15s作答时间") {
		t.Fatalf("reminder replies = %q", replies)
	}

	send("一心一意")
	before := len(replies)
	fire()
	if len(replies) != before {
		t.Fatalf("stale reminder should be ignored: %q", replies[before:])
	}
}

func TestRestartButtons(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)

	got := callReply(t, st, "g", "u1", "团队猜成语")
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "再来一局") ||
		!strings.Contains(got.Keyboard, "结束猜成语") {
		t.Fatalf("start reply = %+v", got)
	}
	call(t, st, "g", "u1", "一心一意")
	if got := call(t, st, "g", "u1", "再来一局"); !strings.Contains(got, "7次机会") {
		t.Fatalf("restart = %q", got)
	}
}

func TestRestartPersonalOwner(t *testing.T) {
	st := memState{}
	fixedPick(t, 0)

	call(t, st, "g", "u1", "个人猜成语")
	call(t, st, "g", "u1", "结束")
	if got := call(t, st, "g", "u2", "再来一局"); got != "" {
		t.Fatalf("other user restarted: %q", got)
	}
	if got := call(t, st, "g", "u1", "再来一局"); !strings.Contains(got, "7次机会") {
		t.Fatalf("owner restart = %q", got)
	}
}

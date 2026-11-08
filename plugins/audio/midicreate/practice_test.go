package midicreate

import (
	"strconv"
	"strings"
	"testing"
)

func sessionOf(t *testing.T, key string) *practiceSession {
	t.Helper()
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return sessions[key]
}

func scoreOf(t *testing.T, s *practiceSession, uid string) float64 {
	t.Helper()
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return s.scores[uid]
}

func requireText(t *testing.T, rec *recorder, substr string) {
	t.Helper()
	if got := rec.text(); !strings.Contains(got, substr) {
		t.Fatalf("replies %q do not contain %q", got, substr)
	}
}

func TestPersonalPracticeScoringAndRounds(t *testing.T) {
	st, _ := setupTest(t, 5) // 目标 60，答案 C5
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "小明", "个人听音练习"); err != nil {
		t.Fatal(err)
	}
	replies := rec.all()
	if len(replies) != 3 {
		t.Fatalf("start replies = %+v", replies)
	}
	if replies[0].Text != "欢迎来到听音练习, 一共有5个问题, 每个问题1分" {
		t.Fatalf("welcome = %q", replies[0].Text)
	}
	voiceData(t, replies[1])
	if replies[2].Text != practiceNotePrompt {
		t.Fatalf("prompt = %q", replies[2].Text)
	}

	s := sessionOf(t, practiceKey("g1", "u1", practicePersonal))
	if s == nil {
		t.Fatal("session not registered")
	}
	if s.answer != "C5" || s.round != 1 || s.errorCount != 0 {
		t.Fatalf("session = %+v", s)
	}

	// 个人练习只接受发起者的回答。
	rec.reset()
	if err := runMsg(t, st, rec, "u2", "g1", "小红", "C5"); err != nil {
		t.Fatal(err)
	}
	if len(rec.all()) != 0 {
		t.Fatalf("other user got replies: %+v", rec.all())
	}
	if got := scoreOf(t, s, "u1"); got != 0 {
		t.Fatalf("score = %v", got)
	}

	// 两次错误。
	for want := 1; want <= 2; want++ {
		rec.reset()
		if err := runMsg(t, st, rec, "u1", "g1", "小明", "D5"); err != nil {
			t.Fatal(err)
		}
		if len(rec.all()) != 3 {
			t.Fatalf("wrong replies = %+v", rec.all())
		}
		voiceData(t, rec.all()[1])
		requireText(t, rec, "错误次数为"+strconv.Itoa(want)+", 请继续回答")
	}

	// 第三次答对，按上游规则得 0.2 分。
	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "小明", "C5"); err != nil {
		t.Fatal(err)
	}
	requireText(t, rec, "恭喜你回答正确, 答案是: C5")
	if got := scoreOf(t, s, "u1"); got != 0.2 {
		t.Fatalf("score after two errors = %v", got)
	}
	if s.round != 2 || s.errorCount != 0 {
		t.Fatalf("round = %d error = %d", s.round, s.errorCount)
	}

	// 剩余 4 题全部答对。
	for i := 0; i < 4; i++ {
		rec.reset()
		if err := runMsg(t, st, rec, "u1", "g1", "小明", "C5"); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			requireText(t, rec, "恭喜你回答正确")
		}
	}
	requireText(t, rec, "回答完毕, 所得分数如下:")
	requireText(t, rec, "小明: 4.2")
	if s := sessionOf(t, practiceKey("g1", "u1", practicePersonal)); s != nil {
		t.Fatal("session should be removed after final round")
	}
}

func TestPersonalPracticeMaxErrors(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "小明", "个人听音练习"); err != nil {
		t.Fatal(err)
	}
	s := sessionOf(t, practiceKey("g1", "u1", practicePersonal))
	if s == nil {
		t.Fatal("session not registered")
	}

	for i := 0; i < 3; i++ {
		rec.reset()
		if err := runMsg(t, st, rec, "u1", "g1", "小明", "D5"); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			requireText(t, rec, "请继续回答")
		}
	}
	requireText(t, rec, "错误次数已达3次, 进入下一关")
	if s.round != 2 || s.errorCount != 0 {
		t.Fatalf("round = %d error = %d", s.round, s.errorCount)
	}
	if got := scoreOf(t, s, "u1"); got != 0 {
		t.Fatalf("score = %v, want 0", got)
	}

	// 新一题错误次数已重置。
	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "小明", "D5"); err != nil {
		t.Fatal(err)
	}
	requireText(t, rec, "错误次数为1")
}

func TestTeamPracticeMultiUser(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g2", "Alice", "团队听音练习"); err != nil {
		t.Fatal(err)
	}
	if s := sessionOf(t, practiceKey("g2", "", practiceTeam)); s == nil {
		t.Fatal("team session not registered")
	}

	users := []struct{ uid, name string }{
		{"u2", "Bob"},
		{"u1", "Alice"},
		{"u2", "Bob"},
		{"u1", "Alice"},
		{"u2", "Bob"},
	}
	for i, u := range users {
		rec.reset()
		if err := runMsg(t, st, rec, u.uid, "g2", u.name, "C5"); err != nil {
			t.Fatal(err)
		}
		if i < len(users)-1 {
			requireText(t, rec, "恭喜你回答正确")
		}
	}
	requireText(t, rec, "回答完毕, 所得分数如下:")
	requireText(t, rec, "Bob: 3.0")
	requireText(t, rec, "Alice: 2.0")
	if s := sessionOf(t, practiceKey("g2", "", practiceTeam)); s != nil {
		t.Fatal("team session should be removed")
	}
}

func TestTeamPracticeMaxErrors(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g3", "Alice", "团队听音练习"); err != nil {
		t.Fatal(err)
	}
	s := sessionOf(t, practiceKey("g3", "", practiceTeam))
	if s == nil {
		t.Fatal("team session not registered")
	}
	for i := 0; i < teamMaxErrors; i++ {
		uid, name := "u1", "Alice"
		if i%2 == 1 {
			uid, name = "u2", "Bob"
		}
		rec.reset()
		if err := runMsg(t, st, rec, uid, "g3", name, "D5"); err != nil {
			t.Fatal(err)
		}
	}
	requireText(t, rec, "错误次数已达10次, 进入下一关")
	if s.round != 2 || s.errorCount != 0 {
		t.Fatalf("round = %d error = %d", s.round, s.errorCount)
	}
	if len(s.scores) != 0 {
		t.Fatalf("scores = %+v", s.scores)
	}
}

func TestPracticeReminderAndTimeout(t *testing.T) {
	st, clock := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g4", "小明", "个人听音练习"); err != nil {
		t.Fatal(err)
	}

	rec.reset()
	if !clock.fire(practiceReminder) {
		t.Fatal("reminder timer not found")
	}
	requireText(t, rec, "听音练习, 你还有15s作答时间")

	rec.reset()
	if !clock.fire(practiceTimeout) {
		t.Fatal("timeout timer not found")
	}
	requireText(t, rec, "听音练习超时, 练习结束...答案是: C5")
	requireText(t, rec, "所得分数如下:")
	if s := sessionOf(t, practiceKey("g4", "u1", practicePersonal)); s != nil {
		t.Fatal("session should be removed after timeout")
	}
}

func TestPracticeRequiresGroup(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	for _, msg := range []string{"个人听音练习", "团队听音练习"} {
		rec.reset()
		if err := runMsg(t, st, rec, "u1", "", "小明", msg); err != nil {
			t.Fatal(err)
		}
		if len(rec.all()) != 0 {
			t.Fatalf("%q got replies: %+v", msg, rec.all())
		}
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if len(sessions) != 0 {
		t.Fatalf("sessions = %d", len(sessions))
	}
}

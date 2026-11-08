package midicreate

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	practiceQuestions  = 5
	practiceMaxRound   = practiceQuestions + 1
	personalMaxErrors  = 3
	teamMaxErrors      = 10
	practiceReminder   = 45 * time.Second
	practiceTimeout    = 60 * time.Second
	practiceNotePrompt = "判断上面的音频, 输入音符, 例如C#6"
)

// timerHandle 抽象 time.Timer，便于测试注入假定时器。
type timerHandle interface {
	Stop() bool
	Reset(time.Duration) bool
}

// newTimer 可被测试替换。
var newTimer = func(d time.Duration, f func()) timerHandle {
	return time.AfterFunc(d, f)
}

// randIntn 可被测试替换，用于注入确定的目标音。
var randIntn = rand.Intn

type practiceMode int

const (
	practicePersonal practiceMode = iota
	practiceTeam
)

func (m practiceMode) maxErrors() int {
	if m == practiceTeam {
		return teamMaxErrors
	}
	return personalMaxErrors
}

func (m practiceMode) label() string {
	if m == practiceTeam {
		return "团队"
	}
	return "个人"
}

type practiceSession struct {
	key    string
	mode   practiceMode
	group  string
	owner  string
	target uint8
	answer string
	round  int

	errorCount int
	timbre     int
	scores     map[string]float64
	names      map[string]string

	reminder timerHandle
	timeout  timerHandle
	closed   bool
	call     sdk.Call
}

var (
	sessionMu sync.Mutex
	sessions  = map[string]*practiceSession{}
)

func practiceKey(group, user string, mode practiceMode) string {
	if mode == practiceTeam {
		return "midicreate:team:" + group
	}
	return "midicreate:personal:" + group + ":" + user
}

// startPractice 处理「个人/团队听音练习」。
func startPractice(ctx context.Context, c sdk.Call, text string) error {
	m := practiceRegexp.FindStringSubmatch(text)
	if m == nil || c.Event.GroupID == "" {
		return nil
	}
	mode := practicePersonal
	if m[1] == "团队" {
		mode = practiceTeam
	}
	key := practiceKey(c.Event.GroupID, c.Event.UserID, mode)

	sessionMu.Lock()
	if _, exists := sessions[key]; exists {
		sessionMu.Unlock()
		return c.Reply(ctx, "本群已有正在进行的"+mode.label()+"听音练习")
	}
	target := uint8(55 + randIntn(34))
	s := &practiceSession{
		key:    key,
		mode:   mode,
		group:  c.Event.GroupID,
		owner:  c.Event.UserID,
		target: target,
		answer: answerFor(target),
		round:  1,
		timbre: getTimbre(ctx, c),
		scores: map[string]float64{},
		names:  map[string]string{},
		call:   c,
	}
	sessions[key] = s
	sessionMu.Unlock()

	messages := []practiceMessage{
		{text: "欢迎来到听音练习, 一共有5个问题, 每个问题1分"},
		{voice: synthesizeNote(target, s.timbre)},
		{text: practiceNotePrompt},
	}
	sendPracticeMessages(ctx, c, messages)

	sessionMu.Lock()
	if !s.closed {
		s.reminder = newTimer(practiceReminder, s.onReminder)
		s.timeout = newTimer(practiceTimeout, s.onTimeout)
	}
	sessionMu.Unlock()
	return nil
}

// handlePracticeAnswer 在存在匹配会话时处理用户的音符回答。
// 返回 handled 表示该消息已被练习消费。
func handlePracticeAnswer(ctx context.Context, c sdk.Call, text string) (bool, error) {
	if c.Event.GroupID == "" || !practiceNoteRegexp.MatchString(text) {
		return false, nil
	}
	key := practiceKey(c.Event.GroupID, c.Event.UserID, practicePersonal)
	if s := activeSession(key, c.Event.UserID, practicePersonal); s != nil {
		return true, answerPractice(ctx, c, s, text)
	}
	key = practiceKey(c.Event.GroupID, "", practiceTeam)
	if s := activeSession(key, "", practiceTeam); s != nil {
		return true, answerPractice(ctx, c, s, text)
	}
	return false, nil
}

func activeSession(key, user string, mode practiceMode) *practiceSession {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	s := sessions[key]
	if s == nil || s.closed {
		return nil
	}
	if mode == practicePersonal && s.owner != user {
		return nil
	}
	return s
}

type practiceMessage struct {
	text  string
	voice []byte
}

func answerPractice(ctx context.Context, c sdk.Call, s *practiceSession, text string) error {
	n := processOne(text)
	var messages []practiceMessage
	finished := false

	sessionMu.Lock()
	if s.closed || sessions[s.key] != s {
		sessionMu.Unlock()
		return nil
	}
	if s.reminder != nil {
		s.reminder.Reset(practiceReminder)
	}
	if s.timeout != nil {
		s.timeout.Reset(practiceTimeout)
	}
	if name := c.Event.Nickname; name != "" {
		s.names[c.Event.UserID] = name
	}
	if n != s.target {
		s.errorCount++
	}
	maxErrors := s.mode.maxErrors()
	if s.errorCount == maxErrors || n == s.target {
		switch {
		case n == s.target:
			messages = append(messages, practiceMessage{text: "恭喜你回答正确, 答案是: " + s.answer})
		case s.errorCount == maxErrors:
			messages = append(messages,
				practiceMessage{text: "你的回答是: "},
				practiceMessage{voice: synthesizeNote(n, s.timbre)},
				practiceMessage{text: fmt.Sprintf("回答错误, 答案是: %s, 错误次数已达%d次, 进入下一关", s.answer, maxErrors)},
			)
		}
		// 统计分数，与上游保持一致。
		if s.mode == practicePersonal {
			switch s.errorCount {
			case 0:
				s.scores[c.Event.UserID] += 1.0
			case 1:
				s.scores[c.Event.UserID] += 0.5
			case 2:
				s.scores[c.Event.UserID] += 0.2
			}
		} else if s.errorCount != maxErrors {
			s.scores[c.Event.UserID] += 1.0
		}
		s.round++
		if s.round != practiceMaxRound {
			s.errorCount = 0
			s.target = uint8(55 + randIntn(34))
			s.answer = answerFor(s.target)
			messages = append(messages,
				practiceMessage{voice: synthesizeNote(s.target, s.timbre)},
				practiceMessage{text: practiceNotePrompt},
			)
		} else {
			finished = true
			messages = append(messages, practiceMessage{text: "回答完毕, 所得分数如下:\n" + s.scoreText()})
		}
	} else if n != s.target {
		messages = append(messages,
			practiceMessage{text: "你的回答是: "},
			practiceMessage{voice: synthesizeNote(n, s.timbre)},
			practiceMessage{text: fmt.Sprintf("回答错误, 错误次数为%d, 请继续回答", s.errorCount)},
		)
	}

	if finished {
		s.closed = true
		delete(sessions, s.key)
		s.stopTimers()
	}
	sessionMu.Unlock()

	sendPracticeMessages(ctx, c, messages)
	return nil
}

func (s *practiceSession) scoreText() string {
	ids := make([]string, 0, len(s.scores))
	for id := range s.scores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		display := s.names[id]
		if display == "" {
			display = id
		}
		fmt.Fprintf(&b, "%s: %.1f\n", display, s.scores[id])
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *practiceSession) stopTimers() {
	if s.reminder != nil {
		s.reminder.Stop()
	}
	if s.timeout != nil {
		s.timeout.Stop()
	}
}

// onReminder 在 45 秒时提醒，调用时不持有锁。
func (s *practiceSession) onReminder() {
	sessionMu.Lock()
	active := !s.closed && sessions[s.key] == s
	sessionMu.Unlock()
	if !active {
		return
	}
	sendPracticeMessages(context.Background(), s.call, []practiceMessage{
		{text: "听音练习, 你还有15s作答时间"},
	})
}

// onTimeout 在 60 秒时结束练习并清理会话。
func (s *practiceSession) onTimeout() {
	sessionMu.Lock()
	if s.closed || sessions[s.key] != s {
		sessionMu.Unlock()
		return
	}
	s.closed = true
	delete(sessions, s.key)
	s.stopTimers()
	answer := s.answer
	score := s.scoreText()
	sessionMu.Unlock()

	sendPracticeMessages(context.Background(), s.call, []practiceMessage{
		{text: "听音练习超时, 练习结束...答案是: " + answer + "\n所得分数如下:\n" + score},
	})
}

func sendPracticeMessages(ctx context.Context, c sdk.Call, messages []practiceMessage) {
	for _, m := range messages {
		if m.voice != nil {
			_ = c.ReplyVoiceBytes(ctx, m.voice, "audio/wav")
			continue
		}
		_ = c.Reply(ctx, m.text)
	}
}

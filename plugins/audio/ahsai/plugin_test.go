package ahsai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// TestMain 默认关闭后台超时任务，避免测试结束后仍有计时器触发。
func TestMain(m *testing.M) {
	afterFunc = func(time.Duration, func()) *time.Timer { return nil }
	os.Exit(m.Run())
}

// memState 是内存版 sdk.State，供会话状态测试使用。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

// testWAV 是一段合法的 44 字节 WAV 头，足以代表合成结果。
var testWAV = []byte{
	'R', 'I', 'F', 'F', 36, 0, 0, 0, 'W', 'A', 'V', 'E',
	'f', 'm', 't', ' ', 16, 0, 0, 0, 1, 0, 1, 0,
	0x44, 0xAC, 0, 0, 0x88, 0x58, 1, 0, 2, 0, 16, 0,
	'd', 'a', 't', 'a', 0, 0, 0, 0,
}

type harness struct {
	state   memState
	now     time.Time
	replies []sdk.Reply
	calls   []string
	speak   func(name, text string) ([]byte, error)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		state: memState{},
		now:   time.Date(2026, 1, 8, 12, 0, 0, 0, time.Local),
	}
	h.speak = func(name, text string) ([]byte, error) {
		h.calls = append(h.calls, name+":"+text)
		return testWAV, nil
	}
	oldNow, oldRand, oldSpeak := nowFunc, randIntn, speakFn
	nowFunc = func() time.Time { return h.now }
	randIntn = func(int) int { return 0 }
	speakFn = func(name, text string) ([]byte, error) { return h.speak(name, text) }
	t.Cleanup(func() {
		nowFunc, randIntn, speakFn = oldNow, oldRand, oldSpeak
	})
	return h
}

// send 执行一条群消息并收集回复。
func (h *harness) send(t *testing.T, msg string) []sdk.Reply {
	t.Helper()
	h.replies = nil
	c := sdk.Call{State: h.state, Event: sdk.Event{
		Message: msg,
		GroupID: "g1",
		UserID:  "u1",
		Reply: func(_ context.Context, r sdk.Reply) error {
			h.replies = append(h.replies, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("handle %q: %v", msg, err)
	}
	return h.replies
}

// pendingOf 读取当前会话的待选择状态。
func (h *harness) pendingOf(t *testing.T) pendingSession {
	t.Helper()
	var p pendingSession
	c := sdk.Call{State: h.state, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}
	if err := sdk.LoadJSON(context.Background(), c.State, pendingKey(c), &p); err != nil {
		t.Fatalf("load pending: %v", err)
	}
	return p
}

func textReplies(replies []sdk.Reply) []string {
	var out []string
	for _, r := range replies {
		if r.Kind == "text" {
			out = append(out, r.Text)
		}
	}
	return out
}

func TestValidNameSynthesizes(t *testing.T) {
	h := newHarness(t)
	got := h.send(t, "使伊織弓鶴说你好")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "text" || got[0].Text != "少女祈祷中..." {
		t.Fatalf("first reply = %+v", got[0])
	}
	if got[1].Kind != "voice" || !strings.HasPrefix(got[1].URL, "data:audio/wav;base64,") {
		t.Fatalf("voice reply = %+v", got[1])
	}
	if len(h.calls) != 1 || h.calls[0] != "伊織弓鶴:你好" {
		t.Fatalf("calls = %v", h.calls)
	}
	if p := h.pendingOf(t); p.Text != "" {
		t.Fatalf("unexpected pending state: %+v", p)
	}
}

func TestInvalidNameShowsList(t *testing.T) {
	h := newHarness(t)
	got := h.send(t, "使张三说春はあけぼの")
	if len(got) != 1 || got[0].Kind != "text" {
		t.Fatalf("replies = %+v", got)
	}
	text := got[0].Text
	if !strings.Contains(text, "输入的音源为空, 请输入音源序号") {
		t.Fatalf("list reply = %q", text)
	}
	for i, name := range namelist {
		want := fmt.Sprintf("%d. %s", i, name)
		if !strings.Contains(text, want) {
			t.Fatalf("list missing %q:\n%s", want, text)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("should not synthesize: %v", h.calls)
	}
	p := h.pendingOf(t)
	if p.Text != "春はあけぼの" || p.Expire == 0 {
		t.Fatalf("pending = %+v", p)
	}
}

func TestEmptyNameShowsList(t *testing.T) {
	h := newHarness(t)
	got := h.send(t, "使说こんにちは")
	if len(got) != 1 || !strings.Contains(got[0].Text, "输入的音源为空") {
		t.Fatalf("replies = %+v", got)
	}
	if p := h.pendingOf(t); p.Text != "こんにちは" {
		t.Fatalf("pending = %+v", p)
	}
}

func TestIndexSelection(t *testing.T) {
	h := newHarness(t)
	h.send(t, "使无效音源说おはよう")
	if got := h.send(t, "5"); len(got) != 2 || got[1].Kind != "voice" {
		t.Fatalf("replies = %+v", got)
	}
	if len(h.calls) != 1 || h.calls[0] != "月読アイ:おはよう" {
		t.Fatalf("calls = %v", h.calls)
	}
	if p := h.pendingOf(t); p.Text != "" {
		t.Fatalf("pending should be cleared: %+v", p)
	}
}

func TestIndexOutOfRange(t *testing.T) {
	h := newHarness(t)
	h.send(t, "使无效音源说おはよう")
	got := h.send(t, "18")
	if len(got) != 1 || got[0].Text != "序号非法!" {
		t.Fatalf("replies = %+v", got)
	}
	if len(h.calls) != 0 {
		t.Fatalf("should not synthesize: %v", h.calls)
	}
	if p := h.pendingOf(t); p.Text != "おはよう" {
		t.Fatalf("pending should be kept: %+v", p)
	}
	// 会话仍然有效，可以继续选择。
	if got = h.send(t, "0"); len(got) != 2 || h.calls[0] != "ついなちゃん標準語:おはよう" {
		t.Fatalf("replies = %+v, calls = %v", got, h.calls)
	}
}

func TestPendingExpired(t *testing.T) {
	h := newHarness(t)
	h.send(t, "使无效音源说おやすみ")
	h.now = h.now.Add(11 * time.Second)
	got := h.send(t, "5")
	texts := textReplies(got)
	if len(texts) != 2 || !strings.Contains(texts[0], "时间太久啦！帮你选择ついなちゃん標準語") ||
		texts[1] != "少女祈祷中..." {
		t.Fatalf("texts = %q", texts)
	}
	if len(got) != 3 || got[2].Kind != "voice" {
		t.Fatalf("replies = %+v", got)
	}
	if len(h.calls) != 1 || h.calls[0] != "ついなちゃん標準語:おやすみ" {
		t.Fatalf("calls = %v", h.calls)
	}
	if p := h.pendingOf(t); p.Text != "" {
		t.Fatalf("pending should be cleared: %+v", p)
	}
}

// TestPendingTimeoutProactivelySynthesizes 校验超时后无需下一条消息也会主动合成。
func TestPendingTimeoutProactivelySynthesizes(t *testing.T) {
	h := newHarness(t)
	var delay time.Duration
	var fire func()
	old := afterFunc
	afterFunc = func(d time.Duration, f func()) *time.Timer {
		delay, fire = d, f
		return nil
	}
	t.Cleanup(func() { afterFunc = old })

	h.send(t, "使无效音源说おやすみ")
	if delay != pendingTTL || fire == nil {
		t.Fatalf("timeout delay = %v, fire nil = %v", delay, fire == nil)
	}

	h.now = h.now.Add(pendingTTL + time.Second)
	h.replies = nil
	fire()

	texts := textReplies(h.replies)
	if len(texts) != 2 || !strings.Contains(texts[0], "时间太久啦！帮你选择ついなちゃん標準語") ||
		texts[1] != "少女祈祷中..." {
		t.Fatalf("texts = %q", texts)
	}
	if len(h.replies) != 3 || h.replies[2].Kind != "voice" {
		t.Fatalf("replies = %+v", h.replies)
	}
	if len(h.calls) != 1 || h.calls[0] != "ついなちゃん標準語:おやすみ" {
		t.Fatalf("calls = %v", h.calls)
	}
	if p := h.pendingOf(t); p.Text != "" {
		t.Fatalf("pending should be cleared: %+v", p)
	}
}

func TestPendingIgnoresOtherMessages(t *testing.T) {
	h := newHarness(t)
	h.send(t, "使无效音源说おはよう")
	if got := h.send(t, "今天天气不错"); len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
	if p := h.pendingOf(t); p.Text != "おはよう" {
		t.Fatalf("pending should be kept: %+v", p)
	}
}

func TestSynthesisError(t *testing.T) {
	h := newHarness(t)
	h.speak = func(string, string) ([]byte, error) { return nil, errors.New("boom") }
	got := h.send(t, "使伊織弓鶴说你好")
	if len(got) != 2 || got[1].Kind != "text" || !strings.Contains(got[1].Text, "ERROR: boom") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestIgnored(t *testing.T) {
	h := newHarness(t)
	if got := h.send(t, "今天天气不错"); len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

package midicreate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"shizukubot/sdk"
)

// memState 是 sdk.State 的内存实现。
type memState struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[key], nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

func (m *memState) value(t *testing.T, key string) json.RawMessage {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[key]
}

// recorder 收集一次或多次处理中发出的回复。
type recorder struct {
	mu      sync.Mutex
	replies []sdk.Reply
}

func (r *recorder) reply(_ context.Context, rep sdk.Reply) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, rep)
	return nil
}

func (r *recorder) all() []sdk.Reply {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sdk.Reply(nil), r.replies...)
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = nil
}

func (r *recorder) text() string {
	var b strings.Builder
	for _, rep := range r.all() {
		if rep.Text != "" {
			b.WriteString(rep.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// fakeTimer 是 timerHandle 的假实现。
type fakeTimer struct {
	mu      sync.Mutex
	d       time.Duration
	cb      func()
	stopped bool
}

func (f *fakeTimer) Stop() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := !f.stopped
	f.stopped = true
	return was
}

func (f *fakeTimer) Reset(d time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := !f.stopped
	f.d = d
	f.stopped = false
	return was
}

func (f *fakeTimer) fire() bool {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return false
	}
	f.stopped = true
	cb := f.cb
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
	return true
}

func (f *fakeTimer) duration() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.d
}

func (f *fakeTimer) active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.stopped
}

// fakeClock 记录创建的定时器，并允许测试手动触发。
type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

func (c *fakeClock) newTimer(d time.Duration, cb func()) timerHandle {
	t := &fakeTimer{d: d, cb: cb}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	return t
}

// fire 触发最近创建且仍在运行的指定时长定时器。
func (c *fakeClock) fire(d time.Duration) bool {
	c.mu.Lock()
	var target *fakeTimer
	for i := len(c.timers) - 1; i >= 0; i-- {
		if c.timers[i].duration() == d && c.timers[i].active() {
			target = c.timers[i]
			break
		}
	}
	c.mu.Unlock()
	if target == nil {
		return false
	}
	return target.fire()
}

func setupTest(t *testing.T, targetOffset int) (*memState, *fakeClock) {
	t.Helper()
	resetSessions()
	clock := &fakeClock{}
	oldTimer, oldRand := newTimer, randIntn
	newTimer = clock.newTimer
	randIntn = func(int) int { return targetOffset }
	t.Cleanup(func() {
		newTimer = oldTimer
		randIntn = oldRand
		resetSessions()
	})
	return newMemState(), clock
}

func resetSessions() {
	sessionMu.Lock()
	for _, s := range sessions {
		s.closed = true
		s.stopTimers()
	}
	sessions = map[string]*practiceSession{}
	sessionMu.Unlock()
}

func runMsg(t *testing.T, st *memState, rec *recorder, uid, gid, nick, msg string) error {
	t.Helper()
	c := sdk.Call{
		Event: sdk.Event{
			Message:  msg,
			UserID:   uid,
			GroupID:  gid,
			Nickname: nick,
			Reply:    rec.reply,
		},
		State: st,
	}
	return Handle(context.Background(), c)
}

// voiceData 从 data URI 语音回复中解出 WAV 字节。
func voiceData(t *testing.T, rep sdk.Reply) []byte {
	t.Helper()
	const prefix = "data:audio/wav;base64,"
	if rep.Kind != "voice" || !strings.HasPrefix(rep.URL, prefix) {
		t.Fatalf("reply is not a wav voice: %+v", rep)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(rep.URL, prefix))
	if err != nil {
		t.Fatalf("decode voice: %v", err)
	}
	return data
}

// checkWAV 校验 RIFF/WAVE 头并返回时长（秒）。
func checkWAV(t *testing.T, data []byte) float64 {
	t.Helper()
	if len(data) < 44 {
		t.Fatalf("wav too short: %d", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" ||
		string(data[12:16]) != "fmt " || string(data[36:40]) != "data" {
		t.Fatalf("bad wav header: % x", data[:44])
	}
	if got := binary.LittleEndian.Uint32(data[4:8]); got != uint32(36+len(data)-44) {
		t.Fatalf("riff size = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[20:22]); got != 1 {
		t.Fatalf("audio format = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[22:24]); got != 1 {
		t.Fatalf("channels = %d", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != sampleRate {
		t.Fatalf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(data[28:32]); got != sampleRate*2 {
		t.Fatalf("byte rate = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[32:34]); got != 2 {
		t.Fatalf("block align = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[34:36]); got != 16 {
		t.Fatalf("bits = %d", got)
	}
	dataLen := int(binary.LittleEndian.Uint32(data[40:44]))
	if dataLen != len(data)-44 {
		t.Fatalf("data len = %d, actual = %d", dataLen, len(data)-44)
	}
	return float64(dataLen/2) / sampleRate
}

func approx(t *testing.T, got, want float64, name string) {
	t.Helper()
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("%s = %.4f, want %.4f", name, got, want)
	}
}

func TestMidiCreateHelp(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "", "midi制作"); err != nil {
		t.Fatal(err)
	}
	text := rec.text()
	if !strings.Contains(text, "midi制作 <音符串>") || !strings.Contains(text, "示例") {
		t.Fatalf("help = %q", text)
	}
}

func TestMidiCreateVoice(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "", "midi制作 C"); err != nil {
		t.Fatal(err)
	}
	replies := rec.all()
	if len(replies) != 1 {
		t.Fatalf("replies = %+v", replies)
	}
	data := voiceData(t, replies[0])
	want := float64(samplesForTicks(ticks4th)) / sampleRate
	approx(t, checkWAV(t, data), want, "duration")
}

func TestMidiCreateParseError(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "", "midi制作 X"); err != nil {
		t.Fatal(err)
	}
	if got := rec.text(); !strings.Contains(got, "无法解析第0个位置的X字符") {
		t.Fatalf("reply = %q", got)
	}
}

func TestSetTimbrePersists(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "", "设置音色 7"); err != nil {
		t.Fatal(err)
	}
	if got := rec.text(); !strings.Contains(got, "成功") {
		t.Fatalf("reply = %q", got)
	}
	groupKey := "midicreate:timbre:g:g1"
	if got := string(st.value(t, groupKey)); got != "7" {
		t.Fatalf("stored timbre = %s", got)
	}
	c := sdk.Call{Event: sdk.Event{UserID: "u1", GroupID: "g1"}, State: st}
	if got := getTimbre(context.Background(), c); got != 7 {
		t.Fatalf("getTimbre = %d", got)
	}
	// 上游按群保存：同群其他成员共享音色。
	c.Event.UserID = "u2"
	if got := getTimbre(context.Background(), c); got != 7 {
		t.Fatalf("group timbre = %d", got)
	}
	// 其他群与私聊不共享。
	c.Event.GroupID = "g2"
	if got := getTimbre(context.Background(), c); got != defaultTimbre {
		t.Fatalf("other group timbre = %d", got)
	}
	c.Event.GroupID = ""
	if got := getTimbre(context.Background(), c); got != defaultTimbre {
		t.Fatalf("private timbre = %d", got)
	}

	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "", "设置音色 128"); err != nil {
		t.Fatal(err)
	}
	if got := rec.text(); !strings.Contains(got, "0~127") {
		t.Fatalf("range reply = %q", got)
	}
	if got := string(st.value(t, groupKey)); got != "7" {
		t.Fatalf("stored timbre changed to %s", got)
	}

	rec.reset()
	if err := runMsg(t, st, rec, "u1", "", "", "设置音色 9"); err != nil {
		t.Fatal(err)
	}
	if got := string(st.value(t, "midicreate:timbre:u:u1")); got != "9" {
		t.Fatalf("private stored timbre = %s", got)
	}
	c.Event.UserID = "u1"
	c.Event.GroupID = ""
	if got := getTimbre(context.Background(), c); got != 9 {
		t.Fatalf("private getTimbre = %d", got)
	}

	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "", "设置音色 abc"); err != nil {
		t.Fatal(err)
	}
	if got := rec.text(); !strings.Contains(got, "0~127") {
		t.Fatalf("invalid reply = %q", got)
	}
}

func TestSetTimbreAffectsOutput(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	if err := runMsg(t, st, rec, "u1", "g1", "", "设置音色 0"); err != nil {
		t.Fatal(err)
	}
	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "", "midi制作 C"); err != nil {
		t.Fatal(err)
	}
	first := voiceData(t, rec.all()[0])

	if err := runMsg(t, st, rec, "u1", "g1", "", "设置音色 80"); err != nil {
		t.Fatal(err)
	}
	rec.reset()
	if err := runMsg(t, st, rec, "u1", "g1", "", "midi制作 C"); err != nil {
		t.Fatal(err)
	}
	second := voiceData(t, rec.all()[0])
	if bytes.Equal(first, second) {
		t.Fatal("different timbres produced identical audio")
	}
}

func TestNonTriggerNoReply(t *testing.T) {
	st, _ := setupTest(t, 5)
	rec := &recorder{}
	for _, msg := range []string{"今天天气不错", "midi", "帮我midi制作", "个人听音练习"} {
		rec.reset()
		if err := runMsg(t, st, rec, "u1", "", "", msg); err != nil {
			t.Fatal(err)
		}
		if len(rec.all()) != 0 {
			t.Fatalf("%q got replies: %+v", msg, rec.all())
		}
	}
}

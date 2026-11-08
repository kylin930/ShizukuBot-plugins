package guessmusic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

// memState 是测试用的内存状态。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

// recorder 记录插件发出的回复。
type recorder struct{ replies []sdk.Reply }

func (r *recorder) reply(_ context.Context, rep sdk.Reply) error {
	r.replies = append(r.replies, rep)
	return nil
}

// texts 拼接所有文字回复。
func (r *recorder) texts() string {
	parts := make([]string, 0, len(r.replies))
	for _, rep := range r.replies {
		if rep.Kind == "text" {
			parts = append(parts, rep.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// voice 返回第一条语音回复的地址。
func (r *recorder) voice() string {
	for _, rep := range r.replies {
		if rep.Kind == "voice" {
			return rep.URL
		}
	}
	return ""
}

// send 构造一次消息事件并调用 Handle。
func send(t *testing.T, st sdk.State, group, user, nickname, message string) *recorder {
	t.Helper()
	r := &recorder{}
	e := sdk.Event{
		GroupID:  group,
		UserID:   user,
		Nickname: nickname,
		Message:  message,
		Reply:    r.reply,
	}
	if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
		t.Fatalf("handle %q: %v", message, err)
	}
	return r
}

// stubSongServer 启动本地歌单接口，测试期间替换包级 URL。
func stubSongServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	oldList, oldAudio, oldPage := songListURL, audioURL, songPageURL
	songListURL = srv.URL + "/playlist"
	audioURL = srv.URL + "/audio/%d.mp3"
	songPageURL = srv.URL + "/song/%d"
	t.Cleanup(func() {
		srv.Close()
		songListURL, audioURL, songPageURL = oldList, oldAudio, oldPage
	})
}

// stubSongs 返回包含两首有效歌曲和一首无效歌曲的歌单。
func stubSongs(t *testing.T) {
	t.Helper()
	stubSongServer(t, http.StatusOK, `{"code":200,"playlist":{"tracks":[
		{"id":1001,"name":"晴天","ar":[{"name":"周杰伦"}]},
		{"id":1002,"name":"稻香","artists":[{"name":"周杰伦"}]},
		{"id":0,"name":"无效歌曲"}
	]}}`)
}

// fixRand 固定随机结果，index 对应过滤后的有效歌曲下标。
func fixRand(t *testing.T, index int) {
	t.Helper()
	old := randIntn
	randIntn = func(int) int { return index }
	t.Cleanup(func() { randIntn = old })
}

// fixClock 固定时间，返回可推进的指针。
func fixClock(t *testing.T, start time.Time) *time.Time {
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
	for _, p := range []string{"state.read", "state.write"} {
		if !d.Manifest.Allows(p) {
			t.Fatalf("missing permission %s: %v", p, d.Manifest.Permissions)
		}
	}
}

func TestStartAndWin(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	r := send(t, st, "g1", "u1", "小明", "猜歌")
	wantVoice := fmt.Sprintf(audioURL, 1001)
	if got := r.voice(); got != wantVoice {
		t.Fatalf("voice url = %q, want %q", got, wantVoice)
	}
	if text := r.texts(); !strings.Contains(text, "共 6 次机会") || !strings.Contains(text, "120 秒") {
		t.Fatalf("unexpected start text: %q", text)
	}

	r = send(t, st, "g1", "u2", "小红", "猜歌 晴天")
	text := r.texts()
	for _, want := range []string{"猜对了", "晴天", "周杰伦", "累计答对 1 题"} {
		if !strings.Contains(text, want) {
			t.Fatalf("win text %q missing %q", text, want)
		}
	}

	// 题目答对后立即作废，普通消息不再触发回复。
	if r = send(t, st, "g1", "u2", "小红", "晴天"); len(r.replies) != 0 {
		t.Fatalf("question should be cleared, got %+v", r.replies)
	}
}

func TestWrongAnswerExhaustsTries(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	wrongs := []string{"青花瓷", "七里香", "夜曲", "以父之名", "东风破"}
	for i, guess := range wrongs {
		r := send(t, st, "g1", "u1", "小明", guess)
		want := fmt.Sprintf("还剩 %d 次机会", len(wrongs)-i)
		if text := r.texts(); !strings.Contains(text, want) {
			t.Fatalf("guess %q text %q missing %q", guess, text, want)
		}
	}

	r := send(t, st, "g1", "u1", "小明", "发如雪")
	text := r.texts()
	if !strings.Contains(text, "次数用完了") || !strings.Contains(text, "晴天") {
		t.Fatalf("exhausted text = %q", text)
	}
	if !strings.Contains(text, "答错 1 次") {
		t.Fatalf("wrong score missing: %q", text)
	}

	if r = send(t, st, "g1", "u1", "小明", "揭晓"); !strings.Contains(r.texts(), "当前没有正在进行的猜歌") {
		t.Fatalf("question should be cleared, got %q", r.texts())
	}
}

func TestTimeoutVoidsQuestion(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	clock := fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	*clock = clock.Add(questionTimeout + time.Second)

	r := send(t, st, "g1", "u1", "小明", "晴天")
	text := r.texts()
	if !strings.Contains(text, "超时") || !strings.Contains(text, "晴天") {
		t.Fatalf("timeout text = %q", text)
	}
	if r = send(t, st, "g1", "u1", "小明", "揭晓"); !strings.Contains(r.texts(), "当前没有正在进行的猜歌") {
		t.Fatalf("question should be cleared, got %q", r.texts())
	}
}

func TestReveal(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	r := send(t, st, "g1", "u2", "小红", "揭晓")
	text := r.texts()
	if !strings.Contains(text, "本题答案是") || !strings.Contains(text, "晴天") || !strings.Contains(text, "周杰伦") {
		t.Fatalf("reveal text = %q", text)
	}

	if r = send(t, st, "g1", "u1", "小明", "揭晓"); !strings.Contains(r.texts(), "当前没有正在进行的猜歌") {
		t.Fatalf("second reveal text = %q", r.texts())
	}
}

func TestEndCommand(t *testing.T) {
	stubSongs(t)
	fixRand(t, 1)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	r := send(t, st, "g1", "u1", "小明", "结束猜歌")
	if text := r.texts(); !strings.Contains(text, "已结束本局") || !strings.Contains(text, "稻香") {
		t.Fatalf("end text = %q", text)
	}
}

func TestCancelPermission(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")

	// 非发起者无法取消，题目保留。
	if text := send(t, st, "g1", "u2", "小红", "取消").texts(); !strings.Contains(text, "你无权限取消") {
		t.Fatalf("other user cancel text = %q", text)
	}
	if text := send(t, st, "g1", "u2", "小红", "结束猜歌").texts(); !strings.Contains(text, "你无权限取消") {
		t.Fatalf("other user end text = %q", text)
	}
	if text := send(t, st, "g1", "u2", "小红", "揭晓").texts(); !strings.Contains(text, "本题答案是") {
		t.Fatalf("question should be kept, reveal text = %q", text)
	}

	// 发起者可以取消。
	send(t, st, "g1", "u1", "小明", "猜歌")
	r := send(t, st, "g1", "u1", "小明", "取消")
	if text := r.texts(); !strings.Contains(text, "游戏已取消") || !strings.Contains(text, "晴天") {
		t.Fatalf("cancel text = %q", text)
	}
	if r = send(t, st, "g1", "u1", "小明", "揭晓"); !strings.Contains(r.texts(), "当前没有正在进行的猜歌") {
		t.Fatalf("question should be cleared, got %q", r.texts())
	}
}

func TestRanking(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	send(t, st, "g1", "u1", "小明", "晴天")
	send(t, st, "g1", "u2", "小红", "猜歌")
	send(t, st, "g1", "u2", "小红", "晴天")

	r := send(t, st, "g1", "u1", "小明", "猜歌排行")
	text := r.texts()
	for _, want := range []string{"本群猜歌排行", "小明", "小红", "1 题"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rank text %q missing %q", text, want)
		}
	}

	// 私聊展示个人战绩。
	r = send(t, st, "", "u1", "小明", "猜歌排行")
	if text = r.texts(); !strings.Contains(text, "答对 1 题") || !strings.Contains(text, "答错 0 次") {
		t.Fatalf("personal rank text = %q", text)
	}
}

func TestPersonalMode(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "个人猜歌")

	// 其他成员的消息被忽略，也不消耗次数。
	if r := send(t, st, "g1", "u2", "小红", "晴天"); len(r.replies) != 0 {
		t.Fatalf("other user should be ignored, got %+v", r.replies)
	}
	r := send(t, st, "g1", "u1", "小明", "晴天")
	if !strings.Contains(r.texts(), "猜对了") {
		t.Fatalf("owner answer text = %q", r.texts())
	}
}

// TestHintReplaysAudio 提示前两次重发当前题目音频，第三次提示已用完。
func TestHintReplaysAudio(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	wantVoice := fmt.Sprintf(audioURL, 1001)

	for i := 1; i <= 2; i++ {
		r := send(t, st, "g1", "u1", "小明", "提示")
		if got := r.voice(); got != wantVoice {
			t.Fatalf("hint %d voice = %q, want %q", i, got, wantVoice)
		}
		if text := r.texts(); !strings.Contains(text, "再听这段音频,要仔细听哦") {
			t.Fatalf("hint %d text = %q", i, text)
		}
	}

	r := send(t, st, "g1", "u1", "小明", "提示")
	if r.voice() != "" {
		t.Fatalf("third hint should not send voice: %+v", r.replies)
	}
	if text := r.texts(); !strings.Contains(text, "已经没有提示了哦") {
		t.Fatalf("third hint text = %q", text)
	}

	// 提示次数写入题目状态并持久化。
	var q question
	if err := json.Unmarshal(st["guessmusic:q:g1"], &q); err != nil {
		t.Fatal(err)
	}
	if q.Hints != 3 {
		t.Fatalf("persisted hints = %d, want 3", q.Hints)
	}
}

// TestHintPersonalMode 个人模式下其他人不能使用提示。
func TestHintPersonalMode(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "个人猜歌")
	if r := send(t, st, "g1", "u2", "小红", "提示"); len(r.replies) != 0 {
		t.Fatalf("other user hint should be ignored: %+v", r.replies)
	}
	if r := send(t, st, "g1", "u1", "小明", "提示"); r.voice() == "" {
		t.Fatalf("owner hint should resend audio: %+v", r.replies)
	}
}

// TestReminder 校验 105 秒提醒的延迟与文案，并确认题目结束后不再提醒。
func TestReminder(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	var delay time.Duration
	var fire func()
	old := newReminder
	newReminder = func(d time.Duration, f func()) *time.Timer {
		delay, fire = d, f
		return nil
	}
	t.Cleanup(func() { newReminder = old })

	r := send(t, st, "g1", "u1", "小明", "猜歌")
	if delay != 105*time.Second || fire == nil {
		t.Fatalf("reminder delay = %v, fire nil = %v", delay, fire == nil)
	}
	fire()
	if !strings.Contains(r.texts(), "你还有15s作答时间") {
		t.Fatalf("reminder text = %q", r.texts())
	}

	send(t, st, "g1", "u1", "小明", "晴天")
	before := len(r.replies)
	fire()
	if len(r.replies) != before {
		t.Fatalf("stale reminder should be ignored: %+v", r.replies[before:])
	}
}

// TestGuessLongerThanAnswerRejected 与上游一致：猜测必须被答案包含。
func TestGuessLongerThanAnswerRejected(t *testing.T) {
	stubSongs(t)
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	send(t, st, "g1", "u1", "小明", "猜歌")
	// 「晴天的雨」比答案「晴天」长，旧的双向包含会误判为正确。
	if got := send(t, st, "g1", "u1", "小明", "晴天的雨").texts(); !strings.Contains(got, "答案不对") {
		t.Fatalf("longer guess should be wrong: %q", got)
	}
	// 答案包含猜测时仍算对，与上游方向一致。
	if got := send(t, st, "g1", "u1", "小明", "晴").texts(); !strings.Contains(got, "猜对了") {
		t.Fatalf("contained guess = %q", got)
	}
}

func TestStartFetchError(t *testing.T) {
	stubSongServer(t, http.StatusInternalServerError, "boom")
	fixRand(t, 0)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	r := send(t, st, "g1", "u1", "小明", "猜歌")
	if r.voice() != "" {
		t.Fatalf("should not send voice on failure: %+v", r.replies)
	}
	if text := r.texts(); !strings.Contains(text, "抽歌失败") {
		t.Fatalf("error text = %q", text)
	}
}

func TestAnswerWithoutQuestion(t *testing.T) {
	stubSongs(t)
	fixClock(t, time.Unix(1700000000, 0))
	st := memState{}

	r := send(t, st, "g1", "u1", "小明", "猜歌 晴天")
	if text := r.texts(); !strings.Contains(text, "当前没有正在进行的猜歌") {
		t.Fatalf("explicit guess text = %q", text)
	}
	// 普通聊天在无题目时不产生回复。
	if r = send(t, st, "g1", "u1", "小明", "晴天"); len(r.replies) != 0 {
		t.Fatalf("chat should be ignored, got %+v", r.replies)
	}
}

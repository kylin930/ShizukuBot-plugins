// Package ahsai AH Soft フリーテキスト音声合成：把日语文本合成为语音。
// 移植自 zerobot-plugin/plugin/ahsai，音源列表与指令行为保持一致；
// 上游用 FutureEvent 等待音源序号，这里改用 sdk.State 保存会话。
package ahsai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	ahsaitts "github.com/fumiama/ahsai"
	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// namelist 与上游一致：按名称排序后的音源列表，序号即排序后的位置。
var namelist = func() []string {
	l := []string{
		"伊織弓鶴", "紲星あかり", "結月ゆかり", "京町セイカ", "東北きりたん", "東北イタコ",
		"ついなちゃん標準語", "ついなちゃん関西弁", "音街ウナ", "琴葉茜", "吉田くん", "民安ともえ",
		"桜乃そら", "月読アイ", "琴葉葵", "東北ずん子", "月読ショウタ", "水奈瀬コウ",
	}
	sort.Strings(l)
	return l
}()

// 音源名 -> 是否合法。
var nameSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(namelist))
	for _, n := range namelist {
		m[n] = struct{}{}
	}
	return m
}()

var (
	// speakRe 与上游正则一致。
	speakRe = regexp.MustCompile("^使(.{0,10})说([A-Za-z\\s\\d\u3005\u3040-\u30ff\u4e00-\u9fff\uff11-\uff19\uff21-\uff3a\uff41-\uff5a\uff66-\uff9d\\pP]+)$")

	// nowFunc 与 randIntn 可在测试中替换，保证结果确定。
	nowFunc  = time.Now
	randIntn = rand.Intn

	// speakFn 合成并下载音频，返回 WAV 字节；测试中可替换。
	speakFn = synthesize
	// afterFunc 便于测试关闭或捕获超时主动合成。
	afterFunc = time.AfterFunc
)

const (
	// pendingTTL 是等待用户输入音源序号的有效期，与上游 FutureEvent 一致。
	pendingTTL = 10 * time.Second
	// voiceMIME 是 ahsai 库 SaveOggToFile 实际写出的格式。
	voiceMIME = "audio/wav"
)

// pendingSession 记录一次待选择音源的合成请求。
type pendingSession struct {
	Text   string `json:"text"`
	Expire int64  `json:"expire"` // Unix 秒
}

// Definition 注册语音合成插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "ahsai",
		Name:        "AH Soft 语音合成",
		Version:     "1.0.0",
		Description: "发送「使<音源>说<日语>」合成语音，音源为空或非法时按编号选择",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "ahsai", Trigger: "使"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle 处理音源选择会话与「使X说Y」指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	key := pendingKey(c)
	var pending pendingSession
	if err := sdk.LoadJSON(ctx, c.State, key, &pending); err != nil {
		return err
	}
	if pending.Text != "" {
		if handled, err := continuePending(ctx, c, key, pending, text); handled || err != nil {
			return err
		}
	}

	m := sdk.MatchRegexp(text, speakRe)
	if m == nil {
		return nil
	}
	return startSpeak(ctx, c, key, m[1], m[2])
}

// startSpeak 在音源合法时直接合成，否则发送编号列表并记录待选择会话。
func startSpeak(ctx context.Context, c sdk.Call, key, name, speakText string) error {
	if _, ok := nameSet[name]; !ok {
		pending := pendingSession{
			Text:   speakText,
			Expire: nowFunc().Add(pendingTTL).Unix(),
		}
		if err := sdk.SaveJSON(ctx, c.State, key, pending); err != nil {
			return err
		}
		// 上游在 10 秒后主动随机选择音源并合成，这里用后台计时器还原。
		afterFunc(pendingTTL, func() { expirePending(context.Background(), c, key, pending) })
		return c.Reply(ctx, nameListText())
	}
	return speak(ctx, c, name, speakText)
}

// expirePending 在等待超时后主动随机选音源并合成；若用户已选择或已开始新请求则跳过。
func expirePending(ctx context.Context, c sdk.Call, key string, pending pendingSession) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var cur pendingSession
	if err := sdk.LoadJSON(ctx, c.State, key, &cur); err != nil {
		return
	}
	if cur.Text == "" || cur.Text != pending.Text || cur.Expire != pending.Expire {
		return
	}
	name := namelist[randIntn(len(namelist))]
	if err := clearPending(ctx, c, key); err != nil {
		return
	}
	_ = c.Reply(ctx, "时间太久啦！帮你选择"+name)
	_ = speak(ctx, c, name, cur.Text)
}

// continuePending 处理等待音源序号期间的下一条消息。
// handled 为 false 表示这条消息不是序号选择，交给后续指令处理。
func continuePending(ctx context.Context, c sdk.Call, key string, pending pendingSession, text string) (bool, error) {
	if nowFunc().Unix() >= pending.Expire {
		name := namelist[randIntn(len(namelist))]
		if err := clearPending(ctx, c, key); err != nil {
			return true, err
		}
		if err := c.Reply(ctx, "时间太久啦！帮你选择"+name); err != nil {
			return true, err
		}
		return true, speak(ctx, c, name, pending.Text)
	}

	num, ok := parseIndex(text)
	if !ok {
		return false, nil
	}
	if num < 0 || num >= len(namelist) {
		return true, c.Reply(ctx, "序号非法!")
	}
	name := namelist[num]
	if err := clearPending(ctx, c, key); err != nil {
		return true, err
	}
	return true, speak(ctx, c, name, pending.Text)
}

// clearPending 清空待选择会话。
func clearPending(ctx context.Context, c sdk.Call, key string) error {
	return sdk.SaveJSON(ctx, c.State, key, pendingSession{})
}

// parseIndex 解析纯数字序号；非纯数字返回 false。
func parseIndex(text string) (int, bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	num, err := strconv.Atoi(s)
	if err != nil {
		return -1, true
	}
	return num, true
}

// pendingKey 按会话与用户隔离待选择状态。
func pendingKey(c sdk.Call) string {
	return "pending:" + c.Event.GroupID + ":" + c.Event.UserID
}

// nameListText 返回与上游相同的音源编号列表。
func nameListText() string {
	var b strings.Builder
	b.WriteString("输入的音源为空, 请输入音源序号\n")
	for i, v := range namelist {
		fmt.Fprintf(&b, "%d. %s\n", i, v)
	}
	return b.String()
}

// speak 发送等待提示后合成并发送语音。
func speak(ctx context.Context, c sdk.Call, name, text string) error {
	if err := c.Reply(ctx, "少女祈祷中..."); err != nil {
		return err
	}
	data, err := speakFn(name, text)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if err := c.ReplyVoiceBytes(ctx, data, voiceMIME); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return nil
}

// synthesize 调用 ahsai 合成语音，并把 Ogg 源解码为 wav（与上游 SaveOggToFile 一致）。
func synthesize(name, text string) ([]byte, error) {
	s := ahsaitts.NewSpeaker()
	if err := s.SetName(name); err != nil {
		return nil, err
	}
	u, err := s.Speak(text)
	if err != nil {
		return nil, err
	}
	var out memWriteSeeker
	if err := ahsaitts.SaveOggToWriteSeeker(u, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// memWriteSeeker 是内存版 io.WriteSeeker，供 wav 编码器回填文件头。
type memWriteSeeker struct {
	data []byte
	pos  int
}

func (m *memWriteSeeker) Write(p []byte) (int, error) {
	if m.pos > len(m.data) {
		m.data = append(m.data, make([]byte, m.pos-len(m.data))...)
	}
	n := copy(m.data[m.pos:], p)
	if n < len(p) {
		m.data = append(m.data, p[n:]...)
	}
	m.pos += len(p)
	return len(p), nil
}

func (m *memWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = int64(m.pos) + offset
	case io.SeekEnd:
		abs = int64(len(m.data)) + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("negative seek position")
	}
	m.pos = int(abs)
	return abs, nil
}

// Bytes 返回写入完成后的全部数据。
func (m *memWriteSeeker) Bytes() []byte { return m.data }

// Package guessmusic 猜歌小游戏（精简移植版）。
// 上游 zerobot-plugin/guessmusic 依赖本地歌库、ffmpeg 切歌和网易云私有 API，
// 这里改为从网易云公开歌单接口随机抽歌，按外链发送语音，状态通过 sdk.State 持久化。
package guessmusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 便于测试替换的时间、随机源与 HTTP 地址。
var (
	nowFunc  = time.Now
	randIntn = rand.Intn
	mu       sync.Mutex

	// songListURL 网易云热歌榜，返回 playlist.tracks；测试时替换为本地服务。
	songListURL = "https://music.163.com/api/playlist/detail?id=3778678"
	// audioURL 网易云外链音频地址，框架按 URL 直接发送语音。
	audioURL = "https://music.163.com/song/media/outer/url?id=%d.mp3"
	// songPageURL 歌曲网页地址，语音发送失败时作为文字兜底。
	songPageURL = "https://music.163.com/#/song?id=%d"
	// httpClient 统一 10 秒超时。
	httpClient = &http.Client{Timeout: 10 * time.Second}
	// newReminder 便于测试关闭或捕获 105 秒提醒。
	newReminder = func(d time.Duration, f func()) *time.Timer { return time.AfterFunc(d, f) }
)

const (
	// maxTries 单局最多作答次数。
	maxTries = 6
	// questionTimeout 单局有效期。
	questionTimeout = 120 * time.Second
	// reminderLead 是「还有 15 秒」提醒的提前量。
	reminderLead = 15 * time.Second
	// maxGuessLen 超过该长度的消息视为普通聊天，不计入作答。
	maxGuessLen = 30
	// roomUserLimit 单个房间参与排行的用户上限。
	roomUserLimit = 200
)

// artist 是歌曲作者。
type artist struct {
	Name string `json:"name"`
}

// track 是歌单接口返回的歌曲信息。
type track struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Ar      []artist `json:"ar"`
	Artists []artist `json:"artists"`
}

// performer 返回歌手名，多个歌手用「/」连接。
func (t track) performer() string {
	list := t.Ar
	if len(list) == 0 {
		list = t.Artists
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		if name := strings.TrimSpace(a.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, "/")
}

// chart 是网易云歌单详情接口的响应结构。
type chart struct {
	Code     int `json:"code"`
	Playlist struct {
		Tracks []track `json:"tracks"`
	} `json:"playlist"`
}

// question 是当前房间正在进行的题目。
type question struct {
	SongID   int64  `json:"song_id"`
	Answer   string `json:"answer"`
	Artist   string `json:"artist"`
	Owner    string `json:"owner,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Tries    int    `json:"tries"`
	Hints    int    `json:"hints,omitempty"`
	StartAt  int64  `json:"start_at"`
	Deadline int64  `json:"deadline"`
}

// answerText 输出完整答案（含网易云链接）。
func (q *question) answerText() string {
	var b strings.Builder
	b.WriteString("歌名：" + q.Answer)
	if q.Artist != "" {
		b.WriteString("\n歌手：" + q.Artist)
	}
	if q.SongID > 0 {
		b.WriteString("\n网易云：" + fmt.Sprintf(songPageURL, q.SongID))
	}
	return b.String()
}

// score 是单个用户的猜歌成绩。
type score struct {
	Name    string `json:"name,omitempty"`
	Correct int    `json:"correct"`
	Wrong   int    `json:"wrong"`
}

// rankEntry 是排行中的一行。
type rankEntry struct {
	Name    string
	Correct int
}

// Definition 注册猜歌插件的消息订阅与状态权限。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:      "guessmusic",
		Name:    "猜歌",
		Version: "1.0.0",
		Description: "发送「猜歌」随机抽一首网易云歌曲并发送语音，群友猜歌名或歌手；" +
			"支持「猜歌 <答案>」「提示」「揭晓」「结束猜歌」「猜歌排行」。" +
			"精简自上游 guessmusic，未包含本地歌库、歌单管理与 ffmpeg 切歌。",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "guessmusic", Trigger: "猜歌"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle 先处理指令，再在有进行中题目时把普通消息当作答案。
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.UserID == "" {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	// 兼容上游「-答案」「-提示」「-取消」的写法。
	text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
	if text == "" {
		return nil
	}

	if text == "猜歌排行" {
		return showRanking(ctx, c)
	}
	if mode, ok := startMode(text); ok {
		return startGame(ctx, c, mode)
	}
	// 「猜歌 <答案>」：显式作答，没有题目时给出提示。
	if arg, ok := sdk.MatchPrefix(text, "猜歌"); ok && arg != "" {
		return judge(ctx, c, arg, true)
	}

	switch text {
	case "揭晓":
		return reveal(ctx, c)
	case "结束猜歌":
		return cancel(ctx, c, true)
	case "取消":
		return cancel(ctx, c, false)
	case "提示":
		return hint(ctx, c)
	}

	// 过长的消息按普通聊天处理，避免刷屏内容消耗作答次数。
	if utf8.RuneCountInString(text) > maxGuessLen {
		return nil
	}
	return judge(ctx, c, text, false)
}

// startMode 识别开始指令，返回「个人」「团队」或空（默认团队）。
func startMode(text string) (string, bool) {
	stem := text
	// 上游允许用「-歌单名」指定歌单；本插件没有本地歌单，这里只忽略后缀。
	if i := strings.Index(text, "-"); i > 0 {
		stem = strings.TrimSpace(text[:i])
	}
	switch stem {
	case "猜歌":
		return "", true
	case "个人猜歌":
		return "个人", true
	case "团队猜歌":
		return "团队", true
	}
	return "", false
}

// startGame 随机抽歌、保存题目并发送语音。
func startGame(ctx context.Context, c sdk.Call, mode string) error {
	mu.Lock()
	if q := loadQuestion(ctx, c); q != nil && !expired(q) {
		mu.Unlock()
		return c.Reply(ctx, "已经有正在进行的猜歌啦，先作答或发送「揭晓」吧。")
	}
	mu.Unlock()

	song, err := pickSong(ctx)
	if err != nil {
		return c.Reply(ctx, "抽歌失败："+err.Error())
	}

	now := nowFunc()
	q := &question{
		SongID:   song.ID,
		Answer:   strings.TrimSpace(song.Name),
		Artist:   song.performer(),
		Owner:    c.Event.UserID,
		Mode:     mode,
		StartAt:  now.Unix(),
		Deadline: now.Add(questionTimeout).Unix(),
	}

	// 抽歌期间可能已有别人开局，保存前再检查一次。
	mu.Lock()
	if old := loadQuestion(ctx, c); old != nil && !expired(old) {
		mu.Unlock()
		return c.Reply(ctx, "已经有正在进行的猜歌啦，先作答或发送「揭晓」吧。")
	}
	err = saveQuestion(ctx, c, q)
	mu.Unlock()
	if err != nil {
		return err
	}

	scheduleReminder(c, q)
	tips := startTips(mode)
	if err := c.ReplyVoice(ctx, fmt.Sprintf(audioURL, song.ID)); err != nil {
		// 降级：语音发不出去时给出网页地址，题目继续有效。
		return c.Reply(ctx, fmt.Sprintf("语音发送失败，可到 %s 试听后作答。\n%s",
			fmt.Sprintf(songPageURL, song.ID), tips))
	}
	return c.Reply(ctx, tips)
}

// scheduleReminder 在 105 秒时提醒还有 15 秒作答时间。
// 宿主在 handler 返回后仍可调用事件回复能力，因此与上游一致使用后台计时器。
func scheduleReminder(c sdk.Call, q *question) {
	newReminder(questionTimeout-reminderLead, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mu.Lock()
		cur := loadQuestion(ctx, c)
		active := cur != nil && cur.StartAt == q.StartAt && !expired(cur)
		mu.Unlock()
		if !active {
			return
		}
		_ = c.Reply(ctx, "猜歌游戏,你还有15s作答时间")
	})
}

// startTips 返回开局提示文案。
func startTips(mode string) string {
	scope := "本群可作答"
	if mode == "个人" {
		scope = "仅发起者可作答"
	}
	return fmt.Sprintf("开始猜歌！请听语音，直接发送歌名或歌手名作答。\n%s，共 %d 次机会，%d 秒内有效；"+
		"发送「提示」再听一遍，「揭晓」看答案，「结束猜歌」放弃。",
		scope, maxTries, int(questionTimeout/time.Second))
}

// judge 判定答案；explicit 表示来自「猜歌 <答案>」，没有题目时需要提示。
func judge(ctx context.Context, c sdk.Call, guess string, explicit bool) error {
	mu.Lock()
	q := loadQuestion(ctx, c)
	if q == nil {
		mu.Unlock()
		if explicit {
			return c.Reply(ctx, "当前没有正在进行的猜歌，发送「猜歌」开始一局。")
		}
		return nil
	}
	if expired(q) {
		text := "猜歌超时，本题作废。\n" + q.answerText()
		clearQuestion(ctx, c)
		mu.Unlock()
		return c.Reply(ctx, text)
	}
	// 个人模式只允许发起者作答，其他人的消息忽略且不消耗次数。
	if q.Mode == "个人" && q.Owner != c.Event.UserID {
		mu.Unlock()
		return nil
	}
	if matches(q, guess) {
		answer := q.answerText()
		clearQuestion(ctx, c)
		sc, err := addScore(ctx, c, true)
		mu.Unlock()
		if err != nil {
			return err
		}
		return c.Reply(ctx, fmt.Sprintf("太棒了，你猜对了！答案是\n%s\n你累计答对 %d 题。", answer, sc.Correct))
	}

	q.Tries++
	if q.Tries >= maxTries {
		answer := q.answerText()
		clearQuestion(ctx, c)
		sc, err := addScore(ctx, c, false)
		mu.Unlock()
		if err != nil {
			return err
		}
		return c.Reply(ctx, fmt.Sprintf("次数用完了，答案是\n%s\n你累计答对 %d 题、答错 %d 次。",
			answer, sc.Correct, sc.Wrong))
	}
	err := saveQuestion(ctx, c, q)
	left := maxTries - q.Tries
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("答案不对哦，还剩 %d 次机会。", left))
}

// hint 重新发送当前题目音频，每题最多两次，第三次返回已无提示。
func hint(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	q := loadQuestion(ctx, c)
	if q == nil {
		mu.Unlock()
		return nil
	}
	if expired(q) {
		text := "猜歌超时，本题作废。\n" + q.answerText()
		clearQuestion(ctx, c)
		mu.Unlock()
		return c.Reply(ctx, text)
	}
	if q.Mode == "个人" && q.Owner != c.Event.UserID {
		mu.Unlock()
		return nil
	}
	q.Hints++
	if q.Hints > 2 {
		q.Hints = 3
	}
	err := saveQuestion(ctx, c, q)
	hints, songID := q.Hints, q.SongID
	mu.Unlock()
	if err != nil {
		return err
	}
	if hints > 2 {
		return c.Reply(ctx, "已经没有提示了哦")
	}
	if err := c.ReplyVoice(ctx, fmt.Sprintf(audioURL, songID)); err != nil {
		return c.Reply(ctx, fmt.Sprintf("语音发送失败，可到 %s 试听后作答。\n再听这段音频,要仔细听哦",
			fmt.Sprintf(songPageURL, songID)))
	}
	return c.Reply(ctx, "再听这段音频,要仔细听哦")
}

// reveal 揭晓当前题目的答案，题目随即作废。
func reveal(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	q := loadQuestion(ctx, c)
	if q == nil {
		mu.Unlock()
		return c.Reply(ctx, "当前没有正在进行的猜歌，发送「猜歌」开始一局。")
	}
	if expired(q) {
		text := "猜歌超时，本题作废。\n" + q.answerText()
		clearQuestion(ctx, c)
		mu.Unlock()
		return c.Reply(ctx, text)
	}
	answer := q.answerText()
	clearQuestion(ctx, c)
	mu.Unlock()
	return c.Reply(ctx, "本题答案是\n"+answer)
}

// cancel 取消或结束当前题目。
// 对齐上游：只有发起者可以取消，其他人发送时回复「你无权限取消」。
func cancel(ctx context.Context, c sdk.Call, ending bool) error {
	mu.Lock()
	q := loadQuestion(ctx, c)
	if q == nil {
		mu.Unlock()
		return c.Reply(ctx, "当前没有正在进行的猜歌，发送「猜歌」开始一局。")
	}
	if q.Owner != "" && q.Owner != c.Event.UserID {
		mu.Unlock()
		return c.Reply(ctx, "你无权限取消")
	}
	if expired(q) {
		text := "猜歌超时，本题作废。\n" + q.answerText()
		clearQuestion(ctx, c)
		mu.Unlock()
		return c.Reply(ctx, text)
	}
	answer := q.answerText()
	clearQuestion(ctx, c)
	mu.Unlock()
	if ending {
		return c.Reply(ctx, "已结束本局，答案是\n"+answer)
	}
	return c.Reply(ctx, "游戏已取消，猜歌答案是\n"+answer)
}

// showRanking 群聊展示本群排行，私聊展示个人战绩。
func showRanking(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	if c.Event.GroupID == "" {
		sc := score{}
		_ = sdk.LoadJSON(ctx, c.State, scoreKey(c.Event.UserID), &sc)
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("你的猜歌战绩：答对 %d 题，答错 %d 次。", sc.Correct, sc.Wrong))
	}

	var uids []string
	_ = sdk.LoadJSON(ctx, c.State, roomUsersKey(c), &uids)
	entries := make([]rankEntry, 0, len(uids))
	for _, uid := range uids {
		sc := score{}
		_ = sdk.LoadJSON(ctx, c.State, scoreKey(uid), &sc)
		if sc.Correct <= 0 {
			continue
		}
		name := sc.Name
		if name == "" {
			name = "用户" + tail(uid, 4)
		}
		entries = append(entries, rankEntry{Name: name, Correct: sc.Correct})
	}
	mu.Unlock()

	if len(entries) == 0 {
		return c.Reply(ctx, "本群还没有人猜对过，发送「猜歌」来第一局吧！")
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Correct > entries[j].Correct })
	if len(entries) > 10 {
		entries = entries[:10]
	}
	var b strings.Builder
	b.WriteString("本群猜歌排行\n")
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. %s  %d 题\n", i+1, e.Name, e.Correct)
	}
	return c.Reply(ctx, strings.TrimRight(b.String(), "\n"))
}

// pickSong 调用网易云歌单接口随机取一首歌。
func pickSong(ctx context.Context) (track, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, songListURL, nil)
	if err != nil {
		return track{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return track{}, errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return track{}, errors.New("歌曲接口返回 " + resp.Status)
	}

	var data chart
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return track{}, errors.New("歌曲列表解析失败")
	}
	if data.Code != 0 && data.Code != http.StatusOK {
		return track{}, fmt.Errorf("歌曲接口错误码 %d", data.Code)
	}
	list := make([]track, 0, len(data.Playlist.Tracks))
	for _, t := range data.Playlist.Tracks {
		if t.ID > 0 && strings.TrimSpace(t.Name) != "" {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		return track{}, errors.New("歌曲列表为空")
	}
	return list[randIntn(len(list))], nil
}

// matches 支持歌名/歌手的包含匹配与完全匹配（忽略大小写、空白和常见标点）。
func matches(q *question, guess string) bool {
	g := normalize(guess)
	if g == "" {
		return false
	}
	title := normalize(q.Answer)
	artist := normalize(q.Artist)
	if g == title {
		return true
	}
	if artist != "" && g == artist {
		return true
	}
	// 与上游一致：只判断答案是否包含猜测，猜得比答案长不算对。
	if strings.Contains(title, g) {
		return true
	}
	return strings.Contains(artist, g)
}

// ignorePunct 是比对时忽略的空白与标点。
const ignorePunct = "《》〈〉「」『』【】()（）[]{}<>\"'“”‘’.,，。!！?？:：;；-—~～·、/\\|&+*"

// normalize 把文本转为小写并去掉空白和常见标点。
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsSpace(r) || strings.ContainsRune(ignorePunct, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// roomID 群聊用群号，私聊回退到用户 ID。
func roomID(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return c.Event.GroupID
	}
	return c.Event.UserID
}

func questionKey(c sdk.Call) string  { return "guessmusic:q:" + roomID(c) }
func roomUsersKey(c sdk.Call) string { return "guessmusic:room:" + roomID(c) }
func scoreKey(userID string) string  { return "guessmusic:user:" + userID }

// loadQuestion 读取当前题目，不存在时返回 nil。
func loadQuestion(ctx context.Context, c sdk.Call) *question {
	q := question{}
	_ = sdk.LoadJSON(ctx, c.State, questionKey(c), &q)
	if q.Answer == "" {
		return nil
	}
	return &q
}

func saveQuestion(ctx context.Context, c sdk.Call, q *question) error {
	return sdk.SaveJSON(ctx, c.State, questionKey(c), q)
}

// clearQuestion 写入空题目，等价于作废。
func clearQuestion(ctx context.Context, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, c.State, questionKey(c), question{})
}

// expired 判断题目是否已超时。
func expired(q *question) bool { return nowFunc().Unix() > q.Deadline }

// addScore 累加用户成绩，并把用户登记进房间排行索引。
func addScore(ctx context.Context, c sdk.Call, correct bool) (score, error) {
	sc := score{}
	_ = sdk.LoadJSON(ctx, c.State, scoreKey(c.Event.UserID), &sc)
	if name := strings.TrimSpace(c.Event.Nickname); name != "" {
		sc.Name = name
	}
	if correct {
		sc.Correct++
	} else {
		sc.Wrong++
	}
	err := sdk.SaveJSON(ctx, c.State, scoreKey(c.Event.UserID), sc)

	var uids []string
	_ = sdk.LoadJSON(ctx, c.State, roomUsersKey(c), &uids)
	if !containsString(uids, c.Event.UserID) && len(uids) < roomUserLimit {
		uids = append(uids, c.Event.UserID)
		_ = sdk.SaveJSON(ctx, c.State, roomUsersKey(c), uids)
	}
	return sc, err
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// tail 取字符串末尾 n 个字符。
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// Package wordle 猜单词（文本版）。上游用图片绘制色块并通过翻译接口给出中文释义，
// 这里改为文本反馈；题库使用上游 CET-4 词表，校验词典使用上游 dict 词表，均内嵌。
package wordle

import (
	"context"
	_ "embed"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// CET-4 题库与校验词典均来自上游 Wordle 数据。
var (
	//go:embed cet4_5.txt
	cet4Five string
	//go:embed cet4_6.txt
	cet4Six string
	//go:embed cet4_7.txt
	cet4Seven string

	//go:embed dict_5.txt
	dictFive string
	//go:embed dict_6.txt
	dictSix string
	//go:embed dict_7.txt
	dictSeven string
)

var (
	nowFunc = time.Now
	pick    = rand.Intn
	mu      sync.Mutex

	startRe = regexp.MustCompile(`^(个人|团队)(五阶|六阶|七阶)?猜单词$`)
	guessRe = regexp.MustCompile(`^[a-zA-Z]{5,7}$`)

	// newReminder 便于测试关闭或捕获 105 秒提醒。
	newReminder = func(d time.Duration, f func()) *time.Timer { return time.AfterFunc(d, f) }
)

const (
	gameTimeout = 2 * time.Minute
	// reminderLead 是「还有 15 秒」提醒的提前量。
	reminderLead = 15 * time.Second
)

var classMap = map[string]int{"": 5, "五阶": 5, "六阶": 6, "七阶": 7}

type session struct {
	Word     string   `json:"word"`
	Owner    string   `json:"owner,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Class    string   `json:"class,omitempty"`
	Tries    int      `json:"tries"`
	Guesses  []string `json:"guesses,omitempty"`
	Deadline int64    `json:"deadline"`
	Active   bool     `json:"active"`
}

// Definition subscribes to group messages for 猜单词.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "wordle",
		Name:        "猜单词",
		Version:     "1.0.0",
		Description: "发送「个人猜单词」或「团队猜单词」开始猜英文单词，支持五/六/七阶",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "wordle", Trigger: "个人猜单词"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle starts a game or processes a letter-only guess.
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	if m := startRe.FindStringSubmatch(text); m != nil {
		return startGame(ctx, c, m[1], m[2])
	}

	mu.Lock()
	s := load(ctx, c)
	mu.Unlock()
	if s.Owner != "" && s.Owner != c.Event.UserID {
		return nil
	}
	if text == "再来一局" {
		return restart(ctx, c, s)
	}
	if !s.Active {
		return nil
	}
	if text == "结束猜单词" || text == "结束" {
		return endGame(ctx, c, s, "游戏已结束")
	}
	if !guessRe.MatchString(text) {
		return nil
	}
	if nowFunc().Unix() > s.Deadline {
		return endGame(ctx, c, s, "猜单词超时，游戏结束")
	}
	word := strings.ToLower(text)
	if len(word) != len(s.Word) {
		return c.Reply(ctx, "单词长度错误")
	}
	// 与上游一致：先判断是否命中答案，其余猜测必须存在于词典中。
	if word != s.Word && !inDict(len(word), word) {
		return c.Reply(ctx, "你确定存在这样的单词吗？")
	}

	mu.Lock()
	s = load(ctx, c)
	if !s.Active {
		mu.Unlock()
		return nil
	}
	s.Guesses = append(s.Guesses, word)
	win := word == s.Word
	runOut := len(s.Guesses) >= s.Tries
	if win || runOut {
		s.Active = false
	}
	if err := save(ctx, c, s); err != nil {
		mu.Unlock()
		return err
	}
	answer, tries, guesses := s.Word, s.Tries, append([]string(nil), s.Guesses...)
	mu.Unlock()

	var b strings.Builder
	b.WriteString(board(answer, guesses))
	b.WriteString("\n")
	switch {
	case win:
		b.WriteString("太棒了，你猜出来了！答案是: " + answer)
	case runOut:
		b.WriteString("游戏结束...答案是: " + answer)
	default:
		b.WriteString("剩余次数: " + strconv.Itoa(tries-len(guesses)))
	}
	return c.ReplyKeyboardOrText(ctx, b.String(), gameKeyboard())
}

// gameKeyboard 对局下方的快捷按钮。
func gameKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("wordle-again", "再来一局", "再来一局"),
		sdk.CommandButton("wordle-stop", "结束", "结束猜单词"),
	))
}

// restart 「再来一局」：沿用上一局的模式与阶数重新开始。
func restart(ctx context.Context, c sdk.Call, s session) error {
	if s.Mode == "" {
		return c.Reply(ctx, "先发送「个人猜单词」或「团队猜单词」开始一局")
	}
	return startGame(ctx, c, s.Mode, s.Class)
}

func startGame(ctx context.Context, c sdk.Call, mode, classText string) error {
	class := classMap[classText]
	candidates := wordsOfLength(class)
	if len(candidates) == 0 {
		return c.Reply(ctx, "这个阶数的词表为空")
	}

	mu.Lock()
	s := load(ctx, c)
	if s.Active && nowFunc().Unix() <= s.Deadline {
		mu.Unlock()
		return c.Reply(ctx, "已经有正在进行的游戏...")
	}
	owner := ""
	if mode == "个人" {
		owner = c.Event.UserID
	}
	s = session{
		Word:     candidates[pick(len(candidates))],
		Owner:    owner,
		Mode:     mode,
		Class:    classText,
		Tries:    class + 1,
		Deadline: nowFunc().Add(gameTimeout).Unix(),
		Active:   true,
	}
	err := save(ctx, c, s)
	tries := s.Tries
	mu.Unlock()
	if err != nil {
		return err
	}
	scheduleReminder(c, s)
	return c.ReplyKeyboardOrText(ctx, strconv.Itoa(tries)+"次机会猜出单词，单词长度为"+strconv.Itoa(class)+"，请发送单词\n"+
		"√ 位置正确，△ 字母存在但位置不对，× 不存在", gameKeyboard())
}

func endGame(ctx context.Context, c sdk.Call, s session, prefix string) error {
	answer := s.Word
	mu.Lock()
	cur := load(ctx, c)
	if cur.Active {
		cur.Active = false
		_ = save(ctx, c, cur)
	}
	mu.Unlock()
	return c.ReplyKeyboardOrText(ctx, prefix+"...答案是: "+answer, gameKeyboard())
}

// scheduleReminder 在 105 秒时提醒还有 15 秒作答时间。
// 答案翻译依赖上游的在线翻译接口，宿主 SDK 未提供翻译能力，故只展示英文答案。
func scheduleReminder(c sdk.Call, s session) {
	newReminder(gameTimeout-reminderLead, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mu.Lock()
		cur := load(ctx, c)
		active := cur.Active && cur.Word == s.Word && cur.Deadline == s.Deadline && nowFunc().Unix() <= cur.Deadline
		mu.Unlock()
		if !active {
			return
		}
		_ = c.Reply(ctx, "猜单词，你还有15s作答时间")
	})
}

func stateKey(c sdk.Call) string { return "wordle:" + c.Event.GroupID }

func load(ctx context.Context, c sdk.Call) session {
	s := session{}
	_ = sdk.LoadJSON(ctx, c.State, stateKey(c), &s)
	return s
}

func save(ctx context.Context, c sdk.Call, s session) error {
	return sdk.SaveJSON(ctx, c.State, stateKey(c), s)
}

func wordsOfLength(n int) []string {
	switch n {
	case 6:
		return words6
	case 7:
		return words7
	default:
		return words5
	}
}

// feedback marks each letter: 2 位置正确, 1 存在但位置不对, 0 不存在。
func feedback(target, guess string) []int {
	marks := make([]int, len(guess))
	rest := map[byte]int{}
	for i := 0; i < len(target); i++ {
		if guess[i] == target[i] {
			marks[i] = 2
		} else {
			rest[target[i]]++
		}
	}
	for i := 0; i < len(guess); i++ {
		if marks[i] == 2 {
			continue
		}
		if rest[guess[i]] > 0 {
			marks[i] = 1
			rest[guess[i]]--
		}
	}
	return marks
}

func board(answer string, guesses []string) string {
	rows := make([]string, 0, len(guesses)*2)
	for _, g := range guesses {
		rows = append(rows, spaced(strings.ToUpper(g)))
		rows = append(rows, spaced(marks(feedback(answer, g))))
	}
	return strings.Join(rows, "\n")
}

func spaced(s string) string {
	var b strings.Builder
	first := true
	for _, r := range s {
		if !first {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		first = false
	}
	return b.String()
}

func marks(m []int) string {
	var b strings.Builder
	for _, v := range m {
		switch v {
		case 2:
			b.WriteString("√")
		case 1:
			b.WriteString("△")
		default:
			b.WriteString("×")
		}
	}
	return b.String()
}

// splitWords 按行切分内嵌词表，忽略空白行。
func splitWords(data string) []string {
	lines := strings.Split(data, "\n")
	words := make([]string, 0, len(lines))
	for _, line := range lines {
		if w := strings.TrimSpace(line); w != "" {
			words = append(words, w)
		}
	}
	return words
}

var (
	words5 = splitWords(cet4Five)
	words6 = splitWords(cet4Six)
	words7 = splitWords(cet4Seven)

	dicts5 = splitWords(dictFive)
	dicts6 = splitWords(dictSix)
	dicts7 = splitWords(dictSeven)
)

func dictOfLength(n int) []string {
	switch n {
	case 6:
		return dicts6
	case 7:
		return dicts7
	default:
		return dicts5
	}
}

// inDict 用二分查找校验单词是否在对应阶词典中。
func inDict(n int, word string) bool {
	list := dictOfLength(n)
	i := sort.SearchStrings(list, word)
	return i < len(list) && list[i] == word
}

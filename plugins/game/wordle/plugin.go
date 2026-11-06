// Package wordle 猜单词（文本版）。上游用图片绘制色块并通过翻译接口给出中文释义，
// 这里改为文本反馈，词表内嵌在插件中。
package wordle

import (
	"context"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

var (
	nowFunc = time.Now
	pick    = rand.Intn
	mu      sync.Mutex

	startRe = regexp.MustCompile(`^(个人|团队)(五阶|六阶|七阶)?猜单词$`)
	guessRe = regexp.MustCompile(`^[a-zA-Z]{5,7}$`)
)

const gameTimeout = 2 * time.Minute

var classMap = map[string]int{"": 5, "五阶": 5, "六阶": 6, "七阶": 7}

type session struct {
	Word     string   `json:"word"`
	Owner    string   `json:"owner,omitempty"`
	Tries    int      `json:"tries"`
	Guesses  []string `json:"guesses,omitempty"`
	Deadline int64    `json:"deadline"`
	Active   bool     `json:"active"`
}

// Definition subscribes to group messages for 猜单词.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "wordle",
		Name:        "猜单词",
		Version:     "1.0.0",
		Description: "发送「个人猜单词」或「团队猜单词」开始猜英文单词，支持五/六/七阶",
	}, sdk.MessageEvents, Handle, sdk.WithPermissions("state.read", "state.write"))
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
	if !s.Active {
		return nil
	}
	if s.Owner != "" && s.Owner != c.Event.UserID {
		return nil
	}
	if text == "结束猜单词" {
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
	return c.Reply(ctx, b.String())
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
	return c.Reply(ctx, strconv.Itoa(tries)+"次机会猜出单词，单词长度为"+strconv.Itoa(class)+"，请发送单词\n"+
		"√ 位置正确，△ 字母存在但位置不对，× 不存在")
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
	return c.Reply(ctx, prefix+"...答案是: "+answer)
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

var words5 = []string{
	"about", "above", "actor", "adapt", "admit", "adopt", "adult", "after", "again", "agent",
	"agree", "ahead", "alarm", "album", "alert", "alike", "alive", "allow", "alone", "along",
	"alter", "among", "anger", "angle", "angry", "apart", "apple", "apply", "arena", "argue",
	"arise", "array", "aside", "asset", "avoid", "awake", "award", "aware", "badly", "baker",
	"basic", "beach", "began", "begin", "being", "below", "bench", "birth", "black", "blame",
	"blind", "block", "blood", "board", "brain", "brand", "bread", "break", "bring", "broad",
	"brown", "build", "built", "cable", "carry", "catch", "cause", "chain", "chair", "chart",
	"chase", "cheap", "check", "chest", "child", "chose", "civil", "claim", "class", "clean",
	"clear", "climb", "clock", "close", "cloud", "coach", "coast", "could", "count", "court",
	"cover", "crack", "craft", "crash", "crazy", "cream", "crime", "cross", "crowd", "crown",
	"curve", "cycle",
}

var words6 = []string{
	"animal", "around", "answer", "appear", "arrive", "artist", "aspect", "attack", "autumn", "basket",
	"battle", "beauty", "become", "before", "behind", "better", "beyond", "bishop", "bottle", "bottom",
	"branch", "breath", "bridge", "bright", "broken", "budget", "butter", "camera", "cancer", "carbon",
	"castle", "center", "change", "charge", "choice", "church", "circle", "client", "closed", "coffee",
	"column", "combat", "common", "corner", "cotton", "county", "course", "cousin", "danger", "dealer",
	"debate", "decide", "degree", "demand", "desert", "design", "desire", "detail", "device", "dinner",
	"doctor", "dollar", "double", "driver", "easily", "effect", "eleven", "empire", "energy", "engine",
	"enough", "escape", "except", "excuse", "family", "famous", "father", "fellow", "female", "figure",
	"finger", "flight", "flower", "forest", "forget", "former", "friend", "future", "garden", "gather",
	"gender", "ground", "growth", "guilty", "health", "height", "highly", "theory",
}

var words7 = []string{
	"ability", "account", "achieve", "acquire", "address", "advance", "against", "already", "ancient", "another",
	"article", "balance", "battery", "because", "believe", "benefit", "between", "brother", "capital", "century",
	"certain", "chamber", "chapter", "college", "comment", "company", "compare", "project", "service", "support",
	"tonight", "trouble", "various", "without", "working",
}

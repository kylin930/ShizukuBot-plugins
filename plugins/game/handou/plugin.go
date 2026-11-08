// Package handou 猜成语（文本版）。上游依赖图片绘制、字体与百度词典接口，
// 这里改为文本反馈，并内嵌一个小规模兜底成语表；首次开局时会惰性下载上游
// 完整 idiom.json，下载失败则继续使用内置词库，不阻塞游戏。
package handou

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

var (
	nowFunc = time.Now
	pick    = rand.Intn
	mu      sync.Mutex
	statsMu sync.Mutex

	startRe = regexp.MustCompile(`^(个人|团队)猜成语$`)
	hotRe   = regexp.MustCompile(`^猜成语热门(汉字|成语)$`)
	hanRe   = regexp.MustCompile(`^[\p{Han}]+$`)

	// idiomURL 是上游完整成语词库，开局时惰性下载。
	idiomURL = "https://cdn.jsdelivr.net/gh/FloatTech/zbpdata@main/Handou/idiom.json"
	// httpClient 用于下载词库，测试通过替换 fetchIdiomJSON 保持离线。
	httpClient = &http.Client{Timeout: 20 * time.Second}
	// fetchIdiomJSON 拉取词库原始 JSON，测试中可整体替换。
	fetchIdiomJSON = defaultFetchIdiomJSON

	idiomMu      sync.RWMutex
	idiomCache   map[string]idiom
	idiomKeys    []string
	idiomDetails map[string]idiomDetail
	idiomTried   bool

	// newReminder 便于测试关闭或捕获 105 秒提醒。
	newReminder = func(d time.Duration, f func()) *time.Timer { return time.AfterFunc(d, f) }
)

const (
	tries       = 7
	gameTimeout = 2 * time.Minute
	// reminderLead 是「还有 15 秒」提醒的提前量。
	reminderLead = 15 * time.Second
)

type idiom struct {
	Word    string
	Pinyin  []string
	Explain string
}

// idiomDetail 保存上游词库中的附加释义字段，内置词库没有这些字段时留空。
type idiomDetail struct {
	Baobian    string
	Derivation string
	Synonyms   []string
}

type session struct {
	Word     string   `json:"word"`
	Owner    string   `json:"owner,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Guesses  []string `json:"guesses,omitempty"`
	Deadline int64    `json:"deadline"`
	Active   bool     `json:"active"`
}

// Definition subscribes to group messages for 猜成语.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "handou",
		Name:        "猜成语",
		Version:     "1.0.0",
		Description: "发送「个人猜成语」或「团队猜成语」开始猜四字成语，每人七次机会",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "handou", Trigger: "个人猜成语"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle starts a round or checks a four-character guess.
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	if m := startRe.FindStringSubmatch(text); m != nil {
		return startGame(ctx, c, m[1])
	}
	if m := hotRe.FindStringSubmatch(text); m != nil {
		return showHot(ctx, c, m[1])
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
	if text == "结束猜成语" || text == "结束" {
		return endGame(ctx, c, "游戏已结束")
	}
	if !hanRe.MatchString(text) || len([]rune(text)) != len([]rune(s.Word)) {
		return nil
	}
	if nowFunc().Unix() > s.Deadline {
		return endGame(ctx, c, "猜成语超时，游戏结束")
	}
	if guessedBefore(s.Guesses, text) {
		return c.Reply(ctx, "该成语已经猜过了")
	}
	if _, ok := lookup(text); !ok {
		return c.Reply(ctx, "你确定存在这样的成语吗？")
	}

	mu.Lock()
	s = load(ctx, c)
	if !s.Active {
		mu.Unlock()
		return nil
	}
	s.Guesses = append(s.Guesses, text)
	win := text == s.Word
	runOut := len(s.Guesses) >= tries
	if win || runOut {
		s.Active = false
	}
	if err := save(ctx, c, s); err != nil {
		mu.Unlock()
		return err
	}
	answer, guesses := s.Word, append([]string(nil), s.Guesses...)
	mu.Unlock()

	recordGuess(ctx, c, text)
	answerItem, _ := lookup(answer)

	var b strings.Builder
	b.WriteString(board(answer, guesses))
	b.WriteString("\n")
	switch {
	case win:
		b.WriteString("太棒了，你猜出来了！\n答案是: " + answerText(answerItem))
	case runOut:
		b.WriteString("游戏结束...\n答案是: " + answerText(answerItem))
	default:
		b.WriteString("已确认: " + confirmed(answer, guesses))
		b.WriteString("\n剩余次数: " + strconv.Itoa(tries-len(guesses)))
	}
	return c.ReplyKeyboardOrText(ctx, b.String(), gameKeyboard())
}

// gameKeyboard 对局下方的快捷按钮。
func gameKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("handou-again", "再来一局", "再来一局"),
		sdk.CommandButton("handou-stop", "结束", "结束猜成语"),
	))
}

// restart 「再来一局」：沿用上一局的模式重新开始。
func restart(ctx context.Context, c sdk.Call, s session) error {
	if s.Mode == "" {
		return c.Reply(ctx, "先发送「个人猜成语」或「团队猜成语」开始一局")
	}
	return startGame(ctx, c, s.Mode)
}

func startGame(ctx context.Context, c sdk.Call, mode string) error {
	ensureIdioms(ctx)
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
	target := pickIdiom()
	s = session{
		Word:     target.Word,
		Owner:    owner,
		Mode:     mode,
		Deadline: nowFunc().Add(gameTimeout).Unix(),
		Active:   true,
	}
	err := save(ctx, c, s)
	mu.Unlock()
	if err != nil {
		return err
	}
	scheduleReminder(c, s)
	return c.ReplyKeyboardOrText(ctx, "你有7次机会猜出"+strconv.Itoa(len([]rune(target.Word)))+"字成语\n首字拼音为："+target.Pinyin[0]+
		"\n每次猜词后显示 对/有/无：对=位置正确，有=字存在但位置不对，无=不存在", gameKeyboard())
}

func endGame(ctx context.Context, c sdk.Call, prefix string) error {
	mu.Lock()
	s := load(ctx, c)
	if !s.Active {
		mu.Unlock()
		return nil
	}
	s.Active = false
	err := save(ctx, c, s)
	answer := s.Word
	mu.Unlock()
	if err != nil {
		return err
	}
	item, _ := lookup(answer)
	return c.ReplyKeyboardOrText(ctx, prefix+"...\n答案是: "+answerText(item), gameKeyboard())
}

// scheduleReminder 在 105 秒时提醒还有 15 秒作答时间。
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
		_ = c.Reply(ctx, "猜成语，你还有15s作答时间")
	})
}

// answerText 按上游格式输出答案、褒贬、词源、解释与近义词。
func answerText(v idiom) string {
	var b strings.Builder
	b.WriteString(v.Word)
	d := idiomDetailOf(v.Word)
	if d.Baobian != "" && d.Baobian != "-" {
		b.WriteString("\n" + d.Baobian + "词")
	}
	if d.Derivation != "" && d.Derivation != "-" {
		b.WriteString("\n词源:\n" + d.Derivation)
	} else {
		b.WriteString("\n词源:无")
	}
	if v.Explain != "" && v.Explain != "-" {
		b.WriteString("\n解释:\n" + v.Explain)
	} else {
		b.WriteString("\n解释:无")
	}
	if len(d.Synonyms) > 0 {
		b.WriteString("\n近义词:\n" + strings.Join(d.Synonyms, ","))
	}
	return b.String()
}

func idiomDetailOf(word string) idiomDetail {
	idiomMu.RLock()
	defer idiomMu.RUnlock()
	return idiomDetails[word]
}

func stateKey(c sdk.Call) string { return "handou:" + c.Event.GroupID }

func load(ctx context.Context, c sdk.Call) session {
	s := session{}
	_ = sdk.LoadJSON(ctx, c.State, stateKey(c), &s)
	return s
}

func save(ctx context.Context, c sdk.Call, s session) error {
	return sdk.SaveJSON(ctx, c.State, stateKey(c), s)
}

// idiomRecord 对应上游 idiom.json 中的单条成语。
type idiomRecord struct {
	Word        string   `json:"word"`
	Chars       []string `json:"chars"`
	Pinyin      []string `json:"pinyin"`
	Baobian     string   `json:"baobian"`
	Explanation string   `json:"explanation"`
	Derivation  string   `json:"derivation"`
	Synonyms    []string `json:"synonyms"`
}

// defaultFetchIdiomJSON 下载上游完整词库。
func defaultFetchIdiomJSON(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, idiomURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("idiom.json 返回 %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// ensureIdioms 只在首次开局时尝试下载完整词库，失败后继续使用内置词库。
func ensureIdioms(ctx context.Context) {
	idiomMu.Lock()
	defer idiomMu.Unlock()
	if idiomTried {
		return
	}
	idiomTried = true
	data, err := fetchIdiomJSON(ctx)
	if err != nil {
		return
	}
	cache, keys, details, err := parseIdiomJSON(data)
	if err != nil || len(keys) == 0 {
		return
	}
	idiomCache, idiomKeys, idiomDetails = cache, keys, details
}

// parseIdiomJSON 把上游 JSON 映射解析为内存词库，只保留字段完整的成语。
func parseIdiomJSON(data []byte) (map[string]idiom, []string, map[string]idiomDetail, error) {
	var records map[string]idiomRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, nil, nil, err
	}
	cache := make(map[string]idiom, len(records))
	details := make(map[string]idiomDetail, len(records))
	for key, r := range records {
		word := r.Word
		if word == "" {
			word = key
		}
		n := len([]rune(word))
		if n == 0 || len(r.Chars) != n || len(r.Pinyin) != n || r.Pinyin[0] == "" {
			continue
		}
		explain := r.Explanation
		if explain == "" {
			explain = r.Derivation
		}
		if explain == "" {
			explain = "暂无解释"
		}
		cache[word] = idiom{Word: word, Pinyin: r.Pinyin, Explain: explain}
		details[word] = idiomDetail{Baobian: r.Baobian, Derivation: r.Derivation, Synonyms: r.Synonyms}
	}
	keys := make([]string, 0, len(cache))
	for k := range cache {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return cache, keys, details, nil
}

// pickIdiom 从已加载的上游词库中随机取题，未加载时回退内置词库。
func pickIdiom() idiom {
	idiomMu.RLock()
	defer idiomMu.RUnlock()
	if len(idiomKeys) > 0 {
		return idiomCache[idiomKeys[pick(len(idiomKeys))]]
	}
	return idioms[pick(len(idioms))]
}

func lookup(word string) (idiom, bool) {
	idiomMu.RLock()
	v, ok := idiomCache[word]
	idiomMu.RUnlock()
	if ok {
		return v, true
	}
	for _, v := range idioms {
		if v.Word == word {
			return v, true
		}
	}
	return idiom{}, false
}

// handouStats 是跨局累计的猜词统计，用于「猜成语热门」。
type handouStats struct {
	Chars  map[string]int `json:"chars,omitempty"`
	Idioms map[string]int `json:"idioms,omitempty"`
}

func statsKey() string { return "handou:stats" }

// recordGuess 累计每个汉字与每个成语被猜中的次数。
func recordGuess(ctx context.Context, c sdk.Call, word string) {
	if c.State == nil {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()
	st := handouStats{}
	_ = sdk.LoadJSON(ctx, c.State, statsKey(), &st)
	if st.Chars == nil {
		st.Chars = make(map[string]int)
	}
	if st.Idioms == nil {
		st.Idioms = make(map[string]int)
	}
	for _, r := range word {
		st.Chars[string(r)]++
	}
	st.Idioms[word]++
	_ = sdk.SaveJSON(ctx, c.State, statsKey(), st)
}

// showHot 输出热门汉字或热门成语 Top10。
func showHot(ctx context.Context, c sdk.Call, kind string) error {
	statsMu.Lock()
	st := handouStats{}
	_ = sdk.LoadJSON(ctx, c.State, statsKey(), &st)
	statsMu.Unlock()

	counts := st.Chars
	if kind == "成语" {
		counts = st.Idioms
	}
	if len(counts) == 0 {
		return c.Reply(ctx, "还没有统计数据，先来一局猜成语吧")
	}
	type entry struct {
		key   string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for k, v := range counts {
		entries = append(entries, entry{key: k, count: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].key < entries[j].key
	})
	if len(entries) > 10 {
		entries = entries[:10]
	}
	lines := []string{"热门" + kind + "："}
	for _, e := range entries {
		lines = append(lines, e.key+":"+strconv.Itoa(e.count))
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// feedback marks each character: 2 位置正确, 1 字存在但位置不对, 0 不存在。
func feedback(target, guess []rune) []int {
	marks := make([]int, len(guess))
	rest := map[rune]int{}
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
	var rows []string
	for _, g := range guesses {
		rows = append(rows, spaced(g))
		rows = append(rows, markText(feedback([]rune(answer), []rune(g))))
	}
	return strings.Join(rows, "\n")
}

func guessedBefore(guesses []string, word string) bool {
	for _, g := range guesses {
		if g == word {
			return true
		}
	}
	return false
}

// confirmed 展示已经猜对位置的汉字，其余用问号占位。
func confirmed(answer string, guesses []string) string {
	target := []rune(answer)
	known := make([]bool, len(target))
	for _, g := range guesses {
		guess := []rune(g)
		for i := range target {
			if i < len(guess) && guess[i] == target[i] {
				known[i] = true
			}
		}
	}
	var b strings.Builder
	for i, r := range target {
		if known[i] {
			b.WriteRune(r)
		} else {
			b.WriteString("？")
		}
	}
	return b.String()
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

func markText(m []int) string {
	var b strings.Builder
	for i, v := range m {
		if i > 0 {
			b.WriteByte(' ')
		}
		switch v {
		case 2:
			b.WriteString("对")
		case 1:
			b.WriteString("有")
		default:
			b.WriteString("无")
		}
	}
	return b.String()
}

var idioms = []idiom{
	{"一心一意", []string{"yi", "xin", "yi", "yi"}, "形容心思专一，没有别的念头。"},
	{"三心二意", []string{"san", "xin", "er", "yi"}, "形容犹豫不决或意志不坚定。"},
	{"画蛇添足", []string{"hua", "she", "tian", "zu"}, "比喻做了多余的事，反而把事情弄坏。"},
	{"守株待兔", []string{"shou", "zhu", "dai", "tu"}, "比喻死守狭隘经验，不知变通。"},
	{"亡羊补牢", []string{"wang", "yang", "bu", "lao"}, "比喻出了问题以后想办法补救。"},
	{"掩耳盗铃", []string{"yan", "er", "dao", "ling"}, "比喻自己欺骗自己，明明掩盖不住。"},
	{"刻舟求剑", []string{"ke", "zhou", "qiu", "jian"}, "比喻拘泥成法，不知随情况变化。"},
	{"杯弓蛇影", []string{"bei", "gong", "she", "ying"}, "比喻疑神疑鬼，自相惊扰。"},
	{"井底之蛙", []string{"jing", "di", "zhi", "wa"}, "比喻见识狭窄的人。"},
	{"对牛弹琴", []string{"dui", "niu", "tan", "qin"}, "比喻对不懂道理的人讲道理。"},
	{"狐假虎威", []string{"hu", "jia", "hu", "wei"}, "比喻依仗别人的势力欺压人。"},
	{"叶公好龙", []string{"ye", "gong", "hao", "long"}, "比喻口头上说爱好某事物，实际并不真爱好。"},
	{"画龙点睛", []string{"hua", "long", "dian", "jing"}, "比喻在关键处加上一笔，使内容更加生动。"},
	{"塞翁失马", []string{"sai", "weng", "shi", "ma"}, "比喻暂时受到损失，也许反而因此得到好处。"},
	{"愚公移山", []string{"yu", "gong", "yi", "shan"}, "比喻坚持不懈地改造自然和坚定不移地斗争。"},
	{"精卫填海", []string{"jing", "wei", "tian", "hai"}, "比喻意志坚决，不畏艰难。"},
	{"夸父逐日", []string{"kua", "fu", "zhu", "ri"}, "比喻人有宏大的志向或巨大的力量。"},
	{"后羿射日", []string{"hou", "yi", "she", "ri"}, "比喻为民除害的英雄行为。"},
	{"嫦娥奔月", []string{"chang", "e", "ben", "yue"}, "指女子向往月宫，也比喻追求美好的事物。"},
	{"牛郎织女", []string{"niu", "lang", "zhi", "nv"}, "比喻夫妻分离，一年只能相会一次。"},
	{"望梅止渴", []string{"wang", "mei", "zhi", "ke"}, "比喻用空想安慰自己。"},
	{"青梅竹马", []string{"qing", "mei", "zhu", "ma"}, "形容男女小时候天真无邪，一起玩耍。"},
	{"一鸣惊人", []string{"yi", "ming", "jing", "ren"}, "比喻平时没有突出的表现，一下子做出惊人的成绩。"},
	{"一箭双雕", []string{"yi", "jian", "shuang", "diao"}, "比喻做一件事达到两个目的。"},
	{"一石二鸟", []string{"yi", "shi", "er", "niao"}, "比喻做一件事得到两种好处。"},
	{"一举两得", []string{"yi", "ju", "liang", "de"}, "做一件事得到两方面的好处。"},
	{"一诺千金", []string{"yi", "nuo", "qian", "jin"}, "形容说话算数，极有信用。"},
	{"一帆风顺", []string{"yi", "fan", "feng", "shun"}, "比喻非常顺利，毫无挫折。"},
	{"一路平安", []string{"yi", "lu", "ping", "an"}, "祝愿旅途平安。"},
	{"万事如意", []string{"wan", "shi", "ru", "yi"}, "一切都符合心意。"},
	{"心想事成", []string{"xin", "xiang", "shi", "cheng"}, "心里想到的都能实现。"},
	{"五福临门", []string{"wu", "fu", "lin", "men"}, "五种福气一起来到家中。"},
	{"年年有余", []string{"nian", "nian", "you", "yu"}, "每年都有多余的财富。"},
	{"岁岁平安", []string{"sui", "sui", "ping", "an"}, "祝愿年年平安无事。"},
	{"大吉大利", []string{"da", "ji", "da", "li"}, "非常吉祥顺利。"},
	{"喜气洋洋", []string{"xi", "qi", "yang", "yang"}, "充满了欢喜的神色或气氛。"},
	{"张灯结彩", []string{"zhang", "deng", "jie", "cai"}, "形容场面喜庆热闹。"},
	{"春暖花开", []string{"chun", "nuan", "hua", "kai"}, "比喻大好时机或美好景象。"},
	{"鸟语花香", []string{"niao", "yu", "hua", "xiang"}, "形容春天的美好景象。"},
	{"山清水秀", []string{"shan", "qing", "shui", "xiu"}, "形容风景优美。"},
	{"风和日丽", []string{"feng", "he", "ri", "li"}, "形容天气晴朗暖和。"},
	{"风平浪静", []string{"feng", "ping", "lang", "jing"}, "比喻平静无事。"},
	{"海阔天空", []string{"hai", "kuo", "tian", "kong"}, "形容大自然的广阔，也比喻心胸开阔。"},
	{"天高云淡", []string{"tian", "gao", "yun", "dan"}, "形容秋天晴朗的天空。"},
	{"秋高气爽", []string{"qiu", "gao", "qi", "shuang"}, "形容秋天天空晴朗，空气清新。"},
	{"冰天雪地", []string{"bing", "tian", "xue", "di"}, "形容冰雪漫天盖地，非常寒冷。"},
	{"万里无云", []string{"wan", "li", "wu", "yun"}, "形容天气非常晴朗。"},
	{"阳光明媚", []string{"yang", "guang", "ming", "mei"}, "形容天气晴朗，阳光灿烂。"},
	{"星光灿烂", []string{"xing", "guang", "can", "lan"}, "形容星光闪耀，十分明亮。"},
	{"月明星稀", []string{"yue", "ming", "xing", "xi"}, "月亮明亮时星星显得稀疏。"},
	{"灯火通明", []string{"deng", "huo", "tong", "ming"}, "形容灯光很亮，一片光明。"},
	{"车水马龙", []string{"che", "shui", "ma", "long"}, "形容车马往来不绝，非常热闹。"},
	{"人山人海", []string{"ren", "shan", "ren", "hai"}, "形容聚集的人极多。"},
	{"人来人往", []string{"ren", "lai", "ren", "wang"}, "形容来往的人很多，非常热闹。"},
	{"川流不息", []string{"chuan", "liu", "bu", "xi"}, "形容行人车马像水流一样连续不断。"},
	{"络绎不绝", []string{"luo", "yi", "bu", "jue"}, "形容来往的人或车马连续不断。"},
	{"门庭若市", []string{"men", "ting", "ruo", "shi"}, "形容来的人很多，非常热闹。"},
	{"安居乐业", []string{"an", "ju", "le", "ye"}, "形容生活安定，愉快地从事自己的工作。"},
	{"国泰民安", []string{"guo", "tai", "min", "an"}, "国家太平，人民安乐。"},
	{"太平盛世", []string{"tai", "ping", "sheng", "shi"}, "形容安定兴盛的时代。"},
	{"欣欣向荣", []string{"xin", "xin", "xiang", "rong"}, "形容事业蓬勃发展，兴旺昌盛。"},
	{"蒸蒸日上", []string{"zheng", "zheng", "ri", "shang"}, "形容事业一天天向上发展。"},
	{"日新月异", []string{"ri", "xin", "yue", "yi"}, "形容变化很快，不断出现新事物。"},
	{"翻天覆地", []string{"fan", "tian", "fu", "di"}, "形容变化巨大而彻底。"},
	{"焕然一新", []string{"huan", "ran", "yi", "xin"}, "形容出现了崭新的面貌。"},
	{"与时俱进", []string{"yu", "shi", "ju", "jin"}, "随着时代的发展而不断发展前进。"},
	{"再接再厉", []string{"zai", "jie", "zai", "li"}, "比喻继续努力，再加一把劲。"},
	{"持之以恒", []string{"chi", "zhi", "yi", "heng"}, "长久地坚持下去。"},
	{"坚持不懈", []string{"jian", "chi", "bu", "xie"}, "坚持到底，一点也不松懈。"},
	{"锲而不舍", []string{"qie", "er", "bu", "she"}, "比喻有恒心，有毅力。"},
	{"精益求精", []string{"jing", "yi", "qiu", "jing"}, "已经很好了，还要求更好。"},
	{"一丝不苟", []string{"yi", "si", "bu", "gou"}, "形容做事认真细致，一点也不马虎。"},
	{"全力以赴", []string{"quan", "li", "yi", "fu"}, "把全部力量都投入进去。"},
	{"全心全意", []string{"quan", "xin", "quan", "yi"}, "形容用全部的精力去做某事。"},
	{"专心致志", []string{"zhuan", "xin", "zhi", "zhi"}, "形容一心一意，集中精神。"},
	{"聚精会神", []string{"ju", "jing", "hui", "shen"}, "形容集中精神，专心一意。"},
	{"心无旁骛", []string{"xin", "wu", "pang", "wu"}, "形容心思集中，专心致志。"},
	{"废寝忘食", []string{"fei", "qin", "wang", "shi"}, "形容专心努力，顾不上吃饭睡觉。"},
	{"夜以继日", []string{"ye", "yi", "ji", "ri"}, "用夜晚的时间接上白天，形容日夜不停。"},
	{"争分夺秒", []string{"zheng", "fen", "duo", "miao"}, "形容抓紧时间，不放过一分一秒。"},
	{"分秒必争", []string{"fen", "miao", "bi", "zheng"}, "一分一秒也必须争取。"},
	{"只争朝夕", []string{"zhi", "zheng", "zhao", "xi"}, "形容抓紧时间，力争在最短时间内达到目的。"},
	{"自强不息", []string{"zi", "qiang", "bu", "xi"}, "自觉地努力向上，永不松懈。"},
	{"厚德载物", []string{"hou", "de", "zai", "wu"}, "形容品德高尚，能够包容万物。"},
	{"志存高远", []string{"zhi", "cun", "gao", "yuan"}, "形容志向远大。"},
	{"一贫如洗", []string{"yi", "pin", "ru", "xi"}, "形容穷得像被水洗过一样，一无所有。"},
	{"家徒四壁", []string{"jia", "tu", "si", "bi"}, "形容家里非常贫穷，什么都没有。"},
	{"丰衣足食", []string{"feng", "yi", "zu", "shi"}, "形容生活富裕。"},
	{"锦衣玉食", []string{"jin", "yi", "yu", "shi"}, "形容生活极其豪华奢侈。"},
	{"花好月圆", []string{"hua", "hao", "yue", "yuan"}, "比喻美好圆满，多用于祝贺新婚。"},
	{"合家欢乐", []string{"he", "jia", "huan", "le"}, "全家人都欢乐。"},
	{"其乐融融", []string{"qi", "le", "rong", "rong"}, "形容十分欢乐和睦的样子。"},
	{"宾至如归", []string{"bin", "zhi", "ru", "gui"}, "形容招待客人热情周到。"},
	{"高朋满座", []string{"gao", "peng", "man", "zuo"}, "形容宾客很多。"},
	{"座无虚席", []string{"zuo", "wu", "xu", "xi"}, "形容出席的人非常多。"},
	{"人声鼎沸", []string{"ren", "sheng", "ding", "fei"}, "形容人声嘈杂喧闹。"},
	{"鸦雀无声", []string{"ya", "que", "wu", "sheng"}, "形容非常安静，一点声音也没有。"},
	{"沉默寡言", []string{"chen", "mo", "gua", "yan"}, "形容性情沉静，很少说话。"},
	{"滔滔不绝", []string{"tao", "tao", "bu", "jue"}, "形容话很多，说起来没完没了。"},
	{"口若悬河", []string{"kou", "ruo", "xuan", "he"}, "形容能言善辩，说起话来像瀑布一样。"},
	{"妙语连珠", []string{"miao", "yu", "lian", "zhu"}, "形容说话风趣，妙语不断。"},
	{"出口成章", []string{"chu", "kou", "cheng", "zhang"}, "形容文思敏捷，说出话来就是文章。"},
	{"文思泉涌", []string{"wen", "si", "quan", "yong"}, "形容写文章的思路像泉水一样涌出。"},
	{"才华横溢", []string{"cai", "hua", "heng", "yi"}, "形容很有才华，充分显露出来。"},
	{"博学多才", []string{"bo", "xue", "duo", "cai"}, "形容学识丰富，有多方面的才能。"},
	{"学富五车", []string{"xue", "fu", "wu", "che"}, "形容读书多，学问渊博。"},
	{"满腹经纶", []string{"man", "fu", "jing", "lun"}, "形容人很有才学和智谋。"},
}

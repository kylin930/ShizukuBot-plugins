// Package wordcount 对消息文本做字数统计与词频统计。
//
// 原版 ZeroBot 插件会拉取群历史消息、用 jieba 分词并渲染柱状图；
// 本框架只能拿到当前一条消息，也没有第三方分词/绘图库，因此做等价降级：
// 只统计本条消息的文本，中文按连续汉字切出二元词，纯文本输出。
package wordcount

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const usage = "用法：\n" +
	"字数 <文本> 或 字数统计 <文本>：统计总字符数、汉字数、英文字母数、英文单词数、数字个数、标点数、行数和去重字符数\n" +
	"词频 <文本> [N]：统计中文二字词与英文单词词频，默认前 10 名，最多 50 名\n" +
	"热词 [N]：统计群聊最近 N 条消息的热词（需要宿主提供历史消息）\n" +
	"说明：字数/词频只统计数据就是本条消息里的文本，不读取群历史。"

// Definition subscribes to the 字数 / 字数统计 / 词频 / 热词 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "wordcount",
		Name:        "文本字数与词频",
		Version:     "1.1.0",
		Description: "对发送的文本做统计：字数 文本 查看字符/汉字/单词等数量，词频 文本 [N] 查看排行，热词 [N] 统计群历史热词（依赖宿主历史消息）",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "wordcount", Trigger: "字数"},
	})
}

// Handle 先匹配更长的「字数统计」，避免被「字数」抢先截断。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if rest, ok := sdk.MatchPrefix(text, "字数统计"); ok {
		if rest == "" {
			return c.Reply(ctx, usage)
		}
		return c.Reply(ctx, formatStats(rest))
	}
	if rest, ok := sdk.MatchPrefix(text, "字数"); ok {
		if rest == "" {
			return c.Reply(ctx, usage)
		}
		return c.Reply(ctx, formatStats(rest))
	}
	if rest, ok := sdk.MatchPrefix(text, "词频"); ok {
		body, limit := splitLimit(rest)
		if body == "" {
			return c.Reply(ctx, usage)
		}
		return c.Reply(ctx, formatFrequency(body, limit))
	}
	if rest, ok := sdk.MatchPrefix(text, "热词"); ok {
		limit, ok := hotLimit(rest)
		if !ok {
			return nil
		}
		return c.Reply(ctx, fmt.Sprintf("热词统计需要宿主提供历史消息，暂不可用（计划统计最近 %d 条）。", limit))
	}
	return nil
}

// hotLimit 解析「热词 [N]」的条数：默认 1000，最大 10000，非数字视为不匹配。
func hotLimit(rest string) (int, bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return 1000, true
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	if n > 10000 {
		n = 10000
	}
	if n == 0 {
		n = 1000
	}
	return n, true
}

type textStats struct {
	total   int // 总字符数（rune）
	han     int // 汉字数
	letters int // 英文字母数
	words   int // 英文单词数
	digits  int // 数字个数
	punct   int // 标点数
	lines   int // 行数
	unique  int // 去重字符数（不计空白）
}

// analyze 遍历一遍文本累计各项指标。英文单词按连续 ASCII 字母计一个。
func analyze(text string) textStats {
	s := textStats{lines: 1}
	seen := make(map[rune]struct{})
	inWord := false
	for _, r := range text {
		s.total++
		if r == '\n' {
			s.lines++
		}
		if unicode.Is(unicode.Han, r) {
			s.han++
		}
		if isASCIILetter(r) {
			s.letters++
			if !inWord {
				s.words++
				inWord = true
			}
		} else {
			inWord = false
		}
		if unicode.IsDigit(r) {
			s.digits++
		}
		if unicode.IsPunct(r) {
			s.punct++
		}
		if !unicode.IsSpace(r) {
			seen[r] = struct{}{}
		}
	}
	s.unique = len(seen)
	return s
}

func formatStats(text string) string {
	s := analyze(text)
	lines := []string{
		"字数统计：",
		fmt.Sprintf("总字符数：%d", s.total),
		fmt.Sprintf("汉字数：%d", s.han),
		fmt.Sprintf("英文字母数：%d", s.letters),
		fmt.Sprintf("英文单词数：%d", s.words),
		fmt.Sprintf("数字个数：%d", s.digits),
		fmt.Sprintf("标点数：%d", s.punct),
		fmt.Sprintf("行数：%d", s.lines),
		fmt.Sprintf("去重字符数：%d", s.unique),
	}
	return strings.Join(lines, "\n")
}

type termCount struct {
	word  string
	count int
}

// wordFrequency 把连续汉字切成二元词，把连续英文字母转小写后整体计词。
func wordFrequency(text string) map[string]int {
	freq := make(map[string]int)
	var han []rune
	var word []rune

	flushHan := func() {
		for i := 0; i+1 < len(han); i++ {
			freq[string(han[i:i+2])]++
		}
		han = han[:0]
	}
	flushWord := func() {
		if len(word) > 0 {
			freq[strings.ToLower(string(word))]++
			word = word[:0]
		}
	}

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case isASCIILetter(r):
			flushHan()
			word = append(word, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return freq
}

func formatFrequency(text string, limit int) string {
	freq := wordFrequency(text)
	if len(freq) == 0 {
		return "没有可统计的词频，请输入包含汉字或英文的内容。"
	}
	terms := make([]termCount, 0, len(freq))
	for word, count := range freq {
		terms = append(terms, termCount{word: word, count: count})
	}
	// 次数降序；次数相同按词面升序，保证输出稳定。
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].count != terms[j].count {
			return terms[i].count > terms[j].count
		}
		return terms[i].word < terms[j].word
	})
	if len(terms) > limit {
		terms = terms[:limit]
	}
	lines := []string{fmt.Sprintf("词频统计（前 %d 名）：", limit)}
	for _, t := range terms {
		lines = append(lines, fmt.Sprintf("%s ×%d", t.word, t.count))
	}
	return strings.Join(lines, "\n")
}

// splitLimit 从参数末尾取出可选的名次 N，默认 10，最大 50。
func splitLimit(rest string) (string, int) {
	fields := strings.Fields(rest)
	limit := 10
	if len(fields) > 1 {
		if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			limit = n
			fields = fields[:len(fields)-1]
		}
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	return strings.Join(fields, " "), limit
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

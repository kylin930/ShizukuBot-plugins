// Package idiom 成语、近义词与反义词查询。
// 行为对齐上游 koishi-plugin-idiom-dictionary：
// 数据来自 hanyuguoxue.com，可选图片输出（RenderHTML 渲染 newword.html）。
package idiom

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed newword.html
var newwordHTML string

// 站点地址；测试可替换。
var (
	baseURL = "https://www.hanyuguoxue.com"
	client  = &http.Client{Timeout: 20 * time.Second}
	// renderHTML 便于测试替换。
	renderHTML = sdk.RenderHTML
)

const maxResponseBytes = 4 << 20

// 配置键。
const (
	configMaximumIdiom   = "Maximumidiom"
	configMaximumSynonym = "MaximumnearSynonyms"
	configImagifyOutput  = "imagifyoutput"
	configConsoleInfo    = "consoleinfo"
)

var (
	idiomTrigger   = regexp.MustCompile(`^成语查询\s*([\s\S]*)$`)
	synonymTrigger = regexp.MustCompile(`^近义词查询\s*([\s\S]*)$`)
	antonymTrigger = regexp.MustCompile(`^反义词查询\s*([\s\S]*)$`)

	descriptionRe = regexp.MustCompile(`<meta content="([^"]+)" name="description">`)
	titleBlockRe  = regexp.MustCompile(`<div class="title">[\s\S]*?</div>`)
	titleAttrRe   = regexp.MustCompile(`title="([^"]+)">`)
	pinyinRe      = regexp.MustCompile(`<span class="pinyin">([^<]+)</span>`)
	wordItemRe    = regexp.MustCompile(`<li>[\s\S]*?<h3><a[^>]+title=[^>]+>([\s\S]*?)</a></h3>[\s\S]*?<p class=pinyin>([\s\S]*?)</p>[\s\S]*?<p class=summary>([\s\S]*?)</p>[\s\S]*?</li>`)
	moreSpanRe    = regexp.MustCompile(`<span class=more>[\s\S]*?</span>`)
)

// Definition 订阅三个中文查询指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "idiom",
		Name:        "成语词典",
		Version:     "1.0.0",
		Description: "成语查询、近义词查询与反义词查询，支持模糊查询与图片输出",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "idiom", Trigger: "成语查询"}},
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				configMaximumIdiom: {
					Type:        "number",
					Description: "返回成语的最大数量限制",
					Default:     json.RawMessage(`10`),
				},
				configMaximumSynonym: {
					Type:        "number",
					Description: "返回近义词/反义词的最大数量限制",
					Default:     json.RawMessage(`5`),
				},
				configImagifyOutput: {
					Type:        "boolean",
					Description: "图片输出模式",
					Default:     json.RawMessage(`false`),
				},
				configConsoleInfo: {
					Type:        "boolean",
					Description: "日志调试模式",
					Default:     json.RawMessage(`false`),
				},
			},
		}))
}

// Handle 分发三类查询。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if m := sdk.MatchRegexp(text, idiomTrigger); m != nil {
		return handleIdiom(ctx, c, strings.TrimSpace(m[1]))
	}
	if m := sdk.MatchRegexp(text, synonymTrigger); m != nil {
		return handleWord(ctx, c, strings.TrimSpace(m[1]), wordSynonyms)
	}
	if m := sdk.MatchRegexp(text, antonymTrigger); m != nil {
		return handleWord(ctx, c, strings.TrimSpace(m[1]), wordAntonyms)
	}
	return nil
}

// handleIdiom 处理成语查询：无问号为精确释义，含问号走模糊搜索。
func handleIdiom(ctx context.Context, c sdk.Call, idiom string) error {
	if idiom == "" {
		// 上游发送未配置的 helptip，实际无输出。
		return nil
	}
	if len([]rune(idiom)) != 4 {
		return c.Reply(ctx, "请输入四字成语哦，未知的字可以用【？】代替。")
	}
	if !strings.ContainsAny(idiom, "?？") {
		page, err := fetch(ctx, "/chengyu/search?words="+url.QueryEscape(idiom))
		if err != nil {
			return c.Reply(ctx, "查询失败，请稍后再试。")
		}
		match := descriptionRe.FindStringSubmatch(page)
		if match == nil || match[1] == "" {
			return c.Reply(ctx, "未找到成语释义。")
		}
		if imagifyOutput(c.Config) {
			return sendImage(ctx, c, idiom, match[1], titleDefinition)
		}
		return c.Reply(ctx, fmt.Sprintf("“%s”\n%s", idiom, match[1]))
	}

	pattern := wildcardPattern(idiom)
	page, err := fetch(ctx, "/chengyu/search?words="+url.QueryEscape(pattern))
	if err != nil {
		return c.Reply(ctx, "查询失败，请稍后再试。")
	}
	blocks := titleBlockRe.FindAllString(page, -1)
	if len(blocks) == 0 {
		return c.Reply(ctx, "未找到成语释义。")
	}
	limit := maxIdiom(c.Config)
	if len(blocks) > limit {
		blocks = blocks[:limit]
	}
	var lines []string
	for _, block := range blocks {
		title := titleAttrRe.FindStringSubmatch(block)
		pinyin := pinyinRe.FindStringSubmatch(block)
		if title == nil || pinyin == nil {
			continue
		}
		lines = append(lines, title[1]+"  "+pinyin[1])
	}
	results := strings.Join(lines, "\n")
	if imagifyOutput(c.Config) {
		return sendImage(ctx, c, idiom, results, titleIdiomResults)
	}
	return c.Reply(ctx, fmt.Sprintf("“%s” 的搜索结果：\n%s", idiom, results))
}

// wildcardPattern 把「？/？」转为站点支持的模糊匹配符号：
// 未知字之后仍有已知字用 ?，末尾连续未知字用 *。
func wildcardPattern(idiom string) string {
	chars := []rune(idiom)
	var b strings.Builder
	for i, ch := range chars {
		if ch != '?' && ch != '？' {
			b.WriteRune(ch)
			continue
		}
		hasKnownAfter := false
		for j := i + 1; j < len(chars); j++ {
			if chars[j] != '?' && chars[j] != '？' {
				hasKnownAfter = true
				break
			}
		}
		if hasKnownAfter {
			b.WriteRune('?')
		} else {
			b.WriteRune('*')
		}
	}
	return b.String()
}

type wordKind int

const (
	wordSynonyms wordKind = iota
	wordAntonyms
)

func (k wordKind) label() string {
	if k == wordSynonyms {
		return "近义词"
	}
	return "反义词"
}

func (k wordKind) path() string {
	if k == wordSynonyms {
		return "/jinyici/search"
	}
	return "/fanyici/search"
}

func (k wordKind) titleIndex() int {
	if k == wordSynonyms {
		return titleSynonymResults
	}
	return titleAntonymResults
}

// handleWord 处理近义词/反义词查询。
func handleWord(ctx context.Context, c sdk.Call, word string, kind wordKind) error {
	if word == "" || strings.ContainsAny(word, "?？") {
		return c.Reply(ctx, "输入内容不完整哦~")
	}
	page, err := fetch(ctx, kind.path()+"?words="+url.QueryEscape(word))
	if err != nil {
		return c.Reply(ctx, "查询失败，请稍后再试。")
	}
	matches := wordItemRe.FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		return c.Reply(ctx, "未找到匹配的"+kind.label()+"。")
	}
	limit := maxSynonym(c.Config)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	var lines []string
	for _, m := range matches {
		summary := strings.TrimSpace(moreSpanRe.ReplaceAllString(m[3], ""))
		lines = append(lines, fmt.Sprintf("%s (%s): %s", m[1], m[2], summary))
	}
	results := strings.Join(lines, "\n\n")
	if imagifyOutput(c.Config) {
		return sendImage(ctx, c, word, results, kind.titleIndex())
	}
	return c.Reply(ctx, fmt.Sprintf("%s [%s] 查询结果：\n%s", kind.label(), word, results))
}

// 卡片标题编号，与上游 replaceTitle 对应。
const (
	titleDefinition = iota
	titleIdiomResults
	titleSynonymResults
	titleAntonymResults
)

// sendImage 用 newword.html 渲染图片并回复。
func sendImage(ctx context.Context, c sdk.Call, words, paraphrase string, title int) error {
	doc := renderCard(words, paraphrase, title)
	png, err := renderHTML(ctx, []byte(doc), sdk.RenderOptions{Width: 750, Height: 250, FullPage: true})
	if err != nil {
		return c.Reply(ctx, "图片生成失败，请稍后再试。")
	}
	if consoleInfo(c.Config) && c.Log != nil {
		c.Log(fmt.Sprintf("%s：\n %s", words, paraphrase))
	}
	return c.ReplyImageBytes(ctx, png)
}

// renderCard 填充 newword.html 的标题、方格与释义。
func renderCard(words, paraphrase string, title int) string {
	titles := map[int]string{
		titleDefinition:     "【释义】",
		titleIdiomResults:   "【成语结果】",
		titleSynonymResults: "【近义词查询结果】",
		titleAntonymResults: "【反义词查询结果】",
	}
	// 上游用正则替换 CSS content 中的字面量转义序列。
	doc := strings.ReplaceAll(newwordHTML, `\3010\91ca\4e49\3011`, titles[title])

	var wordHTML strings.Builder
	for _, ch := range words {
		char := string(ch)
		if ch == '?' || ch == '？' {
			char = " "
		}
		wordHTML.WriteString("\n      <div class=\"word\" data-v-1ac9b022>\n" +
			"          <span class=\"line\" data-v-1ac9b022></span>\n" +
			"          <span class=\"line\" data-v-1ac9b022></span>\n" +
			"          <span class=\"text\" data-v-1ac9b022>" + html.EscapeString(char) + "</span>\n" +
			"      </div>\n      ")
	}
	doc = strings.Replace(doc, "<!--词语-->", wordHTML.String(), 1)

	var lines []string
	for _, line := range strings.Split(paraphrase, "\n") {
		lines = append(lines, "<div>"+html.EscapeString(line)+"</div>")
	}
	doc = strings.Replace(doc, "<!--词语的释义-->", strings.Join(lines, "<br>"), 1)
	return doc
}

// fetch 拉取 hanyuguoxue 页面文本。
func fetch(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", errors.New("响应内容为空")
	}
	return string(body), nil
}

func imagifyOutput(config map[string]any) bool {
	v, _ := config[configImagifyOutput].(bool)
	return v
}

func consoleInfo(config map[string]any) bool {
	v, _ := config[configConsoleInfo].(bool)
	return v
}

func maxIdiom(config map[string]any) int {
	if v, ok := toInt(config[configMaximumIdiom]); ok && v > 0 {
		return v
	}
	return 10
}

func maxSynonym(config map[string]any) int {
	if v, ok := toInt(config[configMaximumSynonym]); ok && v > 0 {
		return v
	}
	return 5
}

func toInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	}
	return 0, false
}

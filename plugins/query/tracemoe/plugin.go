// Package tracemoe 以图搜番：调用 trace.moe 识别番剧截图。
package tracemoe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// searchURL 为 trace.moe 的搜索接口，测试时可替换。
var searchURL = "https://api.trace.moe/search"

var client = &http.Client{Timeout: 10 * time.Second}

// triggers 与上游一致，按长度从长到短匹配，避免歧义。
var triggers = []string{"以图搜番", "搜索番剧", "搜番"}

const usage = "用法：发送图片并配文「以图搜番」，或发送「以图搜番 <图片URL>」。"

type searchResult struct {
	Error  string  `json:"error"`
	Result []match `json:"result"`
}

type match struct {
	Anilist struct {
		Title struct {
			Native  string `json:"native"`
			Romaji  string `json:"romaji"`
			English string `json:"english"`
		} `json:"title"`
		Synonyms []string `json:"synonyms"`
		IsAdult  bool     `json:"isAdult"`
	} `json:"anilist"`
	Episode    any     `json:"episode"`
	From       float64 `json:"from"`
	To         float64 `json:"to"`
	Similarity float64 `json:"similarity"`
	Image      string  `json:"image"`
	Filename   string  `json:"filename"`
}

// Definition registers the 以图搜番 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "tracemoe",
		Name:        "以图搜番",
		Version:     "1.0.0",
		Description: "发送「以图搜番」「搜番」「搜索番剧」并附上截图，调用 trace.moe 识别番剧出处",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "tracemoe", Trigger: "以图搜番"},
	})
}

// Handle 匹配触发词，逐张取出图片地址后查询 trace.moe。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	matched, exact := false, false
	for _, trigger := range triggers {
		if strings.Contains(text, trigger) {
			matched = true
		}
		if text == trigger {
			exact = true
		}
	}
	if !matched {
		return nil
	}

	// 关键词可出现在句中；优先取文本里的图片链接，其次取消息附件。
	var imageURLs []string
	if isHTTPURL(text) {
		imageURLs = []string{text}
	}
	if len(imageURLs) == 0 {
		for _, field := range strings.Fields(text) {
			if isHTTPURL(field) {
				imageURLs = append(imageURLs, field)
			}
		}
	}
	if len(imageURLs) == 0 {
		imageURLs = sdk.ImageURLs(c.Event)
	}
	if len(imageURLs) == 0 {
		// 只有单独发送触发词时才提示用法，避免句中关键词误触发。
		if exact {
			return c.Reply(ctx, usage)
		}
		return nil
	}

	if err := c.Reply(ctx, "少女祈祷中……"); err != nil {
		return err
	}
	for i, imageURL := range imageURLs {
		result, err := search(ctx, imageURL)
		if err != nil {
			if err := c.Reply(ctx, "搜番失败："+err.Error()); err != nil {
				return err
			}
			continue
		}
		if result.Error != "" {
			if err := c.Reply(ctx, "搜番失败："+result.Error); err != nil {
				return err
			}
			continue
		}
		if len(result.Result) == 0 {
			if err := c.Reply(ctx, emptyText(i, len(imageURLs))); err != nil {
				return err
			}
			continue
		}

		first := result.Result[0]
		if err := c.Reply(ctx, formatResult(first)); err != nil {
			return err
		}
		// 预览图外链直接交给宿主发送，不限制 https 前缀，也不下载到本地。
		if first.Image != "" {
			_ = c.ReplyImage(ctx, first.Image)
		}
	}
	return nil
}

// emptyText 为无匹配结果的图片给出明确提示。
func emptyText(index, total int) string {
	if total <= 1 {
		return "没有找到相似的番剧，换张更清晰的截图试试？"
	}
	return "第 " + strconv.Itoa(index+1) + " 张图片没有找到相似的番剧，换张更清晰的截图试试？"
}

// isHTTPURL 判断文本是否为可用的图片链接。
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func search(ctx context.Context, imageURL string) (searchResult, error) {
	var result searchResult
	query := url.Values{}
	query.Set("url", imageURL)
	query.Set("cutBorders", "")
	query.Set("anilistInfo", "")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?"+query.Encode(), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	return result, nil
}

func formatResult(r match) string {
	hint := "我有把握是这个！"
	if similarityPercent(r.Similarity) < 80 {
		hint = "大概是这个？"
	}
	lines := []string{hint}

	title := r.Anilist.Title
	if cn := chineseTitle(r.Anilist.Synonyms); cn != "" {
		lines = append(lines, "中文名："+cn)
	}
	if title.Native != "" {
		lines = append(lines, "日文名："+title.Native)
	}
	if title.Romaji != "" {
		lines = append(lines, "罗马音："+title.Romaji)
	}
	if title.English != "" {
		lines = append(lines, "英文名："+title.English)
	}
	if len(lines) == 1 {
		lines = append(lines, "番剧名：未知")
	}

	episode := episodeText(r.Episode)
	if episode == "" {
		episode = "未知"
	}
	lines = append(lines,
		"话数："+episode,
		"时间："+formatTime(r.From)+" - "+formatTime(r.To),
		fmt.Sprintf("相似度：%.1f%%", similarityPercent(r.Similarity)),
	)
	if r.Anilist.IsAdult {
		lines = append(lines, "成人内容：是（可能含 R18 内容，注意场合）")
	} else {
		lines = append(lines, "成人内容：否")
	}
	if filename := strings.TrimSpace(r.Filename); filename != "" {
		lines = append(lines, "文件名："+filename)
	}
	return strings.Join(lines, "\n")
}

// similarityPercent 兼容上游 0~1 与 0~100 两种相似度表示。
func similarityPercent(v float64) float64 {
	if v <= 1 {
		return v * 100
	}
	return v
}

func formatTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func episodeText(v any) string {
	switch e := v.(type) {
	case float64:
		if e == float64(int64(e)) {
			return strconv.FormatInt(int64(e), 10)
		}
		return strconv.FormatFloat(e, 'f', -1, 64)
	case string:
		return strings.TrimSpace(e)
	default:
		return ""
	}
}

// chineseTitle 从 AniList 同义词里挑一个含汉字的标题，API 本身不提供中文字段。
func chineseTitle(synonyms []string) string {
	for _, s := range synonyms {
		for _, r := range s {
			if unicode.Is(unicode.Han, r) {
				return s
			}
		}
	}
	return ""
}

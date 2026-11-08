// Package hitokoto 一言：从一言 API 取随机句子，并支持分类、关键词与「系列一言」。
package hitokoto

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

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// apiURL 与上游 v1.hitokoto.cn 一致，测试可替换。
var apiURL = "https://v1.hitokoto.cn/"

var (
	client  = &http.Client{Timeout: 10 * time.Second}
	nowFunc = time.Now
)

const (
	// seriesTimeout 与上游一致，系列一言选择有效期为 120 秒。
	seriesTimeout = 120 * time.Second
	// batchSize 与上游一致，一次最多发送 10 条。
	batchSize = 10
	// keywordAttempts 关键词过滤时最多请求的随机条数。
	keywordAttempts = 20
)

type sentence struct {
	Hitokoto string `json:"hitokoto"`
	From     string `json:"from"`
	FromWho  string `json:"from_who"`
}

// category 是一言的分类，Code 对应 API 的 c 参数。
type category struct {
	Code string
	Name string
}

// categories 与 v1.hitokoto.cn 的分类代码一致。
var categories = []category{
	{"a", "动画"},
	{"b", "漫画"},
	{"c", "游戏"},
	{"d", "文学"},
	{"e", "原创"},
	{"f", "来自网络"},
	{"g", "其他"},
	{"h", "影视"},
	{"i", "诗词"},
	{"j", "网易云"},
	{"k", "哲学"},
	{"l", "抖机灵"},
}

// seriesSession 记录一次进行中的「系列一言」选择。
type seriesSession struct {
	Deadline int64 `json:"deadline"`
}

// Definition subscribes to the 「一言」「系列一言」 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "hitokoto",
		Name:        "一言",
		Version:     "1.0.0",
		Description: "发送「一言」随机返回一句一言；支持「一言 分类/关键词」与「系列一言」",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "hitokoto", Trigger: "一言"},
	}, sdk.WithPermissions("state.read", "state.write"))
}

// Handle 处理一言、系列一言与系列选择，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	if sdk.MatchFull(text, "系列一言") {
		return startSeries(ctx, c)
	}
	if handled, err := consumeSeries(ctx, c, text); handled {
		return err
	}
	rest, ok := sdk.MatchPrefix(text, "一言")
	if !ok {
		return nil
	}
	return replyHitokoto(ctx, c, rest)
}

// startSeries 发送分类序号列表并进入 120 秒选择态。
func startSeries(ctx context.Context, c sdk.Call) error {
	if err := sdk.SaveJSON(ctx, c.State, seriesKey(c), seriesSession{
		Deadline: nowFunc().Add(seriesTimeout).Unix(),
	}); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	var b strings.Builder
	b.WriteString("请输入系列一言序号\n")
	for i, item := range categories {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(". ")
		b.WriteString(item.Name)
		b.WriteString("\n")
	}
	return c.Reply(ctx, strings.TrimRight(b.String(), "\n"))
}

// consumeSeries 消费系列一言选择，返回是否已处理该消息。
func consumeSeries(ctx context.Context, c sdk.Call, text string) (bool, error) {
	session := seriesSession{}
	if err := sdk.LoadJSON(ctx, c.State, seriesKey(c), &session); err != nil {
		return false, nil
	}
	if session.Deadline == 0 {
		return false, nil
	}
	if nowFunc().Unix() > session.Deadline {
		clearSeries(ctx, c)
		return true, c.Reply(ctx, "系列一言指令过期")
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return true, c.Reply(ctx, "请输入数字!")
	}
	if n < 0 || n >= len(categories) {
		return true, c.Reply(ctx, "序号非法!")
	}
	chosen := categories[n]
	clearSeries(ctx, c)
	if err := c.Reply(ctx, "请欣赏系列一言: "+chosen.Name); err != nil {
		return true, err
	}
	items, err := fetchBatch(ctx, chosen.Code, batchSize)
	if err != nil {
		return true, c.Reply(ctx, "ERROR: "+err.Error())
	}
	if len(items) == 0 {
		return true, c.Reply(ctx, "ERROR: hitokoto empty")
	}
	for _, item := range items {
		if err := c.Reply(ctx, format(item)); err != nil {
			return true, err
		}
	}
	return true, nil
}

func clearSeries(ctx context.Context, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, c.State, seriesKey(c), seriesSession{})
}

// replyHitokoto 处理「一言 [分类|关键词]」。
func replyHitokoto(ctx context.Context, c sdk.Call, arg string) error {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		items, err := fetchBatch(ctx, "", batchSize)
		if err != nil {
			return c.Reply(ctx, "获取一言失败: "+err.Error())
		}
		if len(items) == 0 {
			return c.Reply(ctx, "没有找到相关的一言")
		}
		return replyMany(ctx, c, items)
	}
	if item, ok := findCategory(arg); ok {
		items, err := fetchBatch(ctx, item.Code, batchSize)
		if err != nil {
			return c.Reply(ctx, "获取一言失败: "+err.Error())
		}
		if len(items) == 0 {
			return c.Reply(ctx, "没有找到相关的一言")
		}
		return replyMany(ctx, c, items)
	}
	items, err := fetchKeyword(ctx, arg)
	if err != nil {
		return c.Reply(ctx, "获取一言失败: "+err.Error())
	}
	if len(items) == 0 {
		return c.Reply(ctx, "没有找到相关的一言")
	}
	return replyMany(ctx, c, items)
}

// replyMany 逐条发送多条一言，首条保留「再来一条」按钮。
func replyMany(ctx context.Context, c sdk.Call, items []sentence) error {
	for i, item := range items {
		if i == 0 {
			keyboard := sdk.NewKeyboard(sdk.NewRow(
				sdk.CommandButton("hitokoto-more", "再来一条", "一言"),
			))
			if err := c.ReplyKeyboardOrText(ctx, format(item), keyboard); err != nil {
				return err
			}
			continue
		}
		if err := c.Reply(ctx, format(item)); err != nil {
			return err
		}
	}
	return nil
}

// findCategory 同时接受分类代码（不区分大小写）与中文名。
func findCategory(arg string) (category, bool) {
	for _, item := range categories {
		if strings.EqualFold(arg, item.Code) || arg == item.Name {
			return item, true
		}
	}
	return category{}, false
}

// fetchBatch 按分类随机取至多 n 条并去重。
func fetchBatch(ctx context.Context, code string, n int) ([]sentence, error) {
	seen := map[string]bool{}
	items := make([]sentence, 0, n)
	for i := 0; i < n*2 && len(items) < n; i++ {
		item, err := fetch(ctx, code)
		if err != nil {
			if len(items) == 0 {
				return nil, err
			}
			break
		}
		key := item.Hitokoto + "|" + item.From
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, item)
	}
	return items, nil
}

// fetchKeyword 随机请求若干条后按关键词过滤（匹配句子或出处）。
func fetchKeyword(ctx context.Context, keyword string) ([]sentence, error) {
	seen := map[string]bool{}
	items := make([]sentence, 0, batchSize)
	var lastErr error
	for i := 0; i < keywordAttempts && len(items) < batchSize; i++ {
		item, err := fetch(ctx, "")
		if err != nil {
			lastErr = err
			continue
		}
		if !strings.Contains(item.Hitokoto, keyword) && !strings.Contains(item.From, keyword) &&
			!strings.Contains(item.FromWho, keyword) {
			continue
		}
		key := item.Hitokoto + "|" + item.From
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, item)
	}
	if len(items) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return items, nil
}

// endpoint 在分类查询时追加 c 参数。
func endpoint(code string) string {
	if code == "" {
		return apiURL
	}
	sep := "?"
	if strings.Contains(apiURL, "?") {
		sep = "&"
	}
	return apiURL + sep + "c=" + url.QueryEscape(code)
}

// format 拼接句子与出处。
func format(item sentence) string {
	text := item.Hitokoto
	source := item.From
	switch {
	case item.FromWho != "" && item.From != "":
		source = item.FromWho + "《" + item.From + "》"
	case item.FromWho != "":
		source = item.FromWho
	}
	if strings.TrimSpace(source) != "" {
		text += "\n——" + source
	}
	return text
}

func fetch(ctx context.Context, code string) (sentence, error) {
	var item sentence
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(code), nil)
	if err != nil {
		return item, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return item, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return item, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal(body, &item); err != nil {
		return item, err
	}
	if strings.TrimSpace(item.Hitokoto) == "" {
		return item, fmt.Errorf("一言内容为空")
	}
	return item, nil
}

// seriesKey 会话状态按群/私聊与用户隔离。
func seriesKey(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return "series:" + c.Event.GroupID + ":" + c.Event.UserID
	}
	return "series:c2c:" + c.Event.UserID
}

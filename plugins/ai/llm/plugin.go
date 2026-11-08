// Package llm 移植 ZeroBot llm：/gpt 对话与群聊总结，聊天记录存 SQLite。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	pluginVersion   = "1.0.0"
	purgeInterval   = 5 * time.Minute
	summaryCooldown = 30 * time.Second
	// splitLimit 与上游一致：按 1000 字符切分后逐条发送。
	splitLimit = 1000
	// defaultSummaryPrompt 对齐上游「极简总结」提示词。
	defaultSummaryPrompt = `请对以下群聊对话进行【极简总结】。
要求：
1. 剔除客套与废话，直击主题。
2. 使用 Markdown 列表格式。
3. 按以下结构输出：
   - 🎯 核心议题：(一句话概括)
   - 💡 关键观点/结论：(提取3-5个重点)
   - ✅ 下一步/待办：(如果有，明确谁做什么)

群聊对话内容如下：`
)

type config struct {
	APIURL              string
	APIKey              string
	Model               string
	SystemPrompt        string
	Temperature         float64
	MaxTokens           int
	ContextSize         int
	RetentionHours      int
	SummaryDefaultLimit int
	SummaryMaxLimit     int
	SummaryPrompt       string
	MaxSummaryChars     int
	AdminUsers          []string
	DatabasePath        string
}

func defaultConfig() config {
	return config{
		APIURL:              "https://api.siliconflow.cn/v1/chat/completions",
		Model:               "Qwen/Qwen3-8B",
		Temperature:         0.7,
		ContextSize:         50,
		RetentionHours:      24,
		SummaryDefaultLimit: 200,
		SummaryMaxLimit:     1000,
		SummaryPrompt:       defaultSummaryPrompt,
		MaxSummaryChars:     20000,
	}
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"apiUrl":              {Type: "string", Description: "OpenAI 兼容的 chat/completions 地址", Default: json.RawMessage(`"https://api.siliconflow.cn/v1/chat/completions"`)},
			"apiKey":              {Type: "string", Description: "大模型 API 密钥", Default: json.RawMessage(`""`)},
			"model":               {Type: "string", Description: "模型名", Default: json.RawMessage(`"Qwen/Qwen3-8B"`)},
			"systemPrompt":        {Type: "string", Description: "/gpt 的系统提示词，留空则不发送", Default: json.RawMessage(`""`)},
			"temperature":         {Type: "number", Description: "采样温度（0~2）", Default: json.RawMessage(`0.7`)},
			"maxTokens":           {Type: "integer", Description: "最大输出 token，0 表示由服务端决定", Default: json.RawMessage(`0`)},
			"contextSize":         {Type: "integer", Description: "/gpt 保留的最近对话条数", Default: json.RawMessage(`50`)},
			"retentionHours":      {Type: "integer", Description: "聊天记录保留小时数（默认一天）", Default: json.RawMessage(`24`)},
			"summaryDefaultLimit": {Type: "integer", Description: "群聊总结默认消息条数", Default: json.RawMessage(`200`)},
			"summaryMaxLimit":     {Type: "integer", Description: "群聊总结最多消息条数", Default: json.RawMessage(`1000`)},
			"summaryPrompt":       {Type: "string", Description: "群聊总结的系统提示词", Default: jsonString(defaultSummaryPrompt)},
			"maxSummaryChars":     {Type: "integer", Description: "送入总结的最大字符数（从最新消息往前保留）", Default: json.RawMessage(`20000`)},
			"adminUsers":          {Type: "array", Description: "可使用群聊总结的用户 ID 列表", Items: &sdk.Schema{Type: "string"}},
			"databasePath":        {Type: "string", Description: "SQLite 文件路径，留空使用用户缓存目录", Default: json.RawMessage(`""`)},
		},
	}
}

// jsonString 把文本编码为合法的 JSON 字符串，供配置默认值使用。
func jsonString(value string) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	readString := func(key string, dst *string) {
		if value, ok := raw[key]; ok {
			if v, ok := value.(string); ok {
				*dst = strings.TrimSpace(v)
			}
		}
	}
	readInt := func(key string, dst *int) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case float64:
				*dst = int(v)
			case int:
				*dst = v
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					*dst = n
				}
			}
		}
	}
	readFloat := func(key string, dst *float64) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case float64:
				*dst = v
			case int:
				*dst = float64(v)
			case string:
				if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					*dst = n
				}
			}
		}
	}
	readList := func(key string, dst *[]string) {
		value, ok := raw[key]
		if !ok {
			return
		}
		switch v := value.(type) {
		case []string:
			*dst = v
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			*dst = out
		case string:
			for _, item := range strings.Split(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					*dst = append(*dst, item)
				}
			}
		}
	}
	readString("apiUrl", &cfg.APIURL)
	readString("apiKey", &cfg.APIKey)
	readString("model", &cfg.Model)
	readString("systemPrompt", &cfg.SystemPrompt)
	readFloat("temperature", &cfg.Temperature)
	readInt("maxTokens", &cfg.MaxTokens)
	readInt("contextSize", &cfg.ContextSize)
	readInt("retentionHours", &cfg.RetentionHours)
	readInt("summaryDefaultLimit", &cfg.SummaryDefaultLimit)
	readInt("summaryMaxLimit", &cfg.SummaryMaxLimit)
	readString("summaryPrompt", &cfg.SummaryPrompt)
	readInt("maxSummaryChars", &cfg.MaxSummaryChars)
	readList("adminUsers", &cfg.AdminUsers)
	readString("databasePath", &cfg.DatabasePath)
	if cfg.ContextSize <= 0 {
		cfg.ContextSize = 50
	}
	if cfg.RetentionHours <= 0 {
		cfg.RetentionHours = 24
	}
	if cfg.SummaryDefaultLimit <= 0 {
		cfg.SummaryDefaultLimit = 200
	}
	if cfg.SummaryMaxLimit <= 0 {
		cfg.SummaryMaxLimit = 1000
	}
	if cfg.SummaryMaxLimit < cfg.SummaryDefaultLimit {
		cfg.SummaryMaxLimit = cfg.SummaryDefaultLimit
	}
	if cfg.MaxSummaryChars <= 0 {
		cfg.MaxSummaryChars = 20000
	}
	if cfg.Model == "" {
		cfg.Model = defaultConfig().Model
	}
	return cfg
}

func (cfg config) isAdmin(userID string) bool {
	for _, id := range cfg.AdminUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// Definition 订阅全部消息以记录群聊，并注册 /gpt 与群聊总结。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "llm",
		Name:        "大模型聊天",
		Version:     pluginVersion,
		Description: "/gpt 对话（保留最近 50 条上下文）与群聊总结（SQLite 保留一天群消息）",
	}, append(append([]sdk.EventType{}, sdk.MessageEvents...), sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage),
		Handle, []triggercmd.Command{{Name: "llm", Trigger: "/gpt"}},
		sdk.WithPermissions("state.read", "state.write", "config.read"), sdk.WithConfig(configSchema()))
}

var lastPurge atomic.Int64

// Handle 记录消息并分发 /gpt、群聊总结。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	cfg := decodeConfig(c.Config)
	db, err := openDB(cfg)
	if err != nil {
		if isCommand(text) {
			return c.Reply(ctx, "ERROR: 打开聊天记录库失败: "+err.Error())
		}
		return nil
	}
	now := time.Now()
	recordIncoming(ctx, db, c, text, cfg, now)

	switch {
	case text == "/gpt" || strings.HasPrefix(text, "/gpt "):
		return handleGPT(ctx, c, cfg, db, strings.TrimSpace(strings.TrimPrefix(text, "/gpt")), now)
	case text == "群聊总结" || strings.HasPrefix(text, "群聊总结 "):
		return handleSummary(ctx, c, cfg, db, strings.TrimSpace(strings.TrimPrefix(text, "群聊总结")), now)
	}
	return nil
}

func isCommand(text string) bool {
	return text == "/gpt" || strings.HasPrefix(text, "/gpt ") ||
		text == "群聊总结" || strings.HasPrefix(text, "群聊总结 ")
}

// recordIncoming 把群消息写入 SQLite；每 5 分钟清理一次过期数据。
func recordIncoming(ctx context.Context, db *sqlDB, c sdk.Call, text string, cfg config, now time.Time) {
	if c.Event.GroupID == "" || strings.TrimSpace(text) == "" || isCommand(text) {
		return
	}
	nickname := c.Event.Nickname
	if nickname == "" {
		nickname = c.Event.UserID
	}
	_ = insertMessage(ctx, db, storedMessage{
		GID:      c.Event.GroupID,
		UID:      c.Event.UserID,
		Nickname: nickname,
		Content:  text,
		TS:       now.UnixMilli(),
	})
	last := lastPurge.Load()
	if now.UnixMilli()-last >= purgeInterval.Milliseconds() {
		if lastPurge.CompareAndSwap(last, now.UnixMilli()) {
			_ = purge(ctx, db, retentionCutoff(cfg, now))
		}
	}
}

func conversationKey(c sdk.Call) string {
	parts := []string{c.Event.BotID, string(c.Event.Source)}
	switch {
	case c.Event.GroupID != "":
		parts = append(parts, "group", c.Event.GroupID)
	case c.Event.ChannelID != "":
		parts = append(parts, "channel", c.Event.ChannelID)
	default:
		parts = append(parts, "user", c.Event.UserID)
	}
	return strings.Join(parts, "|")
}

func handleGPT(ctx context.Context, c sdk.Call, cfg config, db *sqlDB, question string, now time.Time) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return c.Reply(ctx, "未配置大模型 API 密钥，请先在插件配置中填写 apiKey。")
	}
	if question == "" {
		return c.Reply(ctx, "请在 /gpt 后输入内容，例如：/gpt 今天适合做什么？")
	}
	cutoff := retentionCutoff(cfg, now)
	history, err := recentContext(ctx, db, conversationKey(c), cfg.ContextSize, cutoff)
	if err != nil {
		return c.Reply(ctx, "ERROR: 读取对话上下文失败: "+err.Error())
	}
	messages := make([]chatMessage, 0, len(history)+2)
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: cfg.SystemPrompt})
	}
	for _, item := range history {
		messages = append(messages, chatMessage{Role: item.Role, Content: item.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: question})

	answer, err := chatCompletion(ctx, cfg, messages)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	conv := conversationKey(c)
	ts := now.UnixMilli()
	_ = insertContext(ctx, db, conv, "user", question, ts)
	_ = insertContext(ctx, db, conv, "assistant", answer, ts+1)
	for _, segment := range splitMessage(answer, splitLimit) {
		if err := c.Reply(ctx, segment); err != nil {
			return err
		}
	}
	return nil
}

func handleSummary(ctx context.Context, c sdk.Call, cfg config, db *sqlDB, args string, now time.Time) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "群聊总结仅限群聊使用。")
	}
	if !cfg.isAdmin(c.Event.UserID) {
		return c.Reply(ctx, "群聊总结仅管理员可用，请在插件配置 adminUsers 中添加你的用户 ID。")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return c.Reply(ctx, "未配置大模型 API 密钥，请先在插件配置中填写 apiKey。")
	}
	limited, remaining := summaryLimited(ctx, c, now)
	if limited {
		return c.Reply(ctx, fmt.Sprintf("总结太频繁了，请 %d 秒后再试。", remaining))
	}

	limit := cfg.SummaryDefaultLimit
	if value, err := strconv.Atoi(strings.TrimSpace(args)); err == nil && value > 0 {
		limit = value
	}
	if limit > cfg.SummaryMaxLimit {
		limit = cfg.SummaryMaxLimit
	}
	cutoff := retentionCutoff(cfg, now)
	_ = purge(ctx, db, cutoff)
	messages, err := recentMessages(ctx, db, c.Event.GroupID, limit)
	if err != nil {
		return c.Reply(ctx, "ERROR: 读取群聊记录失败: "+err.Error())
	}
	if len(messages) == 0 {
		return c.Reply(ctx, "暂无可总结的群聊记录（只保留最近一天）。")
	}
	lines := make([]string, 0, len(messages))
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		line := messages[i].Nickname + "：" + messages[i].Content
		if total+len([]rune(line)) > cfg.MaxSummaryChars && len(lines) > 0 {
			break
		}
		total += len([]rune(line))
		lines = append(lines, line)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	_ = c.Reply(ctx, "少女思考中...")
	request := []chatMessage{
		{Role: "system", Content: cfg.SummaryPrompt},
		{Role: "user", Content: strings.Join(lines, "\n")},
	}
	answer, err := chatCompletion(ctx, cfg, request)
	if err != nil {
		return c.Reply(ctx, "ERROR: 群聊总结失败: "+err.Error())
	}
	// 上游使用合并转发发送总结；宿主不支持合并转发，这里保留逐条发送。
	// 群名同样不在事件中暴露，标题用群号代替 group.Name。
	title := fmt.Sprintf("群 %s(%s) 的 %d 条消息总结:\n\n", c.Event.GroupID, c.Event.GroupID, len(lines))
	for _, segment := range splitMessage(title+answer, splitLimit) {
		if err := c.Reply(ctx, segment); err != nil {
			return err
		}
	}
	return nil
}

var summaryLocks sync.Map

// summaryLimited 实现每群 30 秒一次的限频；优先写入 State，State 为空时用内存。
func summaryLimited(ctx context.Context, c sdk.Call, now time.Time) (bool, int) {
	key := "llm:summary:last:" + c.Event.GroupID
	last := int64(0)
	if c.State != nil {
		var stored int64
		if err := sdk.LoadJSON(ctx, c.State, key, &stored); err == nil {
			last = stored
		}
	} else {
		if value, ok := summaryLocks.Load(key); ok {
			last = value.(int64)
		}
	}
	elapsed := now.UnixMilli() - last
	if last > 0 && elapsed < summaryCooldown.Milliseconds() {
		remaining := int((summaryCooldown.Milliseconds() - elapsed) / 1000)
		if remaining < 1 {
			remaining = 1
		}
		return true, remaining
	}
	if c.State != nil {
		_ = sdk.SaveJSON(ctx, c.State, key, now.UnixMilli())
	} else {
		summaryLocks.Store(key, now.UnixMilli())
	}
	return false, 0
}

func splitMessage(content string, maxLength int) []string {
	if len([]rune(content)) <= maxLength {
		return []string{content}
	}
	var out []string
	runes := []rune(content)
	for len(runes) > maxLength {
		cut := maxLength
		for i := maxLength - 1; i > maxLength/2; i-- {
			if runes[i] == '\n' || runes[i] == '。' || runes[i] == '！' || runes[i] == '？' {
				cut = i + 1
				break
			}
		}
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if segment := strings.TrimSpace(string(runes)); segment != "" {
		out = append(out, segment)
	}
	return out
}

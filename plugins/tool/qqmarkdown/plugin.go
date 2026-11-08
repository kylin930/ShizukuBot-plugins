// Package qqmarkdown 移植自 koishi-plugin-qq-markdown-button。
//
// 通过模板目录中的 JSON 文件发送 QQ 官方 Markdown / 按钮消息，
// 支持 json 模板、markdown 模板、raw 原生 Markdown 以及键盘按钮。
package qqmarkdown

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed templates/*
var templateFS embed.FS

// Definition 返回插件定义。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "qq-markdown-button",
		Name:        "QQ按钮菜单",
		Version:     "1.0.0",
		Author:      "shangxue",
		Description: "发送 QQ 官方 Markdown 消息与按钮菜单，支持多模板组合发送",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "qq-markdown-button", Usage: "发送配置的按钮菜单指令名即可触发，无需 / 指令"},
	}, sdk.WithConfig(configSchema()),
	)
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"command_name": {
				Type:        "string",
				Description: "注册的指令名称",
				Default:     json.RawMessage(`"按钮菜单"`),
			},
			"file_name_v2": {
				Type:        "array",
				Description: "模板目录路径（以机器人工作目录为基准的路径分段）",
				Default:     json.RawMessage(`["data","qq-markdown-button-v2","按钮菜单配置1"]`),
				Items:       &sdk.Schema{Type: "string"},
			},
			"send_sequence": {
				Type:        "array",
				Description: "发送步骤，按顺序发送，格式为 <类型>/<模板名>，类型支持 json、markdown、raw",
				Default:     json.RawMessage(`["json/json"]`),
				Items:       &sdk.Schema{Type: "string"},
			},
			"Allow_INTERACTION_CREATE": {
				Type:        "boolean",
				Description: "允许按钮回调执行按钮数据（宿主暂不支持交互事件路由，保留配置以兼容上游）",
				Default:     json.RawMessage(`false`),
			},
			"consoleinfo": {
				Type:        "boolean",
				Description: "日志调试模式",
				Default:     json.RawMessage(`false`),
			},
		},
	}
}

type config struct {
	CommandName            string
	FileName               []string
	SendSequence           []string
	AllowInteractionCreate bool
	ConsoleInfo            bool
}

func defaultConfig() config {
	return config{
		CommandName:  "按钮菜单",
		FileName:     []string{"data", "qq-markdown-button-v2", "按钮菜单配置1"},
		SendSequence: []string{"json/json"},
	}
}

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	if v, ok := raw["command_name"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.CommandName = v
	}
	if arr, ok := raw["file_name_v2"].([]any); ok && len(arr) > 0 {
		cfg.FileName = stringSlice(arr)
	}
	if arr, ok := raw["send_sequence"].([]any); ok {
		cfg.SendSequence = stringSlice(arr)
	}
	if v, ok := raw["Allow_INTERACTION_CREATE"].(bool); ok {
		cfg.AllowInteractionCreate = v
	}
	if v, ok := raw["consoleinfo"].(bool); ok {
		cfg.ConsoleInfo = v
	}
	return cfg
}

func stringSlice(arr []any) []string {
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Handle 处理按钮菜单指令。
func Handle(ctx context.Context, c sdk.Call) error {
	cfg := decodeConfig(c.Config)
	ev := c.Event
	rest, ok := commandRest(sdk.Text(ev), cfg.CommandName)
	if !ok {
		return nil
	}
	if !supportedPlatform(ev) {
		return c.Reply(ctx, "仅支持QQ官方平台使用本指令。")
	}
	if len(cfg.SendSequence) == 0 {
		return c.Reply(ctx, "当前未配置发送步骤。")
	}

	baseDir := resolveBaseDir(cfg.FileName)
	if err := ensureTemplateFiles(baseDir); err != nil {
		return c.Reply(ctx, "发送按钮菜单时出错："+err.Error())
	}

	args := splitArgs(rest)
	interaction := interactionID(ev)
	var firstErr error
	for _, ref := range cfg.SendSequence {
		out, err := buildMenuMessage(baseDir, ref, ev, cfg, interaction, args)
		if err != nil {
			if cfg.ConsoleInfo && c.Log != nil {
				c.Log("qq-markdown-button: " + err.Error())
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := sendOutgoing(ctx, c, out); err != nil {
			if cfg.ConsoleInfo && c.Log != nil {
				c.Log("qq-markdown-button: " + err.Error())
			}
			// 模板/样式不被宿主支持时降级为纯文本，避免整条消息丢失。
			if out.Markdown != "" {
				if fallbackErr := c.Reply(ctx, out.Markdown); fallbackErr == nil {
					continue
				}
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// resolveBaseDir 把配置的分段路径解析为模板目录，并修正旧版目录名。
func resolveBaseDir(segments []string) string {
	parts := make([]string, len(segments))
	copy(parts, segments)
	if len(parts) > 1 && parts[1] == "qq-markdown-button" {
		parts[1] = "qq-markdown-button-v2"
	}
	return filepath.Join(parts...)
}

// defaultTemplates 为内置模板文件名及其在模板目录中的相对路径。
var defaultTemplates = []string{
	"json/json.json",
	"markdown/markdown.json",
	"raw/raw-markdown.json",
	"raw/raw-markdown.md",
	"raw/raw-without-keyboard.json",
	"raw/raw-without-keyboard.md",
}

// ensureTemplateFiles 在模板目录缺失时写入内置模板。
func ensureTemplateFiles(baseDir string) error {
	for _, rel := range defaultTemplates {
		target := filepath.Join(baseDir, filepath.FromSlash(rel))
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data, err := templateFS.ReadFile("templates/" + filepath.Base(rel))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type outgoing struct {
	Markdown string
	Keyboard string
}

func parseTemplateRef(ref string) (typ, name string, err error) {
	idx := strings.Index(ref, "/")
	if idx <= 0 || idx >= len(ref)-1 {
		return "", "", fmt.Errorf("无效的发送步骤：%s", ref)
	}
	typ = ref[:idx]
	name = ref[idx+1:]
	switch typ {
	case "json", "markdown", "raw":
	default:
		return "", "", fmt.Errorf("不支持的模板类型：%s", typ)
	}
	if strings.ContainsAny(name, `/\`) {
		return "", "", fmt.Errorf("无效的模板名称：%s", name)
	}
	return typ, name, nil
}

func buildMenuMessage(baseDir, ref string, ev sdk.Event, cfg config, interaction string, args []string) (outgoing, error) {
	typ, name, err := parseTemplateRef(ref)
	if err != nil {
		return outgoing{}, err
	}
	raw, err := os.ReadFile(filepath.Join(baseDir, typ, name+".json"))
	if err != nil {
		return outgoing{}, err
	}

	vars := buildVariables(ev, cfg, interaction)
	if typ == "raw" {
		md, err := os.ReadFile(filepath.Join(baseDir, "raw", name+".md"))
		if err != nil {
			return outgoing{}, err
		}
		resolved, _ := replacePlaceholders(string(md), vars, args).(string)
		vars["markdown"] = resolved
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return outgoing{}, fmt.Errorf("模板 %s 不是有效的 JSON：%w", ref, err)
	}
	decoded = replacePlaceholders(decoded, vars, args)

	message, ok := decoded.(map[string]any)
	if !ok {
		return outgoing{}, fmt.Errorf("模板 %s 必须是 JSON 对象", ref)
	}

	if id, _ := message["msg_id"].(string); id != "" {
		delete(message, "event_id")
	} else {
		delete(message, "msg_id")
	}

	return messageToOutgoing(message), nil
}

func messageToOutgoing(message map[string]any) outgoing {
	var out outgoing
	if md, ok := message["markdown"].(map[string]any); ok {
		if content, ok := md["content"].(string); ok {
			out.Markdown = content
		}
		if out.Markdown == "" {
			if _, ok := md["custom_template_id"]; ok {
				out.Markdown = templateParamsText(md["params"])
			}
		}
	}
	if out.Markdown == "" {
		if content, ok := message["content"].(string); ok {
			out.Markdown = content
		}
	}
	if keyboard, ok := message["keyboard"]; ok && keyboard != nil {
		if data, err := json.Marshal(keyboard); err == nil {
			out.Keyboard = string(data)
		}
	}
	return out
}

// templateParamsText 将 markdown 模板参数按顺序展开为纯文本。
// 宿主 SDK 仅支持原生 Markdown 内容，无法下发 custom_template_id，
// 因此这里保留模板中的全部参数值，避免内容丢失。
func templateParamsText(params any) string {
	list, ok := params.([]any)
	if !ok {
		return ""
	}
	lines := make([]string, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		values, ok := m["values"].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			switch v := value.(type) {
			case string:
				if v != "" {
					lines = append(lines, v)
				}
			default:
				lines = append(lines, fmt.Sprint(v))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// sendOutgoing 发送模板消息。
// 宿主 Reply 不暴露 messageId、msg_seq 或平台错误码，因此无法复刻上游
// msg_seq 递增与 40054005 去重重试；发送失败时由调用方降级为纯文本。
func sendOutgoing(ctx context.Context, c sdk.Call, out outgoing) error {
	if out.Keyboard != "" {
		return c.Send(ctx, sdk.Reply{Kind: "keyboard", Text: out.Markdown, Keyboard: out.Keyboard})
	}
	if out.Markdown != "" {
		return c.Send(ctx, sdk.Reply{Kind: "markdown", Text: out.Markdown})
	}
	return nil
}

// ---- 模板变量 ----

func buildVariables(ev sdk.Event, cfg config, interaction string) map[string]any {
	platform := "qq"
	if string(ev.Source) == "guild" || string(ev.Source) == "direct" {
		platform = "qqguild"
	}
	channelID := ev.ChannelID
	if channelID == "" {
		channelID = ev.GroupID
	}
	guildID := ev.GuildID
	if guildID == "" && platform == "qqguild" {
		guildID = ev.ChannelID
	}
	session := map[string]any{
		"messageId": ev.MessageID,
		"userId":    ev.UserID,
		"selfId":    ev.BotID,
		"botId":     ev.BotID,
		"channelId": channelID,
		"guildId":   guildID,
		"groupId":   ev.GroupID,
		"platform":  platform,
		"content":   ev.Message,
		"event": map[string]any{
			"messageId": ev.MessageID,
			"userId":    ev.UserID,
			"channelId": channelID,
			"guildId":   guildID,
			"groupId":   ev.GroupID,
			"guild":     map[string]any{"id": guildID},
			"user":      map[string]any{"id": ev.UserID},
			"message":   map[string]any{"id": ev.MessageID, "content": ev.Message},
		},
	}
	return map[string]any{
		"INTERACTION_CREATE": interaction,
		"session":            session,
		"config": map[string]any{
			"command_name":             cfg.CommandName,
			"file_name_v2":             cfg.FileName,
			"send_sequence":            cfg.SendSequence,
			"Allow_INTERACTION_CREATE": cfg.AllowInteractionCreate,
			"consoleinfo":              cfg.ConsoleInfo,
		},
	}
}

var placeholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func replacePlaceholders(data any, vars map[string]any, args []string) any {
	switch v := data.(type) {
	case string:
		return placeholderPattern.ReplaceAllStringFunc(v, func(match string) string {
			key := match[2 : len(match)-1]
			value, ok := lookupVariable(key, vars, args)
			if !ok {
				return match
			}
			return value
		})
	case []any:
		for i, item := range v {
			v[i] = replacePlaceholders(item, vars, args)
		}
		return v
	case map[string]any:
		for key, item := range v {
			v[key] = replacePlaceholders(item, vars, args)
		}
		return v
	default:
		return data
	}
}

func lookupVariable(key string, vars map[string]any, args []string) (string, bool) {
	if index, err := strconv.Atoi(key); err == nil {
		if index >= 0 && index < len(args) {
			return args[index], true
		}
		return "undefined", true
	}
	var current any = vars
	for _, segment := range strings.Split(key, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return "", false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(node) {
				return "", false
			}
			current = node[index]
		default:
			return "", false
		}
	}
	switch value := current.(type) {
	case nil:
		return "undefined", true
	case string:
		return value, true
	case bool:
		if value {
			return "true", true
		}
		return "false", true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ","), true
	default:
		return fmt.Sprint(value), true
	}
}

// interactionID 从原始事件中提取按钮交互 ID。
func interactionID(ev sdk.Event) string {
	if len(ev.Raw) == 0 {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(ev.Raw, &raw); err != nil {
		return ""
	}
	data, ok := raw["_data"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := data["id"].(string)
	return id
}

func supportedPlatform(ev sdk.Event) bool {
	switch string(ev.Source) {
	case "group", "c2c", "guild", "direct":
		return true
	default:
		return false
	}
}

// ---- 命令行解析 ----

// commandRest 判断 text 是否以 name 作为完整命令开头，并返回其后的参数部分。
func commandRest(text, name string) (string, bool) {
	if name == "" || !strings.HasPrefix(text, name) {
		return "", false
	}
	rest := text[len(name):]
	if rest == "" {
		return "", true
	}
	r := rune(rest[0])
	if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// splitArgs 按空白拆分参数，支持单双引号与反斜杠转义。
func splitArgs(input string) []string {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	hasToken := false
	flush := func() {
		if hasToken {
			args = append(args, current.String())
			current.Reset()
			hasToken = false
		}
	}
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			hasToken = true
			escaped = false
		case r == '\\':
			escaped = true
			hasToken = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			hasToken = true
		case r == '"' || r == '\'':
			quote = r
			hasToken = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			current.WriteRune(r)
			hasToken = true
		}
	}
	flush()
	return args
}

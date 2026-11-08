// Package huozi 活字乱刷：调用本地/远端 HUOZI_koi 服务生成语音。
// 行为对齐上游 koishi-plugin-huozi-generator：
// 指令 huozi-generator（别名 活字乱刷 / 活字印刷），选项 -y/-n/-r/-s/-p。
package huozi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 接口默认地址；测试可替换。
var (
	apiEndpoint = "http://127.0.0.1:8989/api/make"
	client      = &http.Client{Timeout: 60 * time.Second}
)

const (
	configWaitTip         = "waitTip_Switch"
	configAPIEndpoint     = "apiEndpoint"
	configYsddMode        = "default_inYsddMode"
	configNorm            = "default_norm"
	configReverse         = "default_reverse"
	configSpeedMult       = "default_speedMult"
	configPitchMult       = "default_pitchMult"
	configLoggerInfo      = "loggerinfo"
	defaultSpeedMult      = 1.0
	defaultPitchMult      = 1.0
	defaultAPIEndpoint    = "http://127.0.0.1:8989/api/make"
	maxResponseBytes      = 1 << 20
	messageExpectText     = "请输入要生成音频的文本。"
	messageGenerationFail = "音频生成失败，请稍后再试。"
)

var trigger = regexp.MustCompile(`^(?:huozi-generator|活字乱刷|活字印刷)\s*([\s\S]*)$`)

// Definition 订阅活字乱刷指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "huozi",
		Name:        "活字乱刷",
		Version:     "1.0.0",
		Description: "发送「活字乱刷 [-y] [-n] [-r] [-s 速度] [-p 音调] 文本」生成语音",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "huozi", Trigger: "活字乱刷"}},
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				configWaitTip: {
					Type:        "string",
					Description: "是否返回等待提示。留空不提示，填写文字则发送该提示语",
					Default:     json.RawMessage(`""`),
				},
				configAPIEndpoint: {
					Type:        "string",
					Description: "使用的 apiEndpoint",
					Default:     json.RawMessage(`"` + defaultAPIEndpoint + `"`),
				},
				configYsddMode: {
					Type:        "boolean",
					Description: "匹配到特定文字时使用原声大碟",
					Default:     json.RawMessage(`false`),
				},
				configNorm: {
					Type:        "boolean",
					Description: "统一音频的音量",
					Default:     json.RawMessage(`false`),
				},
				configReverse: {
					Type:        "boolean",
					Description: "倒放生成的音频",
					Default:     json.RawMessage(`false`),
				},
				configSpeedMult: {
					Type:        "number",
					Description: "速度",
					Default:     json.RawMessage(`1.0`),
				},
				configPitchMult: {
					Type:        "number",
					Description: "音调",
					Default:     json.RawMessage(`1.0`),
				},
				configLoggerInfo: {
					Type:        "boolean",
					Description: "日志调试输出",
					Default:     json.RawMessage(`false`),
				},
			},
		}))
}

// options 为指令选项，nil 表示未显式指定、沿用配置。
type options struct {
	ysddMode  *bool
	norm      *bool
	reverse   *bool
	speedMult *string
	pitchMult *string
}

// Handle 解析选项并生成音频。
func Handle(ctx context.Context, c sdk.Call) error {
	raw := sdk.Text(c.Event)
	m := sdk.MatchRegexp(raw, trigger)
	if m == nil {
		return nil
	}
	opts, content := parseArgs(m[1])
	if strings.TrimSpace(content) == "" {
		return c.Reply(ctx, messageExpectText)
	}
	if tip := waitTip(c.Config); tip != "" {
		if err := c.Reply(ctx, tip); err != nil {
			return err
		}
	}
	fileURL, err := generateAudio(ctx, c.Config, content, opts)
	if err != nil {
		logInfo(c, "Audio generation failed: "+err.Error())
		return c.Reply(ctx, messageGenerationFail)
	}
	logInfo(c, "result: "+fileURL)
	return c.ReplyVoice(ctx, fileURL)
}

// parseArgs 拆分 -y/-n/-r/-s/-p 与文本，保留文本中的原始换行。
func parseArgs(content string) (options, string) {
	var opts options
	tokens := scanTokens(content)
	var parts []string
	lastEnd := -1
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if !tok.flag {
			if lastEnd >= 0 {
				between := content[lastEnd:tok.start]
				if hasFlag(tokens, lastEnd, tok.start) {
					parts = append(parts, " ")
				} else {
					parts = append(parts, between)
				}
			}
			parts = append(parts, content[tok.start:tok.end])
			lastEnd = tok.end
			continue
		}
		switch tok.name {
		case "-y", "--ysddMode":
			opts.ysddMode = boolPtr(true)
		case "-n", "--norm":
			opts.norm = boolPtr(true)
		case "-r", "--reverse":
			opts.reverse = boolPtr(true)
		case "-s", "--speedMult", "-p", "--pitchMult":
			var value string
			if tok.hasValue {
				value = tok.value
			} else if i+1 < len(tokens) && !tokens[i+1].flag {
				value = content[tokens[i+1].start:tokens[i+1].end]
				tokens[i+1].flag = true
				tokens[i+1].name = ""
				i++
			} else {
				continue
			}
			if tok.name == "-s" || tok.name == "--speedMult" {
				opts.speedMult = &value
			} else {
				opts.pitchMult = &value
			}
		}
	}
	return opts, strings.Join(parts, "")
}

func hasFlag(tokens []token, start, end int) bool {
	for _, tok := range tokens {
		if tok.flag && tok.start >= start && tok.end <= end {
			return true
		}
	}
	return false
}

type token struct {
	start    int
	end      int
	flag     bool
	name     string
	value    string
	hasValue bool
}

// scanTokens 按空白切分原始文本并标注选项。
func scanTokens(s string) []token {
	var tokens []token
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		word := s[start:i]
		name, inline, hasInline := splitOption(word)
		if name == "" {
			tokens = append(tokens, token{start: start, end: i})
			continue
		}
		tokens = append(tokens, token{start: start, end: i, flag: true, name: name, value: inline, hasValue: hasInline})
	}
	return tokens
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func splitOption(word string) (name, value string, hasValue bool) {
	if len(word) < 2 || word[0] != '-' {
		return "", "", false
	}
	if idx := strings.IndexByte(word, '='); idx > 0 {
		base := word[:idx]
		switch base {
		case "-s", "--speedMult", "-p", "--pitchMult":
			return base, word[idx+1:], true
		}
		return "", "", false
	}
	switch word {
	case "-y", "--ysddMode", "-n", "--norm", "-r", "--reverse":
		return word, "", false
	case "-s", "--speedMult", "-p", "--pitchMult":
		return word, "", false
	}
	return "", "", false
}

// generateAudio 请求生成接口并返回音频地址。
func generateAudio(ctx context.Context, config map[string]any, content string, opts options) (string, error) {
	escaped := strings.ReplaceAll(content, "\n", `\n`)
	endpoint := apiEndpointFromConfig(config)
	query := "?text=" + percentEncode(escaped) +
		"&inYsddMode=" + fmt.Sprint(resolveBool(opts.ysddMode, config, configYsddMode)) +
		"&norm=" + fmt.Sprint(resolveBool(opts.norm, config, configNorm)) +
		"&reverse=" + fmt.Sprint(resolveBool(opts.reverse, config, configReverse)) +
		"&speedMult=" + resolveNumber(opts.speedMult, config, configSpeedMult, defaultSpeedMult) +
		"&pitchMult=" + resolveNumber(opts.pitchMult, config, configPitchMult, defaultPitchMult)
	requestURL := endpoint + query

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", err
	}
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
	var payload struct {
		Code    int    `json:"code"`
		FileURL string `json:"file_url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if payload.Code == 200 && payload.FileURL != "" {
		return payload.FileURL, nil
	}
	return "", errors.New("Audio generation API failed.")
}

// percentEncode 对齐 encodeURIComponent：空格编码为 %20。
func percentEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func apiEndpointFromConfig(config map[string]any) string {
	if v, ok := config[configAPIEndpoint].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return apiEndpoint
}

func waitTip(config map[string]any) string {
	if v, ok := config[configWaitTip].(string); ok {
		return v
	}
	return ""
}

func resolveBool(override *bool, config map[string]any, key string) bool {
	if override != nil {
		return *override
	}
	v, _ := config[key].(bool)
	return v
}

func resolveNumber(override *string, config map[string]any, key string, fallback float64) string {
	if override != nil {
		return *override
	}
	switch v := config[key].(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	}
	return strconv.FormatFloat(fallback, 'f', -1, 64)
}

func boolPtr(v bool) *bool { return &v }

func logInfo(c sdk.Call, message string) {
	enabled, _ := c.Config[configLoggerInfo].(bool)
	if !enabled || c.Log == nil {
		return
	}
	c.Log(message)
}

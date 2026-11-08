// Package universaltranslation 多语言翻译。
// 行为对齐上游 koishi-plugin-universal-translation：
// 指令 universal-translation <文本>，选项 -t 指定目标语言，
// 依次尝试 api.jaxing.cc 的腾讯翻译与 Cloudflare 翻译接口。
package universaltranslation

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

//go:embed languageMap.json
var languageMapJSON []byte

// 上游接口地址；测试可替换。
var (
	tencentURL    = "https://api.jaxing.cc/v2/Translate/Tencent"
	cloudflareURL = "https://translate.cloudflare.jaxing.cc/"
	client        = &http.Client{Timeout: 20 * time.Second}
)

const (
	// defaultTargetLang 与上游配置默认值一致。
	defaultTargetLang = "en"
	// maxResponseBytes 限制单次接口响应体大小。
	maxResponseBytes = 1 << 20
	// tableNotice 是「语言代码对照表」子指令的固定回复。
	tableNotice = "注意：并不是所列语言均支持翻译，此表仅供参考语言代码\n对照表请见这里【http://www.lingoes.cn/zh/translator/langcode.htm】"
)

// 配置键。
const configDefaultTargetLang = "defaultTargetLang"

var (
	langMapOnce sync.Once
	langMap     map[string]string
)

// languageMap 返回上游语言中文名到代码的映射，重复键后值覆盖前值（与 JS 对象一致）。
func languageMap() map[string]string {
	langMapOnce.Do(func() {
		langMap = make(map[string]string)
		_ = json.Unmarshal(languageMapJSON, &langMap)
	})
	return langMap
}

// Definition 注册 /universal-translation 指令。
func Definition() sdk.Definition {
	return sdk.CommandPlugin(sdk.Meta{
		ID:          "universaltranslation",
		Name:        "通用翻译",
		Version:     "1.0.0",
		Description: "翻译文本：/universal-translation [-t 目标语言] 文本；/universal-translation 语言代码对照表",
	}, "universal-translation", Handle,
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				configDefaultTargetLang: {
					Type:        "string",
					Description: "默认的目标语言代码（例如 en、zh、ja 等）",
					Default:     json.RawMessage(`"en"`),
				},
			},
		}))
}

// Handle 处理语言代码对照表子指令与翻译请求。
func Handle(ctx context.Context, c sdk.Call) error {
	if len(c.Args) > 0 && c.Args[0] == "语言代码对照表" {
		return c.Reply(ctx, tableNotice)
	}
	target, text, err := parseArgs(c.Args, defaultTarget(c.Config))
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	if text == "" {
		return c.Reply(ctx, "请输入要翻译的文本...")
	}
	result, err := translate(ctx, text, target)
	if err != nil {
		// 与上游一致：两个接口都失败时静默，不回复错误信息。
		return nil
	}
	return c.Reply(ctx, result)
}

// defaultTarget 读取配置中的默认目标语言。
func defaultTarget(config map[string]any) string {
	if v, ok := config[configDefaultTargetLang].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return defaultTargetLang
}

// parseArgs 拆分 -t 选项与待翻译文本，并把中文语言名转换为代码。
func parseArgs(args []string, fallback string) (string, string, error) {
	target := fallback
	var words []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := splitFlag(arg)
		if name == "" {
			words = append(words, arg)
			continue
		}
		value := inline
		if !hasInline {
			if i+1 < len(args) {
				i++
				value = args[i]
			} else {
				value = fallback
			}
		}
		if value == "" {
			value = fallback
		}
		target = resolveLanguage(value)
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	return target, text, nil
}

func splitFlag(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "-") || len(arg) < 2 {
		return "", "", false
	}
	if idx := strings.IndexByte(arg, '='); idx > 0 {
		base := arg[:idx]
		if base == "-t" || base == "--to" {
			return "-t", arg[idx+1:], true
		}
		return "", "", false
	}
	if arg == "-t" || arg == "--to" {
		return "-t", "", false
	}
	return "", "", false
}

// resolveLanguage 与上游 `languageMap[to] || to` 一致。
func resolveLanguage(value string) string {
	if code, ok := languageMap()[value]; ok && code != "" {
		return code
	}
	return value
}

// translate 依次尝试腾讯翻译与 Cloudflare 翻译，任意一个返回非空即成功。
func translate(ctx context.Context, input, target string) (string, error) {
	var lastErr error
	providers := []func(context.Context, string, string) (string, error){
		translateTencent,
		translateCloudflare,
	}
	for _, provider := range providers {
		out, err := provider(ctx, input, target)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("所有翻译接口均未返回结果")
	}
	return "", lastErr
}

func translateTencent(ctx context.Context, input, target string) (string, error) {
	endpoint, err := url.Parse(tencentURL)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("SourceText", input)
	query.Set("Target", target)
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Data struct {
			Response struct {
				TargetText string `json:"TargetText"`
			} `json:"Response"`
		} `json:"data"`
	}
	if err := getJSON(ctx, endpoint.String(), &payload); err != nil {
		return "", err
	}
	return payload.Data.Response.TargetText, nil
}

func translateCloudflare(ctx context.Context, input, target string) (string, error) {
	endpoint, err := url.Parse(cloudflareURL)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("text", input)
	query.Set("source_lang", "zh")
	query.Set("target_lang", target)
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Response struct {
			TranslatedText string `json:"translated_text"`
		} `json:"response"`
	}
	if err := getJSON(ctx, endpoint.String(), &payload); err != nil {
		return "", err
	}
	return payload.Response.TranslatedText, nil
}

func getJSON(ctx context.Context, address string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

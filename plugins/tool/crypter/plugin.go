// Package crypter 移植 ZeroBot-Plugin 的奇怪语言加解密插件，
// 支持齁语（兽音译者）与 Fumo 语（芙语/臭鼬语）的加密与解密。
//
// 上游的 QQ 小黄脸（qq加密/qq解密）依赖消息中的 face 段，
// 当前框架的 Event 不暴露表情段，无法实现，故未移植。
package crypter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 触发指令沿用上游写法，同时保留长短两种形式。
var (
	houEncryptPrefixes = []string{"齁语加密", "h加密"}
	houDecryptPrefixes = []string{"齁语解密", "h解密"}
	fumoEncryptPrefix  = "fumo加密"
	fumoDecryptPrefix  = "fumo解密"
)

const (
	houEncryptUsage  = "用法：齁语加密 [文本] 或 h加密 [文本]"
	houDecryptUsage  = "用法：齁语解密 [密文] 或 h解密 [密文]"
	fumoEncryptUsage = "用法：fumo加密 [文本]"
	fumoDecryptUsage = "用法：fumo解密 [密文]"
)

// Definition 订阅齁语与 Fumo 语的加解密指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:      "crypter",
		Name:    "奇怪语言加解密",
		Version: "1.0.0",
		Description: "齁语（兽音）与 Fumo 语（芙语）加解密：" +
			"齁语加密/h加密、齁语解密/h解密、fumo加密、fumo解密；" +
			"QQ 小黄脸加解密需要表情消息段，当前框架不支持，未移植",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "crypter", Trigger: "齁语加密"},
	}, sdk.WithConfig(sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			configVoiceEndpoint: {
				Type: "string",
				Description: "齁语加密的语音合成接口（GET ?text=密文，返回 {\"file_url\":\"...\"} 或纯 URL）；" +
					"留空时只回复文本，配置后优先发送语音并在失败时回退文本",
				Default: json.RawMessage(`""`),
			},
		},
	}))
}

const configVoiceEndpoint = "voiceEndpoint"

// voiceClient 发送语音合成请求；测试可替换 voiceRequest。
var (
	voiceClient  = &http.Client{Timeout: 20 * time.Second}
	voiceRequest = requestVoice
)

// requestVoice 调用上游语音接口并把返回值解析为可发送的语音 URL。
func requestVoice(ctx context.Context, endpoint, text string) string {
	if endpoint == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+sep+"text="+url.QueryEscape(text), nil)
	if err != nil {
		return ""
	}
	resp, err := voiceClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var payload struct {
		FileURL string `json:"file_url"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.FileURL) != "" {
		return strings.TrimSpace(payload.FileURL)
	}
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "http") {
		return trimmed
	}
	return ""
}

// replyHou 优先把齁语密文合成为语音，接口不可用或发送失败时回退文本。
func replyHou(ctx context.Context, c sdk.Call, text string) error {
	endpoint, _ := c.Config[configVoiceEndpoint].(string)
	if strings.TrimSpace(endpoint) != "" {
		if record := voiceRequest(ctx, strings.TrimSpace(endpoint), text); record != "" {
			if err := c.ReplyVoice(ctx, record); err == nil {
				return nil
			}
		}
	}
	return c.Reply(ctx, text)
}

// Handle 按指令前缀分派到对应的加解密逻辑。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if arg, ok := matchAnyPrefix(text, houEncryptPrefixes); ok {
		if arg == "" {
			return c.Reply(ctx, houEncryptUsage)
		}
		return replyHou(ctx, c, encodeHou(arg))
	}
	if arg, ok := matchAnyPrefix(text, houDecryptPrefixes); ok {
		if arg == "" {
			return c.Reply(ctx, houDecryptUsage)
		}
		return c.Reply(ctx, decodeHou(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, fumoEncryptPrefix); ok {
		if arg == "" {
			return c.Reply(ctx, fumoEncryptUsage)
		}
		return c.Reply(ctx, encryptFumo(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, fumoDecryptPrefix); ok {
		if arg == "" {
			return c.Reply(ctx, fumoDecryptUsage)
		}
		return c.Reply(ctx, decryptFumo(arg))
	}
	return nil
}

// matchAnyPrefix 依次尝试多个前缀，返回首个命中的剩余文本。
func matchAnyPrefix(text string, prefixes []string) (string, bool) {
	for _, prefix := range prefixes {
		if arg, ok := sdk.MatchPrefix(text, prefix); ok {
			return arg, true
		}
	}
	return "", false
}

// 齁语密码表：每个字节拆成高低两个 4 位，分别映射到 16 个字符。
var houCodebook = []string{
	"齁", "哦", "噢", "喔", "咕", "咿", "嗯", "啊",
	"～", "哈", "！", "唔", "哼", "❤", "呃", "呼",
}

var houCodebookIndex = buildHouIndex()

func buildHouIndex() map[string]int {
	m := make(map[string]int, len(houCodebook))
	for idx, ch := range houCodebook {
		m[ch] = idx
	}
	return m
}

// encodeHou 把文本按 UTF-8 字节编码为齁语。
func encodeHou(text string) string {
	var b strings.Builder
	for _, raw := range []byte(text) {
		b.WriteString(houCodebook[(raw>>4)&0x0F])
		b.WriteString(houCodebook[raw&0x0F])
	}
	return b.String()
}

// decodeHou 过滤出合法齁语字符后两两还原字节，非法输入返回提示文案。
func decodeHou(code string) string {
	var chars []string
	for _, r := range code {
		if _, ok := houCodebookIndex[string(r)]; ok {
			chars = append(chars, string(r))
		}
	}
	if len(chars)%2 != 0 {
		return "齁语密文长度错误，无法解密"
	}

	out := make([]byte, 0, len(chars)/2)
	for i := 0; i < len(chars); i += 2 {
		high := houCodebookIndex[chars[i]]
		low := houCodebookIndex[chars[i+1]]
		out = append(out, byte(high<<4|low))
	}
	if !utf8.Valid(out) {
		return "齁语解密失败，结果不是有效的文本"
	}
	return string(out)
}

// Fumo 语的字符表：用 64 个 fumo 变体一一对应标准 Base64 字符。
const fumoBase64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var fumoChars = []string{
	"fumo-", "Fumo-", "fUmo-", "fuMo-", "fumO-", "FUmo-", "FuMo-", "FumO-",
	"fUMo-", "fUmO-", "fuMO-", "FUMo-", "FUmO-", "fUMO-", "FUMO-", "fumo.",
	"Fumo.", "fUmo.", "fuMo.", "fumO.", "FUmo.", "FuMo.", "FumO.", "fUMo.",
	"fUmO.", "fuMO.", "FUMo.", "FUmO.", "fUMO.", "FUMO.", "fumo,", "Fumo,",
	"fUmo,", "fuMo,", "fumO,", "FUmo,", "FuMo,", "FumO,", "fUMo,", "fUmO,",
	"fuMO,", "FUMo,", "FuMO,", "fUMO,", "FUMO,", "fumo+", "Fumo+", "fUmo+",
	"fuMo+", "fumO+", "FUmo+", "FuMo+", "FumO+", "fUMo+", "fUmO+", "fuMO+",
	"FUMo+", "FUmO+", "fUMO+", "FUMO+", "fumo|", "Fumo|", "fUmo|", "fuMo|",
	"fumO|", "FUmo|", "FuMo|", "FumO|", "fUMo|", "fUmO|", "fuMO|", "fumo/",
	"Fumo/", "fUmo/",
}

// fumoTokenRe 与上游一致：一个 fumo 词以分隔符结尾。
var fumoTokenRe = regexp.MustCompile(`(\w+[-.,+|/])`)

var (
	fumoEncodeMap = buildFumoEncode()
	fumoDecodeMap = buildFumoDecode()
)

func buildFumoEncode() map[byte]string {
	m := make(map[byte]string, 64)
	for i := 0; i < 64 && i < len(fumoChars); i++ {
		m[fumoBase64Chars[i]] = fumoChars[i]
	}
	return m
}

func buildFumoDecode() map[string]byte {
	m := make(map[string]byte, 64)
	for i := 0; i < 64 && i < len(fumoChars); i++ {
		m[fumoChars[i]] = fumoBase64Chars[i]
	}
	return m
}

// encryptFumo 把文本做标准 Base64 后逐字符替换为 Fumo 词，保留尾部填充。
func encryptFumo(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	body := strings.TrimRight(encoded, "=")
	padding := len(encoded) - len(body)

	var b strings.Builder
	for i := 0; i < len(body); i++ {
		fumo, ok := fumoEncodeMap[body[i]]
		if !ok {
			return "Fumo加密失败: 未知字符 " + string(body[i])
		}
		b.WriteString(fumo)
	}
	return b.String() + strings.Repeat("=", padding)
}

// decryptFumo 把 Fumo 词还原为 Base64 字符后再解码，非法输入返回提示文案。
func decryptFumo(fumoText string) string {
	body := strings.TrimRight(fumoText, "=")
	padding := len(fumoText) - len(body)

	tokens := fumoTokenRe.FindAllString(body, -1)
	if strings.Join(tokens, "") != body {
		return "Fumo解密失败: 包含无效的Fumo字符或格式错误"
	}

	var b strings.Builder
	for _, token := range tokens {
		ch, ok := fumoDecodeMap[token]
		if !ok {
			return "Fumo解密失败: 包含无效的Fumo字符 " + token
		}
		b.WriteByte(ch)
	}

	decoded, err := base64.StdEncoding.DecodeString(b.String() + strings.Repeat("=", padding))
	if err != nil {
		return "Fumo解密失败: Base64解码错误 " + err.Error()
	}
	return string(decoded)
}

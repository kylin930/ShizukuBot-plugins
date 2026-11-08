// Package furrycode 兽语加密与兽语解密。
// 行为对齐上游 koishi-plugin-furry-encode-decode：
// legacy（嗷呜啊~）、msbt（16 字符表）与 custom 自定义四字码本三种方式。
package furrycode

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// legacyCodebook 是上游默认码本，顺序为 嗷 呜 啊 ~。
const legacyCodebook = "嗷呜啊~"

// msbtCodebook 是上游 msbt 方式的 16 字符码本。
var msbtCodebook = []rune{'齁', '哦', '噢', '喔', '咕', '咿', '嗯', '啊', '～', '哈', '！', '唔', '哼', '❤', '呃', '呼'}

// 配置键与默认值。
const (
	configEncryptMethod  = "encryptMethod"
	configCustomCodebook = "customCodebook"
	configLoggerInfo     = "loggerinfo"
)

var (
	encodeTrigger = regexp.MustCompile(`^兽语加密[\s]*([\s\S]*)$`)
	decodeTrigger = regexp.MustCompile(`^兽语解密[\s]*([\s\S]*)$`)
)

// Definition 订阅「兽语加密」与「兽语解密」。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "furrycode",
		Name:        "兽语加解密",
		Version:     "1.0.0",
		Description: "发送「兽语加密 内容」或「兽语解密 内容」，支持 legacy、msbt 与自定义码本",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "furrycode", Trigger: "兽语加密"},
	},
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				configLoggerInfo: {
					Type:        "boolean",
					Description: "调试日志开关",
					Default:     json.RawMessage(`false`),
				},
				configEncryptMethod: {
					Type:        "string",
					Description: "加密方式：legacy、msbt 或 custom",
					Default:     json.RawMessage(`"legacy"`),
					Enum:        []any{"legacy", "msbt", "custom"},
				},
				configCustomCodebook: {
					Type:        "string",
					Description: "自定义加密字符串，必须是 4 个不重复字符",
					Default:     json.RawMessage(`"嗷呜啊~"`),
				},
			},
		}))
}

// Handle 分发加密/解密指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if m := sdk.MatchRegexp(text, encodeTrigger); m != nil {
		return reply(ctx, c, m[1], true)
	}
	if m := sdk.MatchRegexp(text, decodeTrigger); m != nil {
		return reply(ctx, c, m[1], false)
	}
	return nil
}

func reply(ctx context.Context, c sdk.Call, text string, encode bool) error {
	codec, err := createCodec(c.Config)
	if err != nil {
		logDebug(c, "兽语编码器初始化失败: "+err.Error())
		return c.Reply(ctx, "插件配置有误，请检查“加密方式”和“自定义加密字符串”")
	}
	input := strings.TrimSpace(text)
	if input == "" {
		return c.Reply(ctx, "请输入要处理的内容")
	}
	if encode {
		logDebug(c, "encode 完成")
		return c.Reply(ctx, codec.Encode(input))
	}
	out, err := codec.Decode(input)
	if err != nil {
		logDebug(c, "decode 失败: "+err.Error())
		return c.Reply(ctx, err.Error())
	}
	logDebug(c, "decode 完成")
	return c.Reply(ctx, out)
}

func logDebug(c sdk.Call, message string) {
	enabled, _ := c.Config[configLoggerInfo].(bool)
	if !enabled || c.Log == nil {
		return
	}
	c.Log(message)
}

// codec 是兽语编解码器。
type codec interface {
	Encode(string) string
	Decode(string) (string, error)
}

// createCodec 依据配置构建编解码器，与上游 createFurryCodec 一致。
func createCodec(config map[string]any) (codec, error) {
	mode, _ := config[configEncryptMethod].(string)
	if mode == "" {
		mode = "legacy"
	}
	switch mode {
	case "msbt":
		return &byteCodec{codebook: msbtCodebook}, nil
	case "legacy":
		book, err := fourChars(legacyCodebook, "默认加密方式")
		if err != nil {
			return nil, err
		}
		return &legacyCodec{codebook: book}, nil
	case "custom":
		custom, _ := config[configCustomCodebook].(string)
		book, err := fourChars(custom, "自定义加密方式")
		if err != nil {
			return nil, err
		}
		return &legacyCodec{codebook: book}, nil
	default:
		return nil, errors.New("未知的加密方式: " + mode)
	}
}

// fourChars 校验码本为 4 个不重复字符。
func fourChars(value, label string) ([]rune, error) {
	chars := []rune(strings.TrimSpace(value))
	if len(chars) != 4 {
		return nil, errors.New(label + "必须是 4 个字符")
	}
	seen := map[rune]bool{}
	for _, r := range chars {
		if seen[r] {
			return nil, errors.New(label + "不能包含重复字符")
		}
		seen[r] = true
	}
	return chars, nil
}

// legacyCodec 还原原始兽语风格：前缀 + 每个 UTF-16 码元 8 字符 + 后缀。
type legacyCodec struct {
	codebook []rune
}

func (l *legacyCodec) Encode(text string) string {
	chars := []rune(strings.TrimSpace(text))
	if len(chars) < 1 {
		return ""
	}
	var b strings.Builder
	b.WriteRune(l.codebook[3])
	b.WriteRune(l.codebook[1])
	b.WriteRune(l.codebook[0])
	offset := 0
	for _, r := range chars {
		codeUnit := firstCodeUnit(r)
		for bit := 12; bit >= 0; bit -= 4 {
			hex := ((codeUnit >> bit) + offset) & 15
			offset++
			b.WriteRune(l.codebook[hex>>2])
			b.WriteRune(l.codebook[hex&3])
		}
	}
	b.WriteRune(l.codebook[2])
	return b.String()
}

func (l *legacyCodec) Decode(text string) (string, error) {
	chars := []rune(strings.TrimSpace(text))
	if !l.identify(chars) {
		return "", errors.New("输入格式不正确")
	}
	var units []uint16
	index := 3
	offset := 0
	for index < len(chars)-1 {
		codeUnit := 0
		end := index + 8
		for index < end {
			high := indexRune(l.codebook, chars[index])
			index++
			low := indexRune(l.codebook, chars[index])
			index++
			if high < 0 || low < 0 {
				return "", errors.New("输入包含非法字符")
			}
			codeUnit = (codeUnit << 4) | ((((high << 2) | low) + offset) & 15)
			if offset == 0 {
				offset = 15
			} else {
				offset--
			}
		}
		units = append(units, uint16(codeUnit))
	}
	return string(utf16.Decode(units)), nil
}

func (l *legacyCodec) identify(chars []rune) bool {
	if len(chars) <= 11 {
		return false
	}
	if chars[0] != l.codebook[3] || chars[1] != l.codebook[1] || chars[2] != l.codebook[0] {
		return false
	}
	if chars[len(chars)-1] != l.codebook[2] {
		return false
	}
	if (len(chars)-4)%8 != 0 {
		return false
	}
	for _, r := range chars {
		if indexRune(l.codebook, r) < 0 {
			return false
		}
	}
	return true
}

// byteCodec 是 msbt 的字节级编码：每个字节拆成高/低 4 位。
type byteCodec struct {
	codebook []rune
}

func (c *byteCodec) Encode(text string) string {
	var b strings.Builder
	for _, by := range []byte(text) {
		b.WriteRune(c.codebook[by>>4])
		b.WriteRune(c.codebook[by&15])
	}
	return b.String()
}

func (c *byteCodec) Decode(text string) (string, error) {
	chars := []rune(strings.TrimSpace(text))
	if len(chars)%2 != 0 {
		return "", errors.New("输入长度必须为偶数")
	}
	out := make([]byte, len(chars)/2)
	for i := 0; i < len(chars); i += 2 {
		high := indexRune(c.codebook, chars[i])
		low := indexRune(c.codebook, chars[i+1])
		if high < 0 || low < 0 {
			return "", errors.New("输入包含非法字符")
		}
		out[i/2] = byte((high << 4) | low)
	}
	if !utf8.Valid(out) {
		return "", errors.New("无法正确解码为 UTF-8 文本")
	}
	return string(out), nil
}

// firstCodeUnit 返回 JS charCodeAt(0) 的结果：非 BMP 字符取高位代理。
func firstCodeUnit(r rune) int {
	if r <= 0xFFFF {
		return int(r)
	}
	return 0xD800 + int((r-0x10000)>>10)
}

func indexRune(list []rune, target rune) int {
	for i, r := range list {
		if r == target {
			return i
		}
	}
	return -1
}

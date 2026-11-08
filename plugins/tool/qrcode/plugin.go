// Package qrcode 生成二维码图片。
// 行为对齐上游 koishi-plugin-qrcode-service-null 的 qrcode 指令：
// -m 边界、-s 缩放、-w 图片大小、-d 暗部颜色、-l 亮部颜色。
package qrcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"shizukubot/sdk"
)

// 上游默认值。
const (
	defaultMargin = 4
	defaultScale  = 4
	// configEnableCommand 对齐上游 enablecommand：默认不注册/响应 qrcode 指令。
	configEnableCommand = "enablecommand"
)

// Definition 注册 /qrcode 指令。
func Definition() sdk.Definition {
	return sdk.CommandPlugin(sdk.Meta{
		ID:          "qrcode",
		Name:        "二维码生成",
		Version:     "1.0.0",
		Description: "生成二维码：/qrcode [-m 边界] [-s 缩放] [-w 大小] [-d 暗色] [-l 亮色] 文本",
	}, "qrcode", Handle, sdk.WithConfig(sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			configEnableCommand: {
				Type:        "boolean",
				Description: "是否启用 qrcode 指令",
				Default:     json.RawMessage(`false`),
			},
		},
	}))
}

// options 对应上游指令选项。
type options struct {
	margin int
	scale  int
	width  int
	dark   color.RGBA
	light  color.RGBA
}

// Handle 解析选项并回复二维码图片。
func Handle(ctx context.Context, c sdk.Call) error {
	if enabled, _ := c.Config[configEnableCommand].(bool); !enabled {
		return nil
	}
	opts, text, err := parseArgs(c.Args)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	if text == "" {
		return c.Reply(ctx, "请输入源文本。")
	}
	if strings.Contains(text, "[CQ:") {
		return c.Reply(ctx, "禁止输入纯文本以外的内容。")
	}
	data, err := render(text, opts)
	if err != nil {
		return c.Reply(ctx, "二维码生成失败："+err.Error())
	}
	return c.ReplyImageBytes(ctx, data)
}

// parseArgs 拆分选项与文本，选项值缺失时返回上游同义提示。
func parseArgs(args []string) (options, string, error) {
	opts := options{
		margin: defaultMargin,
		scale:  defaultScale,
		dark:   color.RGBA{R: 0, G: 0, B: 0, A: 255},
		light:  color.RGBA{R: 255, G: 255, B: 255, A: 255},
	}
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
			if i+1 >= len(args) {
				return opts, "", errors.New("选项 " + name + " 缺少参数值。")
			}
			i++
			value = args[i]
		}
		if err := applyOption(&opts, name, value); err != nil {
			return opts, "", err
		}
	}
	return opts, strings.TrimSpace(strings.Join(words, " ")), nil
}

// splitFlag 识别 -m/-s/-w/-d/-l 及其 --name=value 形式。
func splitFlag(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "-") || len(arg) < 2 {
		return "", "", false
	}
	if idx := strings.IndexByte(arg, '='); idx > 0 {
		return normalizeFlag(arg[:idx]), arg[idx+1:], true
	}
	return normalizeFlag(arg), "", false
}

func normalizeFlag(flag string) string {
	switch flag {
	case "-m", "--margin":
		return "-m"
	case "-s", "--scale":
		return "-s"
	case "-w", "--width":
		return "-w"
	case "-d", "--dark":
		return "-d"
	case "-l", "--light":
		return "-l"
	}
	return ""
}

func applyOption(opts *options, name, value string) error {
	switch name {
	case "-m", "-s", "-w":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("选项 %s 需要非负整数，收到 %q。", name, value)
		}
		switch name {
		case "-m":
			opts.margin = n
		case "-s":
			opts.scale = n
		case "-w":
			opts.width = n
		}
		return nil
	case "-d", "-l":
		parsed, err := parseColor(value)
		if err != nil {
			return fmt.Errorf("选项 %s 颜色无法识别：%s。", name, value)
		}
		if name == "-d" {
			opts.dark = parsed
		} else {
			opts.light = parsed
		}
		return nil
	}
	return nil
}

// render 生成二维码 PNG。渲染规则与 node-qrcode 一致：
// 无 -w 时按 (模块数+2*边界)*缩放 输出，有 -w 时以 -w 为图片边长。
func render(text string, opts options) ([]byte, error) {
	code, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	// 自行绘制静区，以支持任意 margin。
	code.DisableBorder = true
	matrix := code.Bitmap()
	if len(matrix) == 0 {
		return nil, errors.New("二维码矩阵为空")
	}
	modules := len(matrix)
	margin := opts.margin
	scale := opts.scale
	if scale <= 0 {
		scale = 1
	}
	cells := modules + margin*2
	var size int
	if opts.width > 0 {
		size = opts.width
	} else {
		size = cells * scale
	}
	if size < cells {
		size = cells
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: opts.light}, image.Point{}, draw.Src)

	factor := float64(size) / float64(cells)
	dark := &image.Uniform{C: opts.dark}
	for y := range matrix {
		for x, on := range matrix[y] {
			if !on {
				continue
			}
			x0 := int(math.Round(float64(x+margin) * factor))
			x1 := int(math.Round(float64(x+margin+1) * factor))
			y0 := int(math.Round(float64(y+margin) * factor))
			y1 := int(math.Round(float64(y+margin+1) * factor))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			draw.Draw(img, image.Rect(x0, y0, x1, y1), dark, image.Point{}, draw.Src)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseColor 支持 #rgb/#rrggbb/#rrggbbaa、rgb()/rgba() 与常见 CSS 颜色名。
func parseColor(raw string) (color.RGBA, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return color.RGBA{}, errors.New("颜色为空")
	}
	if strings.HasPrefix(s, "#") {
		return parseHexColor(s[1:])
	}
	if strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba(") {
		return parseRGBFunc(s)
	}
	if c, ok := namedColors[s]; ok {
		return c, nil
	}
	return color.RGBA{}, errors.New("不支持的颜色")
}

func parseHexColor(hex string) (color.RGBA, error) {
	full := hex
	switch len(hex) {
	case 3, 4:
		var b strings.Builder
		for _, r := range hex {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		full = b.String()
	case 6, 8:
	default:
		return color.RGBA{}, errors.New("十六进制颜色长度不正确")
	}
	if len(full) == 6 {
		full += "ff"
	}
	v, err := strconv.ParseUint(full, 16, 64)
	if err != nil {
		return color.RGBA{}, errors.New("十六进制颜色解析失败")
	}
	a := uint8(v & 0xff)
	v >>= 8
	b := uint8(v & 0xff)
	v >>= 8
	g := uint8(v & 0xff)
	v >>= 8
	r := uint8(v & 0xff)
	return color.RGBA{R: r, G: g, B: b, A: a}, nil
}

func parseRGBFunc(s string) (color.RGBA, error) {
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < open {
		return color.RGBA{}, errors.New("rgb 颜色格式错误")
	}
	parts := strings.Split(s[open+1:closeIdx], ",")
	if len(parts) != 3 && len(parts) != 4 {
		return color.RGBA{}, errors.New("rgb 颜色分量数量错误")
	}
	var values [4]uint8
	values[3] = 255
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if i == 3 && strings.HasSuffix(part, "%") {
			pct, err := strconv.ParseFloat(strings.TrimSuffix(part, "%"), 64)
			if err != nil {
				return color.RGBA{}, errors.New("rgb 透明度解析失败")
			}
			values[3] = clampByte(pct / 100 * 255)
			continue
		}
		n, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return color.RGBA{}, errors.New("rgb 分量解析失败")
		}
		if i == 3 {
			values[3] = clampByte(n * 255)
			continue
		}
		values[i] = clampByte(n)
	}
	return color.RGBA{R: values[0], G: values[1], B: values[2], A: values[3]}, nil
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

var namedColors = map[string]color.RGBA{
	"black":       {R: 0, G: 0, B: 0, A: 255},
	"white":       {R: 255, G: 255, B: 255, A: 255},
	"red":         {R: 255, G: 0, B: 0, A: 255},
	"green":       {R: 0, G: 128, B: 0, A: 255},
	"blue":        {R: 0, G: 0, B: 255, A: 255},
	"yellow":      {R: 255, G: 255, B: 0, A: 255},
	"gray":        {R: 128, G: 128, B: 128, A: 255},
	"grey":        {R: 128, G: 128, B: 128, A: 255},
	"orange":      {R: 255, G: 165, B: 0, A: 255},
	"purple":      {R: 128, G: 0, B: 128, A: 255},
	"pink":        {R: 255, G: 192, B: 203, A: 255},
	"cyan":        {R: 0, G: 255, B: 255, A: 255},
	"magenta":     {R: 255, G: 0, B: 255, A: 255},
	"transparent": {R: 0, G: 0, B: 0, A: 0},
}

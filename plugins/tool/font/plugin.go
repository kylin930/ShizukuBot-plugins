// Package font 把任意文字渲染成图片，支持可选字体与抖动 GIF。
package font

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// canvasWidth 是渲染宽度，与上游 text.RenderToBase64 的 400 保持一致。
	canvasWidth = 400
	// jitterFrames 是抖动 GIF 的帧数，沿用上游 10 帧。
	jitterFrames = 10
	// frameDelay 是 GIF 每帧的延时，单位为 10ms。
	frameDelay = 5
	// maxImageBytes 与 sdk.ReplyImageBytes 的内联上传上限一致。
	maxImageBytes = 5 << 20
)

const (
	defaultFontStack      = `"PingFang SC", "Microsoft YaHei", sans-serif`
	syumatuFontStack      = `"Syumatu", "PingFang SC", "Microsoft YaHei", sans-serif`
	boldPingFangFontStack = `"PingFang SC Bold", "PingFang SC Semibold", "Microsoft YaHei", sans-serif`
)

// fontStacks 把上游 12 个字体名映射为 CSS 字体栈。
// 本框架不内置字体文件，渲染效果取决于本机已安装的字体；
// 未安装时由字体栈末尾的通用字体兜底，未知字体名回退默认字体栈。
var fontStacks = map[string]string{
	"用终末体":       syumatuFontStack,
	"用终末变体":      `"Nisi", "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用紫罗兰体":      `"Violet Evergarden", "PingFang SC", "Microsoft YaHei", serif`,
	"用樱酥体":       `"Sakura", "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用Consolas体": `Consolas, monospace`,
	"用粗苹方体":      boldPingFangFontStack,
	"用未来荧黑体":     `"Glow Sans", "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用Gugi体":     `Gugi, "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用八丸体":       `"Hachi Maru Pop", "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用Impact体":   `Impact, Haettenschweiler, sans-serif`,
	"用猫啃体":       `"Maoken", "PingFang SC", "Microsoft YaHei", sans-serif`,
	"用苹方体":       defaultFontStack,
}

// trigger 与上游正则保持一致：可选的「用X体」、可选「抖动」、正文可含换行。
var trigger = regexp.MustCompile(`^(用.+)?渲染(抖动)?文字([\s\S]+)$`)

// renderRequest 是一次触发解析后的渲染参数。
type renderRequest struct {
	fontStack string
	jitter    bool
	text      string
}

// Definition 订阅「(用X体)渲染(抖动)文字<内容>」。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "font",
		Name:        "文字渲染",
		Version:     "1.0.0",
		Description: "发送「(用X体)渲染(抖动)文字内容」，把任意文字渲染成图片或抖动 GIF",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "font", Trigger: "渲染文字"},
	})
}

// parseTrigger 解析「(用X体)渲染(抖动)?文字<内容>」。
// 第二个返回值表示是否命中；字体名未知时回退默认字体栈。
func parseTrigger(message string) (renderRequest, bool) {
	match := sdk.MatchRegexp(message, trigger)
	if len(match) < 4 {
		return renderRequest{}, false
	}
	return renderRequest{
		fontStack: fontStack(match[1]),
		jitter:    match[2] != "",
		text:      match[3],
	}, true
}

func fontStack(name string) string {
	if stack, ok := fontStacks[name]; ok {
		return stack
	}
	return defaultFontStack
}

// buildHTML 生成单帧 HTML。抖动模式下每个字符独立随机偏移，与上游逐字符
// DrawString 的偏移方式一致；非抖动模式直接输出转义后的正文。
func buildHTML(stack, text string, jitter bool) string {
	body := html.EscapeString(text)
	if jitter {
		body = jitterHTML(text)
	}
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;padding:0;background:#fff;}
body{width:%dpx;box-sizing:border-box;padding:20px;}
.text{font-family:%s;font-size:32px;line-height:1.4;color:#111;white-space:pre-wrap;word-break:break-word;}
</style></head><body><div class="text">%s</div></body></html>`,
		canvasWidth, stack, body)
}

// jitterHTML 把每个字符包进带随机位移的 span，抖动幅度与上游 rand.Intn(5) 一致。
func jitterHTML(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("<br>")
		}
		for _, r := range line {
			fmt.Fprintf(&b, `<span style="display:inline-block;transform:translate(%dpx,%dpx)">%s</span>`,
				rand.Intn(5), rand.Intn(5), html.EscapeString(string(r)))
		}
	}
	return b.String()
}

// Handle 渲染图片；渲染失败时回复纯文本提示，不向框架返回错误。
func Handle(ctx context.Context, c sdk.Call) error {
	request, ok := parseTrigger(sdk.Text(c.Event))
	if !ok {
		return nil
	}
	if !request.jitter {
		img, err := renderFrame(ctx, request)
		if err != nil {
			return c.Reply(ctx, "渲染失败："+err.Error())
		}
		return c.ReplyImageBytes(ctx, img)
	}
	frames := make([][]byte, 0, jitterFrames)
	for i := 0; i < jitterFrames; i++ {
		img, err := renderFrame(ctx, request)
		if err != nil {
			if len(frames) == 0 {
				return c.Reply(ctx, "渲染失败："+err.Error())
			}
			break
		}
		frames = append(frames, img)
	}
	if len(frames) == 1 {
		return c.ReplyImageBytes(ctx, frames[0])
	}
	// 合成 GIF；合成失败或超出内联上限时退化成第一帧静态图。
	if data, err := composeGIF(frames); err == nil && len(data) <= maxImageBytes {
		return c.ReplyImageBytes(ctx, data)
	}
	return c.ReplyImageBytes(ctx, frames[0])
}

func renderFrame(ctx context.Context, request renderRequest) ([]byte, error) {
	return sdk.RenderHTML(ctx, []byte(buildHTML(request.fontStack, request.text, request.jitter)), sdk.RenderOptions{
		Width:    canvasWidth,
		FullPage: true,
		Timeout:  30 * time.Second,
	})
}

// composeGIF 把多张截图合成为循环 GIF。各帧统一到首帧尺寸，
// 避免整页截图高度不一致导致 gif.EncodeAll 失败。
func composeGIF(frames [][]byte) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("没有可合成的帧")
	}
	decoded := make([]image.Image, 0, len(frames))
	for _, data := range frames {
		img, err := decodeFrame(data)
		if err != nil {
			return nil, err
		}
		decoded = append(decoded, img)
	}
	bounds := decoded[0].Bounds()
	animation := &gif.GIF{LoopCount: 0}
	for _, img := range decoded {
		frame := image.NewPaletted(bounds, palette.WebSafe)
		draw.FloydSteinberg.Draw(frame, bounds, img, img.Bounds().Min)
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, frameDelay)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, animation); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeFrame 解码截图。框架当前的整页截图输出 JPEG，
// 因此先按 PNG 解码，失败后再按 JPEG 解码。
func decodeFrame(data []byte) (image.Image, error) {
	if img, err := png.Decode(bytes.NewReader(data)); err == nil {
		return img, nil
	}
	return jpeg.Decode(bytes.NewReader(data))
}

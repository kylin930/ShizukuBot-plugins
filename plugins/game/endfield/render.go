package endfield

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"image/png"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	renderScale    = 1.3
	defaultTimeout = 30 * time.Second
)

var (
	cssOnce    sync.Once
	cssText    string
	renderHTML = func(ctx context.Context, html []byte, opts sdk.RenderOptions) ([]byte, error) {
		return sdk.RenderHTML(ctx, html, opts)
	}
)

func cardCSS() string {
	cssOnce.Do(func() {
		if data, err := asset("card.css"); err == nil {
			cssText = string(data)
		}
	})
	return cssText
}

// htmlEscape 转义用户可控文本。
func htmlEscape(text string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	).Replace(text)
}

type pageOptions struct {
	title    string
	eyebrow  string
	subtitle string
	seal     string
	tags     []string
	body     string
	width    int
}

// buildPage 生成统一风格的卡片 HTML。
func buildPage(o pageOptions) string {
	width := o.width
	if width <= 0 {
		width = 900
	}
	eyebrow := o.eyebrow
	if eyebrow == "" {
		eyebrow = "终末地协议终端"
	}
	seal := ""
	if o.seal != "" {
		seal = `<div class="seal">` + htmlEscape(o.seal) + `</div>`
	}
	tags := ""
	if len(o.tags) > 0 {
		var b strings.Builder
		b.WriteString(`<div class="tags">`)
		for _, tag := range o.tags {
			b.WriteString(`<span class="chip">` + htmlEscape(tag) + `</span>`)
		}
		b.WriteString(`</div>`)
		tags = b.String()
	}
	subtitle := ""
	if o.subtitle != "" {
		subtitle = `<p class="subtitle">` + o.subtitle + `</p>`
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh"><head><meta charset="UTF-8">
<meta name="viewport" content="width=%d, initial-scale=1.0">
<style>%s</style></head>
<body><main class="page">
<div class="head"><div><p class="eyebrow">%s</p><h1>%s</h1>%s%s</div>%s</div>
%s
<footer class="footer">%s</footer>
</main></body></html>`,
		width, cardCSS(), htmlEscape(eyebrow), htmlEscape(o.title), subtitle, tags, seal, o.body, copyrightText())
}

func copyrightText() string {
	return "Created By ShizukuBot &amp; Endfield-plugin &amp; by QingYingX(HInXe)"
}

// renderPNG 渲染 HTML 并转成 PNG。
func renderPNG(ctx context.Context, html string, width int) ([]byte, error) {
	if width <= 0 {
		width = 900
	}
	scaled := int(float64(width) * renderScale)
	data, err := renderHTML(ctx, []byte(html), sdk.RenderOptions{
		Width:    scaled,
		Height:   600,
		FullPage: true,
		Timeout:  defaultTimeout,
	})
	if err != nil {
		return nil, err
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// replyCard 发送渲染图；渲染或发送失败时回退纯文本。
func replyCard(ctx context.Context, c sdk.Call, html string, width int, fallback string) error {
	img, err := renderPNG(ctx, html, width)
	if err == nil {
		if err := c.ReplyImageBytes(ctx, img); err == nil {
			return nil
		}
	}
	return replyText(ctx, c, fallback)
}

func replyText(ctx context.Context, c sdk.Call, text string) error {
	return c.Reply(ctx, text)
}

// splitMessage 按长度切分，尽量在标点或换行处断开。
func splitMessage(content string, maxLength int) []string {
	if maxLength <= 0 {
		maxLength = 2000
	}
	var out []string
	for len(content) > maxLength {
		cut := maxLength
		window := content[:maxLength]
		if index := strings.LastIndexAny(window, "。！？\n"); index > maxLength/2 {
			cut = index + len([]rune(string(window[index])))
		}
		segment := strings.TrimSpace(content[:cut])
		if segment != "" {
			out = append(out, segment)
		}
		content = content[cut:]
	}
	if segment := strings.TrimSpace(content); segment != "" {
		out = append(out, segment)
	}
	return out
}

// assetDataURL 把资源图片转成 data URL。
func assetDataURL(path, mime string) string {
	data, err := asset(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func joinParagraphs(items ...string) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return strings.Join(out, "\n")
}

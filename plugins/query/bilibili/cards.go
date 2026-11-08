package bilibili

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	// cardWidth 成分/弹幕卡片的默认渲染宽度。
	cardWidth = 960
	// cardNarrowWidth 输出过大时的降级宽度。
	cardNarrowWidth = 760
	// cardMaxBytes 与 sdk.ReplyImageBytes 的 5MiB 上限保持一致。
	cardMaxBytes = 5 << 20
	// maxInlineImage 单张远程图片最大内嵌大小，超过则跳过。
	maxInlineImage = 256 << 10
)

// renderHTML 出图入口，测试中替换为固定 JPEG 字节的桩以避免启动浏览器。
var renderHTML = sdk.RenderHTML

// componentView 查成分卡片的展示数据。
type componentView struct {
	Name       string
	UID        int64
	Face       template.URL
	Fans       string
	Attention  string
	Level      string
	RegTime    string
	QueryTime  string
	Percent    string
	Hit        int
	Total      int
	Notices    []string
	Rows       []componentRow
	Truncated  bool
	SpaceURL   string
	MedalCount int
}

// componentRow 成分列表中的一行（VUP 或粉丝牌）。
type componentRow struct {
	Uname       string
	UID         int64
	VUP         bool
	MedalName   string
	MedalLevel  int64
	MedalStart  string
	MedalEnd    string
	MedalBorder string
	MedalIcon   template.URL
}

// danmakuView 查弹幕卡片的展示数据。
type danmakuView struct {
	Name      string
	UID       int64
	Face      template.URL
	Fans      string
	Attention string
	Page      string
	Link      string
	QueryTime string
	Notices   []string
	Lives     []danmakuLiveView
}

// danmakuLiveView 一场直播及其弹幕。
type danmakuLiveView struct {
	Title       string
	Anchor      string
	Face        template.URL
	RoomID      string
	Start       string
	End         string
	Duration    string
	Living      bool
	DanmakuNum  int
	WatchNum    int
	Income      string
	Items       []danmakuItemView
	HiddenCount int
}

// danmakuItemView 单条弹幕的展示字段。
type danmakuItemView struct {
	Clock   string
	UName   string
	Type    string
	Message string
	Price   string
}

// replyRendered 将 HTML 渲染为图片发送，渲染失败或超出 5MiB 时回退纯文本。
// 首次输出过大时会降低宽度重试一次。
func replyRendered(ctx context.Context, c sdk.Call, build func(width int) ([]byte, error), fallback string) error {
	for _, width := range []int{cardWidth, cardNarrowWidth} {
		html, err := build(width)
		if err != nil {
			break
		}
		data, err := renderHTML(ctx, html, sdk.RenderOptions{
			Width:    width,
			FullPage: true,
			Timeout:  60 * time.Second,
		})
		if err != nil || len(data) == 0 {
			break
		}
		if len(data) > cardMaxBytes {
			continue
		}
		if err := c.ReplyImageBytes(ctx, data); err != nil {
			break
		}
		return nil
	}
	return c.Reply(ctx, fallback)
}

// safeURL 标记已由插件生成的 data URI，避免 html/template 将其当作不安全 URL。
func safeURL(s string) template.URL {
	return template.URL(s)
}

// inlineImage 下载远程图片并转成 data URI；失败或超限时返回空字符串，不阻塞卡片。
func inlineImage(ctx context.Context, rawURL string) string {
	rawURL = normalizeImageURL(rawURL)
	if rawURL == "" {
		return ""
	}
	body, err := getImage(ctx, rawURL)
	if err != nil || len(body) == 0 || len(body) > maxInlineImage {
		return ""
	}
	mime := http.DetectContentType(body)
	if !strings.HasPrefix(mime, "image/") {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body)
}

// normalizeImageURL 把 B 站常见的 // 与 http 图片地址规范为 https；本机地址保持原样便于测试。
func normalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		u.Scheme = "https"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

// isLoopbackHost 判断是否为本地测试地址。
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// getImage 下载图片，限制单图大小。
func getImage(ctx context.Context, rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := biliClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("图片返回 %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxInlineImage+1))
}

// medalColor 把 B 站牌子颜色整数转为 #rrggbb，与上游 int2rbg 一致。
func medalColor(n int64) string {
	r := n & 0xff
	g := (n >> 8) & 0xff
	b := (n >> 16) & 0xff
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// danmakuTypeLabel 返回弹幕类型的中文名称。
func danmakuTypeLabel(t int64) string {
	if label, ok := danmakuTypeMap[t]; ok {
		return label
	}
	return fmt.Sprintf("未知类型%d", t)
}

// danmakuTypeClass 返回弹幕类型对应的 CSS 类名。
func danmakuTypeClass(t int64) string {
	switch t {
	case 1:
		return "gift"
	case 2:
		return "guard"
	case 3:
		return "sc"
	case 4, 5, 6, 7, 8:
		return "notice"
	default:
		return "normal"
	}
}

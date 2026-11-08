// Package animetrace AnimeTrace 动画/Galgame 识图：上传图片识别作品与角色。
package animetrace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 以下地址与模型名均为包级变量，测试可替换。
var (
	// searchURL 与上游 api.animetrace.com 一致。
	searchURL = "https://api.animetrace.com/v1/search"
	// animeModel 动漫识图的默认模型。
	animeModel = "anime_model_lovelive"
	// galModel Gal 识图的默认模型。
	galModel = "full_game_model_kira"
	// stableModel 「动漫识图 2」使用的模型。
	stableModel = "pre_stable"

	client = &http.Client{Timeout: 10 * time.Second}

	// triggerPattern 匹配「动漫识图」「Gal识图」及后续参数，兼容大小写。
	triggerPattern = regexp.MustCompile(`(?is)^(动漫识图|gal识图)(.*)$`)
	// urlPattern 从指令参数中提取图片地址。
	urlPattern = regexp.MustCompile(`https?://[^\s]+`)
)

const (
	// maxImageBytes 图片下载上限 5 MiB。
	maxImageBytes = 5 << 20
	// maxResults 最多展示的识别结果条数，上游展示全部，这里给一个防刷屏的上限。
	maxResults = 20
	// jpegQuality 裁剪图编码质量。
	jpegQuality = 85
)

// apiResponse 是 AnimeTrace v1/search 的响应结构。
type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"zh_message"`
	Data    []apiResult `json:"data"`
}

type apiResult struct {
	Box       []float64      `json:"box"`
	Character []apiCharacter `json:"character"`
}

type apiCharacter struct {
	Work       string          `json:"work"`
	Character  string          `json:"character"`
	Confidence float64         `json:"confidence"`
	Time       json.RawMessage `json:"time"`
}

// Definition 订阅消息事件，处理「动漫识图」「Gal识图」指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "animetrace",
		Name:        "AnimeTrace 识图",
		Version:     "1.0.0",
		Description: "发送「动漫识图」或「Gal识图」并附带图片，识别动画/Galgame 作品与角色",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "animetrace", Trigger: "动漫识图"},
	})
}

// Handle 解析指令与图片，调用上游识别，逐条发送裁剪图与文本结果。
func Handle(ctx context.Context, c sdk.Call) error {
	m := sdk.MatchRegexp(sdk.Text(c.Event), triggerPattern)
	if m == nil {
		return nil
	}
	gal := strings.EqualFold(m[1], "gal识图")
	model := animeModel
	if gal {
		model = galModel
	}

	args := strings.TrimSpace(m[2])
	imageURL := ""
	if u := urlPattern.FindString(args); u != "" {
		imageURL = u
		args = strings.TrimSpace(strings.Replace(args, u, "", 1))
	}
	if args != "" {
		if !gal && args == "2" {
			model = stableModel
		} else {
			model = args
		}
	}

	if imageURL == "" {
		urls := sdk.ImageURLs(c.Event)
		if len(urls) == 0 {
			return c.Reply(ctx, usage())
		}
		imageURL = urls[0]
	}

	data, contentType, err := fetchImage(ctx, imageURL)
	if err != nil {
		return c.Reply(ctx, "下载图片失败: "+err.Error())
	}
	result, err := recognize(ctx, data, contentType, model)
	if err != nil {
		return c.Reply(ctx, "识别请求失败: "+err.Error())
	}
	if result.Code != 0 {
		msg := strings.TrimSpace(result.Message)
		if msg == "" {
			msg = "错误码 " + strconv.Itoa(result.Code)
		}
		return c.Reply(ctx, "识别失败: "+msg)
	}
	if len(result.Data) == 0 {
		return c.Reply(ctx, "未识别到任何角色")
	}

	if err := c.Reply(ctx, formatHeader(len(result.Data))); err != nil {
		return err
	}
	// 框架没有群转发能力，改为依次发送「裁剪图 + 文本」。
	src := decodeImage(data)
	sentOriginal := false
	shown := 0
	for _, item := range result.Data {
		if shown >= maxResults {
			break
		}
		shown++
		replyCroppedImage(ctx, c, src, item.Box, data, &sentOriginal)
		if err := c.Reply(ctx, formatItem(shown, item)); err != nil {
			return err
		}
	}
	if len(result.Data) > shown {
		return c.Reply(ctx, fmt.Sprintf("……另有 %d 条结果未展示", len(result.Data)-shown))
	}
	return nil
}

// usage 返回未带图片时的用法提示。
func usage() string {
	return "用法：发送「动漫识图」或「Gal识图」并附带一张图片。\n" +
		"也可以把图片链接写在指令里，例如「动漫识图 https://example.com/a.jpg」。\n" +
		"动漫识图默认模型 anime_model_lovelive，发送「动漫识图 2」使用 pre_stable；\n" +
		"Gal识图默认模型 full_game_model_kira；两者都支持在指令后追加自定义模型名。"
}

// fetchImage 下载待识别图片，限制大小并校验状态码。
func fetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 ShizukuBot/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("图片地址返回状态 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("图片内容为空")
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("图片超过 5 MiB")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

// recognize 以 multipart 表单上传图片并解析识别结果。
func recognize(ctx context.Context, data []byte, contentType, model string) (apiResponse, error) {
	var result apiResponse
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "image"+extFor(contentType))
	if err != nil {
		return result, err
	}
	if _, err = part.Write(data); err != nil {
		return result, err
	}
	for _, field := range [][2]string{
		{"is_multi", "0"},
		{"model", model},
		{"ai_detect", "0"},
	} {
		if err = writer.WriteField(field[0], field[1]); err != nil {
			return result, err
		}
	}
	if err = writer.Close(); err != nil {
		return result, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, &buf)
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "Mozilla/5.0 ShizukuBot/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	return result, nil
}

// extFor 按图片类型给出上传文件名后缀。
func extFor(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/png"):
		return ".png"
	case strings.HasPrefix(contentType, "image/gif"):
		return ".gif"
	case strings.HasPrefix(contentType, "image/webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}

// decodeImage 解码下载到的图片，仅支持标准库 JPEG/PNG/GIF。
func decodeImage(data []byte) image.Image {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// cropRegion 把归一化坐标 [x1,y1,x2,y2] 换算成像素矩形并裁剪。
func cropRegion(src image.Image, box []float64) (image.Image, bool) {
	if src == nil || len(box) != 4 {
		return nil, false
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, false
	}
	rect := image.Rect(
		bounds.Min.X+int(box[0]*float64(width)),
		bounds.Min.Y+int(box[1]*float64(height)),
		bounds.Min.X+int(box[2]*float64(width)),
		bounds.Min.Y+int(box[3]*float64(height)),
	).Intersect(bounds)
	if rect.Empty() {
		return nil, false
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(out, out.Bounds(), src, rect.Min, draw.Src)
	return out, true
}

// replyCroppedImage 发送裁剪图；裁剪失败时退回原图一次，仍失败则跳过图片。
func replyCroppedImage(ctx context.Context, c sdk.Call, src image.Image, box []float64, original []byte, sentOriginal *bool) {
	if region, ok := cropRegion(src, box); ok {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, region, &jpeg.Options{Quality: jpegQuality}); err == nil {
			if err := c.ReplyImageBytes(ctx, buf.Bytes()); err == nil {
				return
			}
		}
	}
	if *sentOriginal || len(original) == 0 || len(original) > maxImageBytes {
		return
	}
	if err := c.ReplyImageBytes(ctx, original); err == nil {
		*sentOriginal = true
	}
}

// formatHeader 返回发送裁剪图之前的提示与摘要。
func formatHeader(count int) string {
	return fmt.Sprintf("少女祈祷中……\n共识别到 %d 个角色，可能是以下来源：", count)
}

// formatItem 把单条识别结果转成文本。
func formatItem(index int, item apiResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d.", index)
	if len(item.Character) == 0 {
		b.WriteString("\n   未能识别出具体角色")
		return b.String()
	}
	for _, ch := range item.Character {
		line := "\n   "
		if ch.Work != "" {
			line += "《" + ch.Work + "》"
		}
		if ch.Character != "" {
			if ch.Work != "" {
				line += "的角色 "
			}
			line += ch.Character
		}
		if line == "\n   " {
			continue
		}
		var extras []string
		if conf := formatConfidence(ch.Confidence); conf != "" {
			extras = append(extras, conf)
		}
		if t := formatTime(ch.Time); t != "" {
			extras = append(extras, "时间点 "+t)
		}
		if len(extras) > 0 {
			line += "（" + strings.Join(extras, "，") + "）"
		}
		b.WriteString(line)
	}
	return b.String()
}

// formatConfidence 把 0~1 或 0~100 的相似度统一成百分比。
func formatConfidence(v float64) string {
	if v <= 0 {
		return ""
	}
	if v <= 1 {
		v *= 100
	}
	return fmt.Sprintf("相似度 %.1f%%", v)
}

// formatTime 兼容字符串与数字形式的时间点字段。
func formatTime(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return ""
}

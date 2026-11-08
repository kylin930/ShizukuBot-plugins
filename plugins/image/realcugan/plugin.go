// Package realcugan Real-CUGAN 清晰术：调用 HuggingFace 上的 Real-CUGAN
// 推理接口放大图片并去噪。移植自 zerobot-plugin/plugin/realcugan。
package realcugan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

var (
	// predictURL 为包级变量，测试中替换为 httptest 服务地址。
	predictURL = "https://shichen1231-real-cugan.hf.space/run/predict"
	// httpClient 供图片下载与推理请求共用。
	httpClient = &http.Client{Timeout: 60 * time.Second}
)

const (
	// pixelLimit 是启用三重/四重吟唱的像素上限。
	pixelLimit = 400000
	// maxDownloadBytes 是单张输入图片的大小上限。
	maxDownloadBytes = 10 << 20
	// maxResponseBytes 限制推理响应体大小。
	maxResponseBytes = 32 << 20
)

// Definition 订阅「清晰术」指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "realcugan",
		Name:        "Real-CUGAN 清晰术",
		Version:     "1.0.0",
		Description: "发送「清晰术(双重吟唱|三重吟唱|四重吟唱)(强力术式|中等术式|弱术式|不变式|原式)」并附带图片，放大并去噪",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "realcugan", Trigger: "清晰术"}})
}

// Handle 对消息附带的每张图片执行清晰术，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if !strings.HasPrefix(text, "清晰术") {
		return nil
	}
	urls := sdk.ImageURLs(c.Event)
	if len(urls) == 0 {
		return nil
	}
	if err := c.Reply(ctx, "少女祈祷中..."); err != nil {
		return err
	}
	for _, u := range urls {
		data, err := downloadImage(ctx, u)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		scale, con := chooseParams(text, img.Bounds().Dx(), img.Bounds().Dy())
		out, err := predict(ctx, data, scale, con)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		// 对齐上游：说明文字与图片在同一消息里且文字在前。
		if err := c.Reply(ctx, fmt.Sprintf("%d重唱%s分支大清晰术!", scale, con)); err != nil {
			return err
		}
		if err := c.ReplyImageBytes(ctx, out); err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
	}
	return nil
}

// chooseParams 按上游规则选择放大倍数与去噪模型。
func chooseParams(fashu string, width, height int) (int, string) {
	scale := 2
	pixels := width * height
	switch {
	case strings.Contains(fashu, "双重吟唱"):
		scale = 2
	case strings.Contains(fashu, "三重吟唱") && pixels < pixelLimit:
		scale = 3
	case strings.Contains(fashu, "四重吟唱") && pixels < pixelLimit:
		scale = 4
	}

	con := "conservative"
	switch {
	case strings.Contains(fashu, "强力术式"):
		con = "denoise3x"
	case strings.Contains(fashu, "中等术式"):
		con = "no-denoise"
		if scale == 2 {
			con = "denoise2x"
		}
	case strings.Contains(fashu, "弱术式"):
		con = "no-denoise"
		if scale == 2 {
			con = "denoise1x"
		}
	case strings.Contains(fashu, "不变式"):
		con = "no-denoise"
	case strings.Contains(fashu, "原式"):
		con = "conservative"
	}
	return scale, con
}

// modelName 生成上游使用的模型文件名。
func modelName(scale int, con string) string {
	return fmt.Sprintf("up%dx-latest-%s.pth", scale, con)
}

// downloadImage 下载待处理图片并校验大小。
func downloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("图片地址返回状态 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("图片内容为空")
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("图片超过 10 MiB")
	}
	return data, nil
}

// predictRequest 是 HuggingFace gradio 推理接口的请求体。
type predictRequest struct {
	Data []any `json:"data"`
}

// predictResponse 是推理接口的响应体，data[0] 为带前缀的图片 base64。
type predictResponse struct {
	Data []string `json:"data"`
}

// predict 提交图片并解码返回的图片字节。
func predict(ctx context.Context, img []byte, scale int, con string) ([]byte, error) {
	payload, err := json.Marshal(predictRequest{Data: []any{
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img),
		modelName(scale, con),
		2,
	}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, predictURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("推理接口返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	var result predictResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("推理接口没有返回结果")
	}
	encoded := strings.TrimPrefix(result.Data[0], "data:image/png;base64,")
	out, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("推理结果图片为空")
	}
	return out, nil
}

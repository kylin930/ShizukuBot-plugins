package patina

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxImageBytes = 32 << 20

var imageHTTPClient = &http.Client{Timeout: 30 * time.Second}

// downloadImage 下载图片，测试时替换。
var downloadImage = func(ctx context.Context, url string) ([]byte, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := imageHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, errors.New("图片内容为空")
		}
		return data, nil
	}
	return nil, fmt.Errorf("不支持的图片地址: %s", url)
}

type preparedImage struct {
	mime string
	data []byte
}

func (p preparedImage) dataURL() string {
	return dataURL(p.mime, p.data)
}

func (p preparedImage) filename() string {
	switch p.mime {
	case "image/jpeg":
		return "input.jpg"
	case "image/gif":
		return "input.gif"
	case "image/webp":
		return "input.webp"
	case "image/bmp":
		return "input.bmp"
	case "image/apng":
		return "input.apng"
	}
	return "input.png"
}

// prepareStaticImage 下载图片；GIF 取第一帧转 PNG，其余保持原格式。
func prepareStaticImage(ctx context.Context, url string) (preparedImage, error) {
	data, err := downloadImage(ctx, url)
	if err != nil {
		return preparedImage{}, err
	}
	mime := detectImageMime(data)
	if mime == "image/gif" {
		frame, err := gif.Decode(bytes.NewReader(data))
		if err != nil {
			return preparedImage{}, err
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, frame); err != nil {
			return preparedImage{}, err
		}
		return preparedImage{mime: "image/png", data: buf.Bytes()}, nil
	}
	return preparedImage{mime: mime, data: data}, nil
}

func detectImageMime(data []byte) string {
	switch {
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 2 && data[0] == 'B' && data[1] == 'M':
		return "image/bmp"
	}
	return "image/png"
}

func dataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func decodeDataURL(value string) ([]byte, error) {
	idx := strings.Index(value, ",")
	if idx < 0 || !strings.HasPrefix(value, "data:") {
		return nil, errors.New("无效的 data URL")
	}
	return base64.StdEncoding.DecodeString(value[idx+1:])
}

// decodeImage 解码静态图片（GIF 取第一帧）。
func decodeImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return img, nil
}

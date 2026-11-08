package wife

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxWifeImageBytes = 16 << 20

// wifeBases 依次尝试：jsDelivr、GitHub raw、gitea 镜像。
var wifeBases = []string{
	"https://cdn.jsdelivr.net/gh/FloatTech/zbpwife@main/wives/",
	"https://raw.githubusercontent.com/FloatTech/zbpwife/main/wives/",
	"https://gitea.seku.su/fumiama/zbpwife/raw/branch/main/wives/",
}

var (
	httpClient   = &http.Client{Timeout: 60 * time.Second}
	wifeCacheDir = defaultCacheDir()
	// downloadWifeImage 便于单测替换。
	downloadWifeImage = defaultDownloadImage
)

func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ShizukuBot", "wife", "wives")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func wifeURLs(card string) []string {
	escaped := url.PathEscape(card)
	out := make([]string, 0, len(wifeBases))
	for _, base := range wifeBases {
		out = append(out, base+escaped)
	}
	return out
}

// wifeImage 返回图片字节与首个可用 URL；优先读缓存。
func wifeImage(ctx context.Context, card string) ([]byte, string, error) {
	urls := wifeURLs(card)
	cachePath := filepath.Join(wifeCacheDir, cacheName(card))
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		return data, urls[0], nil
	}
	var lastErr error
	for _, imageURL := range urls {
		data, err := downloadWifeImage(ctx, imageURL)
		if err != nil || len(data) == 0 {
			lastErr = err
			continue
		}
		if len(data) > maxWifeImageBytes {
			return nil, imageURL, fmt.Errorf("图片超过 16 MiB")
		}
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			lastErr = fmt.Errorf("返回内容不是图片")
			continue
		}
		_ = os.WriteFile(cachePath, data, 0o644)
		return data, imageURL, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("图片下载失败")
	}
	return nil, urls[0], lastErr
}

func cacheName(card string) string {
	sum := sha1.Sum([]byte(card))
	return hex.EncodeToString(sum[:]) + ".img"
}

func defaultDownloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 ShizukuBot")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxWifeImageBytes+1))
}

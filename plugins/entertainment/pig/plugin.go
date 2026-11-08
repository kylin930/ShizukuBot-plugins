// Package pig 猪猪表情包：随机、搜索与按 ID 精确查找。
// 行为对齐上游 zerobot-plugin/plugin/pig，数据来自 zbpdata。
package pig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 数据与资源地址均为包级变量，测试可替换。
var (
	pigDataURL   = "https://cdn.jsdelivr.net/gh/FloatTech/zbpdata@latest/Pig/pig_data.json"
	pigAssetBase = "https://cdn.jsdelivr.net/gh/FloatTech/zbpdata@latest/Pig/assets/"
)

var (
	client   = &http.Client{Timeout: 15 * time.Second}
	nowFunc  = time.Now
	randIntn = rand.Intn
)

var (
	randomRe  = regexp.MustCompile(`^(随机猪猪|来份猪猪|抽个猪猪)$`)
	searchRe  = regexp.MustCompile(`^搜索猪猪\s+(.+)$`)
	pigIDRe   = regexp.MustCompile(`^猪猪id\s+(\d+)$`)
	pigMu     sync.Mutex
	pigCache  []pigImage
	pigMap    = make(map[string]*pigImage)
	lastFetch time.Time
)

const (
	// cacheTTL 数据缓存有效期。
	cacheTTL = 24 * time.Hour
	// maxImageBytes 图片下载上限，需与 ReplyImageBytes 的 5 MiB 限制一致。
	maxImageBytes = 5 << 20
	// maxDataBytes 数据文件下载上限。
	maxDataBytes = 4 << 20
	// maxShow 搜索结果最多展示的条数。
	maxShow = 10
)

type pigResponse struct {
	Total  int        `json:"total"`
	Images []pigImage `json:"images"`
}

type pigImage struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
}

// Definition 订阅聊天消息，处理随机、搜索与按 ID 查找指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "pig",
		Name:        "来份猪猪",
		Version:     "1.0.0",
		Description: "随机猪猪、搜索猪猪与猪猪id精确查找，发送猪猪表情包",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "pig", Trigger: "随机猪猪"},
	})
}

// Handle 分发三个猪猪指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchRegexp(text, randomRe) != nil {
		return handleRandom(ctx, c)
	}
	if m := sdk.MatchRegexp(text, searchRe); m != nil {
		return handleSearch(ctx, c, strings.TrimSpace(m[1]))
	}
	if m := sdk.MatchRegexp(text, pigIDRe); m != nil {
		return handleID(ctx, c, m[1])
	}
	return nil
}

// handleRandom 随机发送一只猪猪。
func handleRandom(ctx context.Context, c sdk.Call) error {
	if err := checkAndUpdateData(ctx); err != nil {
		return c.Reply(ctx, "[Pig] ERROR: "+err.Error()+"\nEXP: 随机猪猪失败，获取数据错误")
	}

	pigMu.Lock()
	if len(pigCache) == 0 {
		pigMu.Unlock()
		return c.Reply(ctx, "[Pig] ERROR: 暂无猪猪数据，请联系管理员")
	}
	target := pigCache[randIntn(len(pigCache))]
	pigMu.Unlock()

	imgData, err := target.fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "[Pig] ERROR: "+err.Error()+"\nEXP: 图片加载失败")
	}
	if err := c.Reply(ctx, fmt.Sprintf("🐷 ID: %s | %s", target.ID, target.Title)); err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, imgData)
}

// handleSearch 按标题包含关键词过滤，最多列出 10 条并返回第一只猪猪。
func handleSearch(ctx context.Context, c sdk.Call, keyword string) error {
	if err := checkAndUpdateData(ctx); err != nil {
		return c.Reply(ctx, "[Pig] ERROR: "+err.Error()+"\nEXP: 搜索猪猪失败，获取数据错误")
	}

	pigMu.Lock()
	var results []pigImage
	for _, p := range pigCache {
		if strings.Contains(p.Title, keyword) {
			results = append(results, p)
		}
	}
	pigMu.Unlock()

	if len(results) == 0 {
		return c.Reply(ctx, "[Pig] ERROR: 未找到包含“"+keyword+"”的猪猪")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔎 根据关键词“%s”找到 %d 只猪猪：\n", keyword, len(results)))
	for i, p := range results {
		if i >= maxShow {
			sb.WriteString(fmt.Sprintf("\n...等共 %d 条结果", len(results)))
			break
		}
		sb.WriteString(fmt.Sprintf("%d: %s (ID: %s)\n", i+1, p.Title, p.ID))
	}
	sb.WriteString("\n为您返回第一个猪猪：\n💡 提示：输入“猪猪id [id]”可精确获取")

	imgData, err := results[0].fetch(ctx)
	if err != nil {
		return c.Reply(ctx, sb.String()+"\n\n[Pig] ERROR: "+err.Error()+"\nEXP: 图片加载失败")
	}
	if err := c.Reply(ctx, sb.String()); err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, imgData)
}

// handleID 按 ID 精确查找。
func handleID(ctx context.Context, c sdk.Call, targetID string) error {
	if err := checkAndUpdateData(ctx); err != nil {
		return c.Reply(ctx, "[Pig] ERROR: "+err.Error()+"\nEXP: 精确查找失败，获取数据错误")
	}

	pigMu.Lock()
	target, exists := pigMap[targetID]
	pigMu.Unlock()
	if !exists {
		return c.Reply(ctx, "[Pig] ERROR: 未找到 ID 为 "+targetID+" 的猪猪")
	}

	imgData, err := target.fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "[Pig] ERROR: "+err.Error()+"\nEXP: 图片加载失败")
	}
	if err := c.Reply(ctx, fmt.Sprintf("🐷 ID: %s | %s", target.ID, target.Title)); err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, imgData)
}

// checkAndUpdateData 在首次使用或缓存超过 24 小时时拉取并解析数据。
func checkAndUpdateData(ctx context.Context) error {
	pigMu.Lock()
	defer pigMu.Unlock()

	if len(pigCache) > 0 && nowFunc().Sub(lastFetch) < cacheTTL {
		return nil
	}

	dataBytes, err := download(ctx, pigDataURL, maxDataBytes)
	if err != nil {
		return errors.New("读取数据文件失败: " + err.Error())
	}

	var data pigResponse
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return errors.New("解析JSON失败: " + err.Error())
	}
	if len(data.Images) == 0 {
		return errors.New("数据文件为空")
	}

	pigCache = data.Images
	newMap := make(map[string]*pigImage, len(pigCache))
	for i := range pigCache {
		newMap[pigCache[i].ID] = &pigCache[i]
	}
	pigMap = newMap
	lastFetch = nowFunc()
	return nil
}

// fetch 下载当前猪猪的图片。
func (img pigImage) fetch(ctx context.Context) ([]byte, error) {
	if img.Filename == "" {
		return nil, errors.New("图片数据异常，缺少文件名")
	}
	targetPath := "assets/" + img.Filename
	imgData, err := download(ctx, pigAssetBase+img.Filename, maxImageBytes)
	if err != nil {
		return nil, errors.New("图片资源缺失 (" + targetPath + "): " + err.Error())
	}
	return imgData, nil
}

// download 带超时地拉取远端内容，限制响应体大小。
func download(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("响应内容为空")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("响应内容超过大小限制")
	}
	return data, nil
}

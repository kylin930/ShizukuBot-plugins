// Package cnbimagebed 移植上游 cnb-image-bed 插件：把图片上传到 CNB 图床，
// 提供 md5 缓存、上传统计、失败原因汇总与过期缓存清理。
//
// 上游通过覆盖 Bot.imageToUrl 暴露上传能力；宿主没有该钩子，因此这里额外提供
// 「图床上传」指令执行同样的上传流程，其余状态/失败记录指令与上游保持一致。
package cnbimagebed

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/events"
	"shizukubot/sdk"
)

const (
	pluginVersion    = "1.0.0"
	defaultBaseURL   = "https://api.cnb.cool"
	defaultLocalBase = "http://localhost"
	imageCDN         = "https://cnb.cool"
	dayMillis        = int64(24 * 60 * 60 * 1000)
	pendingTimeout   = 30 * time.Second
	noPermission     = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"
	stateCacheKey    = "image_cache"
	stateStatsKey    = "upload_stats"
	stateCleanupKey  = "last_cleanup"
	commandUpload    = "图床上传"
)

var (
	// imageNamePattern 判断文件名是否按图片走 imgs 上传通道。
	imageNamePattern = regexp.MustCompile(`(?i)\.(jpg|jpeg|png|gif|bmp)$`)
	httpClient       = &http.Client{Timeout: 30 * time.Second}
	// scheduleDelete 便于测试观察自动删除计划。
	scheduleDelete = func(delay time.Duration, path string, cfg config) {
		time.AfterFunc(delay, func() {
			_ = deleteFile(path, cfg)
		})
	}
	// now 便于测试固定时间。
	now = time.Now
	// pendings 保存「图床上传」后等待图片的会话。
	pendings sync.Map
)

type config struct {
	BaseURL         string
	LocalBaseURL    string
	Token           string
	DefaultRepo     string
	AutoDelete      int
	Stats           bool
	RecordURL       bool
	CacheExpireDays int
	StatsMaxDays    int
	AdminUsers      []string
}

func defaultConfig() config {
	return config{
		BaseURL:         defaultBaseURL,
		LocalBaseURL:    defaultLocalBase,
		AutoDelete:      30,
		Stats:           true,
		RecordURL:       true,
		CacheExpireDays: 7,
		StatsMaxDays:    30,
	}
}

// Definition 注册 cnb 图床插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "cnb-image-bed",
		Name:        "cnb图床状态",
		Version:     pluginVersion,
		Description: "上传图片到 CNB 图床，并提供图床状态与失败原因统计",
	}, []sdk.EventType{
		sdk.GroupMessage, sdk.GroupAtMessage, sdk.C2CMessage,
		sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage,
	}, Handle, []triggercmd.Command{
		{Name: "cnb-image-bed", Trigger: "图床上传"},
	},
		sdk.WithConfig(configSchema()),
		sdk.WithPermissions("state.read", "state.write"),
	)
}

func f64(v float64) *float64 { return &v }

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"baseUrl": {
				Type:        "string",
				Description: "CNB API 地址",
				Default:     json.RawMessage(`"` + defaultBaseURL + `"`),
			},
			"localBaseUrl": {
				Type:        "string",
				Description: "上传失败时回退本地 URL 的基地址（实际地址为 <基地址>/File/<文件名>）",
				Default:     json.RawMessage(`"` + defaultLocalBase + `"`),
			},
			"token": {
				Type:        "string",
				Description: "CNB 访问令牌",
				Default:     json.RawMessage(`""`),
			},
			"defaultRepo": {
				Type:        "string",
				Description: "默认仓库，例如 group/repo",
				Default:     json.RawMessage(`""`),
			},
			"autodelete": {
				Type:        "integer",
				Description: "未记录链接时上传后自动删除的秒数",
				Default:     json.RawMessage(`30`),
				Minimum:     f64(1),
			},
			"stats": {
				Type:        "boolean",
				Description: "是否记录上传统计",
				Default:     json.RawMessage(`true`),
			},
			"recordurl": {
				Type:        "boolean",
				Description: "是否记录图片链接并按 md5 复用缓存",
				Default:     json.RawMessage(`true`),
			},
			"cache_expire_days": {
				Type:        "integer",
				Description: "缓存图片多少天未使用后清理",
				Default:     json.RawMessage(`7`),
				Minimum:     f64(1),
			},
			"stats_max_days": {
				Type:        "integer",
				Description: "上传统计数据保留天数",
				Default:     json.RawMessage(`30`),
				Minimum:     f64(1),
			},
			"admin_users": {
				Type:        "array",
				Description: "允许查看图床状态/失败记录的 member_openid 列表",
				Items:       &sdk.Schema{Type: "string"},
				Default:     json.RawMessage(`[]`),
			},
		},
	}
}

// Handle 处理图床状态、失败记录与图片上传。
func Handle(ctx context.Context, c sdk.Call) error {
	if !qqSource(c.Event.Source) {
		return nil
	}
	cfg := decodeConfig(c.Config)
	text := strings.TrimSpace(sdk.Text(c.Event))

	if rest, ok := matchKeyword(text, "图床状态"); ok {
		return handleStats(ctx, c, cfg, rest)
	}
	if rest, ok := matchKeyword(text, "图床失败记录"); ok {
		return handleFailReasons(ctx, c, cfg, rest)
	}
	if rest, ok := matchKeyword(text, "图床失败原因"); ok {
		return handleFailReasons(ctx, c, cfg, rest)
	}
	if _, ok := matchKeyword(text, commandUpload); ok {
		return handleUpload(ctx, c, cfg)
	}
	if takePending(pendingKey(c.Event)) {
		return handleUpload(ctx, c, cfg)
	}
	return nil
}

func qqSource(s events.Source) bool {
	switch s {
	case events.Group, events.C2C, events.Guild, events.Direct:
		return true
	}
	return false
}

func handleStats(ctx context.Context, c sdk.Call, cfg config, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	days := parseDays(rest, cfg.StatsMaxDays)
	stats := collectStats(ctx, c, cfg, days)
	return replyWithButtons(ctx, c, buildStatsText(stats, cfg.StatsMaxDays))
}

func handleFailReasons(ctx context.Context, c sdk.Call, cfg config, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	days := parseDays(rest, cfg.StatsMaxDays)
	records := loadStats(ctx, c, cfg.StatsMaxDays)
	minTS := now().UnixMilli() - int64(days)*dayMillis
	total, reasons := topFailReasons(records, minTS)
	if total == 0 {
		return replyWithButtons(ctx, c, fmt.Sprintf("📉 近 %d 天没有记录到任何上传失败", days))
	}

	msg := "\r#❌图床失败记录\r"
	msg += fmt.Sprintf("\r> 统计区间：**%d 天**\r", days)
	msg += fmt.Sprintf("\r> 失败总数：**%d 次**", total)
	msg += "\r\r| 失败原因 | 次数 |"
	msg += "\r|---|---|"
	for _, item := range reasons {
		msg += fmt.Sprintf("\r| %s | %d |", truncateReason(item.Reason), item.Count)
	}
	return replyWithButtons(ctx, c, msg)
}

// handleUpload 等价于上游的 imageToUrl：上传当前消息附带的图片。
func handleUpload(ctx context.Context, c sdk.Call, cfg config) error {
	urls := sdk.ImageURLs(c.Event)
	if len(urls) == 0 {
		putPending(pendingKey(c.Event))
		return c.Reply(ctx, "请发送图片：")
	}
	cleanupExpired(ctx, c, cfg)

	lines := make([]string, 0, len(urls))
	for _, raw := range urls {
		data, err := fetchBytes(raw)
		if err != nil {
			lines = append(lines, "上传失败："+err.Error())
			continue
		}
		link, err := uploadFile(ctx, c, cfg, buildFileInfo(data))
		if err != nil {
			lines = append(lines, "上传失败："+err.Error())
			continue
		}
		lines = append(lines, link)
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

func replyWithButtons(ctx context.Context, c sdk.Call, text string) error {
	keyboard := sdk.NewKeyboard(
		sdk.NewRow(
			sdk.CommandButton("stat", "图床状态", "图床状态"),
			sdk.CommandButton("stat7", "图床状态7天", "图床状态7天"),
		),
		sdk.NewRow(
			sdk.CommandButton("stat30", "图床状态30天", "图床状态30天"),
			sdk.CommandButton("fail30", "图床失败原因30天", "图床失败原因30天"),
		),
	)
	return c.ReplyKeyboardOrText(ctx, text, keyboard)
}

// matchKeyword 支持上游的 ^#?(cnb)?关键词 前缀形式。
func matchKeyword(text, keyword string) (string, bool) {
	for _, prefix := range []string{"#cnb", "cnb", "#", ""} {
		if strings.HasPrefix(text, prefix+keyword) {
			return strings.TrimSpace(text[len(prefix+keyword):]), true
		}
	}
	return "", false
}

func parseDays(rest string, max int) int {
	if max <= 0 {
		max = defaultConfig().StatsMaxDays
	}
	s := strings.ReplaceAll(rest, "天", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimSpace(s)
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 1
	}
	days := int(math.Ceil(value))
	if days < 1 {
		days = 1
	}
	if days > max {
		days = max
	}
	return days
}

func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" {
		return false
	}
	for _, id := range adminUsers(c.Config) {
		if id == c.Event.UserID {
			return true
		}
	}
	return false
}

func adminUsers(raw map[string]any) []string {
	if raw == nil {
		return nil
	}
	switch v := raw["admin_users"].(type) {
	case []string:
		return v
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if id, ok := item.(string); ok {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}

// ---- 待处理图片会话 ----

func pendingKey(e sdk.Event) string {
	return strings.Join([]string{e.BotID, string(e.Source), e.UserID, e.GroupID, e.ChannelID}, "|")
}

func putPending(key string) { pendings.Store(key, now()) }

func takePending(key string) bool {
	value, ok := pendings.LoadAndDelete(key)
	if !ok {
		return false
	}
	createdAt, ok := value.(time.Time)
	if !ok || now().Sub(createdAt) > pendingTimeout {
		return false
	}
	return true
}

// ---- 文件信息与上传 ----

type fileInfo struct {
	Name   string
	MIME   string
	MD5    string
	Buffer []byte
}

type assetInfo struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

type uploadInfo struct {
	UploadURL string         `json:"upload_url"`
	Form      map[string]any `json:"form"`
	Token     string         `json:"token"`
	Assets    *assetInfo     `json:"assets"`
}

var mimeExt = map[string]string{
	"image/jpeg":       "jpg",
	"image/png":        "png",
	"image/gif":        "gif",
	"image/webp":       "webp",
	"image/bmp":        "bmp",
	"image/x-icon":     "ico",
	"image/tiff":       "tiff",
	"application/pdf":  "pdf",
	"application/zip":  "zip",
	"application/json": "json",
	"text/plain":       "txt",
	"video/mp4":        "mp4",
	"audio/mpeg":       "mp3",
}

func buildFileInfo(data []byte) fileInfo {
	sum := md5.Sum(data)
	hash := hex.EncodeToString(sum[:])
	mime := http.DetectContentType(data)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	ext := mimeExt[mime]
	if ext == "" {
		ext = "bin"
	}
	name := fmt.Sprintf("%s.%s.%s", base36(now().UnixMilli()), hash[:8], ext)
	return fileInfo{Name: name, MIME: mime, MD5: hash, Buffer: data}
}

func base36(value int64) string {
	if value == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, 0, 12)
	for value > 0 {
		out = append([]byte{digits[value%36]}, out...)
		value /= 36
	}
	return string(out)
}

// encodeURIComponent 对齐 JS encodeURIComponent 的保留字符集合。
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isImageName(name string) bool {
	if strings.HasSuffix(strings.ToLower(name), ".webp") {
		return true
	}
	return imageNamePattern.MatchString(name)
}

func withCDN(link string) string {
	if strings.HasPrefix(link, "http") {
		return link
	}
	return imageCDN + link
}

// localFileURL 复刻上游 fallbackToLocal：返回本地文件服务地址。
// 宿主没有内置静态文件服务，需要把 <基地址> 指向能提供 /File/<文件名> 的服务。
func localFileURL(cfg config, name string) string {
	base := strings.TrimRight(cfg.LocalBaseURL, "/")
	if base == "" {
		base = defaultLocalBase
	}
	return base + "/File/" + name
}

func fetchBytes(rawURL string) ([]byte, error) {
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片失败: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return io.ReadAll(resp.Body)
}

func getUploadURL(repo, fileName string, size int64, cfg config) (uploadInfo, error) {
	uploadPath := "files"
	if isImageName(fileName) {
		uploadPath = "imgs"
	}
	body, err := json.Marshal(map[string]any{"name": fileName, "size": size})
	if err != nil {
		return uploadInfo{}, err
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/" + repo + "/-/upload/" + uploadPath
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return uploadInfo{}, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return uploadInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return uploadInfo{}, fmt.Errorf("获取上传URL失败: %d %s - %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(text))
	}
	var info uploadInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return uploadInfo{}, err
	}
	return info, nil
}

func uploadWithPut(data []byte, info *uploadInfo) error {
	uploadURL := info.UploadURL
	if len(info.Form) > 0 {
		parsed, err := url.Parse(uploadURL)
		if err != nil {
			return err
		}
		query := parsed.Query()
		for key, value := range info.Form {
			query.Add(key, fmt.Sprint(value))
		}
		parsed.RawQuery = query.Encode()
		uploadURL = parsed.String()
	}

	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", strconv.Itoa(len(data)))
	if info.Token != "" {
		req.Header.Set("Authorization", "Bearer "+info.Token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("文件上传失败: %d %s - %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(text))
	}
	return nil
}

func deleteFile(path string, cfg config) error {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(cfg.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("删除文件失败: %d %s - %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(text))
	}
	return nil
}

func uploadToCnb(data []byte, fileName, repo string, cfg config) (uploadInfo, error) {
	if cfg.Token == "" {
		return uploadInfo{}, errors.New("CNB token未配置")
	}
	info, err := getUploadURL(repo, fileName, int64(len(data)), cfg)
	if err != nil {
		return uploadInfo{}, err
	}
	if err := uploadWithPut(data, &info); err != nil {
		return uploadInfo{}, err
	}
	if !cfg.RecordURL && info.Assets != nil && info.Assets.Path != "" {
		seconds := cfg.AutoDelete
		if seconds <= 0 {
			seconds = 30
		}
		scheduleDelete(time.Duration(seconds)*time.Second, info.Assets.Path, cfg)
	}
	return info, nil
}

// uploadFile 对齐上游 imageToUrl：优先缓存，再上传并按需记录统计。
func uploadFile(ctx context.Context, c sdk.Call, cfg config, info fileInfo) (string, error) {
	start := now()

	if info.MD5 != "" && cfg.RecordURL {
		if entry, ok := cachedImage(ctx, c, info.MD5); ok {
			return withCDN(entry.URL), nil
		}
	}

	result, err := uploadToCnb(info.Buffer, encodeURIComponent(info.Name), cfg.DefaultRepo, cfg)
	cost := now().Sub(start).Milliseconds()
	if err != nil {
		recordUploadStats(ctx, c, cfg, statRecord{
			TS: now().UnixMilli(), Type: info.MIME, Size: int64(len(info.Buffer)),
			Cost: cost, Success: false, FailReason: err.Error(),
		})
		// 与上游 fallbackToLocal 一致：上传失败时返回本地 URL 而不是直接报错。
		return localFileURL(cfg, info.Name), nil
	}
	if result.Assets == nil || result.Assets.Path == "" {
		err := errors.New("上传结果缺少文件路径")
		recordUploadStats(ctx, c, cfg, statRecord{
			TS: now().UnixMilli(), Type: info.MIME, Size: int64(len(info.Buffer)),
			Cost: cost, Success: false, FailReason: err.Error(),
		})
		return localFileURL(cfg, info.Name), nil
	}

	if info.MD5 != "" && cfg.RecordURL {
		setCachedImage(ctx, c, info.MD5, cacheEntry{
			URL: result.Assets.Path, Repo: cfg.DefaultRepo, Path: result.Assets.Path,
			Type: info.MIME, Name: info.Name, Size: int64(len(info.Buffer)),
		})
	}
	recordUploadStats(ctx, c, cfg, statRecord{
		TS: now().UnixMilli(), Type: info.MIME, Size: int64(len(info.Buffer)),
		Cost: cost, Success: true,
	})
	return withCDN(result.Assets.Path), nil
}

// ---- 缓存 ----

type cacheEntry struct {
	URL       string `json:"url"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed"`
}

func loadCache(ctx context.Context, c sdk.Call) map[string]cacheEntry {
	cache := map[string]cacheEntry{}
	if c.State != nil {
		_ = sdk.LoadJSON(ctx, c.State, stateCacheKey, &cache)
	}
	if cache == nil {
		cache = map[string]cacheEntry{}
	}
	return cache
}

func saveCache(ctx context.Context, c sdk.Call, cache map[string]cacheEntry) {
	if c.State == nil {
		return
	}
	_ = sdk.SaveJSON(ctx, c.State, stateCacheKey, cache)
}

func cachedImage(ctx context.Context, c sdk.Call, hash string) (cacheEntry, bool) {
	cache := loadCache(ctx, c)
	entry, ok := cache[hash]
	if !ok || entry.URL == "" {
		return cacheEntry{}, false
	}
	entry.LastUsed = now().UnixMilli()
	cache[hash] = entry
	saveCache(ctx, c, cache)
	return entry, true
}

func setCachedImage(ctx context.Context, c sdk.Call, hash string, entry cacheEntry) {
	cache := loadCache(ctx, c)
	entry.CreatedAt = now().UnixMilli()
	entry.LastUsed = entry.CreatedAt
	cache[hash] = entry
	saveCache(ctx, c, cache)
}

// cleanupExpired 每天最多触发一次过期图片清理（上游使用 3 点定时任务）。
func cleanupExpired(ctx context.Context, c sdk.Call, cfg config) {
	if c.State == nil || cfg.CacheExpireDays <= 0 {
		return
	}
	var last int64
	_ = sdk.LoadJSON(ctx, c.State, stateCleanupKey, &last)
	current := now().UnixMilli()
	if current-last < dayMillis {
		return
	}
	_ = sdk.SaveJSON(ctx, c.State, stateCleanupKey, current)

	cache := loadCache(ctx, c)
	expire := int64(cfg.CacheExpireDays) * dayMillis
	changed := false
	for hash, entry := range cache {
		if entry.Path == "" || entry.LastUsed <= 0 || current-entry.LastUsed <= expire {
			continue
		}
		if err := deleteFile(entry.Path, cfg); err != nil {
			continue
		}
		delete(cache, hash)
		changed = true
	}
	if changed {
		saveCache(ctx, c, cache)
	}
}

// ---- 统计 ----

type statRecord struct {
	TS         int64  `json:"ts"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Cost       int64  `json:"cost"`
	Success    bool   `json:"success"`
	FailReason string `json:"failReason"`
}

type uploadStats struct {
	Days      int
	Count     int
	Fail      int
	TotalSize int64
	MinCost   int64
	MaxCost   int64
	AvgCost   int64
	MinSize   int64
	MaxSize   int64
}

type reasonCount struct {
	Reason string
	Count  int
}

func loadStats(ctx context.Context, c sdk.Call, maxDays int) []statRecord {
	records := []statRecord{}
	if c.State != nil {
		_ = sdk.LoadJSON(ctx, c.State, stateStatsKey, &records)
	}
	if maxDays <= 0 {
		maxDays = defaultConfig().StatsMaxDays
	}
	cutoff := now().UnixMilli() - int64(maxDays)*dayMillis
	kept := records[:0]
	for _, record := range records {
		if record.TS >= cutoff {
			kept = append(kept, record)
		}
	}
	return kept
}

func saveStats(ctx context.Context, c sdk.Call, records []statRecord) {
	if c.State == nil {
		return
	}
	_ = sdk.SaveJSON(ctx, c.State, stateStatsKey, records)
}

func recordUploadStats(ctx context.Context, c sdk.Call, cfg config, record statRecord) {
	if !cfg.Stats {
		return
	}
	records := loadStats(ctx, c, cfg.StatsMaxDays)
	records = append(records, record)
	saveStats(ctx, c, records)
}

func collectStats(ctx context.Context, c sdk.Call, cfg config, days int) uploadStats {
	records := loadStats(ctx, c, cfg.StatsMaxDays)
	current := now().UnixMilli()
	minTS := current - int64(days)*dayMillis

	out := uploadStats{Days: days}
	var totalCost int64
	var okCost int64
	for _, record := range records {
		if record.TS < minTS || record.TS > current {
			continue
		}
		if !record.Success {
			out.Fail++
			continue
		}
		size := record.Size
		cost := record.Cost
		if out.Count == 0 {
			out.MinSize, out.MaxSize = size, size
			out.MinCost, out.MaxCost = cost, cost
		} else {
			if size < out.MinSize {
				out.MinSize = size
			}
			if size > out.MaxSize {
				out.MaxSize = size
			}
			if cost < out.MinCost {
				out.MinCost = cost
			}
			if cost > out.MaxCost {
				out.MaxCost = cost
			}
		}
		out.Count++
		out.TotalSize += size
		totalCost += cost
		okCost++
	}
	if okCost > 0 {
		out.AvgCost = int64(math.Round(float64(totalCost) / float64(okCost)))
	}
	return out
}

func topFailReasons(records []statRecord, minTS int64) (int, []reasonCount) {
	order := make([]string, 0)
	counts := map[string]int{}
	total := 0
	for _, record := range records {
		if record.Success || record.FailReason == "" || record.TS < minTS {
			continue
		}
		total++
		if _, ok := counts[record.FailReason]; !ok {
			order = append(order, record.FailReason)
		}
		counts[record.FailReason]++
	}
	out := make([]reasonCount, 0, len(order))
	for _, reason := range order {
		out = append(out, reasonCount{Reason: reason, Count: counts[reason]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 10 {
		out = out[:10]
	}
	return total, out
}

func truncateReason(reason string) string {
	reason = strings.ReplaceAll(reason, "\r", " ")
	reason = strings.ReplaceAll(reason, "\n", " ")
	runes := []rune(reason)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}

func buildStatsText(stats uploadStats, maxDays int) string {
	avgSize := int64(0)
	if stats.Count > 0 {
		avgSize = int64(math.Round(float64(stats.TotalSize) / float64(stats.Count)))
	}
	msg := "\r# 📅 图床状态" +
		fmt.Sprintf("\r>统计区间：**%d天**", stats.Days) +
		fmt.Sprintf("\r>数据保留：**%d天**", maxDays) +
		fmt.Sprintf("\r> 上传表现：**%s**", speedLevel(stats.AvgCost)) +
		"\r## 📊 状态统计" +
		"\r| 指标 | 数值 |" +
		"\r|---|---|" +
		fmt.Sprintf("\r| 上传成功 | **%d** 张 |", stats.Count) +
		fmt.Sprintf("\r| 上传失败 | **%d** 次 |", stats.Fail) +
		fmt.Sprintf("\r| 成功率 | **%s** |", percent(stats.Count, stats.Count+stats.Fail)) +
		fmt.Sprintf("\r| 最大 | **%s** |", formatBytes(stats.MaxSize)) +
		fmt.Sprintf("\r| 最小 | **%s** |", formatBytes(stats.MinSize)) +
		fmt.Sprintf("\r| 总大小 | **%s** |", formatBytes(stats.TotalSize)) +
		fmt.Sprintf("\r| 平均单张大小 | **%s** |", formatBytes(avgSize)) +
		"\r## ⏰ 耗时统计" +
		"\r| 项目 | 数值 |" +
		"\r|---|---|" +
		fmt.Sprintf("\r| 最短上传耗时 | **%s** |", formatMs(stats.MinCost)) +
		fmt.Sprintf("\r| 最长上传耗时 | **%s** |", formatMs(stats.MaxCost)) +
		fmt.Sprintf("\r| 平均上传耗时 | **%s** |", formatMs(stats.AvgCost))
	return msg
}

func speedLevel(avgCost int64) string {
	switch {
	case avgCost <= 600:
		return "⚡ 很快"
	case avgCost <= 1500:
		return "🚀 良好"
	case avgCost <= 3000:
		return "🟡 一般"
	default:
		return "🐢 偏慢"
	}
}

func percent(num, den int) string {
	if den == 0 {
		return "0.00%"
	}
	return fmt.Sprintf("%.2f%%", float64(num)/float64(den)*100)
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	index := int(math.Floor(math.Log(float64(bytes)) / math.Log(1024)))
	if index < 0 {
		index = 0
	}
	if index >= len(units) {
		index = len(units) - 1
	}
	value := float64(bytes) / math.Pow(1024, float64(index))
	if index == 0 {
		return fmt.Sprintf("%.0f %s", value, units[index])
	}
	return fmt.Sprintf("%.2f %s", value, units[index])
}

func formatMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// ---- 配置 ----

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	if v, ok := raw["baseUrl"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.BaseURL = v
	}
	if v, ok := raw["localBaseUrl"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.LocalBaseURL = v
	}
	if v, ok := raw["token"].(string); ok {
		cfg.Token = v
	}
	if v, ok := raw["defaultRepo"].(string); ok {
		cfg.DefaultRepo = v
	}
	cfg.AutoDelete = intValue(raw["autodelete"], cfg.AutoDelete)
	cfg.Stats = boolValue(raw["stats"], cfg.Stats)
	cfg.RecordURL = boolValue(raw["recordurl"], cfg.RecordURL)
	cfg.CacheExpireDays = intValue(raw["cache_expire_days"], cfg.CacheExpireDays)
	cfg.StatsMaxDays = intValue(raw["stats_max_days"], cfg.StatsMaxDays)
	cfg.AdminUsers = adminUsers(raw)
	return cfg
}

func intValue(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fallback
		}
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func boolValue(value any, fallback bool) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return fallback
}

// Package jandan 煎蛋网无聊图：更新并维护本地历史图库，随机返回一张图片。
package jandan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// picPostURL 无聊图原帖地址，用于回复里的可点击链接。
	picPostURL = "https://jandan.net/pic"
	// picPostID 是无聊图版块在上游评论接口中的帖子 ID。
	picPostID = 26402
	// libraryKey 保存本地历史图库。
	libraryKey = "jandan:pictures"
	// maxLibrary 限制历史图库条目数，避免状态过大。
	maxLibrary = 3000
	// maxUpdatePages 单次更新的最大翻页数，避免上游分页异常时长时间占用。
	maxUpdatePages = 100
)

// noPermission 是缺少管理员身份时的统一提示。
const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

var (
	// apiBase 上游站点，测试可替换。
	apiBase = "https://jandan.net"

	client   = &http.Client{Timeout: 15 * time.Second}
	randIntn = rand.Intn

	picTrigger    = regexp.MustCompile(`^来份[屌弔吊]图$`)
	updateTrigger = regexp.MustCompile(`^更新[屌弔吊]图$`)
	imgPattern    = regexp.MustCompile(`<img\b[^>]*>`)
	attrPattern   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"`)

	errLoginRequired = errors.New("较老的数据仅对登录用户开放访问")
)

// pictureLibrary 是本地保存的历史图片 URL 表。
type pictureLibrary struct {
	URLs []string `json:"urls"`
}

type commentPage struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data *struct {
		TotalPages  int `json:"total_pages"`
		CurrentPage int `json:"current_page"`
		List        []struct {
			Content string `json:"content"`
		} `json:"list"`
	} `json:"data"`
}

type pageResult struct {
	TotalPages  int
	CurrentPage int
	Links       []string
}

// Definition subscribes to the 「来份屌图」「更新屌图」 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "jandan",
		Name:        "煎蛋无聊图",
		Version:     "1.0.0",
		Description: "发送「来份屌图」「来份弔图」或「来份吊图」，从本地历史图库随机返回一张煎蛋网无聊图；管理员可发送「更新屌图」抓取新图",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "jandan", Trigger: "来份屌图"}},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许使用「更新屌图」的 member_openid 列表",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle 处理更新与随机取图；取图优先使用本地历史库，空库时回退到最新一页。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchRegexp(text, updateTrigger) != nil {
		return handleUpdate(ctx, c)
	}
	if sdk.MatchRegexp(text, picTrigger) == nil {
		return nil
	}
	lib := loadLibrary(ctx, c)
	if len(lib.URLs) == 0 {
		result, err := fetchPage(ctx, 0)
		if err != nil {
			return c.Reply(ctx, "获取煎蛋图片失败: "+err.Error())
		}
		lib.URLs = result.Links
		if len(lib.URLs) == 0 {
			return c.Reply(ctx, "获取煎蛋图片失败: 未找到图片")
		}
		_ = saveLibrary(ctx, c, lib)
	}
	// 图片消息不能带键盘，先发一条带「换一张」与原帖链接的 Markdown，再发图片。
	text = "煎蛋无聊图 · [原帖](" + picPostURL + ")"
	if err := c.ReplyKeyboardOrText(ctx, text, picKeyboard()); err != nil {
		return err
	}
	return c.ReplyImage(ctx, lib.URLs[randIntn(len(lib.URLs))])
}

// handleUpdate 抓取上游当前允许匿名访问的全部历史页并合并进本地图库。
func handleUpdate(ctx context.Context, c sdk.Call) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	if err := c.Reply(ctx, "少女更新中..."); err != nil {
		return err
	}
	links, err := crawl(ctx)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if len(links) == 0 {
		return c.Reply(ctx, "ERROR: 未找到图片")
	}
	lib := loadLibrary(ctx, c)
	lib.URLs = mergeLibrary(lib.URLs, links)
	if err := saveLibrary(ctx, c, lib); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return c.Reply(ctx, "更新完成!")
}

// crawl 从最新一页向前翻页，遇到登录限制或达到安全上限时停止。
func crawl(ctx context.Context) ([]string, error) {
	first, err := fetchPage(ctx, 0)
	if err != nil {
		return nil, err
	}
	links := append([]string(nil), first.Links...)
	start := first.CurrentPage
	if start == 0 {
		start = first.TotalPages
	}
	end := start - maxUpdatePages
	if end < 1 {
		end = 1
	}
	for page := start - 1; page >= end; page-- {
		result, err := fetchPage(ctx, page)
		if errors.Is(err, errLoginRequired) {
			break
		}
		if err != nil {
			return nil, err
		}
		links = append(links, result.Links...)
	}
	return links, nil
}

// fetchPage 请求指定页的评论接口并提取图片地址。
func fetchPage(ctx context.Context, page int) (pageResult, error) {
	endpoint := fmt.Sprintf("%s/api/comment/post/%d?order=desc&page=%d", apiBase, picPostID, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return pageResult{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	req.Header.Set("Referer", picPostURL)
	resp, err := client.Do(req)
	if err != nil {
		return pageResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pageResult{}, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return pageResult{}, err
	}
	var parsed commentPage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return pageResult{}, err
	}
	if parsed.Code == 1000 {
		return pageResult{}, errLoginRequired
	}
	if parsed.Code != 0 {
		return pageResult{}, errors.New(parsed.Msg)
	}
	if parsed.Data == nil {
		return pageResult{}, nil
	}
	result := pageResult{TotalPages: parsed.Data.TotalPages, CurrentPage: parsed.Data.CurrentPage}
	for _, comment := range parsed.Data.List {
		result.Links = append(result.Links, imageLinks(comment.Content)...)
	}
	return result, nil
}

// imageLinks 提取评论正文中 img 标签的 src 或 data-src。
func imageLinks(page string) []string {
	var links []string
	for _, tag := range imgPattern.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2])
		}
		src := strings.TrimSpace(attrs["src"])
		if src == "" {
			src = strings.TrimSpace(attrs["data-src"])
		}
		if src == "" || strings.HasPrefix(src, "data:") {
			continue
		}
		links = append(links, normalize(src))
	}
	return links
}

func normalize(href string) string {
	switch {
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "http://"):
		return "https://" + strings.TrimPrefix(href, "http://")
	case strings.HasPrefix(href, "/"):
		return "https://jandan.net" + href
	default:
		return href
	}
}

// loadLibrary 读取本地历史图库。
func loadLibrary(ctx context.Context, c sdk.Call) pictureLibrary {
	var lib pictureLibrary
	_ = sdk.LoadJSON(ctx, c.State, libraryKey, &lib)
	return lib
}

func saveLibrary(ctx context.Context, c sdk.Call, lib pictureLibrary) error {
	return sdk.SaveJSON(ctx, c.State, libraryKey, lib)
}

// mergeLibrary 合并新旧图库并按最近优先截断。
func mergeLibrary(old, added []string) []string {
	seen := make(map[string]bool, len(old)+len(added))
	out := make([]string, 0, len(old)+len(added))
	for _, group := range [][]string{added, old} {
		for _, url := range group {
			if url == "" || seen[url] {
				continue
			}
			seen[url] = true
			out = append(out, url)
		}
	}
	if len(out) > maxLibrary {
		out = out[:maxLibrary]
	}
	return out
}

// isAdmin 判断发送者是否在 admin_users 中。
func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" || c.Config == nil {
		return false
	}
	switch v := c.Config["admin_users"].(type) {
	case []string:
		for _, id := range v {
			if id == c.Event.UserID {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if id, ok := item.(string); ok && id == c.Event.UserID {
				return true
			}
		}
	}
	return false
}

// picKeyboard 无聊图下方的快捷按钮。
func picKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("jandan-next", "换一张", "来份屌图"),
	))
}

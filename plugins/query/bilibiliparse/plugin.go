// Package bilibiliparse B 站链接解析：群内发送 B 站链接时自动回复视频、专栏或直播间信息。
package bilibiliparse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// 下列接口与上游一致，测试中替换为 httptest 服务。
	videoAPI   = "https://api.bilibili.com/x/web-interface/view"
	articleAPI = "https://api.bilibili.com/x/article/viewinfo"
	liveAPI    = "https://api.live.bilibili.com/room/v1/Room/get_info"
	// 页面地址，用于回复中的可点击链接。
	videoPage   = "https://www.bilibili.com/video/"
	articlePage = "https://www.bilibili.com/read/cv"
	livePage    = "https://live.bilibili.com/"
	// cooldown 同一会话内两次解析的最小间隔，避免刷屏。
	cooldown = 10 * time.Second
)

var (
	viewURL = videoAPI
	artURL  = articleAPI
	roomURL = liveAPI

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	client = &http.Client{Timeout: 10 * time.Second}
	// redirectClient 不自动跟随跳转，用于还原 b23.tv 等短链。
	redirectClient = &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	shortRe = regexp.MustCompile(`(?:https?://)?(?:b23\.tv|acg\.tv|bili2233\.cn)/[0-9A-Za-z]+`)
	videoRe = regexp.MustCompile(`(?:https?://)?(?:www\.|m\.)?bilibili\.com/video/(?:av(\d+)|[Bb][Vv]([0-9A-Za-z]+))`)
	readRe  = regexp.MustCompile(`(?:https?://)?(?:www\.|m\.)?bilibili\.com/read/(?:cv|mobile/)(\d+)`)
	roomRe  = regexp.MustCompile(`(?:https?://)?live\.bilibili\.com/(?:h5/)?(\d+)`)

	throttleMu sync.Mutex
	lastParsed = map[string]time.Time{}
)

type target struct {
	kind string
	aid  string
	bvid string
	id   string
}

type videoData struct {
	BVID     string `json:"bvid"`
	AID      int64  `json:"aid"`
	Title    string `json:"title"`
	Pic      string `json:"pic"`
	Desc     string `json:"desc"`
	Duration int    `json:"duration"`
	Owner    struct {
		Name string `json:"name"`
	} `json:"owner"`
	Stat struct {
		View    int `json:"view"`
		Danmaku int `json:"danmaku"`
		Like    int `json:"like"`
	} `json:"stat"`
}

type articleData struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	BannerURL  string `json:"banner_url"`
	AuthorName string `json:"author_name"`
	Stats      struct {
		View int `json:"view"`
		Like int `json:"like"`
	} `json:"stats"`
}

type liveData struct {
	RoomID     int64  `json:"room_id"`
	Title      string `json:"title"`
	Uname      string `json:"uname"`
	UserCover  string `json:"user_cover"`
	Keyframe   string `json:"keyframe"`
	LiveStatus int    `json:"live_status"`
	Online     int    `json:"online"`
}

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Definition subscribes to every message, parses B 站 links and stays quiet otherwise.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "bilibiliparse",
		Name:        "B站链接解析",
		Version:     "1.0.0",
		Description: "群里发 B 站视频/专栏/直播间链接或 b23.tv 短链时自动解析并回复信息",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "bilibiliparse", Usage: "发送 B 站视频、专栏或直播间链接即可自动解析，无需指令"},
	})
}

// Handle detects a B 站 link, resolves short links and replies with the parsed card.
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	targetText := text
	if short := shortRe.FindString(text); short != "" {
		if real, err := resolve(ctx, short); err == nil {
			targetText = real
		}
	}
	t, ok := detect(targetText)
	if !ok {
		return nil
	}
	if !allow(sessionKey(c.Event)) {
		return nil
	}
	switch t.kind {
	case "video":
		return handleVideo(ctx, c, t)
	case "article":
		return handleArticle(ctx, c, t)
	case "live":
		return handleLive(ctx, c, t)
	}
	return nil
}

// detect 从文本中识别链接类型，视频优先于专栏与直播间。
func detect(text string) (target, bool) {
	if m := videoRe.FindStringSubmatch(text); m != nil {
		if m[1] != "" {
			return target{kind: "video", aid: m[1]}, true
		}
		return target{kind: "video", bvid: "BV" + m[2]}, true
	}
	if m := readRe.FindStringSubmatch(text); m != nil {
		return target{kind: "article", id: m[1]}, true
	}
	if m := roomRe.FindStringSubmatch(text); m != nil {
		return target{kind: "live", id: m[1]}, true
	}
	return target{}, false
}

// resolve 逐跳读取短链的 Location，最多三跳，命中 B 站页面后立即返回。
func resolve(ctx context.Context, raw string) (string, error) {
	current := raw
	if !strings.HasPrefix(current, "http://") && !strings.HasPrefix(current, "https://") {
		current = "https://" + current
	}
	for i := 0; i < 3; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := redirectClient.Do(req)
		if err != nil {
			return "", err
		}
		location := resp.Header.Get("Location")
		_ = resp.Body.Close()
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
			return current, nil
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", err
		}
		next, err := base.Parse(location)
		if err != nil {
			return "", err
		}
		current = next.String()
		if _, ok := detect(current); ok {
			return current, nil
		}
	}
	return current, nil
}

func handleVideo(ctx context.Context, c sdk.Call, t target) error {
	params := url.Values{}
	if t.bvid != "" {
		params.Set("bvid", t.bvid)
	} else {
		params.Set("aid", t.aid)
	}
	var data videoData
	if err := getJSON(ctx, viewURL+"?"+params.Encode(), &data); err != nil {
		return c.Reply(ctx, "B站链接解析失败: "+err.Error())
	}
	if data.BVID == "" && data.Title == "" {
		return c.Reply(ctx, "没有查到这条视频")
	}
	lines := []string{fmt.Sprintf("[%s](%s)", data.Title, videoPage+data.BVID)}
	head := ""
	if data.Owner.Name != "" {
		head = "UP：" + data.Owner.Name
	}
	if data.Duration > 0 {
		if head != "" {
			head += " ｜ "
		}
		head += "时长 " + formatDuration(data.Duration)
	}
	if head != "" {
		lines = append(lines, head)
	}
	if data.Stat.View > 0 || data.Stat.Danmaku > 0 || data.Stat.Like > 0 {
		lines = append(lines, fmt.Sprintf("播放 %d · 弹幕 %d · 点赞 %d", data.Stat.View, data.Stat.Danmaku, data.Stat.Like))
	}
	if desc := clip(data.Desc, 60); desc != "" {
		lines = append(lines, desc)
	}
	if err := replyMarkdown(ctx, c, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if data.Pic != "" {
		_ = c.ReplyImage(ctx, data.Pic)
	}
	return nil
}

func handleArticle(ctx context.Context, c sdk.Call, t target) error {
	id, err := strconv.ParseInt(t.id, 10, 64)
	if err != nil {
		return nil
	}
	var data articleData
	if err := getJSON(ctx, artURL+"?id="+t.id, &data); err != nil {
		return c.Reply(ctx, "B站链接解析失败: "+err.Error())
	}
	title := data.Title
	if title == "" {
		title = "专栏 cv" + t.id
	}
	lines := []string{fmt.Sprintf("[%s](%s%d)", title, articlePage, id)}
	head := ""
	if data.AuthorName != "" {
		head = "作者：" + data.AuthorName
	}
	if data.Stats.View > 0 {
		if head != "" {
			head += " ｜ "
		}
		head += fmt.Sprintf("阅读 %d · 点赞 %d", data.Stats.View, data.Stats.Like)
	}
	if head != "" {
		lines = append(lines, head)
	}
	if summary := clip(data.Summary, 80); summary != "" {
		lines = append(lines, summary)
	}
	if err := replyMarkdown(ctx, c, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if data.BannerURL != "" {
		_ = c.ReplyImage(ctx, data.BannerURL)
	}
	return nil
}

func handleLive(ctx context.Context, c sdk.Call, t target) error {
	var data liveData
	if err := getJSON(ctx, roomURL+"?room_id="+t.id, &data); err != nil {
		return c.Reply(ctx, "B站链接解析失败: "+err.Error())
	}
	if data.RoomID == 0 && data.Title == "" {
		return c.Reply(ctx, "没有查到这个直播间")
	}
	lines := []string{fmt.Sprintf("[%s](%s%d)", data.Title, livePage, data.RoomID)}
	if data.Uname != "" {
		lines = append(lines, "主播："+data.Uname)
	}
	status := "未开播"
	if data.LiveStatus == 1 {
		status = "直播中"
	}
	lines = append(lines, "状态："+status)
	if data.LiveStatus == 1 && data.Online > 0 {
		lines = append(lines, fmt.Sprintf("在线人气：%d", data.Online))
	}
	if err := replyMarkdown(ctx, c, strings.Join(lines, "\n")); err != nil {
		return err
	}
	cover := data.Keyframe
	if cover == "" {
		cover = data.UserCover
	}
	if cover != "" {
		_ = c.ReplyImage(ctx, cover)
	}
	return nil
}

// getJSON 请求 B 站接口并把 code 非 0 的响应转成错误。
func getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return err
	}
	var meta apiResp
	if err = json.Unmarshal(body, &meta); err != nil {
		return err
	}
	if meta.Code != 0 {
		return fmt.Errorf("%s (code %d)", notnull(meta.Message), meta.Code)
	}
	if len(meta.Data) == 0 {
		return nil
	}
	return json.Unmarshal(meta.Data, out)
}

// replyMarkdown 发送可点击链接的 Markdown，平台不支持时退回纯文本。
func replyMarkdown(ctx context.Context, c sdk.Call, text string) error {
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
}

// allow 对同一会话的解析做冷却，避免短时间连续刷屏。
func allow(key string) bool {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	now := time.Now()
	if last, ok := lastParsed[key]; ok && now.Sub(last) < cooldown {
		return false
	}
	lastParsed[key] = now
	return true
}

// sessionKey 优先按群冷却，私聊按用户冷却。
func sessionKey(e sdk.Event) string {
	if e.GroupID != "" {
		return "g:" + e.GroupID
	}
	return "u:" + e.UserID
}

func formatDuration(sec int) string {
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

// clip 按字符截断文本，超出时补省略号。
func clip(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func notnull(text string) string {
	if text == "" {
		return "接口返回错误"
	}
	return text
}

// Package rsshub RSS 订阅：按需抓取 RSSHub 路由，群内可订阅并手动同步更新。
package rsshub

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	stateKey = "rsshub"
	// maxSubs 每个群最多订阅的源数量。
	maxSubs = 5
	// pullCooldown 手动同步的最小间隔。
	pullCooldown = time.Minute
	// pushLimit 每个源单次最多推送的条数，与上游一致。
	pushLimit = 20
	// summaryLimit 摘要最大字数。
	summaryLimit = 120
)

var (
	// rssBases 是 RSSHub 公共实例，按顺序尝试；测试中替换为 httptest 服务。
	rssBases = []string{
		"https://rss.injahow.cn",
		"https://rsshub.rssforever.com",
	}
	// routeRe 限制路由字符，避免拼出任意地址。
	routeRe = regexp.MustCompile(`^/[A-Za-z0-9_\-./~%+?=&]*$`)

	httpClient = &http.Client{Timeout: 10 * time.Second}
)

// Definition subscribes to the RSSHub query and subscription commands.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "rsshub",
		Name:        "RSS订阅",
		Version:     "1.0.0",
		Description: "发送「rss /路由」抓取最新条目；管理员可订阅路由并手动同步更新",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "rss", Trigger: "rss"},
	},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许管理 RSS 订阅的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// rssSub 是某个群对一条 RSSHub 路由的订阅。
type rssSub struct {
	Route string   `json:"route"`
	Title string   `json:"title"`
	Seen  []string `json:"seen"`
}

// rssStore 按群保存订阅与上次同步时间。
type rssStore struct {
	Groups map[string][]rssSub `json:"groups"`
	Pulled map[string]int64    `json:"pulled"`
}

// Handle 路由各条 RSS 指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	switch {
	case sdk.MatchFull(text, "rss帮助"), sdk.MatchFull(text, "RSS帮助"):
		return c.Reply(ctx, helpText)
	case sdk.MatchFull(text, "rss订阅列表"), sdk.MatchFull(text, "RSS订阅列表"),
		sdk.MatchFull(text, "查看rsshub订阅列表"), sdk.MatchFull(text, "查看RSSHub订阅列表"):
		return listSubs(ctx, c)
	case sdk.MatchFull(text, "rss同步"), sdk.MatchFull(text, "RSS同步"),
		sdk.MatchFull(text, "rsshub同步"), sdk.MatchFull(text, "RSSHub同步"):
		return syncSubs(ctx, c)
	}
	for _, prefix := range []string{"添加rsshub订阅-", "添加RSSHub订阅-", "添加rsshub订阅", "添加RSSHub订阅", "添加rss订阅", "添加RSS订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return addSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"删除rsshub订阅-", "删除RSSHub订阅-", "删除rsshub订阅", "删除RSSHub订阅", "删除rss订阅", "删除RSS订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return removeSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"rss ", "RSS "} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return queryFeed(ctx, c, rest)
		}
	}
	return nil
}

const helpText = "RSS 指令：\n" +
	"rss /路由 — 抓取最新条目，例如 rss /bookfere/weekly\n" +
	"rss订阅列表 / 查看rsshub订阅列表 — 查看本群订阅\n" +
	"添加rss订阅 /路由、添加rsshub订阅-/路由 — 管理员添加订阅\n" +
	"删除rss订阅 /路由、删除rsshub订阅-/路由 — 管理员删除订阅\n" +
	"rss同步 / rsshub同步 — 管理员手动同步一次更新（每分钟最多一次）"

// queryFeed 抓取并展示最新条目。
func queryFeed(ctx context.Context, c sdk.Call, arg string) error {
	route, err := normalizeRoute(arg)
	if err != nil {
		return c.Reply(ctx, "用法：rss /bookfere/weekly")
	}
	feed, err := fetchFeed(ctx, route)
	if err != nil {
		return c.Reply(ctx, "抓取失败："+err.Error())
	}
	if len(feed.Entries) == 0 {
		return c.Reply(ctx, "该源暂时没有条目")
	}
	lines := []string{"【RSS】" + feed.Title}
	for i, entry := range feed.Entries {
		if i >= 5 {
			break
		}
		lines = append(lines, "· "+entry.title()+"（"+entry.timeText()+"）")
		if entry.Link != "" {
			lines = append(lines, "  "+entry.Link)
		}
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// addSub 校验管理员后新增订阅，并记录当前条目作为基线。
func addSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	route, err := normalizeRoute(arg)
	if err != nil {
		return c.Reply(ctx, "用法：添加rss订阅 /bookfere/weekly")
	}
	feed, err := fetchFeed(ctx, route)
	if err != nil {
		return c.Reply(ctx, "订阅失败："+err.Error())
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	for _, sub := range subs {
		if sub.Route == route {
			return c.Reply(ctx, "已经订阅了 "+sub.Title)
		}
	}
	if len(subs) >= maxSubs {
		return c.Reply(ctx, fmt.Sprintf("本群订阅已达上限 %d 个，请先删除部分订阅", maxSubs))
	}
	st.Groups[c.Event.GroupID] = append(subs, rssSub{
		Route: route,
		Title: feed.Title,
		Seen:  seenIDs(feed, 20),
	})
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, "已订阅 "+feed.Title+"，发送「rss同步」拉取更新")
}

// removeSub 删除订阅。
func removeSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	route, err := normalizeRoute(arg)
	if err != nil {
		return c.Reply(ctx, "用法：删除rss订阅 /bookfere/weekly")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	kept := subs[:0]
	for _, sub := range subs {
		if sub.Route != route {
			kept = append(kept, sub)
		}
	}
	if len(kept) == len(subs) {
		return c.Reply(ctx, "本群没有订阅该路由")
	}
	st.Groups[c.Event.GroupID] = kept
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, "已删除订阅 "+route)
}

// listSubs 列出本群订阅。
func listSubs(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	if len(subs) == 0 {
		return c.Reply(ctx, "本群还没有 RSS 订阅，管理员可发送「添加rss订阅 /路由」")
	}
	lines := []string{fmt.Sprintf("本群 RSS 订阅（%d/%d）：", len(subs), maxSubs)}
	for _, sub := range subs {
		lines = append(lines, "· "+sub.Title+"（"+sub.Route+"）")
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// syncSubs 手动同步全部订阅，只推送未见过的新条目。
func syncSubs(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	if wait := pullCooldown - time.Duration(time.Now().Unix()-st.Pulled[c.Event.GroupID])*time.Second; wait > 0 {
		return c.Reply(ctx, fmt.Sprintf("同步太频繁，请 %d 秒后再试", int(wait.Seconds())+1))
	}
	subs := st.Groups[c.Event.GroupID]
	if len(subs) == 0 {
		return c.Reply(ctx, "本群还没有 RSS 订阅，管理员可发送「添加rss订阅 /路由」")
	}

	var updates []string
	var images []string
	var failed []string
	for i := range subs {
		sub := &subs[i]
		feed, err := fetchFeed(ctx, sub.Route)
		if err != nil {
			failed = append(failed, sub.Title)
			continue
		}
		seen := make(map[string]bool, len(sub.Seen))
		for _, id := range sub.Seen {
			seen[id] = true
		}
		count := 0
		// 与上游一致：按时间倒序，优先推送最新的 pushLimit 条。
		entries := append([]feedEntry(nil), feed.Entries...)
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].Time.IsZero() {
				return false
			}
			if entries[j].Time.IsZero() {
				return true
			}
			return entries[i].Time.After(entries[j].Time)
		})
		for _, entry := range entries {
			id := entry.id()
			if seen[id] {
				continue
			}
			seen[id] = true
			if count >= pushLimit {
				continue
			}
			updates = append(updates, pushBlock(sub.Title, entry))
			if entry.Image != "" {
				images = append(images, entry.Image)
			}
			count++
		}
		sub.Seen = seenIDs(feed, 30)
	}
	st.Groups[c.Event.GroupID] = subs
	st.Pulled[c.Event.GroupID] = time.Now().Unix()
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	if len(updates) == 0 {
		msg := "本次没有新条目"
		if len(failed) > 0 {
			msg += "；抓取失败：" + strings.Join(failed, "、")
		}
		return c.Reply(ctx, msg)
	}
	msg := "【RSS 更新】\n" + strings.Join(updates, "\n")
	if len(failed) > 0 {
		msg += "\n抓取失败：" + strings.Join(failed, "、")
	}
	if err := c.Reply(ctx, msg); err != nil {
		return err
	}
	// 图片发送失败时保留文字中的标题与链接作为降级。
	for _, image := range images {
		_ = c.ReplyImage(ctx, image)
	}
	return nil
}

// pushBlock 拼接单条推送：标题、时间、摘要与链接。
func pushBlock(source string, entry feedEntry) string {
	block := "· " + source + "：" + entry.title()
	if !entry.Time.IsZero() {
		block += "（" + entry.timeText() + "）"
	}
	if entry.Summary != "" {
		block += "\n  " + entry.Summary
	}
	if entry.Link != "" {
		block += "\n  " + entry.Link
	}
	return block
}

// feedEntry 是统一后的条目。
type feedEntry struct {
	ID      string
	Title   string
	Link    string
	Time    time.Time
	Summary string
	Image   string
}

// title 返回条目名称，缺失时用链接兜底。
func (e feedEntry) title() string {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		if e.Link != "" {
			return e.Link
		}
		return "无标题"
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "…"
	}
	return title
}

// timeText 返回可读时间。
func (e feedEntry) timeText() string {
	if e.Time.IsZero() {
		return "未知时间"
	}
	return e.Time.Local().Format("2006-01-02 15:04")
}

// id 返回条目去重标识。
func (e feedEntry) id() string {
	if e.ID != "" {
		return e.ID
	}
	if e.Link != "" {
		return e.Link
	}
	return e.Title
}

// parsedFeed 是解析后的订阅源。
type parsedFeed struct {
	Title   string
	Entries []feedEntry
}

// xmlMedia 描述 enclosure、thumbnail 与 media:content 等媒体节点。
type xmlMedia struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// xmlRSSItem 同时兼容 RSS 2.0 与 RDF 的 item。
type xmlRSSItem struct {
	Title         string     `xml:"title"`
	Link          string     `xml:"link"`
	GUID          string     `xml:"guid"`
	PubDate       string     `xml:"pubDate"`
	Description   string     `xml:"description"`
	Encoded       string     `xml:"encoded"`
	Enclosures    []xmlMedia `xml:"enclosure"`
	Thumbnails    []xmlMedia `xml:"thumbnail"`
	MediaContents []xmlMedia `xml:"content"`
}

// xmlAtomEntry 是 Atom 条目。
type xmlAtomEntry struct {
	Title     string `xml:"title"`
	ID        string `xml:"id"`
	Updated   string `xml:"updated"`
	Published string `xml:"published"`
	Summary   string `xml:"summary"`
	Content   string `xml:"content"`
	Links     []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
}

// xmlDoc 同时兼容 RSS 2.0、Atom 与 RDF 三种结构。
type xmlDoc struct {
	XMLName xml.Name
	Channel struct {
		Title string       `xml:"title"`
		Items []xmlRSSItem `xml:"item"`
	} `xml:"channel"`
	Title   string         `xml:"title"`
	Entries []xmlAtomEntry `xml:"entry"`
	Items   []xmlRSSItem   `xml:"item"`
}

// fetchFeed 依次尝试各实例抓取并解析。
func fetchFeed(ctx context.Context, route string) (*parsedFeed, error) {
	var lastErr error
	for _, base := range rssBases {
		feed, err := fetchFrom(ctx, strings.TrimRight(base, "/")+route)
		if err == nil {
			return feed, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的 RSSHub 实例")
	}
	return nil, lastErr
}

// fetchFrom 从指定地址抓取并解析。
func fetchFrom(ctx context.Context, rawURL string) (*parsedFeed, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ShizukuBot/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("实例返回 " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, errors.New("读取响应失败")
	}
	return parseFeed(body)
}

// parseFeed 解析 RSS/Atom/RDF。
func parseFeed(body []byte) (*parsedFeed, error) {
	var doc xmlDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("不是有效的 RSS/Atom 内容")
	}
	feed := &parsedFeed{}
	switch doc.XMLName.Local {
	case "rss":
		feed.Title = strings.TrimSpace(doc.Channel.Title)
		for _, item := range doc.Channel.Items {
			feed.Entries = append(feed.Entries, rssEntry(item))
		}
	case "feed":
		feed.Title = strings.TrimSpace(doc.Title)
		for _, entry := range doc.Entries {
			link := ""
			enclosure := ""
			for _, l := range entry.Links {
				if l.Href == "" || l.Rel == "enclosure" {
					if l.Rel == "enclosure" && strings.HasPrefix(strings.ToLower(l.Type), "image/") {
						enclosure = l.Href
					}
					continue
				}
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
				if link == "" {
					link = l.Href
				}
			}
			raw := firstNonEmpty(entry.Summary, entry.Content)
			image := enclosure
			if image == "" {
				image = embeddedImage(entry.Summary, entry.Content)
			}
			feed.Entries = append(feed.Entries, feedEntry{
				ID:      entry.ID,
				Title:   entry.Title,
				Link:    link,
				Time:    parseTime(firstNonEmpty(entry.Updated, entry.Published)),
				Summary: cleanSummary(raw),
				Image:   image,
			})
		}
	default:
		// RDF（RSS 1.0）的 item 直接挂在根节点。
		feed.Title = strings.TrimSpace(doc.Title)
		for _, item := range doc.Items {
			feed.Entries = append(feed.Entries, rssEntry(item))
		}
	}
	if feed.Title == "" {
		feed.Title = "RSS"
	}
	if len(feed.Entries) == 0 && doc.XMLName.Local == "" {
		return nil, errors.New("不是有效的 RSS/Atom 内容")
	}
	return feed, nil
}

// rssEntry 把 RSS/RDF item 转换为统一条目，附带摘要与首图。
func rssEntry(item xmlRSSItem) feedEntry {
	raw := firstNonEmpty(item.Description, item.Encoded)
	image := mediaImage(item.Enclosures, item.Thumbnails, item.MediaContents)
	if image == "" {
		image = embeddedImage(item.Description, item.Encoded)
	}
	return feedEntry{
		ID:      item.GUID,
		Title:   item.Title,
		Link:    strings.TrimSpace(item.Link),
		Time:    parseTime(item.PubDate),
		Summary: cleanSummary(raw),
		Image:   image,
	}
}

// cleanSummary 去除 HTML 标签、反转义并截断摘要。
func cleanSummary(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	text := tagRe.ReplaceAllString(raw, " ")
	text = html.UnescapeString(text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > summaryLimit {
		text = string(runes[:summaryLimit]) + "…"
	}
	return text
}

// mediaImage 从媒体节点中挑选首张图片。
func mediaImage(sets ...[]xmlMedia) string {
	for _, set := range sets {
		for _, media := range set {
			if media.URL == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(media.Type), "image/") || looksImage(media.URL) {
				return media.URL
			}
		}
	}
	return ""
}

// embeddedImage 从正文 HTML 中提取首张图片。
func embeddedImage(contents ...string) string {
	for _, content := range contents {
		if m := imgRe.FindStringSubmatch(content); m != nil {
			return html.UnescapeString(m[1])
		}
	}
	return ""
}

// looksImage 按扩展名判断 URL 是否指向图片。
func looksImage(rawURL string) bool {
	lower := strings.ToLower(strings.SplitN(rawURL, "?", 2)[0])
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

var (
	tagRe = regexp.MustCompile(`<[^>]*>`)
	imgRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)
)

// 兼容常见时间格式。
var timeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// parseTime 依次尝试已知布局，失败返回零值。
func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

// normalizeRoute 校验并整理路由。
func normalizeRoute(arg string) (string, error) {
	route := strings.TrimSpace(arg)
	if route == "" {
		return "", errors.New("empty route")
	}
	route = strings.TrimSpace(strings.TrimPrefix(route, "rss"))
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	if len(route) > 200 || !routeRe.MatchString(route) {
		return "", errors.New("invalid route")
	}
	return route, nil
}

// seenIDs 取前 n 条条目标识。
func seenIDs(feed *parsedFeed, n int) []string {
	ids := make([]string, 0, n)
	for i, entry := range feed.Entries {
		if i >= n {
			break
		}
		if id := entry.id(); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// loadStore 读取状态，保证 map 已初始化。
func loadStore(ctx context.Context, c sdk.Call) (*rssStore, error) {
	st := &rssStore{}
	if err := sdk.LoadJSON(ctx, c.State, stateKey, st); err != nil {
		return nil, err
	}
	if st.Groups == nil {
		st.Groups = map[string][]rssSub{}
	}
	if st.Pulled == nil {
		st.Pulled = map[string]int64{}
	}
	return st, nil
}

// firstNonEmpty 返回第一个非空值。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

// isAdmin 使用插件配置中的 admin_users 做授权。
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

// adminUsers 兼容 []string 与 []any 两种配置形态。
func adminUsers(config map[string]any) []string {
	if config == nil {
		return nil
	}
	switch v := config["admin_users"].(type) {
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
	default:
		return nil
	}
}

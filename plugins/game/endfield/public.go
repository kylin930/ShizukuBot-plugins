package endfield

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

func init() {
	registerRoute(100, prefixMatch("帮助", "help"), handleHelp)
	registerRoute(100, exactMatch("蓝图"), handleBluemap)
	registerRoute(100, exactMatch("maa 教程", "maa教程"), handleMaaTutorial)
	registerRoute(90, exactMatch("公告最新"), handleAnnouncementLatest)
	registerRoute(90, prefixMatch("公告"), handleAnnouncementDetail)
	registerRoute(80, exactMatch("公告"), handleAnnouncementList)
	registerRoute(90, exactMatch("订阅公告 群聊", "公告订阅 群聊"), handleAnnouncementSubscribe)
	registerRoute(90, exactMatch("取消订阅公告", "公告取消订阅"), handleAnnouncementUnsubscribe)
	registerRoute(90, exactMatch("日历"), handleCalendar)
	registerRoute(95, exactMatch("攻略列表"), handleStrategyList)
	registerRoute(85, func(text string) (string, bool) {
		if strings.HasSuffix(text, "攻略") && text != "攻略" {
			return strings.TrimSuffix(text, "攻略"), true
		}
		return "", false
	}, handleStrategyQuery)
	registerRoute(96, prefixMatch("上传攻略", "攻略上传"), handleStrategyUpload)
	registerRoute(90, withPrefixMatch("wiki"), handleWiki)
}

// ---------- 帮助 / 蓝图 / 教程 ----------

func handleHelp(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	var help struct {
		HelpGroup []struct {
			Type  string `json:"type"`
			Group string `json:"group"`
			Items []struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"items"`
			List []struct {
				Title string `json:"title"`
				Desc  string `json:"desc"`
			} `json:"list"`
		} `json:"help_group"`
	}
	if err := loadAssetJSON("defSet/help.json", &help); err != nil {
		return replyText(ctx, c, getMessage("help.fallback_title"))
	}
	replace := func(text string) string {
		text = strings.ReplaceAll(text, "{prefix}", ":")
		text = strings.ReplaceAll(text, "{bluemap_help}", getMessage("bluemap_help_doc"))
		text = strings.ReplaceAll(text, "{official_website}", getMessage("official_website"))
		return text
	}
	lines := []string{getMessage("help.title"), getMessage("prefixTips"), ""}
	for _, group := range help.HelpGroup {
		if group.Type == "tips" {
			for _, item := range group.Items {
				lines = append(lines, "【"+item.Title+"】"+replace(item.Text))
			}
			lines = append(lines, "")
			continue
		}
		if group.Group != "" && len(group.List) == 0 {
			lines = append(lines, replace(group.Group), "")
			continue
		}
		lines = append(lines, "【"+replace(group.Group)+"】")
		for _, item := range group.List {
			line := "· " + replace(item.Title)
			if item.Desc != "" {
				line += "　" + replace(item.Desc)
			}
			lines = append(lines, line)
		}
		lines = append(lines, "")
	}
	for _, segment := range splitMessage(strings.Join(lines, "\n"), 1800) {
		if err := replyText(ctx, c, segment); err != nil {
			return err
		}
	}
	return nil
}

func handleBluemap(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	doc := getMessage("bluemap_help_doc")
	if doc == "" {
		return replyText(ctx, c, getMessage("bluemap.not_configured"))
	}
	return replyText(ctx, c, getMessage("bluemap.doc_url", map[string]any{"url": doc}))
}

var maaTutorialSteps = []struct {
	text  string
	image string
}{
	{"MaaEnd 远程控制教程\n通过 QQ 机器人远程控制 MaaEnd Client，实现终末地自动化任务管理", ""},
	{"=== 第一步：下载 MaaEnd Client ===\n前往 GitHub 下载最新版本：\nhttps://github.com/Entropy-Increase-Team/MaaEnd-Client/releases\n或者蓝奏云：https://wwaln.lanzoum.com/b0sygztzi 密码:maac\n将下载好的 MaaEnd Client 放入 MaaEnd 文件夹下，双击运行即可", "1.png"},
	{"=== 第二步：绑定设备 ===\n1. 私聊机器人发送【:maa 绑定】获取绑定码", "2.png"},
	{"2. 在 MaaEnd Client 中输入绑定码完成绑定", "3.png"},
	{"3. 发送【:maa 设备】确认设备已上线", "4.png"},
	{"=== 第三步：设置默认设备 ===\n发送【:maa 设置设备 1】设置默认设备\n设置后，后续命令可省略设备序号", "5.png"},
	{"=== 第四步：查看并执行任务 ===\n发送【:maa 任务列表】查看可用任务", "6.png"},
	{"发送【:maa 执行 <序号>】执行任务\n任务下发后会自动附带截图，完成后也会推送通知", "7.png"},
	{"", "8.png"},
}

func handleMaaTutorial(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	for _, step := range maaTutorialSteps {
		if step.text != "" {
			if err := replyText(ctx, c, step.text); err != nil {
				return err
			}
		}
		if step.image != "" {
			if data, err := asset("resources/tutorialmaa/" + step.image); err == nil {
				if err := c.ReplyImageBytes(ctx, data); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ---------- 公告 ----------

type announcementItem struct {
	ItemID        any             `json:"item_id"`
	Title         string          `json:"title"`
	PublishedAtTS any             `json:"published_at_ts"`
	Images        json.RawMessage `json:"images"`
	Texts         []struct {
		Content string `json:"content"`
	} `json:"texts"`
	Content struct {
		Blocks []struct {
			Kind string          `json:"kind"`
			Text json.RawMessage `json:"text"`
		} `json:"blocks"`
	} `json:"content"`
}

func announcementTime(item announcementItem) string {
	ts := anyInt(item.PublishedAtTS, 0)
	if ts <= 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("01-02 15:04")
}

func announcementCover(item announcementItem) string {
	if len(item.Images) == 0 {
		return ""
	}
	var images []json.RawMessage
	if err := json.Unmarshal(item.Images, &images); err != nil || len(images) == 0 {
		return ""
	}
	var direct string
	if err := json.Unmarshal(images[0], &direct); err == nil && direct != "" {
		return direct
	}
	var object struct {
		URL          string `json:"url"`
		DisplayInfos []struct {
			URL string `json:"url"`
		} `json:"display_infos"`
	}
	if err := json.Unmarshal(images[0], &object); err == nil {
		if object.URL != "" {
			return object.URL
		}
		if len(object.DisplayInfos) > 0 {
			return object.DisplayInfos[0].URL
		}
	}
	return ""
}

func announcementContent(item announcementItem) string {
	if len(item.Texts) > 0 {
		parts := make([]string, 0, len(item.Texts))
		for _, text := range item.Texts {
			if strings.TrimSpace(text.Content) != "" {
				parts = append(parts, text.Content)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	parts := make([]string, 0, len(item.Content.Blocks))
	for _, block := range item.Content.Blocks {
		if block.Kind != "text" || len(block.Text) == 0 {
			continue
		}
		var text string
		if err := json.Unmarshal(block.Text, &text); err == nil {
			parts = append(parts, text)
			continue
		}
		var object struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block.Text, &object); err == nil && object.Text != "" {
			parts = append(parts, object.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func announcementCard(item announcementItem) string {
	title := item.Title
	if title == "" {
		title = getMessage("announcement.title_unknown")
	}
	cover := announcementCover(item)
	imageURL := ""
	if cover != "" {
		imageURL = cover
	}
	content := announcementContent(item)
	if content == "" {
		content = "（暂无正文）"
	}
	body := ""
	if imageURL != "" {
		body += `<img class="cover" src="` + htmlEscape(imageURL) + `" alt="">`
	}
	body += `<div class="section"><h2>` + htmlEscape(getMessage("announcement.time_label")) + htmlEscape(announcementTime(item)) + `</h2><div class="body-text">` + htmlEscape(content) + `</div></div>`
	return buildPage(pageOptions{title: title, eyebrow: "终末地 · 官方公告", seal: "公告", body: body, width: 720})
}

func handleAnnouncementList(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	client := newAPIClient(cfg, binding{})
	res, err := client.getAnnouncementsData(ctx, "announcements_list", map[string]any{"page": 1, "page_size": 5})
	if err != nil || !res.ok() {
		return replyText(ctx, c, getMessage("announcement.list_failed"))
	}
	var data struct {
		List  []announcementItem `json:"list"`
		Total int                `json:"total"`
	}
	if err := res.decode(&data); err != nil || len(data.List) == 0 {
		return replyText(ctx, c, getMessage("announcement.no_list"))
	}
	lines := []string{getMessage("announcement.list_header")}
	for i, item := range data.List {
		title := item.Title
		if title == "" {
			title = getMessage("announcement.title_unknown")
		}
		lines = append(lines, fmt.Sprintf("%d. %s　%s", i+1, title, announcementTime(item)))
	}
	lines = append(lines, getMessage("announcement.list_footer_line1"))
	return replyText(ctx, c, strings.Join(lines, "\n"))
}

func handleAnnouncementDetail(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	index, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil || index < 1 {
		return handleAnnouncementList(ctx, c, cfg, "", "")
	}
	client := newAPIClient(cfg, binding{})
	listRes, err := client.getAnnouncementsData(ctx, "announcements_list", map[string]any{"page": 1, "page_size": maxInt(index, 20)})
	if err != nil || !listRes.ok() {
		return replyText(ctx, c, getMessage("announcement.list_failed"))
	}
	var list struct {
		List []announcementItem `json:"list"`
	}
	_ = listRes.decode(&list)
	if index > len(list.List) {
		return replyText(ctx, c, getMessage("announcement.detail_index_out", map[string]any{"count": len(list.List)}))
	}
	item := list.List[index-1]
	if id := anyString(item.ItemID); id != "" {
		if detailRes, err := client.getAnnouncementsData(ctx, "announcement_detail", map[string]any{"id": id}); err == nil && detailRes.ok() {
			var detail announcementItem
			if detailRes.decode(&detail) == nil {
				mergeAnnouncement(&item, detail)
			}
		}
	}
	return replyCard(ctx, c, announcementCard(item), 720, announcementContent(item))
}

func handleAnnouncementLatest(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	client := newAPIClient(cfg, binding{})
	res, err := client.getAnnouncementsData(ctx, "announcements_latest", nil)
	if err != nil || !res.ok() {
		return replyText(ctx, c, getMessage("announcement.latest_failed"))
	}
	var item announcementItem
	if err := res.decode(&item); err != nil {
		return replyText(ctx, c, getMessage("announcement.latest_failed"))
	}
	return replyCard(ctx, c, announcementCard(item), 720, announcementContent(item))
}

func mergeAnnouncement(dst *announcementItem, src announcementItem) {
	if src.Title != "" {
		dst.Title = src.Title
	}
	if len(src.Texts) > 0 {
		dst.Texts = src.Texts
	}
	if len(src.Content.Blocks) > 0 {
		dst.Content = src.Content
	}
	if len(src.Images) > 0 {
		dst.Images = src.Images
	}
	if anyInt(src.PublishedAtTS, 0) > 0 {
		dst.PublishedAtTS = src.PublishedAtTS
	}
}

type announcementSub struct {
	GroupID string `json:"group_id"`
	SinceTS int64  `json:"since_ts"`
}

func handleAnnouncementSubscribe(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if c.Event.GroupID == "" {
		return replyText(ctx, c, getMessage("announcement.subscribe_use_in_group"))
	}
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	var subs []announcementSub
	_ = sdk.LoadJSON(ctx, c.State, "endfield:announce:subs", &subs)
	for _, sub := range subs {
		if sub.GroupID == c.Event.GroupID {
			return replyText(ctx, c, getMessage("announcement.already_subscribed"))
		}
	}
	since := int64(0)
	client := newAPIClient(cfg, binding{})
	if res, err := client.getAnnouncementsData(ctx, "announcements_latest", nil); err == nil && res.ok() {
		var item announcementItem
		if res.decode(&item) == nil {
			since = int64(anyInt(item.PublishedAtTS, 0))
		}
	}
	subs = append(subs, announcementSub{GroupID: c.Event.GroupID, SinceTS: since})
	if err := sdk.SaveJSON(ctx, c.State, "endfield:announce:subs", subs); err != nil {
		return replyText(ctx, c, getMessage("announcement.list_failed"))
	}
	return replyText(ctx, c, getMessage("announcement.subscribe_ok"))
}

func handleAnnouncementUnsubscribe(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	if c.Event.GroupID == "" {
		return replyText(ctx, c, getMessage("announcement.unsubscribe_group_only"))
	}
	var subs []announcementSub
	_ = sdk.LoadJSON(ctx, c.State, "endfield:announce:subs", &subs)
	kept := make([]announcementSub, 0, len(subs))
	for _, sub := range subs {
		if sub.GroupID != c.Event.GroupID {
			kept = append(kept, sub)
		}
	}
	_ = sdk.SaveJSON(ctx, c.State, "endfield:announce:subs", kept)
	return replyText(ctx, c, getMessage("announcement.unsubscribe_ok"))
}

// ---------- 活动日历 ----------

type activityItem struct {
	Name     string `json:"name"`
	Desc     string `json:"description"`
	StartTS  any    `json:"activity_start_at_ts"`
	EndTS    any    `json:"activity_end_at_ts"`
	StartTS2 any    `json:"activityStartAtTs"`
	EndTS2   any    `json:"activityEndAtTs"`
	Pic      string `json:"pic"`
}

func handleCalendar(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	client := newAPIClient(cfg, binding{})
	res, err := client.getWikiData(ctx, "wiki_activities", nil)
	if err != nil || !res.ok() {
		return replyText(ctx, c, getMessage("activity.query_failed", map[string]any{"name": "日历"}))
	}
	var raw struct {
		Activities []activityItem `json:"activities"`
		List       []activityItem `json:"list"`
	}
	_ = res.decode(&raw)
	items := raw.Activities
	if len(items) == 0 {
		items = raw.List
	}
	if len(items) == 0 {
		var direct []activityItem
		if res.decode(&direct) == nil {
			items = direct
		}
	}
	if len(items) == 0 {
		return replyText(ctx, c, getMessage("activity.no_records"))
	}
	lines := []string{getMessage("activity.text_title_wrapped"), ""}
	for i, item := range items {
		name := item.Name
		if name == "" {
			name = "未知活动"
		}
		lines = append(lines, getMessage("activity.text_item_line", map[string]any{"index": i + 1, "name": name}))
		if item.Desc != "" {
			lines = append(lines, getMessage("activity.text_item_desc", map[string]any{"desc": item.Desc}))
		}
		start := anyInt(item.StartTS, anyInt(item.StartTS2, 0))
		end := anyInt(item.EndTS, anyInt(item.EndTS2, 0))
		if start > 0 {
			lines = append(lines, getMessage("activity.text_item_start", map[string]any{"time": time.Unix(int64(start), 0).Format("2006-01-02 15:04")}))
		}
		if end > 0 {
			lines = append(lines, getMessage("activity.text_item_end", map[string]any{"time": time.Unix(int64(end), 0).Format("2006-01-02 15:04")}))
		}
		lines = append(lines, "")
	}
	return replyText(ctx, c, strings.TrimSpace(strings.Join(lines, "\n")))
}

// ---------- 攻略 ----------

func strategyDirNames() []string {
	entries, err := fs.ReadDir(assetsFS, "assets/strategy")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func strategyImages(name string) []struct {
	Path   string
	Author string
} {
	safe := sanitizeName(name)
	dir := "assets/strategy/" + safe
	entries, err := fs.ReadDir(assetsFS, dir)
	if err != nil {
		return nil
	}
	out := make([]struct {
		Path   string
		Author string
	}, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isImageName(entry.Name()) {
			continue
		}
		out = append(out, struct {
			Path   string
			Author string
		}{path.Join("strategy", safe, entry.Name()), strategyAuthor(entry.Name())})
	}
	return out
}

func isImageName(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func sanitizeName(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")
	value := strings.TrimSpace(replacer.Replace(name))
	if value == "" {
		return "unknown"
	}
	return value
}

func strategyAuthor(filename string) string {
	base := strings.TrimSuffix(filename, path.Ext(filename))
	parts := strings.Split(base, "_")
	if len(parts) >= 2 {
		if _, err := strconv.ParseInt(parts[len(parts)-1], 10, 64); err == nil {
			return strings.Join(parts[:len(parts)-1], "_")
		}
	}
	return base
}

func handleStrategyList(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	var lines []string
	if names := strategyDirNames(); len(names) > 0 {
		lines = append(lines, "【默认攻略】\n"+strings.Join(names, "、"))
	}
	if cfg.hasAPIKey() && cfg.UseWikiStrategy {
		client := newAPIClient(cfg, binding{})
		if res, err := client.getWikiData(ctx, "wiki_items", map[string]any{
			"main_type_id": "2", "sub_type_id": "11", "page": 1, "page_size": 200,
		}); err == nil && res.ok() {
			var data struct {
				Items []struct {
					Name string `json:"name"`
				} `json:"items"`
			}
			_ = res.decode(&data)
			var names []string
			for _, item := range data.Items {
				name := strings.TrimSpace(strings.TrimPrefix(item.Name, "【玩家攻略】"))
				if name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				lines = append(lines, "【Wiki 干员攻略】\n"+strings.Join(names, "、"))
			}
		}
	}
	if len(lines) == 0 {
		return replyText(ctx, c, getMessage("strategy.list_empty"))
	}
	return replyText(ctx, c, getMessage("strategy.list_header")+"\n\n"+strings.Join(lines, "\n\n"))
}

func handleStrategyQuery(ctx context.Context, c sdk.Call, cfg config, name string, _ string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return replyText(ctx, c, getMessage("strategy.provide_name", map[string]any{"prefix": ":"}))
	}
	images := strategyImages(name)
	if !cfg.UseWikiStrategy {
		return sendStrategyImages(ctx, c, name, images)
	}
	if !cfg.hasAPIKey() {
		if len(images) > 0 {
			return sendStrategyImages(ctx, c, name, images)
		}
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	client := newAPIClient(cfg, binding{})
	listRes, err := client.getWikiData(ctx, "wiki_items", map[string]any{
		"main_type_id": "2", "sub_type_id": "11", "page": 1, "page_size": 100,
	})
	if err != nil || !listRes.ok() {
		return replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": "接口异常"}))
	}
	var list struct {
		Items []struct {
			Name   string `json:"name"`
			ItemID any    `json:"item_id"`
		} `json:"items"`
	}
	_ = listRes.decode(&list)
	var matched []struct {
		Name   string
		ItemID any
	}
	for _, item := range list.Items {
		trimmed := strings.TrimSpace(strings.TrimPrefix(item.Name, "【玩家攻略】"))
		if trimmed == name {
			matched = append([]struct {
				Name   string
				ItemID any
			}{{trimmed, item.ItemID}}, matched...)
		} else if strings.Contains(trimmed, name) || strings.Contains(name, trimmed) {
			matched = append(matched, struct {
				Name   string
				ItemID any
			}{trimmed, item.ItemID})
		}
	}
	if len(matched) == 0 {
		if len(images) > 0 {
			return sendStrategyImages(ctx, c, name, images)
		}
		return replyText(ctx, c, getMessage("strategy.not_found", map[string]any{"name": name}))
	}
	detailRes, err := client.getWikiData(ctx, "wiki_item_detail", map[string]any{"id": anyString(matched[0].ItemID)})
	if err != nil || !detailRes.ok() {
		return replyText(ctx, c, getMessage("strategy.detail_failed", map[string]any{"name": matched[0].Name}))
	}
	var detail struct {
		Item *struct {
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
			Cover   string          `json:"cover"`
		} `json:"item"`
		Name    string          `json:"name"`
		Content json.RawMessage `json:"content"`
		Cover   string          `json:"cover"`
	}
	_ = detailRes.decode(&detail)
	content := detail.Content
	title := detail.Name
	cover := detail.Cover
	if detail.Item != nil {
		content = detail.Item.Content
		if detail.Item.Name != "" {
			title = detail.Item.Name
		}
		cover = detail.Item.Cover
	}
	if title == "" {
		title = matched[0].Name
	}
	text, imageURLs := wikiContentText(content)
	message := "【" + title + "】"
	if text != "" {
		message += "\n" + text
	}
	message = strings.TrimSpace(message)
	if cover != "" {
		message += "\n" + cover
	}
	for _, segment := range splitMessage(message, 1800) {
		if err := replyText(ctx, c, segment); err != nil {
			return err
		}
	}
	for _, imageURL := range imageURLs {
		if err := c.ReplyImage(ctx, imageURL); err != nil {
			return err
		}
	}
	return nil
}

func sendStrategyImages(ctx context.Context, c sdk.Call, name string, images []struct {
	Path   string
	Author string
}) error {
	if len(images) == 0 {
		return replyText(ctx, c, getMessage("strategy.not_found", map[string]any{"name": name})+"\n"+getMessage("strategy.not_found_suffix"))
	}
	for _, image := range images {
		if err := replyText(ctx, c, "【作者】"+image.Author); err != nil {
			return err
		}
		data, err := asset(image.Path)
		if err != nil {
			continue
		}
		if err := c.ReplyImageBytes(ctx, data); err != nil {
			return err
		}
	}
	return nil
}

type storedStrategy struct {
	Author string `json:"author"`
	Data   string `json:"data"`
	Mime   string `json:"mime"`
	Time   int64  `json:"time"`
}

func handleStrategyUpload(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !cfg.isAdmin(c.Event.UserID) {
		return replyText(ctx, c, "仅管理员可上传攻略。")
	}
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return replyText(ctx, c, getMessage("strategy.upload_format"))
	}
	name, author := fields[0], fields[1]
	url := ""
	if len(fields) >= 3 && strings.HasPrefix(fields[2], "http") {
		url = fields[2]
	}
	if url == "" {
		if urls := sdk.ImageURLs(c.Event); len(urls) > 0 {
			url = urls[0]
		}
	}
	if url == "" {
		return replyText(ctx, c, getMessage("strategy.upload_need_image"))
	}
	data, err := downloadBytes(ctx, url)
	if err != nil {
		return replyText(ctx, c, getMessage("strategy.upload_download_failed", map[string]any{"status": err}))
	}
	mime := detectImageMime(data)
	if mime == "" {
		return replyText(ctx, c, getMessage("strategy.upload_need_image"))
	}
	key := "endfield:strategy:" + sanitizeName(name)
	var list []storedStrategy
	_ = sdk.LoadJSON(ctx, c.State, key, &list)
	list = append(list, storedStrategy{Author: sanitizeName(author), Data: encodeBase64(data), Mime: mime, Time: time.Now().UnixMilli()})
	if err := sdk.SaveJSON(ctx, c.State, key, list); err != nil {
		return replyText(ctx, c, getMessage("strategy.upload_save_failed"))
	}
	return replyText(ctx, c, getMessage("strategy.upload_saved", map[string]any{"path": sanitizeName(name) + "/" + sanitizeName(author)}))
}

// ---------- Wiki ----------

var wikiSubTypes = map[string]string{
	"干员": "1", "武器": "2", "威胁": "3", "装备": "4",
	"设备": "5", "物品": "6", "武器基质": "7", "任务": "8", "活动": "9",
}

func handleWiki(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	args = strings.TrimSpace(args)
	subType := "1"
	name := args
	for label, id := range wikiSubTypes {
		if strings.HasPrefix(args, label) {
			subType = id
			name = strings.TrimSpace(strings.TrimPrefix(args, label))
			break
		}
	}
	if name == "" {
		return replyText(ctx, c, getMessage("wiki.provide_content"))
	}
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	typeLabel := "百科"
	for label, id := range wikiSubTypes {
		if id == subType {
			typeLabel = label
		}
	}
	client := newAPIClient(cfg, binding{})
	res, err := client.getWikiData(ctx, "wiki_items", map[string]any{
		"main_type_id": "1", "sub_type_id": subType, "page": 1, "page_size": 100,
	})
	if err != nil || !res.ok() {
		return replyText(ctx, c, getMessage("wiki.query_failed", map[string]any{"name": name}))
	}
	var list struct {
		Items []struct {
			Name   string `json:"name"`
			ItemID any    `json:"item_id"`
		} `json:"items"`
	}
	_ = res.decode(&list)
	var match *struct {
		Name   string
		ItemID any
	}
	for _, item := range list.Items {
		if item.Name == name {
			copyItem := struct {
				Name   string
				ItemID any
			}{item.Name, item.ItemID}
			match = &copyItem
			break
		}
	}
	if match == nil {
		for _, item := range list.Items {
			if strings.Contains(item.Name, name) || strings.Contains(name, item.Name) {
				copyItem := struct {
					Name   string
					ItemID any
				}{item.Name, item.ItemID}
				match = &copyItem
				break
			}
		}
	}
	if match == nil && len(list.Items) > 0 {
		match = &struct {
			Name   string
			ItemID any
		}{list.Items[0].Name, list.Items[0].ItemID}
	}
	if match == nil {
		return replyText(ctx, c, getMessage("wiki.not_found", map[string]any{"name": name, "type_label": typeLabel}))
	}
	detailRes, err := client.getWikiData(ctx, "wiki_item_detail", map[string]any{"id": anyString(match.ItemID)})
	if err != nil || !detailRes.ok() {
		return replyText(ctx, c, getMessage("wiki.detail_failed", map[string]any{"name": match.Name}))
	}
	var detail struct {
		Name    string          `json:"name"`
		Cover   string          `json:"cover"`
		Caption json.RawMessage `json:"caption"`
		Content json.RawMessage `json:"content"`
	}
	_ = detailRes.decode(&detail)
	text, imageURLs := wikiContentText(detail.Content)
	if detail.Name == "" {
		detail.Name = match.Name
	}
	message := "【" + typeLabel + "】" + detail.Name
	if text != "" {
		message += "\n" + text
	}
	if detail.Cover != "" {
		message += "\n" + detail.Cover
	}
	for _, segment := range splitMessage(message, 1800) {
		if err := replyText(ctx, c, segment); err != nil {
			return err
		}
	}
	for _, imageURL := range imageURLs {
		if err := c.ReplyImage(ctx, imageURL); err != nil {
			return err
		}
	}
	return nil
}

// wikiContentText 从 Wiki content.document_map 中按顺序抽取文字与图片。
func wikiContentText(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var content struct {
		DocumentMap map[string]struct {
			BlockIDs []string `json:"block_ids"`
			BlockMap map[string]struct {
				Kind  string          `json:"kind"`
				Text  json.RawMessage `json:"text"`
				Image struct {
					URL string `json:"url"`
					Src string `json:"src"`
				} `json:"image"`
			} `json:"block_map"`
		} `json:"document_map"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		return "", nil
	}
	var lines []string
	var images []string
	seen := map[string]bool{}
	for _, doc := range content.DocumentMap {
		for _, id := range doc.BlockIDs {
			block, ok := doc.BlockMap[id]
			if !ok {
				continue
			}
			switch block.Kind {
			case "text":
				text := blockText(block.Text)
				if text != "" && !seen[text] {
					seen[text] = true
					lines = append(lines, text)
				}
			case "image":
				source := block.Image.URL
				if source == "" {
					source = block.Image.Src
				}
				if strings.HasPrefix(source, "http") {
					images = append(images, source)
				}
			}
		}
	}
	return strings.Join(lines, "\n"), images
}

func blockText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object struct {
		InlineElements []json.RawMessage `json:"inline_elements"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	var b strings.Builder
	for _, item := range object.InlineElements {
		var element struct {
			Kind  string          `json:"kind"`
			Text  json.RawMessage `json:"text"`
			Entry struct {
				Name string `json:"name"`
			} `json:"entry"`
			Link struct {
				Text string `json:"text"`
			} `json:"link"`
		}
		if json.Unmarshal(item, &element) != nil {
			continue
		}
		switch element.Kind {
		case "text":
			var value string
			if json.Unmarshal(element.Text, &value) == nil {
				b.WriteString(value)
			} else {
				var nested struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(element.Text, &nested) == nil {
					b.WriteString(nested.Text)
				}
			}
		case "entry":
			b.WriteString(element.Entry.Name)
		case "link":
			b.WriteString(element.Link.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// ---------- 通用工具 ----------

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func downloadBytes(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("unsupported url")
	}
	req, err := newRequestWithContext(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Package bilibili B 站成分与弹幕查询：查成分、查弹幕、用户与 VUP 资料。
package bilibili

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// maxComponentRows 成分列表最多展示的 UP 主数量，与上游一致。
	maxComponentRows = 50
	// cookieStateKey 保存「设置b站cookie」写入的 Cookie。
	cookieStateKey = "cookie"
	// vupTTL vtbs.moe 列表的缓存时间。
	vupTTL = 10 * time.Minute
	// vupStateKey VUP 列表在插件 state 中的键名。
	vupStateKey = "vup_list"
	// pageSize 弹幕接口固定的每页直播场次数。
	pageSize = 5
)

var (
	// 下列地址均为包级变量，测试中替换为 httptest 服务。
	cardURL    = "https://api.bilibili.com/x/web-interface/card?mid=%d"
	vtbURL     = "https://api.vtbs.moe/v1/detail/%d"
	vtbListURL = "https://api.vtbs.moe/v1/short"
	medalURL   = "https://api.live.bilibili.com/xlive/web-ucenter/user/MedalWall?target_id=%d"
	danmakuURL = "https://ukamnads.icu/api/v2/user?uId=%d&pageNum=%d&pageSize=5&target=-1&useEmoji=true"
	searchURL  = "https://api.bilibili.com/x/web-interface/wbi/search/type"
	navURL     = "https://api.bilibili.com/x/web-interface/nav"
	warmURL    = "https://www.bilibili.com/"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	biliClient = newClient()
	wbi        = &wbiCache{}
)

// 上游的触发词，保持原样（昵称与 UID 都交给 RequireUser 处理）。
var (
	userInfoRe  = regexp.MustCompile(`^>user info\s?(.{1,25})$`)
	vupInfoRe   = regexp.MustCompile(`^>vup info\s?(.{1,25})$`)
	componentRe = regexp.MustCompile(`^查成分\s?(.{1,25})$`)
	danmakuRe   = regexp.MustCompile(`^查弹幕\s?(\S{1,25})(?:\s+(\S+))?$`)
	cookieRe    = regexp.MustCompile(`^设置b站cookie?\s+(.*)$`)
	numberRe    = regexp.MustCompile(`^\d+$`)
)

// danmakuTypeMap 弹幕类型与中文名称的对应关系，与上游一致。
var danmakuTypeMap = map[int64]string{
	0: "普通消息",
	1: "礼物",
	2: "上舰",
	3: "Superchat",
	4: "进入直播间",
	5: "标题变动",
	6: "分区变动",
	7: "直播中止",
	8: "直播继续",
}

// Definition subscribes to the B 站成分与弹幕指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "bilibili",
		Name:        "B站成分查询",
		Version:     "1.1.0",
		Description: "发送「查成分 2」「查弹幕 2」查询 UP 主成分与直播弹幕，结果以图片卡片发送；也支持「>user info」「>vup info」查询资料",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "bilibili", Trigger: "查成分"},
	},
		sdk.WithPermissions("state.read", "state.write"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"cookie": {
					Type:        "string",
					Description: "可选：B 站登录 Cookie，用于昵称搜索与牌子墙；不填时对应功能会提示",
					Default:     json.RawMessage(`""`),
				},
				"admin_users": {
					Type:        "array",
					Description: "允许执行「更新vup」的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle 路由各条 B 站指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	if sdk.MatchFull(text, "b站成分帮助") || sdk.MatchFull(text, "B站成分帮助") {
		return c.Reply(ctx, helpText)
	}
	if sdk.MatchFull(text, "更新vup") || sdk.MatchFull(text, "更新VUP") {
		return updateVup(ctx, c)
	}
	if m := sdk.MatchRegexp(text, cookieRe); m != nil {
		return setCookie(ctx, c, m[1])
	}
	if sdk.MatchFull(text, "设置b站cookie") {
		return c.Reply(ctx, "用法：设置b站cookie <cookie>")
	}
	// 参数缺失时给出用法，避免用户以为机器人没反应。
	if sdk.MatchFull(text, ">user info") {
		return c.Reply(ctx, "用法：>user info <UID或昵称>")
	}
	if sdk.MatchFull(text, ">vup info") {
		return c.Reply(ctx, "用法：>vup info <UID或昵称>")
	}
	if sdk.MatchFull(text, "查成分") {
		return c.Reply(ctx, "用法：查成分 <UID或昵称>")
	}
	if sdk.MatchFull(text, "查弹幕") {
		return c.Reply(ctx, "用法：查弹幕 <UID或昵称> [页码]")
	}
	if m := sdk.MatchRegexp(text, userInfoRe); m != nil {
		return queryUser(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, vupInfoRe); m != nil {
		return queryVup(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, componentRe); m != nil {
		return queryComponent(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, danmakuRe); m != nil {
		return queryDanmaku(ctx, c, m[1], m[2])
	}
	return nil
}

const helpText = "B 站成分 / 弹幕指令：\n" +
	">user info <UID或昵称> — 查看用户资料\n" +
	">vup info <UID或昵称> — 查看 VUP 详情（vtbs.moe）\n" +
	"查成分 <UID或昵称> — 统计关注里的 VUP 成分\n" +
	"查弹幕 <UID或昵称> [页码] — 查看直播弹幕记录（默认第 0 页）\n" +
	"更新vup — 管理员刷新 vtbs.moe 列表缓存\n" +
	"设置b站cookie <cookie> — 管理员保存 B 站 Cookie（不回显）\n" +
	"提示：昵称搜索与牌子墙需要 Cookie，也可在插件配置的 cookie 中填写。"

const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

// queryUser 查询用户名片。
func queryUser(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := resolveUID(ctx, c, arg)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	card, err := fetchCard(ctx, uid, cookieOf(ctx, c))
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	user := card.Data.Card
	name := firstNonEmpty(user.Name, "未知用户")
	lines := []string{
		"【B站用户】" + name,
		"UID：" + strconv.FormatInt(uid, 10),
	}
	if user.Sex != "" {
		lines = append(lines, "性别："+user.Sex)
	}
	if level := user.LevelInfo.CurrentLevel; level > 0 {
		lines = append(lines, "等级：Lv"+strconv.Itoa(level))
	}
	if user.Birthday != "" {
		lines = append(lines, "生日："+user.Birthday)
	}
	if sign := strings.TrimSpace(user.Sign); sign != "" {
		lines = append(lines, "签名："+clip(sign, 100))
	}
	if user.Fans > 0 || user.Attention > 0 {
		lines = append(lines, fmt.Sprintf("粉丝：%s ｜ 关注：%s", formatCount(user.Fans), formatCount(user.Attention)))
	}
	lines = append(lines, fmt.Sprintf("https://space.bilibili.com/%d", uid))
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// queryVup 查询 vtbs.moe 收录的 VUP 详情。
func queryVup(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := resolveUID(ctx, c, arg)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	detail, err := fetchVtbDetail(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	lines := []string{
		"【VUP详情】" + firstNonEmpty(detail.Uname, strconv.FormatInt(uid, 10)),
		"B站ID：" + strconv.FormatInt(uid, 10),
		"粉丝：" + formatCount(detail.Follower),
		"24h涨粉：" + strconv.Itoa(detail.Rise),
		"视频投稿：" + strconv.Itoa(detail.Video),
	}
	if detail.Roomid > 0 {
		lines = append(lines, "直播间："+strconv.FormatInt(detail.Roomid, 10))
	}
	lines = append(lines,
		"舰队："+strconv.Itoa(detail.GuardNum),
		"直播总排名："+strconv.Itoa(detail.AreaRank),
		"数据来源：https://vtbs.moe/detail/"+strconv.FormatInt(uid, 10),
		"获取时间："+time.Now().Format("2006-01-02 15:04:05"),
	)
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// queryComponent 统计用户关注里的 VUP 成分，并用牌子墙补充展示。
func queryComponent(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := resolveUID(ctx, c, arg)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	cookie := cookieOf(ctx, c)
	card, err := fetchCard(ctx, uid, cookie)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	user := card.Data.Card
	name := firstNonEmpty(user.Name, strconv.FormatInt(uid, 10))
	attentions := user.Attentions

	// vtbs.moe 列表用于判断关注里哪些是 VUP，获取失败时不影响名片展示。
	list, listErr := loadVupEntries(ctx, c, false)
	vupMap := make(map[int64]vupEntry, len(list))
	for _, item := range list {
		if item.Mid > 0 {
			vupMap[item.Mid] = item
		}
	}

	medals, medalErr := fetchMedals(ctx, uid, cookie)
	if medalErr == nil {
		sort.Slice(medals, func(i, j int) bool { return medals[i].MedalInfo.Level > medals[j].MedalInfo.Level })
	}

	view := componentView{
		Name:       name,
		UID:        uid,
		Fans:       formatCount(user.Fans),
		Attention:  formatCount(len(attentions)),
		RegTime:    componentRegTime(user.Regtime),
		QueryTime:  time.Now().Format("2006-01-02 15:04:05"),
		SpaceURL:   fmt.Sprintf("https://space.bilibili.com/%d", uid),
		MedalCount: len(medals),
	}
	if level := user.LevelInfo.CurrentLevel; level > 0 {
		view.Level = "Lv" + strconv.Itoa(level)
	}
	if user.Face != "" {
		view.Face = safeURL(inlineImage(ctx, user.Face))
	}

	seen := make(map[int64]bool)
	hit := 0
	for _, medal := range medals {
		mid := medal.MedalInfo.TargetID
		if mid <= 0 || seen[mid] {
			continue
		}
		seen[mid] = true
		entry := vupMap[mid]
		row := componentRow{
			Uname:       firstNonEmpty(medal.TargetName, entry.Uname, strconv.FormatInt(mid, 10)),
			UID:         mid,
			VUP:         entry.Mid > 0,
			MedalName:   medal.MedalInfo.MedalName,
			MedalLevel:  medal.MedalInfo.Level,
			MedalStart:  medalColor(medal.MedalInfo.MedalColorStart),
			MedalEnd:    medalColor(medal.MedalInfo.MedalColorEnd),
			MedalBorder: medalColor(medal.MedalInfo.MedalColorBorder),
		}
		if medal.MedalInfo.IconURL != "" {
			row.MedalIcon = safeURL(inlineImage(ctx, medal.MedalInfo.IconURL))
		}
		view.Rows = append(view.Rows, row)
	}
	for _, mid := range attentions {
		entry, ok := vupMap[mid]
		if !ok {
			continue
		}
		hit++
		if seen[mid] {
			continue
		}
		seen[mid] = true
		view.Rows = append(view.Rows, componentRow{Uname: entry.Uname, UID: mid, VUP: true})
	}
	view.Hit, view.Total = hit, len(attentions)

	// 统计结论放在明细之前，列表接口失败时明确说明无法统计。
	switch {
	case len(attentions) == 0:
		view.Percent = "无法统计"
		view.Notices = append(view.Notices, "关注列表为空或接口未返回关注数据")
	case listErr != nil:
		view.Percent = "无法统计"
		view.Notices = append(view.Notices, fmt.Sprintf("关注 %d 位，但 vtbs.moe 列表获取失败，无法统计成分", len(attentions)))
		view.Notices = append(view.Notices, "vtbs.moe 列表获取失败："+listErr.Error())
	default:
		pct := float64(hit) / float64(len(attentions)) * 100
		view.Percent = fmt.Sprintf("%.2f%%（%d/%d）", pct, hit, len(attentions))
		if hit == 0 {
			view.Notices = append(view.Notices, "关注的 UP 主里没有收录在 vtbs.moe 的 VUP")
		}
	}
	if cookie == "" {
		view.Notices = append(view.Notices, "未配置 cookie，粉丝牌墙已跳过（需要登录才能获取，成分统计仍基于 VUP 列表）")
	} else if medalErr != nil {
		view.Notices = append(view.Notices, "牌子墙获取失败："+medalErr.Error())
	}
	if len(view.Rows) > maxComponentRows {
		view.Rows = view.Rows[:maxComponentRows]
		view.Truncated = true
	}

	fallback := componentText(view)
	return replyRendered(ctx, c, func(_ int) ([]byte, error) {
		return buildComponentHTML(view)
	}, fallback)
}

// componentRegTime 格式化注册时间。
func componentRegTime(sec int64) string {
	if sec <= 0 {
		return "未知"
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04:05")
}

// componentText 生成查成分的纯文本回退内容。
func componentText(view componentView) string {
	lines := []string{
		fmt.Sprintf("【B站成分】%s（UID %d）", view.Name, view.UID),
		fmt.Sprintf("粉丝：%s ｜ 关注：%s", view.Fans, view.Attention),
	}
	if view.Level != "" {
		lines = append(lines, "等级："+view.Level)
	}
	if view.RegTime != "" && view.RegTime != "未知" {
		lines = append(lines, "注册日期："+view.RegTime)
	}
	lines = append(lines, "查询日期："+view.QueryTime)
	lines = append(lines, "管人痴成分："+view.Percent)
	lines = append(lines, view.Notices...)
	for _, row := range view.Rows {
		if row.MedalName != "" {
			lines = append(lines, fmt.Sprintf("· %s（UID %d）牌子：%s %d级", row.Uname, row.UID, row.MedalName, row.MedalLevel))
			continue
		}
		lines = append(lines, fmt.Sprintf("· %s（UID %d）", row.Uname, row.UID))
	}
	if view.Truncated {
		lines = append(lines, fmt.Sprintf("（仅展示前 %d 位）", maxComponentRows))
	}
	lines = append(lines, view.SpaceURL)
	return strings.Join(lines, "\n")
}

// queryDanmaku 查询用户最近的直播弹幕记录。
func queryDanmaku(ctx context.Context, c sdk.Call, arg, pageArg string) error {
	page := 0
	if pageArg != "" {
		n, err := strconv.Atoi(pageArg)
		if err != nil || n < 0 {
			return c.Reply(ctx, "用法：查弹幕 <UID或昵称> [页码]，页码必须是非负整数")
		}
		page = n
	}
	uid, err := resolveUID(ctx, c, arg)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	cookie := cookieOf(ctx, c)
	card, cardErr := fetchCard(ctx, uid, cookie)
	var resp danmakuResp
	referer := "https://danmakus.com/user/" + strconv.FormatInt(uid, 10)
	if err := getJSON(ctx, fmt.Sprintf(danmakuURL, uid, page), referer, "", &resp); err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	if resp.Code != 0 {
		return c.Reply(ctx, fmt.Sprintf("查询失败：弹幕接口返回 %d %s", resp.Code, notnull(resp.Message)))
	}
	records := resp.Data.Data.Records
	if len(records) == 0 {
		return c.Reply(ctx, "没有查到该用户最近的直播弹幕")
	}

	view := danmakuView{
		UID:       uid,
		Name:      firstNonEmpty(records[0].Channel.UName, strconv.FormatInt(uid, 10)),
		Link:      referer,
		QueryTime: time.Now().Format("2006-01-02 15:04:05"),
	}
	if cardErr == nil {
		user := card.Data.Card
		view.Name = firstNonEmpty(user.Name, view.Name)
		view.Fans = formatCount(user.Fans)
		view.Attention = formatCount(user.Attention)
		if user.Face != "" {
			view.Face = safeURL(inlineImage(ctx, user.Face))
		}
	} else {
		view.Notices = append(view.Notices, "用户资料获取失败："+cardErr.Error())
	}
	if resp.Data.Total > 0 {
		totalPage := (resp.Data.Total - 1) / pageSize
		view.Page = fmt.Sprintf("%d/%d ｜ 共 %d 条记录", resp.Data.PageNum, totalPage, resp.Data.Total)
	} else {
		view.Page = fmt.Sprintf("%d ｜ 共 %d 条记录", resp.Data.PageNum, len(records))
	}

	for _, record := range records {
		live := newDanmakuLiveView(record)
		if record.Channel.FaceURL != "" {
			live.Face = safeURL(inlineImage(ctx, record.Channel.FaceURL))
		}
		view.Lives = append(view.Lives, live)
	}

	fallback := danmakuText(view)
	return replyRendered(ctx, c, func(_ int) ([]byte, error) {
		return buildDanmakuHTML(view)
	}, fallback)
}

// newDanmakuLiveView 把一场直播转换为卡片数据。
func newDanmakuLiveView(record danmakuRecord) danmakuLiveView {
	title := firstNonEmpty(record.Live.Title, "无标题直播")
	view := danmakuLiveView{
		Title:      title,
		Anchor:     firstNonEmpty(record.Channel.UName, "未知"),
		RoomID:     strconv.Itoa(record.Channel.RoomID),
		Start:      millisTime(record.Live.StartDate),
		DanmakuNum: record.Live.DanmakusCount,
		WatchNum:   record.Live.WatchCount,
		Income:     fmt.Sprintf("￥%.1f", record.Live.TotalIncome),
	}
	var duration float64
	if record.Live.IsFinish {
		duration = float64(record.Live.StopDate-record.Live.StartDate) / 3600000
		view.End = millisTime(record.Live.StopDate)
	} else {
		duration = float64(time.Now().UnixMilli()-record.Live.StartDate) / 3600000
		view.Living = true
	}
	if duration < 0 {
		duration = 0
	}
	view.Duration = fmt.Sprintf("%.1f 小时", duration)

	// 与上游一致，展示该场直播记录到的全部弹幕。
	for _, item := range record.Danmakus {
		view.Items = append(view.Items, newDanmakuItemView(item))
	}
	return view
}

// newDanmakuItemView 按弹幕类型拼出展示文本。
func newDanmakuItemView(item danmakuItem) danmakuItemView {
	message := clip(item.Message, 60)
	view := danmakuItemView{
		Clock: millisClock(item.SendDate),
		UName: firstNonEmpty(item.UName, "匿名"),
		Type:  danmakuTypeClass(item.Type),
	}
	switch item.Type {
	case 0:
		view.Message = message
	case 1:
		view.Message = "[礼物] " + message
		view.Price = "￥" + strconv.FormatFloat(item.Price, 'f', -1, 64)
	case 2:
		view.Message = "[上舰] " + message
		view.Price = "￥" + strconv.FormatFloat(item.Price, 'f', -1, 64)
	case 3:
		view.Message = "[Superchat] " + message
		view.Price = "￥" + strconv.FormatFloat(item.Price, 'f', -1, 64)
	default:
		view.Message = "[" + danmakuTypeLabel(item.Type) + "]"
		if message != "" {
			view.Message += " " + message
		}
	}
	return view
}

// danmakuText 生成查弹幕的纯文本回退内容。
func danmakuText(view danmakuView) string {
	lines := []string{fmt.Sprintf("【查弹幕】%s（UID %d）", view.Name, view.UID)}
	if view.Fans != "" || view.Attention != "" {
		lines = append(lines, fmt.Sprintf("粉丝：%s ｜ 关注：%s", view.Fans, view.Attention))
	}
	lines = append(lines, "页码："+view.Page)
	lines = append(lines, view.Notices...)
	for _, live := range view.Lives {
		head := fmt.Sprintf("· %s（主播：%s）", live.Title, live.Anchor)
		end := live.End
		if live.Living {
			end = "正在直播"
		}
		detail := fmt.Sprintf("  %s ~ %s（%s）｜ 弹幕 %d ｜ 观看 %d ｜ 收益 %s",
			live.Start, end, live.Duration, live.DanmakuNum, live.WatchNum, live.Income)
		lines = append(lines, head, detail)
		if len(live.Items) == 0 {
			lines = append(lines, "  （本场没有记录到弹幕）")
			continue
		}
		for _, item := range live.Items {
			text := item.Message
			if item.Price != "" {
				text += " " + item.Price
			}
			lines = append(lines, "  "+fmt.Sprintf("%s %s：%s", item.Clock, item.UName, text))
		}
		if live.HiddenCount > 0 {
			lines = append(lines, fmt.Sprintf("  （还有 %d 条弹幕未展示）", live.HiddenCount))
		}
	}
	lines = append(lines, view.Link)
	return strings.Join(lines, "\n")
}

// updateVup 管理员手动刷新 vtbs.moe 列表缓存。
func updateVup(ctx context.Context, c sdk.Call) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	list, err := loadVupEntries(ctx, c, true)
	if err != nil {
		return c.Reply(ctx, "更新失败："+err.Error())
	}
	return c.Reply(ctx, fmt.Sprintf("vup 列表已更新，共 %d 位", len(list)))
}

// cookieStore 是持久化到插件 state 的 Cookie。
type cookieStore struct {
	Cookie string `json:"cookie"`
}

// setCookie 管理员设置 Cookie，写入插件 state 且不回显。
func setCookie(ctx context.Context, c sdk.Call, arg string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	cookie := strings.TrimSpace(arg)
	if cookie == "" {
		return c.Reply(ctx, "用法：设置b站cookie <cookie>")
	}
	if err := sdk.SaveJSON(ctx, c.State, cookieStateKey, cookieStore{Cookie: cookie}); err != nil {
		return c.Reply(ctx, "保存 Cookie 失败："+err.Error())
	}
	return c.Reply(ctx, "b站cookie已保存")
}

// cardResp 用户名片接口响应。
type cardResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Card         memberCard `json:"card"`
		ArchiveCount int        `json:"archive_count"`
	} `json:"data"`
}

// memberCard 用户名片字段，与上游 MemberCard 一致。
type memberCard struct {
	Mid        string  `json:"mid"`
	Name       string  `json:"name"`
	Sex        string  `json:"sex"`
	Face       string  `json:"face"`
	Regtime    int64   `json:"regtime"`
	Birthday   string  `json:"birthday"`
	Sign       string  `json:"sign"`
	Attentions []int64 `json:"attentions"`
	Fans       int     `json:"fans"`
	Attention  int     `json:"attention"`
	LevelInfo  struct {
		CurrentLevel int `json:"current_level"`
	} `json:"level_info"`
}

// fetchCard 拉取用户名片，带 cookie 时能拿到更完整的关注列表。
func fetchCard(ctx context.Context, uid int64, cookie string) (*cardResp, error) {
	var resp cardResp
	referer := fmt.Sprintf("https://space.bilibili.com/%d", uid)
	if err := getJSON(ctx, fmt.Sprintf(cardURL, uid), referer, cookie, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("用户不存在或接口返回 %d %s", resp.Code, notnull(resp.Message))
	}
	return &resp, nil
}

// vtbDetail vtbs.moe 的 VUP 详情。
type vtbDetail struct {
	Mid      int64           `json:"mid"`
	Uname    string          `json:"uname"`
	Video    int             `json:"video"`
	Roomid   int64           `json:"roomid"`
	Face     string          `json:"face"`
	Sign     string          `json:"sign"`
	Title    string          `json:"title"`
	Official json.RawMessage `json:"official"`
	Rise     int             `json:"rise"`
	Follower int             `json:"follower"`
	GuardNum int             `json:"guardNum"`
	AreaRank int             `json:"areaRank"`
}

// fetchVtbDetail 拉取 vtbs.moe 的 VUP 详情。
func fetchVtbDetail(ctx context.Context, uid int64) (*vtbDetail, error) {
	var resp vtbDetail
	referer := "https://vtbs.moe/detail/" + strconv.FormatInt(uid, 10)
	if err := getJSON(ctx, fmt.Sprintf(vtbURL, uid), referer, "", &resp); err != nil {
		return nil, err
	}
	if resp.Mid == 0 && resp.Uname == "" {
		return nil, errors.New("vtbs.moe 没有收录该 UID")
	}
	return &resp, nil
}

// vupEntry vtbs.moe v1/short 列表中的一项，保留上游 VUP 的完整字段。
type vupEntry struct {
	Mid      int64           `json:"mid"`
	Uname    string          `json:"uname"`
	Roomid   int64           `json:"roomid"`
	Face     string          `json:"face"`
	Sign     string          `json:"sign"`
	Title    string          `json:"title"`
	Official json.RawMessage `json:"official"`
	Follower int             `json:"follower"`
	Video    int             `json:"video"`
	Rise     int             `json:"rise"`
	GuardNum int             `json:"guardNum"`
	AreaRank int             `json:"areaRank"`
}

// vupStore VUP 列表在插件 state 中的持久化结构。
type vupStore struct {
	UpdatedAt int64      `json:"updated_at"`
	Entries   []vupEntry `json:"entries"`
}

// loadVupEntries 读取 state 中的 VUP 列表，过期或 force 时重新抓取并写回。
func loadVupEntries(ctx context.Context, c sdk.Call, force bool) ([]vupEntry, error) {
	var cached vupStore
	readErr := sdk.LoadJSON(ctx, c.State, vupStateKey, &cached)
	if !force && readErr == nil && len(cached.Entries) > 0 &&
		time.Since(time.Unix(cached.UpdatedAt, 0)) < vupTTL {
		return cached.Entries, nil
	}
	entries, err := fetchVupList(ctx)
	if err != nil {
		if len(cached.Entries) > 0 {
			return cached.Entries, nil
		}
		return nil, err
	}
	if err := sdk.SaveJSON(ctx, c.State, vupStateKey, vupStore{
		UpdatedAt: time.Now().Unix(),
		Entries:   entries,
	}); err != nil && c.Log != nil {
		c.Log("bilibili: 保存 VUP 缓存失败: " + err.Error())
	}
	return entries, nil
}

// fetchVupList 拉取 vtbs.moe 的 VUP 全量列表。
func fetchVupList(ctx context.Context) ([]vupEntry, error) {
	body, err := getBody(ctx, vtbListURL, "https://vtbs.moe/", "")
	if err != nil {
		return nil, err
	}
	var entries []vupEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, errors.New("vtbs.moe 数据解析失败")
	}
	return entries, nil
}

// medalResp 牌子墙接口响应。
type medalResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		List []medal `json:"list"`
	} `json:"data"`
}

// medal 牌子墙中的一块牌子。
type medal struct {
	TargetName string    `json:"target_name"`
	MedalInfo  medalInfo `json:"medal_info"`
}

// medalInfo 牌子信息，保留渐变颜色与图标供卡片渲染。
type medalInfo struct {
	TargetID         int64  `json:"target_id"`
	MedalName        string `json:"medal_name"`
	Level            int64  `json:"level"`
	MedalColorStart  int64  `json:"medal_color_start"`
	MedalColorEnd    int64  `json:"medal_color_end"`
	MedalColorBorder int64  `json:"medal_color_border"`
	IconURL          string `json:"icon_url"`
}

// fetchMedals 拉取牌子墙，需要登录 Cookie；未配置时直接跳过。
func fetchMedals(ctx context.Context, uid int64, cookie string) ([]medal, error) {
	if cookie == "" {
		return nil, nil
	}
	var resp medalResp
	referer := fmt.Sprintf("https://space.bilibili.com/%d", uid)
	if err := getJSON(ctx, fmt.Sprintf(medalURL, uid), referer, cookie, &resp); err != nil {
		return nil, err
	}
	if resp.Code == -101 {
		return nil, errors.New("Cookie 已失效，请更新插件配置中的 cookie")
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("牌子墙接口返回 %d %s", resp.Code, notnull(resp.Message))
	}
	return resp.Data.List, nil
}

// searchResp 用户搜索接口响应。
type searchResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Result []struct {
			Mid   int64  `json:"mid"`
			Uname string `json:"uname"`
		} `json:"result"`
	} `json:"data"`
}

// danmakuResp 弹幕网接口响应，字段与上游 Danmakusuki 一致。
type danmakuResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Total   int `json:"total"`
		PageNum int `json:"pageNum"`
		Data    struct {
			Records []danmakuRecord `json:"records"`
		} `json:"data"`
	} `json:"data"`
}

// danmakuRecord 一场直播及其弹幕记录。
type danmakuRecord struct {
	Channel struct {
		UID      int    `json:"uId"`
		UName    string `json:"uName"`
		RoomID   int    `json:"roomId"`
		FaceURL  string `json:"faceUrl"`
		IsLiving bool   `json:"isLiving"`
		Title    string `json:"title"`
	} `json:"channel"`
	Live struct {
		IsFinish      bool    `json:"isFinish"`
		DanmakusCount int     `json:"danmakusCount"`
		StartDate     int64   `json:"startDate"`
		StopDate      int64   `json:"stopDate"`
		Title         string  `json:"title"`
		TotalIncome   float64 `json:"totalIncome"`
		WatchCount    int     `json:"watchCount"`
	} `json:"live"`
	Danmakus []danmakuItem `json:"danmakus"`
}

// danmakuItem 单条弹幕。
type danmakuItem struct {
	UID      int     `json:"uId"`
	UName    string  `json:"uName"`
	Type     int64   `json:"type"`
	SendDate int64   `json:"sendDate"`
	Message  string  `json:"message"`
	Price    float64 `json:"price"`
}

// resolveUID 把 UID 或昵称解析为 UID；昵称搜索需要 Cookie。
func resolveUID(ctx context.Context, c sdk.Call, arg string) (int64, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, errors.New("请提供 UID 或昵称")
	}
	if numberRe.MatchString(arg) {
		uid, err := strconv.ParseInt(arg, 10, 64)
		if err == nil && uid > 0 {
			return uid, nil
		}
		return 0, errors.New("UID 必须是正整数")
	}
	cookie := cookieOf(ctx, c)
	if cookie == "" {
		return 0, errors.New("昵称查询需要先设置 B 站 Cookie（管理员发送「设置b站cookie <cookie>」）；也可以直接输入 UID")
	}
	uid, err := searchUser(ctx, arg, cookie)
	if err != nil {
		return 0, errors.New("用户搜索失败：" + err.Error())
	}
	return uid, nil
}

// searchUser 用 WBI 签名的搜索接口按昵称查第一个用户。
func searchUser(ctx context.Context, keyword, cookie string) (int64, error) {
	mixin, err := wbi.mixinKey(ctx)
	if err != nil {
		return 0, err
	}
	params := map[string]string{
		"search_type": "bili_user",
		"keyword":     keyword,
		"page":        "1",
		"page_size":   "1",
		"wts":         strconv.FormatInt(time.Now().Unix(), 10),
	}
	query := signQuery(params, mixin)
	var resp searchResp
	if err := getJSON(ctx, searchURL+"?"+query, "https://search.bilibili.com/", cookie, &resp); err != nil {
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("搜索接口返回 %d %s", resp.Code, notnull(resp.Message))
	}
	if len(resp.Data.Result) == 0 {
		return 0, errors.New("没有找到该用户")
	}
	return resp.Data.Result[0].Mid, nil
}

// newClient 建立带 Cookie 的客户端，B 站接口依赖首页下发的 buvid 等 Cookie。
func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: 10 * time.Second, Jar: jar}
}

// wbiCache 缓存 wbi 签名所需的 mixin key，默认十分钟刷新。
type wbiCache struct {
	mu     sync.Mutex
	mixin  string
	at     time.Time
	warmed bool
}

// mixinKeyEncTab 是 B 站 wbi 签名的字符重排表。
var mixinKeyEncTab = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

// mixinKey 返回缓存的 mixin key，必要时刷新并预热 Cookie。
func (w *wbiCache) mixinKey(ctx context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.mixin != "" && time.Since(w.at) < 10*time.Minute {
		return w.mixin, nil
	}
	if !w.warmed {
		// 首页会下发 buvid3 等 Cookie，缺少时搜索接口容易触发风控。
		_, _ = getBody(ctx, warmURL, "", "")
		w.warmed = true
	}
	body, err := getBody(ctx, navURL, "https://www.bilibili.com/", "")
	if err != nil {
		return "", err
	}
	var nav struct {
		Data struct {
			WbiImg struct {
				ImgURL string `json:"img_url"`
				SubURL string `json:"sub_url"`
			} `json:"wbi_img"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &nav); err != nil {
		return "", errors.New("签名信息解析失败")
	}
	img := keyFromURL(nav.Data.WbiImg.ImgURL)
	sub := keyFromURL(nav.Data.WbiImg.SubURL)
	if len(img)+len(sub) < 64 {
		return "", errors.New("签名信息缺失")
	}
	raw := img + sub
	var b strings.Builder
	for _, i := range mixinKeyEncTab {
		b.WriteByte(raw[i])
	}
	w.mixin, w.at = b.String()[:32], time.Now()
	return w.mixin, nil
}

// signQuery 按 B 站规则排序、过滤特殊字符并计算 w_rid。
func signQuery(params map[string]string, mixin string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.Map(func(r rune) rune {
			if strings.ContainsRune("!'()*", r) {
				return -1
			}
			return r
		}, params[k])
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(v))
	}
	query := b.String()
	sum := md5.Sum([]byte(query + mixin))
	return query + "&w_rid=" + hex.EncodeToString(sum[:])
}

// getJSON 发起 GET 请求并解析 JSON，限制响应体大小。
func getJSON(ctx context.Context, rawURL, referer, cookie string, out any) error {
	body, err := getBody(ctx, rawURL, referer, cookie)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("响应解析失败")
	}
	return nil
}

// getBody 返回限长的响应体，412 时给出风控提示。
func getBody(ctx context.Context, rawURL, referer, cookie string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if referer == "" {
		referer = "https://www.bilibili.com/"
	}
	req.Header.Set("Referer", referer)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := biliClient.Do(req)
	if err != nil {
		return nil, errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, errors.New("B站风控拦截，请稍后再试")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("接口返回 " + resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// keyFromURL 取 wbi 图片地址中的文件名（不含扩展名）。
func keyFromURL(raw string) string {
	name := path.Base(raw)
	return strings.TrimSuffix(name, path.Ext(name))
}

// millisTime 把毫秒时间戳格式化为本地时间。
func millisTime(ms int64) string {
	if ms <= 0 {
		return "未知时间"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

// millisClock 把毫秒时间戳格式化为时分秒。
func millisClock(ms int64) string {
	if ms <= 0 {
		return "--:--:--"
	}
	return time.UnixMilli(ms).Local().Format("15:04:05")
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

// notnull 为空时返回通用错误描述。
func notnull(text string) string {
	if strings.TrimSpace(text) == "" {
		return "接口返回错误"
	}
	return text
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

// formatCount 把大数字转为「万 / 亿」。
func formatCount(n int) string {
	switch {
	case n >= 100000000:
		return strconv.FormatFloat(float64(n)/100000000, 'f', 1, 64) + "亿"
	case n >= 10000:
		return strconv.FormatFloat(float64(n)/10000, 'f', 1, 64) + "万"
	default:
		return strconv.Itoa(n)
	}
}

// cookieOf 优先读取「设置b站cookie」保存的 Cookie，其次读取插件配置。
func cookieOf(ctx context.Context, c sdk.Call) string {
	var stored cookieStore
	if err := sdk.LoadJSON(ctx, c.State, cookieStateKey, &stored); err == nil {
		if v := strings.TrimSpace(stored.Cookie); v != "" {
			return v
		}
	}
	return cookieOfConfig(c.Config)
}

// cookieOfConfig 读取插件配置中的可选 Cookie。
func cookieOfConfig(config map[string]any) string {
	if config == nil {
		return ""
	}
	if v, ok := config["cookie"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

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

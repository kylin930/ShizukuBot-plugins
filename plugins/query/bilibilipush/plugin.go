// Package bilibilipush B 站订阅：按需查询 UP 主资料、动态与直播状态，群内可手动拉取更新。
package bilibilipush

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	stateKey = "bilibili"
	// maxSubs 每个群最多订阅的 UP 主数量，避免一次拉取请求过多。
	maxSubs = 5
	// pullCooldown 手动拉取的最小间隔。
	pullCooldown = time.Minute
	// maxPushImages 一次推送最多附带的动态首图数量。
	maxPushImages = 5
	// maxSummary 动态正文首段的最大字符数。
	maxSummary = 120
)

var (
	// 下列地址均为包级变量，测试中替换为 httptest 服务。
	cardURL  = "https://api.bilibili.com/x/web-interface/card?mid=%d"
	liveURL  = "https://api.live.bilibili.com/room/v1/Room/getRoomInfoOld?mid=%d"
	spaceURL = "https://api.bilibili.com/x/polymer/web-dynamic/v1/feed/space"
	navURL   = "https://api.bilibili.com/x/web-interface/nav"
	warmURL  = "https://www.bilibili.com/"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	biliClient = newClient()
	wbi        = &wbiCache{}
)

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

// mixinKey 是 B 站 wbi 签名的字符重排表。
var mixinKey = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

// Definition subscribes to the B 站查询与订阅指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "bilibilipush",
		Name:        "B站订阅",
		Version:     "1.0.0",
		Description: "发送「b站用户 2」「b站动态 2」「b站直播 2」按需查询；管理员可添加订阅并手动拉取更新",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "bilibilipush", Trigger: "b站用户"},
	},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许管理 B 站订阅的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// biliSub 是某个群对一位 UP 主的订阅。
type biliSub struct {
	UID         int64  `json:"uid"`
	Name        string `json:"name"`
	LastDynamic int64  `json:"last_dynamic"`
	Live        bool   `json:"live"`
	// DynamicOff / LiveOff 对应上游的 dynamic_disable / live_disable，
	// 只关闭其中一类推送时保留订阅记录。
	DynamicOff bool `json:"dynamic_off"`
	LiveOff    bool `json:"live_off"`
}

// biliStore 按群保存订阅、上次拉取时间与艾特全体开关。
type biliStore struct {
	Groups map[string][]biliSub `json:"groups"`
	Pulled map[string]int64     `json:"pulled"`
	AtAll  map[string]bool      `json:"at_all"`
}

// Handle 路由各条 B 站指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	if sdk.MatchFull(text, "b站帮助") || sdk.MatchFull(text, "B站帮助") {
		return c.Reply(ctx, helpText)
	}
	if sdk.MatchFull(text, "b站订阅列表") || sdk.MatchFull(text, "B站订阅列表") ||
		sdk.MatchFull(text, "b站推送列表") || sdk.MatchFull(text, "B站推送列表") {
		return listSubs(ctx, c)
	}
	if sdk.MatchFull(text, "b站推送") || sdk.MatchFull(text, "B站推送") ||
		sdk.MatchFull(text, "拉取b站推送") || sdk.MatchFull(text, "拉取B站推送") {
		return pull(ctx, c)
	}
	if sdk.MatchFull(text, "开启艾特全体") {
		return setAtAll(ctx, c, true)
	}
	if sdk.MatchFull(text, "关闭艾特全体") {
		return setAtAll(ctx, c, false)
	}
	for _, prefix := range []string{"添加b站订阅", "添加B站订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return addSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"取消b站动态订阅", "取消B站动态订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return removePartSub(ctx, c, rest, true)
		}
	}
	for _, prefix := range []string{"取消b站直播订阅", "取消B站直播订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return removePartSub(ctx, c, rest, false)
		}
	}
	for _, prefix := range []string{"取消b站订阅", "取消B站订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return removeSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"b站用户", "B站用户"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return queryUser(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"b站动态", "B站动态"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return queryDynamics(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"b站直播", "B站直播"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return queryLive(ctx, c, rest)
		}
	}
	return nil
}

const helpText = "B 站指令：\n" +
	"b站用户 <UID> — 查看 UP 主资料\n" +
	"b站动态 <UID> — 查看最近 5 条动态\n" +
	"b站直播 <UID> — 查看直播状态\n" +
	"b站订阅列表 — 查看本群订阅\n" +
	"添加b站订阅 <UID> / 取消b站订阅 <UID> — 管理员管理订阅\n" +
	"取消b站动态订阅 <UID> / 取消b站直播订阅 <UID> — 只关闭对应推送\n" +
	"开启艾特全体 / 关闭艾特全体 — 直播间开播时是否 @全体成员\n" +
	"b站推送 — 管理员手动拉取一次更新（每分钟最多一次）"

// queryUser 查询用户名片。
func queryUser(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := parseUID(arg)
	if err != nil {
		return c.Reply(ctx, "用法：b站用户 2")
	}
	card, err := fetchCard(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	name := card.Data.Card.Name
	if name == "" {
		name = "未知用户"
	}
	lines := []string{"【B站用户】" + name}
	if card.Data.Card.Fans > 0 {
		lines = append(lines, "粉丝："+formatCount(card.Data.Card.Fans))
	}
	if card.Data.ArchiveCount > 0 {
		lines = append(lines, "投稿："+strconv.Itoa(card.Data.ArchiveCount))
	}
	if sign := strings.TrimSpace(card.Data.Card.Sign); sign != "" {
		if len([]rune(sign)) > 100 {
			sign = string([]rune(sign)[:100]) + "…"
		}
		lines = append(lines, "签名："+sign)
	}
	lines = append(lines, fmt.Sprintf("https://space.bilibili.com/%d", uid))
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// queryDynamics 查询最近动态。
func queryDynamics(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := parseUID(arg)
	if err != nil {
		return c.Reply(ctx, "用法：b站动态 2")
	}
	items, err := fetchDynamics(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	if len(items) == 0 {
		return c.Reply(ctx, "该 UP 主最近没有动态")
	}
	if len(items) > 5 {
		items = items[:5]
	}
	lines := []string{fmt.Sprintf("【B站动态】%s（%d）", items[0].Modules.ModuleAuthor.Name, uid)}
	for _, item := range items {
		lines = append(lines, "· "+dynamicTitle(item)+"（"+dynamicTime(item)+"）\n  "+dynamicLink(item))
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// queryLive 查询直播状态。
func queryLive(ctx context.Context, c sdk.Call, arg string) error {
	uid, err := parseUID(arg)
	if err != nil {
		return c.Reply(ctx, "用法：b站直播 2")
	}
	info, err := fetchLive(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	if info.Data.LiveStatus != 1 {
		return c.Reply(ctx, fmt.Sprintf("【B站直播】%d\n当前未开播\nhttps://live.bilibili.com/%d", uid, info.Data.RoomID))
	}
	lines := []string{
		fmt.Sprintf("【B站直播】%d", uid),
		"状态：直播中",
	}
	if info.Data.Title != "" {
		lines = append(lines, "标题："+info.Data.Title)
	}
	link := info.Data.URL
	if link == "" {
		link = fmt.Sprintf("https://live.bilibili.com/%d", info.Data.RoomID)
	}
	lines = append(lines, link)
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// addSub 校验管理员后新增订阅，并以当前状态作为推送基线。
func addSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	uid, err := parseUID(arg)
	if err != nil {
		return c.Reply(ctx, "用法：添加b站订阅 2")
	}
	card, err := fetchCard(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "订阅失败："+err.Error())
	}
	name := card.Data.Card.Name
	if name == "" {
		name = strconv.FormatInt(uid, 10)
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	for i := range subs {
		if subs[i].UID == uid {
			// 上游「添加b站订阅」会同时把动态与直播开关复位，便于取消单项后恢复。
			if !subs[i].DynamicOff && !subs[i].LiveOff {
				return c.Reply(ctx, "已经订阅了 "+name)
			}
			if subs[i].DynamicOff {
				if items, err := fetchDynamics(ctx, uid); err == nil && len(items) > 0 {
					if ts, err := strconv.ParseInt(items[0].Modules.ModuleAuthor.PubTS, 10, 64); err == nil {
						subs[i].LastDynamic = ts
					}
				}
			}
			if subs[i].LiveOff {
				if info, err := fetchLive(ctx, uid); err == nil {
					subs[i].Live = info.Data.LiveStatus == 1
				}
			}
			subs[i].DynamicOff, subs[i].LiveOff = false, false
			st.Groups[c.Event.GroupID] = subs
			if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
				return c.Reply(ctx, "保存订阅失败："+err.Error())
			}
			return c.Reply(ctx, fmt.Sprintf("已订阅 %s（UID %d），发送「b站推送」拉取更新", name, uid))
		}
	}
	if len(subs) >= maxSubs {
		return c.Reply(ctx, fmt.Sprintf("本群订阅已达上限 %d 个，请先取消部分订阅", maxSubs))
	}
	sub := biliSub{UID: uid, Name: name}
	// 基线取当前最新动态与直播状态，避免订阅后立刻刷出历史内容。
	if items, err := fetchDynamics(ctx, uid); err == nil && len(items) > 0 {
		if ts, err := strconv.ParseInt(items[0].Modules.ModuleAuthor.PubTS, 10, 64); err == nil {
			sub.LastDynamic = ts
		}
	}
	if info, err := fetchLive(ctx, uid); err == nil {
		sub.Live = info.Data.LiveStatus == 1
	}
	st.Groups[c.Event.GroupID] = append(subs, sub)
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, fmt.Sprintf("已订阅 %s（UID %d），发送「b站推送」拉取更新", name, uid))
}

// removeSub 取消订阅。
func removeSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	uid, err := parseUID(arg)
	if err != nil {
		return c.Reply(ctx, "用法：取消b站订阅 2")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	kept := subs[:0]
	for _, sub := range subs {
		if sub.UID != uid {
			kept = append(kept, sub)
		}
	}
	if len(kept) == len(subs) {
		return c.Reply(ctx, "本群没有订阅该 UID")
	}
	st.Groups[c.Event.GroupID] = kept
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, fmt.Sprintf("已取消订阅 UID %d", uid))
}

// removePartSub 只关闭动态或直播其中一类推送，保留订阅记录。
func removePartSub(ctx context.Context, c sdk.Call, arg string, dynamic bool) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	uid, err := parseUID(arg)
	if err != nil {
		if dynamic {
			return c.Reply(ctx, "用法：取消b站动态订阅 2")
		}
		return c.Reply(ctx, "用法：取消b站直播订阅 2")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	idx := -1
	for i := range subs {
		if subs[i].UID == uid {
			idx = i
			break
		}
	}
	if idx < 0 {
		return c.Reply(ctx, "本群没有订阅该 UID")
	}
	name := subs[idx].Name
	if name == "" {
		name = strconv.FormatInt(uid, 10)
	}
	if dynamic {
		subs[idx].DynamicOff = true
	} else {
		subs[idx].LiveOff = true
	}
	st.Groups[c.Event.GroupID] = subs
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	if dynamic {
		return c.Reply(ctx, "已取消"+name+"的动态订阅")
	}
	return c.Reply(ctx, "已取消"+name+"的直播订阅")
}

// setAtAll 持久化群级艾特全体开关。
func setAtAll(ctx context.Context, c sdk.Call, on bool) error {
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
	st.AtAll[c.Event.GroupID] = on
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存设置失败："+err.Error())
	}
	if on {
		return c.Reply(ctx, "已开启艾特全体Oo")
	}
	return c.Reply(ctx, "已关闭艾特全体Oo")
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
		return c.Reply(ctx, "本群还没有订阅，管理员可发送「添加b站订阅 <UID>」")
	}
	lines := []string{fmt.Sprintf("本群 B 站订阅（%d/%d）：", len(subs), maxSubs)}
	for _, sub := range subs {
		dynamic, live := "●", "●"
		if sub.DynamicOff {
			dynamic = "○"
		}
		if sub.LiveOff {
			live = "○"
		}
		lines = append(lines, fmt.Sprintf("· %s（UID %d）动态：%s 直播：%s", sub.Name, sub.UID, dynamic, live))
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// pull 手动拉取一次更新，含群绑定、管理员校验与频控。
func pull(ctx context.Context, c sdk.Call) error {
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
		return c.Reply(ctx, fmt.Sprintf("拉取太频繁，请 %d 秒后再试", int(wait.Seconds())+1))
	}
	subs := st.Groups[c.Event.GroupID]
	if len(subs) == 0 {
		return c.Reply(ctx, "本群还没有订阅，管理员可发送「添加b站订阅 <UID>」")
	}

	var updates []string
	var failed []string
	var images []string
	liveAlert := false
	for i := range subs {
		sub := &subs[i]
		if !sub.LiveOff {
			info, err := fetchLive(ctx, sub.UID)
			if err != nil {
				failed = append(failed, sub.Name)
			} else {
				live := info.Data.LiveStatus == 1
				if live && !sub.Live {
					updates = append(updates, "· "+sub.Name+" 正在直播："+firstNonEmpty(info.Data.Title, "无标题")+"\n  "+liveLink(info))
					liveAlert = true
				}
				sub.Live = live
			}
		}
		if sub.DynamicOff {
			continue
		}
		items, err := fetchDynamics(ctx, sub.UID)
		if err != nil {
			if !containsString(failed, sub.Name) {
				failed = append(failed, sub.Name)
			}
			continue
		}
		fresh := 0
		// 动态按时间倒序返回，从旧到新推送，保证顺序自然。
		for j := len(items) - 1; j >= 0; j-- {
			ts, err := strconv.ParseInt(items[j].Modules.ModuleAuthor.PubTS, 10, 64)
			if err != nil || ts <= sub.LastDynamic {
				continue
			}
			if fresh >= 3 {
				if ts > sub.LastDynamic {
					sub.LastDynamic = ts
				}
				continue
			}
			line := "· " + sub.Name + " 更新了动态：" + dynamicTitle(items[j]) + "\n  " + dynamicLink(items[j])
			// 上游会发送完整动态卡片；这里补上正文首段与首图，缺少详情时保持原文本。
			if summary := dynamicSummary(items[j]); summary != "" {
				line += "\n  " + summary
			}
			updates = append(updates, line)
			if img := dynamicImage(items[j]); img != "" && len(images) < maxPushImages {
				images = append(images, img)
			}
			fresh++
			if ts > sub.LastDynamic {
				sub.LastDynamic = ts
			}
		}
	}
	st.Groups[c.Event.GroupID] = subs
	st.Pulled[c.Event.GroupID] = time.Now().Unix()
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	if len(updates) == 0 {
		msg := "本次没有新动态或直播"
		if len(failed) > 0 {
			msg += "；获取失败：" + strings.Join(failed, "、")
		}
		return c.Reply(ctx, msg)
	}
	msg := "【B站推送】\n"
	// 宿主 Reply 只能 Mention 单个用户，QQ 官方群消息也不支持真实 @全体，
	// 因此用「@全体成员 」前缀模拟上游的 message.AtAll()。
	if liveAlert && st.AtAll[c.Event.GroupID] {
		msg += "@全体成员 "
	}
	msg += strings.Join(updates, "\n")
	if len(failed) > 0 {
		msg += "\n获取失败：" + strings.Join(failed, "、")
	}
	if err := c.Reply(ctx, msg); err != nil {
		return err
	}
	for _, img := range images {
		// 图片发送失败不影响文本推送。
		_ = c.ReplyImage(ctx, img)
	}
	return nil
}

// cardResp 用户名片接口响应。
type cardResp struct {
	Code int `json:"code"`
	Data struct {
		Card struct {
			Name string `json:"name"`
			Face string `json:"face"`
			Sign string `json:"sign"`
			Fans int    `json:"fans"`
		} `json:"card"`
		ArchiveCount int `json:"archive_count"`
	} `json:"data"`
}

// fetchCard 拉取用户名片。
func fetchCard(ctx context.Context, uid int64) (*cardResp, error) {
	var resp cardResp
	if err := getJSON(ctx, fmt.Sprintf(cardURL, uid), fmt.Sprintf("https://space.bilibili.com/%d", uid), &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, errors.New("用户不存在或接口返回 " + strconv.Itoa(resp.Code))
	}
	return &resp, nil
}

// liveResp 直播状态接口响应。
type liveResp struct {
	Code int `json:"code"`
	Data struct {
		LiveStatus int    `json:"liveStatus"`
		Title      string `json:"title"`
		Cover      string `json:"cover"`
		RoomID     int64  `json:"roomid"`
		URL        string `json:"url"`
	} `json:"data"`
}

// fetchLive 拉取直播间状态。
func fetchLive(ctx context.Context, uid int64) (*liveResp, error) {
	var resp liveResp
	if err := getJSON(ctx, fmt.Sprintf(liveURL, uid), fmt.Sprintf("https://live.bilibili.com/%d", uid), &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, errors.New("直播接口返回 " + strconv.Itoa(resp.Code))
	}
	return &resp, nil
}

// dynamicItem 动态接口的必要字段。
type dynamicItem struct {
	IDStr   string `json:"id_str"`
	Type    string `json:"type"`
	Modules struct {
		ModuleAuthor struct {
			Name    string `json:"name"`
			PubTS   string `json:"pub_ts"`
			PubTime string `json:"pub_time"`
		} `json:"module_author"`
		ModuleDynamic struct {
			Desc *struct {
				Text string `json:"text"`
			} `json:"desc"`
			Major *struct {
				Archive *struct {
					Title   string `json:"title"`
					JumpURL string `json:"jump_url"`
				} `json:"archive"`
				Draw *struct {
					Items []struct {
						Src string `json:"src"`
					} `json:"items"`
				} `json:"draw"`
				Opus *struct {
					Title   string `json:"title"`
					JumpURL string `json:"jump_url"`
					Pics    []struct {
						URL string `json:"url"`
						Src string `json:"src"`
					} `json:"pics"`
				} `json:"opus"`
			} `json:"major"`
		} `json:"module_dynamic"`
	} `json:"modules"`
}

// feedResp 动态列表接口响应。
type feedResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []dynamicItem `json:"items"`
	} `json:"data"`
}

// fetchDynamics 拉取用户最近动态。
func fetchDynamics(ctx context.Context, uid int64) ([]dynamicItem, error) {
	mixin, err := wbi.mixinKey(ctx)
	if err != nil {
		return nil, err
	}
	params := map[string]string{
		"host_mid":        strconv.FormatInt(uid, 10),
		"offset":          "",
		"timezone_offset": "-480",
		"features":        "itemOpusStyle",
		"wts":             strconv.FormatInt(time.Now().Unix(), 10),
	}
	query := signQuery(params, mixin)
	var resp feedResp
	referer := fmt.Sprintf("https://space.bilibili.com/%d", uid)
	if err := getJSON(ctx, spaceURL+"?"+query, referer, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("动态接口返回 %d %s", resp.Code, resp.Message)
	}
	return resp.Data.Items, nil
}

// mixinKey 返回缓存的 mixin key，必要时刷新并预热 Cookie。
func (w *wbiCache) mixinKey(ctx context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.mixin != "" && time.Since(w.at) < 10*time.Minute {
		return w.mixin, nil
	}
	if !w.warmed {
		// 首页会下发 buvid3 等 Cookie，缺少时动态接口容易触发风控。
		_, _ = getBody(ctx, warmURL, "")
		w.warmed = true
	}
	body, err := getBody(ctx, navURL, "https://www.bilibili.com/")
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
	for _, i := range mixinKey {
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
func getJSON(ctx context.Context, rawURL, referer string, out any) error {
	body, err := getBody(ctx, rawURL, referer)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("响应解析失败")
	}
	return nil
}

// getBody 返回限长的响应体，412 时给出风控提示。
func getBody(ctx context.Context, rawURL, referer string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
		req.Header.Set("Origin", "https://space.bilibili.com")
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
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// dynamicTitle 根据动态类型拼出简短标题。
func dynamicTitle(item dynamicItem) string {
	title := ""
	if major := item.Modules.ModuleDynamic.Major; major != nil {
		switch {
		case major.Archive != nil && major.Archive.Title != "":
			title = "投稿：" + major.Archive.Title
		case major.Opus != nil && major.Opus.Title != "":
			title = major.Opus.Title
		case major.Draw != nil && len(major.Draw.Items) > 0:
			title = "发布了图片"
		}
	}
	if desc := item.Modules.ModuleDynamic.Desc; desc != nil {
		text := firstLine(desc.Text)
		if title == "" {
			title = text
		} else if text != "" {
			title += "｜" + text
		}
	}
	if title == "" {
		title = "发布了新动态"
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "…"
	}
	return title
}

// dynamicSummary 返回动态正文首段，超长时截断；无正文时返回空。
func dynamicSummary(item dynamicItem) string {
	desc := item.Modules.ModuleDynamic.Desc
	if desc == nil {
		return ""
	}
	text := strings.TrimSpace(strings.ReplaceAll(desc.Text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) > maxSummary {
		text = string([]rune(text)[:maxSummary]) + "…"
	}
	return text
}

// dynamicImage 返回动态首图地址，优先取图文/opus 的图片。
func dynamicImage(item dynamicItem) string {
	major := item.Modules.ModuleDynamic.Major
	if major == nil {
		return ""
	}
	if major.Draw != nil && len(major.Draw.Items) > 0 {
		return absoluteImageURL(major.Draw.Items[0].Src)
	}
	if major.Opus != nil && len(major.Opus.Pics) > 0 {
		return absoluteImageURL(firstNonEmpty(major.Opus.Pics[0].URL, major.Opus.Pics[0].Src))
	}
	return ""
}

// absoluteImageURL 补全 B 站接口常见的协议相对图片地址。
func absoluteImageURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

// dynamicLink 返回动态详情链接。
func dynamicLink(item dynamicItem) string {
	if item.IDStr != "" {
		return "https://t.bilibili.com/" + item.IDStr
	}
	return "https://space.bilibili.com"
}

// dynamicTime 把发布时间格式化为本地时间。
func dynamicTime(item dynamicItem) string {
	ts, err := strconv.ParseInt(item.Modules.ModuleAuthor.PubTS, 10, 64)
	if err != nil || ts <= 0 {
		if item.Modules.ModuleAuthor.PubTime != "" {
			return item.Modules.ModuleAuthor.PubTime
		}
		return "未知时间"
	}
	return time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
}

// liveLink 优先使用接口返回的直播间地址。
func liveLink(info *liveResp) string {
	if info.Data.URL != "" {
		return info.Data.URL
	}
	return fmt.Sprintf("https://live.bilibili.com/%d", info.Data.RoomID)
}

// keyFromURL 取 wbi 图片地址中的文件名（不含扩展名）。
func keyFromURL(raw string) string {
	name := path.Base(raw)
	return strings.TrimSuffix(name, path.Ext(name))
}

// firstLine 取多行文本的第一行。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
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

// parseUID 解析正整数 UID。
func parseUID(arg string) (int64, error) {
	arg = strings.TrimSpace(arg)
	uid, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || uid <= 0 {
		return 0, errors.New("invalid uid")
	}
	return uid, nil
}

// loadStore 读取状态，保证 map 已初始化。
func loadStore(ctx context.Context, c sdk.Call) (*biliStore, error) {
	st := &biliStore{}
	if err := sdk.LoadJSON(ctx, c.State, stateKey, st); err != nil {
		return nil, err
	}
	if st.Groups == nil {
		st.Groups = map[string][]biliSub{}
	}
	if st.Pulled == nil {
		st.Pulled = map[string]int64{}
	}
	if st.AtAll == nil {
		st.AtAll = map[string]bool{}
	}
	return st, nil
}

// containsString 判断切片是否包含目标字符串。
func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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

// Package score 签到、钱包与排行榜。
// 功能与数值移植自 ZeroBot-Plugin 的 score / wallet 插件，状态按用户与群
// 持久化到 sdk.State，不依赖 SQLite 或外部钱包库。
package score

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// 以下变量可在测试中替换，保证随机与时间可控。
var (
	nowFunc    = time.Now
	randIntn   = rand.Intn
	renderHTML = sdk.RenderHTML
	stateMu    sync.Mutex
)

var (
	signRe        = regexp.MustCompile(`^签到\s?(\d*)$`)
	presetRe      = regexp.MustCompile(`^设置签到预设\s*(\d+)$`)
	amountRe      = regexp.MustCompile(`^[+-]?\d+$`)
	transferAmtRe = regexp.MustCompile(`^[+]?\d+$`)
)

const (
	// scoreMax 经验上限，对齐上游 SCOREMAX。
	scoreMax = 1200
	// rankTopN 排行榜展示数量，对齐上游 TopN。
	rankTopN = 10
	// defaultWalletName 对齐上游钱包默认硬币名。
	defaultWalletName = "ATRI币"
	presetCount       = 4

	usersIndexKey = "users"
	coinNameKey   = "wallet:name"
)

// rankArray 等级阈值，完全对齐上游 getrank。
var rankArray = [...]int{0, 10, 20, 50, 100, 200, 350, 550, 750, 1000, 1200}

var backgroundThemes = []string{"aurora", "sunset", "ocean", "sakura", "midnight"}

var backgroundNames = map[string]string{
	"aurora":   "极光",
	"sunset":   "落日",
	"ocean":    "深海",
	"sakura":   "樱花",
	"midnight": "午夜",
}

// Definition 注册签到插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "score",
		Name:        "签到",
		Version:     "1.0.0",
		Description: "签到、签到背景、签到预设、我的钱包以及等级/钱包排行榜",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "score", Trigger: "签到"},
	},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "超级管理员 member_openid 列表；设置签到预设、硬币名称与管理钱包余额仅限名单内用户，留空时全部拒绝",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// userState 是单个用户的持久化数据。
type userState struct {
	UID         string   `json:"uid"`
	Nickname    string   `json:"nickname"`
	Coins       int      `json:"coins"`
	Exp         int      `json:"exp"`
	Level       int      `json:"level"`
	LastSign    string   `json:"last_sign"`
	LastReward  int      `json:"last_reward"`
	Preset      int      `json:"preset"`
	Backgrounds []string `json:"backgrounds"`
}

// Handle 分发所有签到相关指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	stateMu.Lock()
	defer stateMu.Unlock()

	if m := signRe.FindStringSubmatch(text); m != nil {
		return handleSign(ctx, c, strings.TrimSpace(m[1]))
	}
	if m := presetRe.FindStringSubmatch(text); m != nil {
		return handlePreset(ctx, c, m[1])
	}
	if arg, ok := sdk.MatchPrefix(text, "查看钱包余额"); ok {
		return handleMyWallet(ctx, c, strings.TrimSpace(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, "查看我的钱包"); ok {
		return handleMyWallet(ctx, c, strings.TrimSpace(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, "设置硬币名称"); ok {
		return handleSetCoinName(ctx, c, strings.TrimSpace(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, "管理钱包余额"); ok {
		return handleAdjustWallet(ctx, c, strings.TrimSpace(arg))
	}
	if arg, ok := sdk.MatchPrefix(text, "钱包转账"); ok {
		return handleTransfer(ctx, c, strings.TrimSpace(arg))
	}

	switch {
	case strings.HasPrefix(text, "获得签到背景"):
		return handleBackground(ctx, c, strings.TrimSpace(strings.TrimPrefix(text, "获得签到背景")))
	case text == "查看等级排名":
		return handleLevelRank(ctx, c)
	case text == "查看钱包排名":
		return handleWalletRank(ctx, c)
	}
	return nil
}

func handleSign(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.UserID == "" {
		return nil
	}
	now := nowFunc()
	u, err := loadUser(ctx, c, c.Event.UserID)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if nick := strings.TrimSpace(c.Event.Nickname); nick != "" {
		u.Nickname = nick
	}
	preset, err := signPreset(ctx, c, u, arg)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return dailySign(ctx, c, &u, now, preset)
}

// signPreset 对齐上游：签到后跟数字表示本次签到使用的卡片设定，
// 不带数字时使用群/个人预设。
func signPreset(ctx context.Context, c sdk.Call, u userState, arg string) (int, error) {
	if arg == "" {
		return presetFor(ctx, c, u), nil
	}
	k, err := strconv.Atoi(arg)
	if err != nil {
		return 0, err
	}
	if k < 0 || k >= presetCount {
		return 0, fmt.Errorf("未找到签到设定: %s", arg)
	}
	return k, nil
}

// dailySign 执行每日签到，返回签到卡片；渲染失败时回退为文本摘要。
func dailySign(ctx context.Context, c sdk.Call, u *userState, now time.Time, preset int) error {
	today := now.Format("20060102")
	coin := coinName(ctx, c)
	if u.LastSign == today {
		if err := c.Reply(ctx, "今天你已经签到过了！"); err != nil {
			return err
		}
		// 对齐上游：重复签到时重新发送今日签到图。
		info := cardInfo{
			Title:  "今日已签到",
			Reward: fmt.Sprintf("%s + %d", coin, u.LastReward),
			Hint:   "今天你已经签到过了！",
		}
		return replySignCard(ctx, c, *u, info, preset, u.backgroundOn(today), now, coin)
	}

	capHit := u.Exp >= scoreMax
	if !capHit {
		u.Exp++
	}
	u.Level = getrank(u.Exp)

	// 对齐上游：1 + 随机 0~9 + 等级 * 5。
	add := 1 + randIntn(10) + u.Level*5
	u.Coins += add
	u.LastReward = add
	u.LastSign = today
	bg := u.setTodayBackground(today)

	if err := saveUser(ctx, c, *u); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if err := rememberUser(ctx, c, u.UID); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if capHit {
		if err := c.Reply(ctx, "你的等级已经达到上限"); err != nil {
			return err
		}
	}

	info := cardInfo{
		Title:  "签到成功",
		Reward: fmt.Sprintf("%s + %d", coin, add),
		Hint:   getHourWord(now) + "！今天也要元气满满哦~",
	}
	if capHit {
		info.Hint += "（等级已满）"
	}
	return replySignCard(ctx, c, *u, info, preset, bg, now, coin)
}

// handleBackground 对齐上游“获得签到背景”：仅群聊，查询目标用户今日
// 签到背景并发送背景图；@ 他人或留空查询自己。
func handleBackground(ctx context.Context, c sdk.Call, args string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该功能仅限群聊使用")
	}
	target := ""
	if ids := sdk.MentionUserIDs(c.Event); len(ids) > 0 {
		target = ids[0]
	} else if args == "" {
		target = c.Event.UserID
	}
	if target == "" {
		return c.Reply(ctx, "签到背景加载失败")
	}
	u, err := loadUser(ctx, c, target)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	today := nowFunc().Format("20060102")
	theme := u.backgroundOn(today)
	if !validTheme(theme) {
		return c.Reply(ctx, "签到背景加载失败")
	}

	img, err := renderHTML(ctx, backgroundHTML(theme, displayName(u), today), sdk.RenderOptions{
		Width: 640, Height: 420, FullPage: true,
	})
	if err == nil {
		if sendErr := c.ReplyImageBytes(ctx, img); sendErr == nil {
			return nil
		} else {
			err = sendErr
		}
	}
	return c.Reply(ctx, fmt.Sprintf("%s 今日签到背景：%s（生成背景图失败：%v）", displayName(u), backgroundNames[theme], err))
}

// handlePreset 设置本群（私聊则本人）签到卡片预设。ShizukuBot 没有内置
// 权限系统，因此以插件配置 admin_users 作为超级管理员名单；名单为空时
// 拒绝所有设置，对齐上游 SuperUserPermission。
func handlePreset(ctx context.Context, c sdk.Call, arg string) error {
	k, err := strconv.Atoi(arg)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if k < 0 || k >= presetCount {
		return c.Reply(ctx, "ERROR: 未找到签到设定: "+arg)
	}
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}

	if c.Event.GroupID != "" {
		if err := sdk.SaveJSON(ctx, c.State, groupKey(c.Event.GroupID, "preset"), k); err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
	} else {
		u, err := loadUser(ctx, c, c.Event.UserID)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		u.Preset = k
		if err := saveUser(ctx, c, u); err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
	}

	return c.Reply(ctx, "设置成功")
}

// handleMyWallet 对齐上游钱包查询：支持 @ 他人或直接写 QQ 号，
// 回复格式与上游一致。
func handleMyWallet(ctx context.Context, c sdk.Call, args string) error {
	target := ""
	if ids := sdk.MentionUserIDs(c.Event); len(ids) > 0 {
		target = ids[0]
	} else if args == "" {
		target = c.Event.UserID
	} else {
		if _, err := strconv.ParseInt(args, 10, 64); err != nil {
			return c.Reply(ctx, "QQ号处理失败")
		}
		target = args
	}
	if target == "" {
		return c.Reply(ctx, "QQ号处理失败")
	}
	u, err := loadUser(ctx, c, target)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return c.Reply(ctx, fmt.Sprintf("QQ号：%s，的钱包有 %d%s", u.UID, u.Coins, coinName(ctx, c)))
}

// handleSetCoinName 对齐上游钱包的「设置硬币名称」，仅超级管理员可用。
func handleSetCoinName(ctx context.Context, c sdk.Call, name string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	if err := sdk.SaveJSON(ctx, c.State, coinNameKey, name); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return c.Reply(ctx, "记住啦~")
}

// handleAdjustWallet 对齐上游「管理钱包余额[+金额|-金额][@xxx]」，
// 仅超级管理员可用；未 @ 时修改自己的钱包。
func handleAdjustWallet(ctx context.Context, c sdk.Call, args string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	if !amountRe.MatchString(args) {
		return c.Reply(ctx, "输入的金额异常")
	}
	amount, err := strconv.Atoi(args)
	if err != nil {
		return c.Reply(ctx, "输入的金额异常")
	}
	target := c.Event.UserID
	if ids := sdk.MentionUserIDs(c.Event); len(ids) > 0 {
		target = ids[0]
	}
	if target == "" {
		return c.Reply(ctx, "QQ号处理失败")
	}
	u, err := loadUser(ctx, c, target)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if u.Coins+amount < 0 {
		return c.Reply(ctx, "管理失败:对方钱包余额不足，扣款失败")
	}
	u.Coins += amount
	if err := saveUser(ctx, c, u); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return c.Reply(ctx, fmt.Sprintf("钱包余额修改成功，已修改用户:%s的钱包，修改金额为：%d", target, amount))
}

// handleTransfer 对齐上游「钱包转账[金额][@xxx]」，仅限群聊且必须 @ 收款人。
func handleTransfer(ctx context.Context, c sdk.Call, args string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该功能仅限群聊使用")
	}
	if !transferAmtRe.MatchString(args) {
		return c.Reply(ctx, "输入额异常，请检查金额或at是否正常")
	}
	amount, err := strconv.Atoi(args)
	if err != nil || amount <= 0 {
		return c.Reply(ctx, "输入额异常，请检查金额或at是否正常")
	}
	ids := sdk.MentionUserIDs(c.Event)
	if len(ids) == 0 {
		return c.Reply(ctx, "获取被转方信息失败")
	}
	target := ids[0]
	sender, err := loadUser(ctx, c, c.Event.UserID)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if amount > sender.Coins {
		return c.Reply(ctx, "[ERROR]:钱包余额不足，转账失败")
	}
	sender.Coins -= amount
	if err := saveUser(ctx, c, sender); err != nil {
		return c.Reply(ctx, "[ERROR]:转账失败，扣款异常:\n"+err.Error())
	}
	receiver, err := loadUser(ctx, c, target)
	if err != nil {
		return c.Reply(ctx, "[ERROR]:转账失败，转账时银行被打劫:\n"+err.Error())
	}
	receiver.Coins += amount
	if err := saveUser(ctx, c, receiver); err != nil {
		return c.Reply(ctx, "[ERROR]:转账失败，转账时银行被打劫:\n"+err.Error())
	}
	return c.ReplyAt(ctx, fmt.Sprintf("转账成功:成功给 ,转账:%d%s", amount, coinName(ctx, c)), target)
}

// handleLevelRank 跨群按经验排名 TopN。
func handleLevelRank(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该功能仅限群聊使用")
	}
	users, err := loadIndexedUsers(ctx, c, usersIndexKey)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	entries := make([]rankEntry, 0, len(users))
	for _, u := range users {
		if u.Exp == 0 {
			continue
		}
		entries = append(entries, rankEntry{UID: u.UID, Name: displayName(u), Value: u.Exp})
	}
	return replyRank(ctx, c, "等级排名", "经验", entries, "ERROR: 目前还没有人签到过", "rank:level")
}

// handleWalletRank 仅统计本群签到过的用户，按金币排名 TopN。
func handleWalletRank(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该功能仅限群聊使用")
	}
	users, err := loadIndexedUsers(ctx, c, groupKey(c.Event.GroupID, "signers"))
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	entries := make([]rankEntry, 0, len(users))
	for _, u := range users {
		if u.Coins == 0 {
			continue
		}
		entries = append(entries, rankEntry{UID: u.UID, Name: displayName(u), Value: u.Coins})
	}
	coin := coinName(ctx, c)
	return replyRank(ctx, c, coin+"排名", coin, entries, "ERROR: 当前没人获取过"+coin, "rank:wallet:"+c.Event.GroupID)
}

// replyRank 排序、截断并通过 HTML 渲染排行榜；渲染或发送失败时回退文本。
// 同一自然日与缓存键命中时直接复用上次渲染的图片，对齐上游「1天只刷新1次」。
func replyRank(ctx context.Context, c sdk.Call, title, unit string, entries []rankEntry, emptyMsg, cacheKey string) error {
	if len(entries) == 0 {
		return c.Reply(ctx, emptyMsg)
	}
	sortRankEntries(entries)
	if len(entries) > rankTopN {
		entries = entries[:rankTopN]
	}
	today := nowFunc().Format("20060102")
	if cached := loadRankCache(ctx, c, cacheKey, today); cached != "" {
		if err := c.ReplyImage(ctx, cached); err == nil {
			return nil
		}
	}
	img, err := renderHTML(ctx, rankHTML(title, unit, entries, nowFunc()), sdk.RenderOptions{
		Width: 720, Height: 480, FullPage: true,
	})
	if err != nil {
		return c.Reply(ctx, rankText(title, unit, entries)+"\n生成排名图失败："+err.Error())
	}
	if err := c.ReplyImageBytes(ctx, img); err != nil {
		return c.Reply(ctx, rankText(title, unit, entries))
	}
	saveRankCache(ctx, c, cacheKey, today, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(img))
	return nil
}

type rankCache struct {
	Day   string `json:"day"`
	Image string `json:"image"`
}

func loadRankCache(ctx context.Context, c sdk.Call, key, day string) string {
	var cache rankCache
	if err := sdk.LoadJSON(ctx, c.State, key, &cache); err != nil {
		return ""
	}
	if cache.Day != day {
		return ""
	}
	return cache.Image
}

func saveRankCache(ctx context.Context, c sdk.Call, key, day, image string) {
	if image == "" {
		return
	}
	_ = sdk.SaveJSON(ctx, c.State, key, rankCache{Day: day, Image: image})
}

func replySignCard(ctx context.Context, c sdk.Call, u userState, info cardInfo, preset int, bg string, now time.Time, coin string) error {
	img, err := renderHTML(ctx, signCardHTML(u, info, preset, bg, now, coin), sdk.RenderOptions{
		Width: 640, Height: 420, FullPage: true,
	})
	if err == nil {
		if sendErr := c.ReplyImageBytes(ctx, img); sendErr == nil {
			return nil
		} else {
			err = sendErr
		}
	}
	return c.Reply(ctx, signSummary(u, info, err, coin))
}

func signSummary(u userState, info cardInfo, renderErr error, coin string) string {
	next := nextRankScore(getrank(u.Exp))
	var b strings.Builder
	fmt.Fprintf(&b, "%s！%s（QQ %s）\n", info.Title, displayName(u), u.UID)
	fmt.Fprintf(&b, "%s，当前 %d %s\n", info.Reward, u.Coins, coin)
	fmt.Fprintf(&b, "等级 %d，经验 %d/%d\n", u.Level, u.Exp, next)
	b.WriteString(info.Hint)
	if renderErr != nil {
		b.WriteString("\n签到图生成失败：" + renderErr.Error())
	}
	return b.String()
}

func loadUser(ctx context.Context, c sdk.Call, uid string) (userState, error) {
	var u userState
	if uid == "" {
		return u, nil
	}
	if err := sdk.LoadJSON(ctx, c.State, userKey(uid), &u); err != nil {
		return u, err
	}
	if u.UID == "" {
		u.UID = uid
	}
	if u.Exp < 0 {
		u.Exp = 0
	}
	if u.Exp > scoreMax {
		u.Exp = scoreMax
	}
	if u.Coins < 0 {
		u.Coins = 0
	}
	u.Level = getrank(u.Exp)
	return u, nil
}

func saveUser(ctx context.Context, c sdk.Call, u userState) error {
	if u.UID == "" {
		u.UID = c.Event.UserID
	}
	return sdk.SaveJSON(ctx, c.State, userKey(u.UID), u)
}

func loadIndexedUsers(ctx context.Context, c sdk.Call, key string) ([]userState, error) {
	var ids []string
	if err := sdk.LoadJSON(ctx, c.State, key, &ids); err != nil {
		return nil, err
	}
	users := make([]userState, 0, len(ids))
	for _, id := range ids {
		u, err := loadUser(ctx, c, id)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// rememberUser 维护 users 索引（跨群等级排名）与 group:<gid>:signers
// 索引（本群钱包排名）。State 无法枚举，因此必须显式维护列表。
func rememberUser(ctx context.Context, c sdk.Call, uid string) error {
	if err := appendUnique(ctx, c.State, usersIndexKey, uid); err != nil {
		return err
	}
	if c.Event.GroupID == "" {
		return nil
	}
	return appendUnique(ctx, c.State, groupKey(c.Event.GroupID, "signers"), uid)
}

func appendUnique(ctx context.Context, st sdk.State, key, item string) error {
	if item == "" {
		return nil
	}
	var list []string
	if err := sdk.LoadJSON(ctx, st, key, &list); err != nil {
		return err
	}
	for _, v := range list {
		if v == item {
			return nil
		}
	}
	list = append(list, item)
	return sdk.SaveJSON(ctx, st, key, list)
}

func userKey(uid string) string { return "user:" + uid }

func groupKey(gid, suffix string) string { return "group:" + gid + ":" + suffix }

func presetFor(ctx context.Context, c sdk.Call, u userState) int {
	if c.Event.GroupID != "" {
		var p int
		if err := sdk.LoadJSON(ctx, c.State, groupKey(c.Event.GroupID, "preset"), &p); err == nil {
			return normalizePreset(p)
		}
		return 0
	}
	return normalizePreset(u.Preset)
}

func normalizePreset(p int) int {
	if p < 0 || p >= presetCount {
		return 0
	}
	return p
}

// backgroundFor 根据日期与用户确定当日背景主题，保证同一天结果稳定。
func backgroundFor(day, uid string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(day + "|" + uid))
	return backgroundThemes[int(h.Sum32())%len(backgroundThemes)]
}

func (u *userState) setTodayBackground(day string) string {
	theme := backgroundFor(day, u.UID)
	prefix := day + ":"
	for i, v := range u.Backgrounds {
		if strings.HasPrefix(v, prefix) {
			u.Backgrounds[i] = prefix + theme
			return theme
		}
	}
	u.Backgrounds = append(u.Backgrounds, prefix+theme)
	if len(u.Backgrounds) > 30 {
		u.Backgrounds = u.Backgrounds[len(u.Backgrounds)-30:]
	}
	return theme
}

func (u userState) backgroundOn(day string) string {
	prefix := day + ":"
	for _, v := range u.Backgrounds {
		if strings.HasPrefix(v, prefix) {
			return strings.TrimPrefix(v, prefix)
		}
	}
	return ""
}

func validTheme(theme string) bool {
	for _, v := range backgroundThemes {
		if v == theme {
			return true
		}
	}
	return false
}

func displayName(u userState) string {
	if name := strings.TrimSpace(u.Nickname); name != "" {
		return name
	}
	if u.UID != "" {
		return u.UID
	}
	return "未知用户"
}

// coinName 返回当前硬币名称，未设置或为空时使用上游默认名。
func coinName(ctx context.Context, c sdk.Call) string {
	var name string
	if err := sdk.LoadJSON(ctx, c.State, coinNameKey, &name); err != nil {
		return defaultWalletName
	}
	if strings.TrimSpace(name) == "" {
		return defaultWalletName
	}
	return strings.TrimSpace(name)
}

func adminIDs(c sdk.Call) []string {
	if c.Config == nil {
		return nil
	}
	// 兼容常见的几种配置键名。
	for _, key := range []string{"admin_users", "admins", "admin"} {
		if ids := toStrings(c.Config[key]); len(ids) > 0 {
			return ids
		}
	}
	return nil
}

func toStrings(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		ids := make([]string, 0, len(value))
		for _, item := range value {
			if id, ok := item.(string); ok && strings.TrimSpace(id) != "" {
				ids = append(ids, id)
			}
		}
		return ids
	case string:
		if id := strings.TrimSpace(value); id != "" {
			return []string{id}
		}
	}
	return nil
}

func isAdmin(c sdk.Call) bool {
	for _, id := range adminIDs(c) {
		if id == c.Event.UserID {
			return true
		}
	}
	return false
}

func getHourWord(t time.Time) string {
	h := t.Hour()
	switch {
	case 6 <= h && h < 12:
		return "早上好"
	case 12 <= h && h < 14:
		return "中午好"
	case 14 <= h && h < 19:
		return "下午好"
	case 19 <= h && h < 24:
		return "晚上好"
	case 0 <= h && h < 6:
		return "凌晨好"
	default:
		return ""
	}
}

// getrank 与上游完全一致：返回经验所在等级。
func getrank(count int) int {
	for k, v := range rankArray {
		if count == v {
			return k
		} else if count < v {
			return k - 1
		}
	}
	return -1
}

func nextRankScore(rank int) int {
	if rank < 0 {
		return rankArray[1]
	}
	if rank < len(rankArray)-1 {
		return rankArray[rank+1]
	}
	return scoreMax
}

type rankEntry struct {
	UID   string
	Name  string
	Value int
}

func sortRankEntries(entries []rankEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Value == entries[j].Value {
			return entries[i].UID < entries[j].UID
		}
		return entries[i].Value > entries[j].Value
	})
}

func rankText(title, unit string, entries []rankEntry) string {
	var b strings.Builder
	b.WriteString(title)
	for i, e := range entries {
		fmt.Fprintf(&b, "\n%d. %s（%s）：%d %s", i+1, e.Name, e.UID, e.Value, unit)
	}
	return b.String()
}

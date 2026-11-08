package gloryofkings

// 营地币余额与营地币兑换。口径对齐上游：
//   apps/campCoin.js      指令与三段式兑换
//   utils/campMall.js     余额解析
//   utils/campExchange.js 兑换清单解析 / 分组 / 编号 / 请求体

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"shizukubot/sdk"
)

const (
	exchangeGameIDSmoba = "20001"
	exchangeUnlimited   = -1
)

var (
	exchangeNoRe      = regexp.MustCompile(`\d+`)
	exchangeConfirmRe = regexp.MustCompile(`确认`)
	exchangeMsgPrefix = regexp.MustCompile(`^-?\d+:`)
)

// ---------------------------------------------------------------- 响应结构

type campCoinData struct {
	UserCurrencyCnt *scalarValue      `json:"userCurrencyCnt"`
	ExchangeInfo    *campExchangeInfo `json:"exchangeInfo"`
	ExchangeBanners []json.RawMessage `json:"exchangeBanners"`
	ServerTime      scalarValue       `json:"serverTime"`
}

type campExchangeInfo struct {
	GiftList   []campExchangeRawGift `json:"giftList"`
	EndTime    string                `json:"endTime"`
	RuleText   string                `json:"ruleText"`
	ExchangeID scalarValue           `json:"exchangeId"`
}

type campExchangeRawGift struct {
	ExchangePos   scalarValue  `json:"exchangePos"`
	Priority      scalarValue  `json:"priority"`
	GameID        scalarValue  `json:"gameId"`
	PackageName   string       `json:"sPackageName"`
	PackageDesc   string       `json:"sPackageDesc"`
	Currency      scalarValue  `json:"currency"`
	PackageID     scalarValue  `json:"sPackageId"`
	ItemLeft      scalarValue  `json:"itemLeft"`
	UserLeft      *scalarValue `json:"userLeft"`
	UserLimit     scalarValue  `json:"userLimit"`
	EndTime       string       `json:"endTime"`
	PackageIcon   string       `json:"sPackageIconUrl"`
	PackageIcon2  string       `json:"sPackageIcon"`
	UserLimitType scalarValue  `json:"userLimitType"`
}

// ---------------------------------------------------------------- API

// getCampCoin 查营地币余额。响应带 data 包裹，同时兼容字段在顶层的形态。
func (c *campClient) getCampCoin(ctx context.Context, a *campAuth) (*campCoinData, error) {
	extra := map[string]string{"accountId": a.UserID}
	raw, err := c.gameFormCall(ctx, "/play/h5lotteryquery", extra, a)
	if err != nil {
		return nil, err
	}
	return decodeCoinData(raw)
}

// getCampExchange 拉兑换清单（必须带 type=exchange，否则 exchangeInfo 恒为 null）。
// 这个端点的响应不套 data，字段全在顶层。
func (c *campClient) getCampExchange(ctx context.Context, a *campAuth) (*campCoinData, error) {
	code, msg, data, full, err := c.callRaw(ctx, "/operation/lotteryquery", map[string]any{
		"gameId": exchangeGameIDSmoba,
		"type":   "exchange",
	}, a)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, businessError(code, msg)
	}
	raw := data
	if len(raw) == 0 {
		raw = full
	}
	return decodeCoinData(raw)
}

// exchangeCampGift 真兑换（写操作，调用方不要再重试）。
func (c *campClient) exchangeCampGift(ctx context.Context, body map[string]string, a *campAuth) (int, string, error) {
	code, msg, _, _, err := c.callRaw(ctx, "/operation/lotterygetgift", body, a)
	return code, msg, err
}

func decodeCoinData(raw json.RawMessage) (*campCoinData, error) {
	if len(raw) == 0 {
		return &campCoinData{}, nil
	}
	var envelope struct {
		Data *json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data != nil {
		payload := strings.TrimSpace(string(*envelope.Data))
		if payload != "" && payload != "null" {
			raw = *envelope.Data
		}
	}
	var out campCoinData
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- 纯逻辑

type campExchangeGift struct {
	No        int
	Pos       string
	GameID    string
	Name      string
	Desc      string
	Price     int
	PkgID     string
	Left      int
	Unlimited bool
	SoldOut   bool
	UserLeft  *int
	Limit     int
	MineUsed  bool
	EndTime   string
	Icon      string
	CycleType int
}

type campExchangeView struct {
	Gifts      []campExchangeGift
	Coin       *int
	EndTime    string
	RuleText   string
	ActivityID string
}

func normalizeExchangeGift(raw campExchangeRawGift) (campExchangeGift, bool) {
	pos := strings.TrimSpace(raw.ExchangePos.String())
	if pos == "" {
		return campExchangeGift{}, false
	}

	gameID := strings.TrimSpace(raw.GameID.String())
	left := exchangeInt(raw.ItemLeft, exchangeUnlimited)
	limit := exchangeInt(raw.UserLimit, 0)

	gift := campExchangeGift{
		Pos:       pos,
		GameID:    gameID,
		Name:      firstNonEmpty(strings.TrimSpace(raw.PackageName), "未命名商品"),
		Desc:      strings.TrimSpace(raw.PackageDesc),
		Price:     exchangeInt(raw.Currency, 0),
		PkgID:     strings.TrimSpace(raw.PackageID.String()),
		Left:      left,
		Unlimited: left == exchangeUnlimited,
		SoldOut:   left == 0,
		Limit:     limit,
		EndTime:   strings.TrimSpace(raw.EndTime),
		Icon:      firstNonEmpty(strings.TrimSpace(raw.PackageIcon), strings.TrimSpace(raw.PackageIcon2)),
		CycleType: exchangeInt(raw.UserLimitType, 0),
	}
	if raw.UserLeft != nil {
		userLeft := exchangeInt(*raw.UserLeft, 0)
		gift.UserLeft = &userLeft
		gift.MineUsed = userLeft == 0
	}
	return gift, true
}

func parseExchange(data *campCoinData) *campExchangeView {
	if data == nil {
		data = &campCoinData{}
	}
	view := &campExchangeView{}
	if data.ExchangeInfo != nil {
		view.EndTime = strings.TrimSpace(data.ExchangeInfo.EndTime)
		view.RuleText = strings.TrimSpace(data.ExchangeInfo.RuleText)
		view.ActivityID = strings.TrimSpace(data.ExchangeInfo.ExchangeID.String())
	}

	if data.UserCurrencyCnt != nil && strings.TrimSpace(data.UserCurrencyCnt.String()) != "" {
		coin := exchangeInt(*data.UserCurrencyCnt, 0)
		view.Coin = &coin
	}

	if data.ExchangeInfo == nil {
		return view
	}

	// 按 priority（默认 9999）再按 exchangePos 排序，和营地 App 一致。
	sorted := make([]campExchangeRawGift, 0, len(data.ExchangeInfo.GiftList))
	sorted = append(sorted, data.ExchangeInfo.GiftList...)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi := exchangeInt(sorted[i].Priority, 9999)
		pj := exchangeInt(sorted[j].Priority, 9999)
		if pi != pj {
			return pi < pj
		}
		return exchangeInt(sorted[i].ExchangePos, 0) < exchangeInt(sorted[j].ExchangePos, 0)
	})

	for _, raw := range sorted {
		if gift, ok := normalizeExchangeGift(raw); ok {
			view.Gifts = append(view.Gifts, gift)
		}
	}
	return view
}

type exchangeGroup struct {
	Type  int
	Title string
	Tip   string
	Gifts []campExchangeGift
}

var exchangeCycleMeta = map[int]struct{ Title, Tip string }{
	0: {"本期", "活动周期内限兑"},
	1: {"本日", "每天 00:00 重置"},
	2: {"本周", "每周一 00:00 重置"},
	3: {"本月", "每月 1 号 00:00 重置"},
}

// groupExchangeByCycle 按限兑周期分组：组按 type 升序，组内保持原顺序。
func groupExchangeByCycle(gifts []campExchangeGift) []exchangeGroup {
	buckets := map[int]*exchangeGroup{}
	order := []int{}
	for _, gift := range gifts {
		group, ok := buckets[gift.CycleType]
		if !ok {
			meta, found := exchangeCycleMeta[gift.CycleType]
			if !found {
				meta = struct{ Title, Tip string }{Title: "限兑"}
			}
			group = &exchangeGroup{Type: gift.CycleType, Title: meta.Title, Tip: meta.Tip}
			buckets[gift.CycleType] = group
			order = append(order, gift.CycleType)
		}
		group.Gifts = append(group.Gifts, gift)
	}
	sort.Ints(order)

	out := make([]exchangeGroup, 0, len(order))
	for _, key := range order {
		out = append(out, *buckets[key])
	}
	return out
}

// numberExchangeGifts 在最终展示顺序上统一编号（必须先分组再编号）。
func numberExchangeGifts(groups []exchangeGroup) []exchangeGroup {
	seq := 0
	out := make([]exchangeGroup, 0, len(groups))
	for _, group := range groups {
		gifts := make([]campExchangeGift, 0, len(group.Gifts))
		for _, gift := range group.Gifts {
			seq++
			gift.No = seq
			gifts = append(gifts, gift)
		}
		group.Gifts = gifts
		out = append(out, group)
	}
	return out
}

func flattenExchangeGroups(groups []exchangeGroup) []campExchangeGift {
	gifts := []campExchangeGift{}
	for _, group := range groups {
		gifts = append(gifts, group.Gifts...)
	}
	return gifts
}

// canExchange 在发写请求前拦掉必然失败的兑换。
func canExchange(gift campExchangeGift, coin *int) (bool, string) {
	if gift.SoldOut {
		return false, "已经抢光了"
	}
	if gift.MineUsed {
		return false, "你已经兑过了，等下次活动"
	}
	if gift.GameID != exchangeGameIDSmoba {
		if gift.GameID == "50001" {
			return false, "这件要在营地 App 里兑"
		}
		return false, "这件是别的游戏的，要在营地 App 里兑"
	}
	if coin != nil && *coin < gift.Price {
		return false, fmt.Sprintf("营地币不够，还差 %d", gift.Price-*coin)
	}
	return true, ""
}

// exchangeBody 兑换请求体（gameId 与 mGameid 必须同时给且等于商品的 gameId）。
func exchangeBody(gift campExchangeGift, role *campRole) map[string]string {
	return map[string]string{
		"giftType":    "exchange",
		"exchangePos": gift.Pos,
		"gameId":      gift.GameID,
		"mGameid":     gift.GameID,
		"platid":      "1",
		"gameOpenid":  roleOpenID(role),
		"areaId":      roleAreaID(role),
		"serverId":    roleServerID(role),
		"campRoleId":  roleRoleID(role),
		"packageNum":  "1",
	}
}

func roleOpenID(role *campRole) string {
	if role == nil {
		return ""
	}
	return strings.TrimSpace(role.OpenID.String())
}

func roleAreaID(role *campRole) string {
	if role == nil {
		return ""
	}
	return strings.TrimSpace(role.AreaID.String())
}

func roleServerID(role *campRole) string {
	if role == nil {
		return ""
	}
	return strings.TrimSpace(role.ServerID.String())
}

func roleRoleID(role *campRole) string {
	if role == nil {
		return ""
	}
	return strings.TrimSpace(role.RoleID.String())
}

func cleanExchangeMsg(msg string) string {
	return strings.TrimSpace(exchangeMsgPrefix.ReplaceAllString(msg, ""))
}

func describeExchangeGift(gift campExchangeGift) string {
	parts := []string{fmt.Sprintf("%d. %s", gift.No, gift.Name), fmt.Sprintf("%d 币", gift.Price)}
	switch {
	case gift.SoldOut:
		parts = append(parts, "已抢光")
	case gift.MineUsed:
		parts = append(parts, "已兑过")
	case gift.GameID != exchangeGameIDSmoba:
		parts = append(parts, "需在 App 兑")
	case !gift.Unlimited && gift.Left > 0:
		parts = append(parts, fmt.Sprintf("剩 %d 份", gift.Left))
	}
	return strings.Join(parts, " · ")
}

func exchangeInt(value scalarValue, fallback int) int {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return fallback
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return fallback
	}
	return int(number)
}

func pickRoleForGame(groups []campGameRoleGroup, gameID string) *campRole {
	for i := range groups {
		if strings.TrimSpace(groups[i].GameID.String()) != gameID {
			continue
		}
		roles := groups[i].Roles
		for j := range roles {
			if strings.TrimSpace(roles[j].IsMain.String()) == "1" {
				return &roles[j]
			}
		}
		if len(roles) > 0 {
			return &roles[0]
		}
		return nil
	}
	for i := range groups {
		if len(groups[i].Roles) > 0 {
			return &groups[i].Roles[0]
		}
	}
	return nil
}

// ---------------------------------------------------------------- 视图

type coinBalanceView struct {
	Coin         *int
	HasExchange  bool
	ExchangeText string
	// BannerLinks 是活动 Banner 里的跳转地址，上游结构不固定，按字段名优先级提取。
	BannerLinks []string
}

// bannerLinkKeys 是活动 Banner 里可能放跳转地址的字段，按优先级排列。
var bannerLinkKeys = []string{"sJumpUrl", "jumpUrl", "jump_url", "sJumpLink", "sUrl", "url", "link", "sLinkUrl"}

// bannerLinks 从原始 Banner 数组里提取 http(s) 跳转链接，去重后按出现顺序返回。
func bannerLinks(banners []json.RawMessage) []string {
	links := []string{}
	seen := map[string]bool{}
	for _, raw := range banners {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		for _, key := range bannerLinkKeys {
			v, ok := obj[key].(string)
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				continue
			}
			if seen[v] {
				continue
			}
			seen[v] = true
			links = append(links, v)
			break
		}
	}
	return links
}

func parseCoinBalance(data *campCoinData) coinBalanceView {
	view := coinBalanceView{}
	if data == nil {
		return view
	}
	if data.UserCurrencyCnt != nil && strings.TrimSpace(data.UserCurrencyCnt.String()) != "" {
		coin := exchangeInt(*data.UserCurrencyCnt, 0)
		view.Coin = &coin
	}
	view.HasExchange = data.ExchangeInfo != nil || len(data.ExchangeBanners) > 0
	view.BannerLinks = bannerLinks(data.ExchangeBanners)
	if data.ExchangeInfo != nil && strings.TrimSpace(data.ExchangeInfo.EndTime) != "" {
		end := strings.TrimSpace(data.ExchangeInfo.EndTime)
		if len(end) > 10 {
			end = end[:10]
		}
		view.ExchangeText = fmt.Sprintf("兑换活动到 %s 截止", end)
	} else if len(data.ExchangeBanners) > 0 {
		view.ExchangeText = fmt.Sprintf("有 %d 个活动入口", len(data.ExchangeBanners))
	}
	return view
}

type exchangeGiftView struct {
	No        int
	SoldOut   bool
	MineUsed  bool
	ShowLeft  bool
	LeftText  string
	Icon      string
	Name      string
	LimitText string
	Price     int
}

type exchangeGroupView struct {
	Title string
	Tip   string
	Gifts []exchangeGiftView
}

type exchangePageView struct {
	Avatar   string
	Who      string
	CoinText string
	Groups   []exchangeGroupView
	EndTime  string
}

func toExchangeCard(gift campExchangeGift) exchangeGiftView {
	limitText := "限兑 1 份"
	if gift.Limit > 1 {
		limitText = fmt.Sprintf("限兑 %d 份", gift.Limit)
	}
	leftText := ""
	if !gift.Unlimited {
		left := gift.Left
		if left < 0 {
			left = 0
		}
		leftText = fmt.Sprintf("剩 %d 份", left)
	}
	return exchangeGiftView{
		No:        gift.No,
		SoldOut:   gift.SoldOut,
		MineUsed:  gift.MineUsed,
		ShowLeft:  !gift.Unlimited,
		LeftText:  leftText,
		Icon:      gift.Icon,
		Name:      gift.Name,
		LimitText: limitText,
		Price:     gift.Price,
	}
}

func buildExchangePage(groups []exchangeGroup, who, coinText, avatar, endTime string) exchangePageView {
	page := exchangePageView{
		Avatar:   avatar,
		Who:      who,
		CoinText: coinText,
		EndTime:  endTime,
	}
	for _, group := range groups {
		view := exchangeGroupView{Title: group.Title, Tip: group.Tip}
		for _, gift := range group.Gifts {
			view.Gifts = append(view.Gifts, toExchangeCard(gift))
		}
		page.Groups = append(page.Groups, view)
	}
	return page
}

// ---------------------------------------------------------------- 指令

// coinCommand 处理 #营地币 / #营地余额。
//
// 余额是账号级的：逐个查本人绑定的营地号，0 枚与没角色的号不显示，
// 读不到余额则明确说「读取失败」。
func coinCommand(ctx context.Context, c sdk.Call) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return c.Reply(ctx, "还没有绑定营地号，先发 #王者绑定 或 #营地QQ全局登录")
	}

	client := newCampClient()
	rows := []string{}
	for _, campID := range state.IDs {
		label := fmt.Sprintf("%s（%s）", accountLabel(state, campID), campID)

		cred := state.Credentials[campID]
		if cred == nil || cred.Token == "" {
			rows = append(rows, label+"：这个营地号还没有登录态，重新登录后再试")
			continue
		}

		data, err := client.getCampCoin(ctx, cred.auth())
		if err != nil {
			rows = append(rows, label+"：查询失败 —— "+signErrorText(err))
			continue
		}
		view := parseCoinBalance(data)
		if view.Coin == nil {
			rows = append(rows, label+"：读取失败")
			continue
		}
		if *view.Coin <= 0 {
			continue
		}
		rows = append(rows, fmt.Sprintf("%s：%d 营地币", label, *view.Coin))
		if view.HasExchange && view.ExchangeText != "" {
			rows = append(rows, "　"+view.ExchangeText)
		}
		if len(view.BannerLinks) > 0 {
			items := make([]string, 0, len(view.BannerLinks))
			for i, link := range view.BannerLinks {
				items = append(items, fmt.Sprintf("[活动入口%d](%s)", i+1, link))
			}
			rows = append(rows, "　"+strings.Join(items, "　"))
		}
	}

	lines := []string{"营地币余额"}
	if len(rows) > 0 {
		lines = append(lines, rows...)
	} else {
		lines = append(lines, "", fmt.Sprintf("名下 %d 个营地号现在都没有营地币", len(state.IDs)))
	}
	lines = append(lines, "", "营地币来自每日签到与营地任务；发 #营地兑换 看能换什么")
	text := strings.Join(lines, "\n")
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
}

// exchangeCommand 处理 #营地兑换 / #营地兑换 N / #营地兑换 N 确认。
//
// 三段式：列清单 → 确认提示 → 真兑换。兑换不可逆，多一步确认少一次手滑。
func exchangeCommand(ctx context.Context, c sdk.Call, rest string) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID := state.currentID()
	if campID == "" {
		return c.Reply(ctx, "还没有绑定营地号，先发 #王者绑定 或 #营地QQ全局登录")
	}
	cred := state.Credentials[campID]
	if cred == nil || cred.Token == "" {
		return c.Reply(ctx, fmt.Sprintf("营地号 %s 还没有登录态，重新登录后再试", campID))
	}

	client := newCampClient()
	data, err := client.getCampExchange(ctx, cred.auth())
	if err != nil {
		return c.Reply(ctx, "兑换清单拉取失败 —— "+signErrorText(err))
	}
	view := parseExchange(data)
	if len(view.Gifts) == 0 {
		return c.Reply(ctx, "现在没有可兑换的东西")
	}

	groups := numberExchangeGifts(groupExchangeByCycle(view.Gifts))
	coinText := "读取失败"
	if view.Coin != nil {
		coinText = strconv.Itoa(*view.Coin)
	}
	who := accountLabel(state, campID)

	noMatch := exchangeNoRe.FindString(rest)
	if noMatch == "" {
		page := buildExchangePage(groups, who, coinText, avatarURL(c.Event.UserID), view.EndTime)
		image, err := renderCampExchange(ctx, page)
		if err == nil && len(image) > 0 {
			// 图片消息不能带键盘：先发一条带快捷按钮的 Markdown，再发清单图。
			if err := c.ReplyKeyboardOrText(ctx,
				fmt.Sprintf("营地币兑换（%s，%s 营地币）", who, coinText), campKeyboard()); err != nil {
				return err
			}
			return c.ReplyImageBytes(ctx, image)
		}
		return c.ReplyKeyboardOrText(ctx, exchangeTextFallback(groups, who, coinText), campKeyboard())
	}

	number, err := strconv.Atoi(noMatch)
	if err != nil {
		return c.Reply(ctx, fmt.Sprintf("没有 %s 号商品，发 #营地兑换 看清单", noMatch))
	}

	var target *campExchangeGift
	ordered := flattenExchangeGroups(groups)
	for i := range ordered {
		if ordered[i].No == number {
			target = &ordered[i]
			break
		}
	}
	if target == nil {
		return c.Reply(ctx, fmt.Sprintf("没有 %d 号商品，发 #营地兑换 看清单", number))
	}

	if ok, reason := canExchange(*target, view.Coin); !ok {
		return c.Reply(ctx, fmt.Sprintf("%s：%s", target.Name, reason))
	}

	if !exchangeConfirmRe.MatchString(rest) {
		return c.ReplyKeyboardOrText(ctx, fmt.Sprintf(
			"要用 %d 营地币换「%s」吗？\n号：%s（现有 %s 营地币）\n确定就发：#营地兑换 %d 确认",
			target.Price, target.Name, who, coinText, target.No,
		), confirmExchangeKeyboard(target.No))
	}

	roleGroups, err := client.getRoleList(ctx, cred.auth())
	if err != nil {
		return c.Reply(ctx, "查角色失败 —— "+signErrorText(err))
	}
	role := pickRoleForGame(roleGroups, target.GameID)
	if role == nil || roleRoleID(role) == "" {
		return c.Reply(ctx, fmt.Sprintf("%s 还没绑定对应的游戏角色，换不了（奖励要发到角色身上）", who))
	}

	code, msg, err := client.exchangeCampGift(ctx, exchangeBody(*target, role), cred.auth())
	if err != nil {
		return c.Reply(ctx, "兑换失败 —— "+signErrorText(err))
	}
	if code != 0 {
		text := cleanExchangeMsg(msg)
		if text == "" {
			text = fmt.Sprintf("错误码 %d", code)
		}
		return c.Reply(ctx, "没换成："+text)
	}

	roleName := strings.TrimSpace(role.RoleName)
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf(
		"换好了：%s\n花了 %d 营地币，奖励发到「%s」，去游戏内邮件查收",
		target.Name, target.Price, roleName,
	), campKeyboard())
}

// confirmExchangeKeyboard 兑换确认提示下的按钮；序号随商品动态拼接，
// 清单刷新后旧按钮会返回「没有该序号商品」。
func confirmExchangeKeyboard(no int) sdk.Keyboard {
	return sdk.NewKeyboard(
		sdk.NewRow(
			sdk.CommandButton(fmt.Sprintf("gok-confirm-%d", no), "确认兑换",
				fmt.Sprintf("#营地兑换 %d 确认", no)),
		),
		sdk.NewRow(
			sdk.CommandButton("gok-exchange", "查看清单", "#营地兑换"),
		),
	)
}

func exchangeTextFallback(groups []exchangeGroup, who, coinText string) string {
	lines := []string{fmt.Sprintf("营地币兑换（%s　%s 营地币）", who, coinText)}
	for _, group := range groups {
		lines = append(lines, "", group.Title+" · 限兑")
		for _, gift := range group.Gifts {
			lines = append(lines, describeExchangeGift(gift))
		}
	}
	lines = append(lines, "", "发 #营地兑换 编号 选一件；真要换再加「确认」两个字")
	return strings.Join(lines, "\n")
}

func avatarURL(userID string) string {
	if userID == "" {
		return ""
	}
	return "https://q1.qlogo.cn/g?b=qq&s=100&nk=" + userID
}

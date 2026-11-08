// Package niuniu 牛牛大作战（文本版）。上游依赖图片绘制与钱包，
// 这里改为文本输出，并用插件状态存放群内玩家与拍卖行数据。
package niuniu

import (
	"context"
	"fmt"
	"math"
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

var (
	nowFunc = time.Now
	// unit 便于测试替换随机源。
	unit = rand.Float64
	mu   sync.Mutex

	glueRe    = regexp.MustCompile(`^(?:使用(.+))?打胶$`)
	jjRe      = regexp.MustCompile(`^(?:使用(.+))?jj@(\S+)$`)
	buyPropRe = regexp.MustCompile(`^购买道具\s*([0-9]+)-([0-9]+)$`)
	buyAucRe  = regexp.MustCompile(`^购买牛牛\s*([0-9]+)$`)
	seqRe     = regexp.MustCompile(`^([0-9]+)$`)
	propSeqRe = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)
)

const (
	startCoins   = 500
	glueCD       = 90 * time.Second
	jjCD         = 150 * time.Second
	redeemWindow = time.Hour
	redeemCount  = 4
	// redeemConfirmWindow 对齐上游赎牛牛的 2 分钟「是/否」确认。
	redeemConfirmWindow = 2 * time.Minute
	// sessionWindow 对齐上游商店/拍卖行的 120 秒回复会话。
	sessionWindow = 2 * time.Minute
	// cancelWindow / cancelFee 对齐上游注销：24 小时内第 N 次注销扣 N*50。
	cancelWindow = 24 * time.Hour
	cancelFee    = 50
)

type item struct {
	ID   int
	Name string
	Cost int
	Desc string
}

var shopItems = []item{
	{1, "伟哥", 100, "打胶时必定增长更多"},
	{2, "媚药", 100, "打胶时必定减少"},
	{3, "击剑神器", 300, "jj 时必定获胜"},
	{4, "击剑神稽", 300, "jj 时必定落败"},
}

type player struct {
	Length   float64        `json:"length"`
	Depth    float64        `json:"depth"`
	Coins    int            `json:"coins"`
	Props    map[string]int `json:"props,omitempty"`
	LastGlue int64          `json:"last_glue,omitempty"`
	LastJJ   int64          `json:"last_jj,omitempty"`
	Sold     bool           `json:"sold,omitempty"`
	PKCount  int            `json:"pk_count,omitempty"`
	PKLength float64        `json:"pk_length,omitempty"`
	PKTime   int64          `json:"pk_time,omitempty"`
}

type auctionItem struct {
	ID     int     `json:"id"`
	From   string  `json:"from"`
	Price  int     `json:"price"`
	Length float64 `json:"length"`
	Depth  float64 `json:"depth"`
}

type game struct {
	Users       map[string]*player `json:"users"`
	Auction     []auctionItem      `json:"auction,omitempty"`
	NextAuction int                `json:"next_auction,omitempty"`
}

// Definition subscribes to group messages for all 牛牛 commands.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "niuniu",
		Name:        "牛牛大作战",
		Version:     "1.0.0",
		Description: "群内牛牛养成：注册牛牛、打胶、jj@用户ID 击剑、排行榜、商店与拍卖行",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "niuniu", Trigger: "注册牛牛"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle routes the group-only commands; every other message is ignored.
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	if done, err := handleSession(ctx, c, text); done {
		return err
	}
	if done, err := handleRedeemConfirm(ctx, c, text); done {
		return err
	}
	switch text {
	case "注册牛牛":
		return doRegister(ctx, c)
	case "查看我的牛牛":
		return doView(ctx, c)
	case "牛子长度排行":
		return doRanking(ctx, c, true)
	case "牛子深度排行":
		return doRanking(ctx, c, false)
	case "出售牛牛":
		return doSell(ctx, c)
	case "牛牛拍卖行":
		return doAuction(ctx, c)
	case "牛牛商店":
		return doShop(ctx, c)
	case "牛牛背包":
		return doBag(ctx, c)
	case "赎牛牛":
		return doRedeem(ctx, c)
	case "注销牛牛":
		return doCancel(ctx, c)
	}
	if m := glueRe.FindStringSubmatch(text); m != nil {
		return doGlue(ctx, c, m[1])
	}
	if m := jjRe.FindStringSubmatch(text); m != nil {
		return doJJ(ctx, c, m[1], m[2])
	}
	if m := buyPropRe.FindStringSubmatch(text); m != nil {
		return doBuyProp(ctx, c, m[1], m[2])
	}
	if m := buyAucRe.FindStringSubmatch(text); m != nil {
		return doBuyAuction(ctx, c, m[1])
	}
	return nil
}

func stateKey(c sdk.Call) string { return "niu:" + c.Event.GroupID }

// shopSession 是上游商店/拍卖行的 120 秒回复会话，按群与用户隔离。
type shopSession struct {
	Kind   string `json:"kind"` // shop 或 auction
	Expire int64  `json:"expire"`
}

func sessionKey(c sdk.Call) string {
	return "niu:session:" + c.Event.GroupID + ":" + c.Event.UserID
}

func loadSession(ctx context.Context, c sdk.Call) *shopSession {
	s := &shopSession{}
	if err := sdk.LoadJSON(ctx, c.State, sessionKey(c), s); err != nil || s.Kind == "" {
		return nil
	}
	return s
}

func saveSession(ctx context.Context, c sdk.Call, kind string) error {
	return sdk.SaveJSON(ctx, c.State, sessionKey(c), shopSession{
		Kind:   kind,
		Expire: nowFunc().Add(sessionWindow).Unix(),
	})
}

func clearSession(ctx context.Context, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, c.State, sessionKey(c), shopSession{})
}

// handleSession 处理商店/拍卖行会话内的序号回复；其他消息保留会话。
func handleSession(ctx context.Context, c sdk.Call, text string) (bool, error) {
	s := loadSession(ctx, c)
	if s == nil {
		return false, nil
	}
	var id, count, rawID string
	switch s.Kind {
	case "shop":
		if m := propSeqRe.FindStringSubmatch(text); m != nil {
			id, count = m[1], m[2]
		} else if m := buyPropRe.FindStringSubmatch(text); m != nil {
			id, count = m[1], m[2]
		}
	case "auction":
		if m := buyAucRe.FindStringSubmatch(text); m != nil {
			rawID = m[1]
		} else if m := seqRe.FindStringSubmatch(text); m != nil {
			rawID = m[1]
		}
	}
	if id == "" && rawID == "" {
		return false, nil
	}
	if nowFunc().Unix() > s.Expire {
		clearSession(ctx, c)
		return true, c.Reply(ctx, "超时，已自动取消")
	}
	clearSession(ctx, c)
	if s.Kind == "shop" {
		return true, doBuyProp(ctx, c, id, count)
	}
	return true, doBuyAuction(ctx, c, rawID)
}

// mainKeyboard 牛牛常用操作的快捷按钮。
func mainKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("niu-glue", "打胶", "打胶"),
		sdk.CommandButton("niu-shop", "商店", "牛牛商店"),
		sdk.CommandButton("niu-bag", "背包", "牛牛背包"),
	))
}

// shopKeyboard 商店的一键购买按钮，编号取自商品 ID。
func shopKeyboard() sdk.Keyboard {
	row := make([]sdk.Button, 0, len(shopItems))
	for _, v := range shopItems {
		row = append(row, sdk.CommandButton(
			"niu-buy-"+strconv.Itoa(v.ID),
			"购买"+v.Name,
			fmt.Sprintf("购买道具 %d-1", v.ID),
		))
	}
	return sdk.NewKeyboard(
		sdk.NewRow(row...),
		sdk.NewRow(
			sdk.CommandButton("niu-glue", "打胶", "打胶"),
			sdk.CommandButton("niu-bag", "背包", "牛牛背包"),
		),
	)
}

// auctionKeyboard 拍卖行的一键购买按钮，序号随商品动态生成；
// 商品售出后按钮仍显示，但点击会返回「没有这个序号的商品」。
func auctionKeyboard(items []auctionItem) sdk.Keyboard {
	row := make([]sdk.Button, 0, len(items))
	for i, v := range items {
		if i >= 5 {
			break
		}
		row = append(row, sdk.CommandButton(
			"niu-auc-"+strconv.Itoa(v.ID),
			fmt.Sprintf("购买%d号", v.ID),
			fmt.Sprintf("购买牛牛 %d", v.ID),
		))
	}
	rows := []sdk.KeyboardRow{}
	if len(row) > 0 {
		rows = append(rows, sdk.NewRow(row...))
	}
	rows = append(rows, sdk.NewRow(
		sdk.CommandButton("niu-shop", "商店", "牛牛商店"),
		sdk.CommandButton("niu-bag", "背包", "牛牛背包"),
	))
	return sdk.NewKeyboard(rows...)
}

func load(ctx context.Context, c sdk.Call) game {
	g := game{}
	_ = sdk.LoadJSON(ctx, c.State, stateKey(c), &g)
	if g.Users == nil {
		g.Users = map[string]*player{}
	}
	return g
}

func save(ctx context.Context, c sdk.Call, g game) error {
	return sdk.SaveJSON(ctx, c.State, stateKey(c), g)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func doRegister(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	uid := c.Event.UserID
	p := g.Users[uid]
	if p != nil && !p.Sold && p.Length > 0 {
		mu.Unlock()
		return c.Reply(ctx, "你已经注册过牛牛了，发送「查看我的牛牛」查看")
	}
	coins := startCoins
	props := map[string]int{}
	if p != nil {
		coins = p.Coins
		props = p.Props
	}
	g.Users[uid] = &player{
		Length: round2(5 + unit()*20),
		Depth:  round2(0.5 + unit()*8),
		Coins:  coins,
		Props:  props,
	}
	err := save(ctx, c, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.ReplyKeyboardOrText(ctx, "注册成功！你获得了牛牛，发送「查看我的牛牛」查看属性", mainKeyboard())
}

func doView(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	mu.Unlock()
	if p == nil || p.Length <= 0 {
		if p != nil && p.PKLength > 0 {
			return c.Reply(ctx, "你的牛牛已经被夺走了，发送「赎牛牛」赎回")
		}
		return c.Reply(ctx, "你还没有牛牛，发送「注册牛牛」获得一只")
	}
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("牛牛主人: %s\n长度: %.2fcm\n深度: %.2fcm\n金币: %d",
		c.Event.UserID, p.Length, p.Depth, p.Coins), mainKeyboard())
}

func doGlue(ctx context.Context, c sdk.Call, prop string) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil || p.Length <= 0 {
		mu.Unlock()
		return c.Reply(ctx, "你还没有牛牛，发送「注册牛牛」获得一只")
	}
	if wait := glueCD - nowFunc().Sub(time.Unix(p.LastGlue, 0)); p.LastGlue > 0 && wait > 0 {
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("休息%ds再来打胶吧", int(wait.Seconds())))
	}
	prop = strings.TrimSpace(prop)
	if prop != "" {
		if p.Props[prop] <= 0 {
			mu.Unlock()
			return c.Reply(ctx, "你的背包里没有这个道具")
		}
		p.Props[prop]--
	}
	var delta float64
	switch prop {
	case "伟哥":
		delta = round2(0.5 + unit()*1.5)
	case "媚药":
		delta = round2(-(0.5 + unit()*1.5))
	default:
		delta = round2(unit()*2 - 0.5)
	}
	p.Length = round2(math.Max(0.01, p.Length+delta))
	p.LastGlue = nowFunc().Unix()
	err := save(ctx, c, g)
	current := p.Length
	mu.Unlock()
	if err != nil {
		return err
	}
	if delta >= 0 {
		return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("打胶成功！牛牛长度增加了%.2fcm，当前长度%.2fcm", delta, current), mainKeyboard())
	}
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("打胶成功！牛牛长度减少了%.2fcm，当前长度%.2fcm", -delta, current), mainKeyboard())
}

func doJJ(ctx context.Context, c sdk.Call, prop, target string) error {
	if target == c.Event.UserID {
		return c.Reply(ctx, "不能和自己击剑")
	}
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil || p.Length <= 0 {
		mu.Unlock()
		return c.Reply(ctx, "你还没有牛牛，发送「注册牛牛」获得一只")
	}
	q := g.Users[target]
	if q == nil || q.Length <= 0 {
		if q != nil && q.PKLength > 0 {
			mu.Unlock()
			return c.Reply(ctx, "对方的牛牛已经被夺走了，等待对方赎回")
		}
		mu.Unlock()
		return c.Reply(ctx, "对方还没有注册牛牛")
	}
	if wait := jjCD - nowFunc().Sub(time.Unix(p.LastJJ, 0)); p.LastJJ > 0 && wait > 0 {
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("休息%ds再来击剑吧", int(wait.Seconds())))
	}
	prop = strings.TrimSpace(prop)
	var forced int // 1 必胜, -1 必败
	switch {
	case prop == "击剑神器" && p.Props[prop] > 0:
		p.Props[prop]--
		forced = 1
	case prop == "击剑神稽" && p.Props[prop] > 0:
		p.Props[prop]--
		forced = -1
	case prop != "":
		mu.Unlock()
		return c.Reply(ctx, "你的背包里没有这个道具")
	}
	now := nowFunc()
	p.LastJJ = now.Unix()

	var msg string
	switch {
	case forced == 1 || forced == 0 && p.Length > q.Length:
		q.PKCount++
		q.PKLength = q.Length
		q.PKTime = now.Unix()
		q.Length = round2(q.Length * 0.9)
		p.Length = round2(p.Length + 0.5)
		msg = fmt.Sprintf("你击败了 %s！牛牛长度增长0.50cm，当前%.2fcm\n对方的牛牛缩短了", target, p.Length)
		if q.PKCount >= redeemCount {
			q.Length = 0
			msg += fmt.Sprintf("\n对方已经被夺走了牛牛（%d次），发送「赎牛牛」可以赎回", q.PKCount)
		}
	case forced == -1 || p.Length < q.Length:
		p.PKCount++
		p.PKLength = p.Length
		p.PKTime = now.Unix()
		p.Length = round2(p.Length * 0.9)
		q.Length = round2(q.Length + 0.5)
		msg = fmt.Sprintf("你输给了 %s，你的牛牛缩短了\n当前长度%.2fcm", target, p.Length)
		if p.PKCount >= redeemCount {
			p.Length = 0
			msg += fmt.Sprintf("\n你已经被夺走了牛牛（%d次），发送「赎牛牛」可以赎回", p.PKCount)
		}
	default:
		msg = fmt.Sprintf("你和 %s 的牛牛一样长，战成平局", target)
	}
	err := save(ctx, c, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, msg)
}

// redeemConfirm 是一次待确认的赎牛牛会话，2 分钟内有效。
type redeemConfirm struct {
	ExpiresAt int64   `json:"expires_at"`
	Length    float64 `json:"length"`
}

func redeemConfirmKey(c sdk.Call) string {
	return "niu:redeem:" + c.Event.GroupID + ":" + c.Event.UserID
}

func loadRedeemConfirm(ctx context.Context, st sdk.State, c sdk.Call) *redeemConfirm {
	confirm := &redeemConfirm{}
	if err := sdk.LoadJSON(ctx, st, redeemConfirmKey(c), confirm); err != nil || confirm.ExpiresAt == 0 {
		return nil
	}
	return confirm
}

func saveRedeemConfirm(ctx context.Context, st sdk.State, c sdk.Call, confirm *redeemConfirm) error {
	return sdk.SaveJSON(ctx, st, redeemConfirmKey(c), confirm)
}

func clearRedeemConfirm(ctx context.Context, st sdk.State, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, st, redeemConfirmKey(c), (*redeemConfirm)(nil))
}

// handleRedeemConfirm 处理赎牛牛确认会话；只认发起人自己的「是/否」，
// 别人的回复查不到会话，直接忽略（与上游 CheckUser 一致）。
func handleRedeemConfirm(ctx context.Context, c sdk.Call, text string) (bool, error) {
	if text != "是" && text != "否" {
		return false, nil
	}
	confirm := loadRedeemConfirm(ctx, c.State, c)
	if confirm == nil {
		return true, nil
	}
	if nowFunc().Unix() > confirm.ExpiresAt {
		clearRedeemConfirm(ctx, c.State, c)
		return true, c.Reply(ctx, "操作超时，已自动取消")
	}
	if text == "否" {
		clearRedeemConfirm(ctx, c.State, c)
		return true, c.Reply(ctx, "取消成功!")
	}
	clearRedeemConfirm(ctx, c.State, c)
	return true, completeRedeem(ctx, c)
}

func doRedeem(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil {
		mu.Unlock()
		return c.Reply(ctx, "你还没有被夺走牛牛")
	}
	if p.Length > 0 {
		mu.Unlock()
		return c.Reply(ctx, "你的牛牛还在，不需要赎回")
	}
	if p.PKLength <= 0 {
		mu.Unlock()
		return c.Reply(ctx, "你还没有被夺走牛牛")
	}
	if nowFunc().Sub(time.Unix(p.PKTime, 0)) > redeemWindow {
		p.PKLength = 0
		p.PKCount = 0
		err := save(ctx, c, g)
		mu.Unlock()
		if err != nil {
			return err
		}
		return c.Reply(ctx, "时间已经过期了，牛牛已被收回!")
	}
	if p.PKCount < redeemCount {
		count := p.PKCount
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("你只被夺走了%d次，凑够%d次才能赎回", count, redeemCount))
	}
	confirm := &redeemConfirm{
		ExpiresAt: nowFunc().Add(redeemConfirmWindow).Unix(),
		Length:    p.PKLength,
	}
	mu.Unlock()
	if err := saveRedeemConfirm(ctx, c.State, c, confirm); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("再次确认一下哦，这次赎牛牛，牛牛长度将会变成%.2fcm\n还需要嘛【是|否】", confirm.Length))
}

// completeRedeem 在用户确认后真正赎回；确认期间状态可能变化，需要重新校验。
func completeRedeem(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil || p.PKLength <= 0 {
		mu.Unlock()
		return c.Reply(ctx, "你还没有被夺走牛牛")
	}
	if p.Length > 0 {
		mu.Unlock()
		return c.Reply(ctx, "你的牛牛还在，不需要赎回")
	}
	if nowFunc().Sub(time.Unix(p.PKTime, 0)) > redeemWindow {
		p.PKLength = 0
		p.PKCount = 0
		err := save(ctx, c, g)
		mu.Unlock()
		if err != nil {
			return err
		}
		return c.Reply(ctx, "时间已经过期了，牛牛已被收回!")
	}
	if p.PKCount < redeemCount {
		count := p.PKCount
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("你只被夺走了%d次，凑够%d次才能赎回", count, redeemCount))
	}
	p.Length = p.PKLength
	p.PKLength = 0
	p.PKCount = 0
	err := save(ctx, c, g)
	length := p.Length
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("恭喜你!成功赎回牛牛,当前长度为:%.2fcm", length))
}

func doSell(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil || p.Length <= 0 {
		mu.Unlock()
		return c.Reply(ctx, "你还没有可出售的牛牛")
	}
	g.NextAuction++
	price := int(math.Round(p.Length*10)) + 100
	g.Auction = append(g.Auction, auctionItem{
		ID:     g.NextAuction,
		From:   c.Event.UserID,
		Price:  price,
		Length: p.Length,
		Depth:  p.Depth,
	})
	p.Coins += price
	p.Length = 0
	p.Sold = true
	p.PKLength = 0
	p.PKCount = 0
	err := save(ctx, c, g)
	id := g.NextAuction
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("出售成功！牛牛已进入拍卖行，商品序号%d，你获得%d金币", id, price))
}

func doAuction(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	items := append([]auctionItem(nil), g.Auction...)
	mu.Unlock()
	if len(items) == 0 {
		return c.Reply(ctx, "牛牛拍卖行暂时没有商品")
	}
	var b strings.Builder
	b.WriteString("牛牛拍卖行有以下牛牛\n")
	for _, v := range items {
		fmt.Fprintf(&b, "商品序号: %d\n牛牛原所属: %s\n牛牛价格: %d金币\n牛牛大小: %.2fcm\n\n",
			v.ID, v.From, v.Price, v.Length)
	}
	b.WriteString("请输入对应序号进行购买（120 秒内有效）")
	if err := saveSession(ctx, c, "auction"); err != nil {
		return err
	}
	return c.ReplyKeyboardOrText(ctx, b.String(), auctionKeyboard(items))
}

func doBuyAuction(ctx context.Context, c sdk.Call, rawID string) error {
	clearSession(ctx, c)
	id, _ := strconv.Atoi(rawID)
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil {
		mu.Unlock()
		return c.Reply(ctx, "请先发送「注册牛牛」")
	}
	if p.Length > 0 {
		mu.Unlock()
		return c.Reply(ctx, "你已经有一只牛牛了，请先出售")
	}
	var found *auctionItem
	for i := range g.Auction {
		if g.Auction[i].ID == id {
			found = &g.Auction[i]
			break
		}
	}
	if found == nil {
		mu.Unlock()
		return c.Reply(ctx, "没有这个序号的商品")
	}
	if p.Coins < found.Price {
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("金币不足，购买需要%d金币", found.Price))
	}
	if seller := g.Users[found.From]; seller != nil {
		seller.Coins += found.Price
	}
	p.Coins -= found.Price
	p.Length = found.Length
	p.Depth = found.Depth
	p.Sold = false
	price, from := found.Price, found.From
	for i := range g.Auction {
		if g.Auction[i].ID == id {
			g.Auction = append(g.Auction[:i], g.Auction[i+1:]...)
			break
		}
	}
	err := save(ctx, c, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("购买成功！你花费%d金币买下了 %s 的牛牛", price, from), mainKeyboard())
}

func doShop(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	p := load(ctx, c).Users[c.Event.UserID]
	mu.Unlock()
	if p == nil {
		return c.Reply(ctx, "请先发送「注册牛牛」")
	}
	var b strings.Builder
	b.WriteString("牛牛商店当前售卖的物品如下\n")
	for _, v := range shopItems {
		fmt.Fprintf(&b, "%d. %s - %d金币 - %s\n", v.ID, v.Name, v.Cost, v.Desc)
	}
	b.WriteString("输入对应序号进行购买商品\n商品id-商品数量\n如想购买2个伟哥，发送「1-2」（120 秒内有效）")
	if err := saveSession(ctx, c, "shop"); err != nil {
		return err
	}
	return c.ReplyKeyboardOrText(ctx, b.String(), shopKeyboard())
}

func doBuyProp(ctx context.Context, c sdk.Call, rawID, rawCount string) error {
	clearSession(ctx, c)
	id, _ := strconv.Atoi(rawID)
	count, _ := strconv.Atoi(rawCount)
	if count <= 0 {
		return c.Reply(ctx, "购买数量必须大于0")
	}
	var target item
	ok := false
	for _, v := range shopItems {
		if v.ID == id {
			target, ok = v, true
			break
		}
	}
	if !ok {
		return c.Reply(ctx, "没有这个商品")
	}
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil {
		mu.Unlock()
		return c.Reply(ctx, "请先发送「注册牛牛」")
	}
	total := target.Cost * count
	if p.Coins < total {
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("金币不足，购买需要%d金币", total))
	}
	p.Coins -= total
	if p.Props == nil {
		p.Props = map[string]int{}
	}
	p.Props[target.Name] += count
	err := save(ctx, c, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("购买成功！获得%d个%s", count, target.Name), mainKeyboard())
}

func doBag(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	p := load(ctx, c).Users[c.Event.UserID]
	mu.Unlock()
	if p == nil {
		return c.Reply(ctx, "请先发送「注册牛牛」")
	}
	if len(p.Props) == 0 {
		return c.Reply(ctx, "你的牛牛背包空空如也")
	}
	names := make([]string, 0, len(p.Props))
	for name, n := range p.Props {
		if n > 0 {
			names = append(names, fmt.Sprintf("%s x%d", name, n))
		}
	}
	sort.Strings(names)
	return c.ReplyKeyboardOrText(ctx, "牛牛背包:\n"+strings.Join(names, "\n"), mainKeyboard())
}

// cancelRecord 记录 24 小时内的注销次数，用于递增收费。
type cancelRecord struct {
	TimeLimit int64 `json:"time_limit"`
	Count     int   `json:"count"`
}

func cancelRecordKey(c sdk.Call) string {
	return "niu:cancel:" + c.Event.GroupID + ":" + c.Event.UserID
}

func loadCancelRecord(ctx context.Context, st sdk.State, c sdk.Call) *cancelRecord {
	record := &cancelRecord{}
	if err := sdk.LoadJSON(ctx, st, cancelRecordKey(c), record); err != nil || record.Count < 1 {
		return nil
	}
	return record
}

func saveCancelRecord(ctx context.Context, st sdk.State, c sdk.Call, record *cancelRecord) error {
	return sdk.SaveJSON(ctx, st, cancelRecordKey(c), record)
}

// applyCancelCharge 结算一次注销：24 小时窗口内第 N 次注销扣 N*50，
// 窗口外（或首次）免费并开启新窗口。余额不足时不扣费、不递增。
func applyCancelCharge(p *player, record *cancelRecord, now time.Time) (*cancelRecord, int, bool) {
	if record == nil || now.Sub(time.Unix(record.TimeLimit, 0)) > cancelWindow {
		return &cancelRecord{TimeLimit: now.Unix(), Count: 1}, 0, true
	}
	fee := record.Count * cancelFee
	if p.Coins < fee {
		return record, fee, false
	}
	p.Coins -= fee
	record.Count++
	return record, fee, true
}

func doCancel(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	g := load(ctx, c)
	p := g.Users[c.Event.UserID]
	if p == nil || (p.Length <= 0 && p.PKLength <= 0) {
		mu.Unlock()
		return c.Reply(ctx, "你还没有注册牛牛")
	}
	record, fee, ok := applyCancelCharge(p, loadCancelRecord(ctx, c.State, c), nowFunc())
	if !ok {
		mu.Unlock()
		return c.Reply(ctx, fmt.Sprintf("你的钱不够你注销牛牛了，这次注销需要%d金币", fee))
	}
	if err := saveCancelRecord(ctx, c.State, c, record); err != nil {
		mu.Unlock()
		return err
	}
	// 钱包与背包挂在玩家记录上，注销只清掉牛牛本身，金币和道具按上游口径保留。
	p.Length = 0
	p.Depth = 0
	p.PKLength = 0
	p.PKCount = 0
	p.PKTime = 0
	p.Sold = false
	err := save(ctx, c, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, "已注销牛牛，再见！")
}

func doRanking(ctx context.Context, c sdk.Call, byLength bool) error {
	mu.Lock()
	g := load(ctx, c)
	mu.Unlock()
	type entry struct {
		uid string
		val float64
	}
	list := make([]entry, 0, len(g.Users))
	for uid, p := range g.Users {
		if p.Length <= 0 {
			continue
		}
		if byLength {
			list = append(list, entry{uid, p.Length})
		} else {
			list = append(list, entry{uid, p.Depth})
		}
	}
	if len(list) == 0 {
		return c.Reply(ctx, "还没有人注册牛牛")
	}
	sort.Slice(list, func(i, j int) bool { return list[i].val > list[j].val })
	if len(list) > 5 {
		list = list[:5]
	}
	title, unitName := "牛牛长度排行", "长度"
	if !byLength {
		title, unitName = "牛牛深度排行", "深度"
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	for i, v := range list {
		fmt.Fprintf(&b, "%d. %s %s%.2fcm\n", i+1, v.uid, unitName, v.val)
	}
	return c.Reply(ctx, strings.TrimRight(b.String(), "\n"))
}

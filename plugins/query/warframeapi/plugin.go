// Package warframeapi 星际战甲世界状态：查询平原时间、警报、仲裁与每日特惠。
package warframeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// wmTTL 多结果/报价的限时选择时长，与上游一致。
	wmTTL = 30 * time.Second
	// wmMaxResults 多结果时最多展示的物品数量，与上游一致。
	wmMaxResults = 25
	// wmMaxOrders 每次最多展示的卖单数量。
	wmMaxOrders = 5
	// worldSyncTTL 本地时间模拟的有效期，与上游 5 分钟同步窗口一致。
	worldSyncTTL = 5 * time.Minute

	// 平原昼夜时长，与上游 world.go 的 DayLen / NightLen 一致。
	earthDayLen    = 100 * time.Minute
	earthNightLen  = 50 * time.Minute
	venusWarmLen   = 400 * time.Second
	venusColdLen   = 20 * time.Minute
	cambionFassLen = 100 * time.Minute
	cambionVomeLen = 50 * time.Minute
)

// worldStateURL 与上游 api.warframestat.us 一致，测试可替换。
var worldStateURL = "https://api.warframestat.us/pc"

var client = &http.Client{Timeout: 10 * time.Second}

var (
	// warframe.market 的物品表与订单接口，测试中替换为 httptest 服务。
	marketItemsURL  = "https://api.warframe.market/v1/items"
	marketOrdersURL = "https://api.warframe.market/v1/items/%s/orders?include=item"
	marketClient    = &http.Client{Timeout: 10 * time.Second}
	nowFunc         = time.Now
)

// worldCache 保存最近一次 wf时间同步 的结果，供平原时间在 5 分钟内复用。
var (
	worldCacheMu sync.Mutex
	worldCache   worldState
	worldCached  time.Time
)

type cycle struct {
	State    string    `json:"state"`
	IsDay    bool      `json:"isDay"`
	IsWarm   bool      `json:"isWarm"`
	Active   string    `json:"active"`
	TimeLeft string    `json:"timeLeft"`
	Expiry   time.Time `json:"expiry"`
}

type worldState struct {
	CetusCycle   cycle `json:"cetusCycle"`
	VallisCycle  cycle `json:"vallisCycle"`
	CambionCycle cycle `json:"cambionCycle"`
	Alerts       []struct {
		Mission struct {
			Node          string `json:"node"`
			Type          string `json:"type"`
			MinEnemyLevel int    `json:"minEnemyLevel"`
			MaxEnemyLevel int    `json:"maxEnemyLevel"`
			Reward        struct {
				AsString string `json:"asString"`
			} `json:"reward"`
		} `json:"mission"`
		Active bool   `json:"active"`
		Eta    string `json:"eta"`
	} `json:"alerts"`
	Arbitration struct {
		Node   string    `json:"node"`
		Type   string    `json:"type"`
		Enemy  string    `json:"enemy"`
		Expiry time.Time `json:"expiry"`
	} `json:"arbitration"`
	DailyDeals []struct {
		Item          string `json:"item"`
		OriginalPrice int    `json:"originalPrice"`
		SalePrice     int    `json:"salePrice"`
		Total         int    `json:"total"`
		Sold          int    `json:"sold"`
		Eta           string `json:"eta"`
	} `json:"dailyDeals"`
}

// Definition subscribes to the 星际战甲 queries.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "warframeapi",
		Name:        "星际战甲世界状态",
		Version:     "1.0.0",
		Description: "发送「地球/金星/火卫二平原时间」「wf警报」「wf仲裁」「wf每日特惠」「.wm 物品」查询星际战甲世界状态与市场报价",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "warframeapi", Trigger: ".wm"},
	}, sdk.WithPermissions("state.read", "state.write"))
}

// Handle dispatches the supported world-state queries.
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	// 市场查价的多结果/报价选择优先于普通指令。
	if key := wmPendingKey(c); key != "" {
		handled, err := continueWM(ctx, c, key, text)
		if handled || err != nil {
			return err
		}
	}
	switch {
	case sdk.MatchFull(text, "wf时间同步"):
		return syncTime(ctx, c)
	case sdk.MatchFull(text, "wf警报"):
		return alerts(ctx, c)
	case sdk.MatchFull(text, "wf仲裁"):
		return arbitration(ctx, c)
	case sdk.MatchFull(text, "wf每日特惠"):
		return deals(ctx, c)
	}
	if rest, ok := sdk.MatchPrefix(text, ".wm"); ok {
		return wmQuery(ctx, c, rest)
	}
	if prefix, ok := sdk.MatchSuffix(text, "平原时间"); ok {
		return plains(ctx, c, prefix)
	}
	return nil
}

// menuKeyboard 世界状态查询的快捷按钮，点击后发送对应文本指令。
func menuKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(
		sdk.NewRow(
			sdk.CommandButton("wf-earth", "地球时间", "地球平原时间"),
			sdk.CommandButton("wf-venus", "金星时间", "金星平原时间"),
			sdk.CommandButton("wf-cambion", "火卫二时间", "火卫二平原时间"),
		),
		sdk.NewRow(
			sdk.CommandButton("wf-alerts", "wf警报", "wf警报"),
			sdk.CommandButton("wf-arbitration", "wf仲裁", "wf仲裁"),
			sdk.CommandButton("wf-deals", "wf每日特惠", "wf每日特惠"),
		),
	)
}

func plains(ctx context.Context, c sdk.Call, prefix string) error {
	w, err := loadWorld(ctx)
	if err != nil {
		return c.Reply(ctx, "获取服务器时间失败: "+err.Error())
	}
	var name, desc string
	switch prefix {
	case "地球", "夜灵":
		name = "地球平原"
		if w.CetusCycle.IsDay {
			desc = "白天"
		} else {
			desc = "夜晚"
		}
		return c.ReplyKeyboardOrText(ctx, formatPlain(name, desc, w.CetusCycle), menuKeyboard())
	case "金星", "奥布山谷":
		name = "金星平原"
		if w.VallisCycle.IsWarm {
			desc = "温暖"
		} else {
			desc = "寒冷"
		}
		return c.ReplyKeyboardOrText(ctx, formatPlain(name, desc, w.VallisCycle), menuKeyboard())
	case "魔胎之境", "火卫二", "火卫":
		name = "火卫二平原"
		desc = w.CambionCycle.Active
		return c.ReplyKeyboardOrText(ctx, formatPlain(name, desc, w.CambionCycle), menuKeyboard())
	default:
		return c.Reply(ctx, "ERROR: 平原不存在")
	}
}

func formatPlain(name, desc string, t cycle) string {
	left := t.TimeLeft
	if left == "" && !t.Expiry.IsZero() {
		left = t.Expiry.Sub(nowFunc()).Round(time.Second).String()
	}
	return fmt.Sprintf("%s: %s, 下次更新: %s", name, desc, left)
}

func alerts(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(w.Alerts) == 0 {
		return c.Reply(ctx, "当前没有警报")
	}
	items := make([]string, 0, len(w.Alerts))
	for _, v := range w.Alerts {
		items = append(items, fmt.Sprintf("激活: %t\n节点: %s\n类型: %s\n敌人等级: %d~%d\n奖励: %s\n剩余时间: %s",
			v.Active, v.Mission.Node, v.Mission.Type, v.Mission.MinEnemyLevel, v.Mission.MaxEnemyLevel,
			v.Mission.Reward.AsString, v.Eta))
	}
	return c.ReplyKeyboardOrText(ctx, strings.Join(items, "\n\n"), menuKeyboard())
}

func arbitration(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	a := w.Arbitration
	if a.Node == "" {
		return c.Reply(ctx, "当前没有仲裁")
	}
	minutes := 0
	if !a.Expiry.IsZero() {
		minutes = int(time.Until(a.Expiry).Minutes())
	}
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("节点: %s\n类型: %s\n阵营: %s\n剩余时间: %dm", a.Node, a.Type, a.Enemy, minutes), menuKeyboard())
}

func deals(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(w.DailyDeals) == 0 {
		return c.Reply(ctx, "当前没有每日特惠")
	}
	items := make([]string, 0, len(w.DailyDeals))
	for _, d := range w.DailyDeals {
		items = append(items, fmt.Sprintf("物品: %s\n价格: %d→%d\n数量: (%d/%d)\n时间: %s",
			d.Item, d.OriginalPrice, d.SalePrice, d.Total, d.Sold, d.Eta))
	}
	return c.ReplyKeyboardOrText(ctx, strings.Join(items, "\n\n"), menuKeyboard())
}

func fetch(ctx context.Context) (worldState, error) {
	var w worldState
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, worldStateURL, nil)
	if err != nil {
		return w, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return w, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return w, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return w, err
	}
	if err = json.Unmarshal(body, &w); err != nil {
		return w, err
	}
	return w, nil
}

// loadWorld 优先复用 5 分钟内 wf时间同步 的结果，并模拟时间的自然流逝。
func loadWorld(ctx context.Context) (worldState, error) {
	worldCacheMu.Lock()
	if !worldCached.IsZero() && nowFunc().Sub(worldCached) < worldSyncTTL {
		w := simulateWorld(worldCache, nowFunc())
		worldCacheMu.Unlock()
		return w, nil
	}
	worldCacheMu.Unlock()
	w, err := fetch(ctx)
	if err != nil {
		return w, err
	}
	// 上游首次查询平原时间也会开启 5 分钟同步窗口。
	storeWorld(w)
	return w, nil
}

// simulateWorld 按昼夜时长推进缓存中的平原状态，避免短时间重复请求时时间停滞。
func simulateWorld(w worldState, now time.Time) worldState {
	// 接口未返回 expiry 时无法推算，保留原始 timeLeft 文本。
	if !w.CetusCycle.Expiry.IsZero() {
		w.CetusCycle.IsDay, w.CetusCycle.Expiry = advanceCycle(w.CetusCycle.IsDay, w.CetusCycle.Expiry,
			now, earthDayLen, earthNightLen)
		w.CetusCycle.TimeLeft = ""
	}
	if !w.VallisCycle.Expiry.IsZero() {
		w.VallisCycle.IsWarm, w.VallisCycle.Expiry = advanceCycle(w.VallisCycle.IsWarm, w.VallisCycle.Expiry,
			now, venusWarmLen, venusColdLen)
		w.VallisCycle.TimeLeft = ""
	}
	if !w.CambionCycle.Expiry.IsZero() {
		day := w.CambionCycle.Active == "fass"
		day, w.CambionCycle.Expiry = advanceCycle(day, w.CambionCycle.Expiry,
			now, cambionFassLen, cambionVomeLen)
		if day {
			w.CambionCycle.Active = "fass"
		} else {
			w.CambionCycle.Active = "vome"
		}
		w.CambionCycle.TimeLeft = ""
	}
	return w
}

// advanceCycle 返回 now 时刻的状态及当前状态的结束时间；过期时按昼夜时长向后推进。
func advanceCycle(day bool, expiry time.Time, now time.Time, dayLen, nightLen time.Duration) (bool, time.Time) {
	for !expiry.IsZero() && !now.Before(expiry) {
		if day {
			expiry = expiry.Add(nightLen)
		} else {
			expiry = expiry.Add(dayLen)
		}
		day = !day
	}
	return day, expiry
}

// storeWorld 记录同步结果并裁剪到 5 分钟有效期。
func storeWorld(w worldState) {
	worldCacheMu.Lock()
	worldCache = w
	worldCached = nowFunc()
	worldCacheMu.Unlock()
}

// resetWorldCache 清空本地时间模拟，测试中用于隔离。
func resetWorldCache() {
	worldCacheMu.Lock()
	worldCache = worldState{}
	worldCached = time.Time{}
	worldCacheMu.Unlock()
}

// syncTime 拉取服务器时间并同步到本地模拟。
func syncTime(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	storeWorld(w)
	lines := []string{
		"已拉取服务器时间并同步到本地模拟",
		"本地时间: " + nowFunc().Local().Format("2006-01-02 15:04:05"),
	}
	cetusDesc := "夜晚"
	if w.CetusCycle.IsDay {
		cetusDesc = "白天"
	}
	vallisDesc := "寒冷"
	if w.VallisCycle.IsWarm {
		vallisDesc = "温暖"
	}
	lines = append(lines,
		formatPlain("地球平原", cetusDesc, w.CetusCycle),
		formatPlain("金星平原", vallisDesc, w.VallisCycle),
		formatPlain("火卫二平原", w.CambionCycle.Active, w.CambionCycle),
	)
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// wmItem 是 warframe.market 物品表中的一项。
type wmItem struct {
	URLName  string `json:"url_name"`
	Thumb    string `json:"thumb"`
	ItemName string `json:"item_name"`
	ID       string `json:"id"`
}

// wmItemsResp 物品表接口响应。
type wmItemsResp struct {
	Payload struct {
		Items []wmItem `json:"items"`
	} `json:"payload"`
}

// wmOrder 是市场卖单的最小字段集合。
type wmOrder struct {
	OrderType string `json:"order_type"`
	Platinum  int    `json:"platinum"`
	ModRank   int    `json:"mod_rank"`
	User      struct {
		IngameName string `json:"ingame_name"`
		Status     string `json:"status"`
	} `json:"user"`
}

// wmItemInfo 是订单接口 include 中的物品信息。
type wmItemInfo struct {
	URLName    string `json:"url_name"`
	ModMaxRank int    `json:"mod_max_rank"`
	En         struct {
		ItemName string `json:"item_name"`
	} `json:"en"`
}

// wmOrdersResp 订单接口响应。
type wmOrdersResp struct {
	Payload struct {
		Orders []wmOrder `json:"orders"`
	} `json:"payload"`
	Include struct {
		Item struct {
			ItemsInSet []wmItemInfo `json:"items_in_set"`
		} `json:"item"`
	} `json:"include"`
}

// wmPending 保存限时选择会话。
type wmPending struct {
	Stage  string    `json:"stage"`
	Items  []wmItem  `json:"items,omitempty"`
	Item   wmItem    `json:"item,omitempty"`
	Orders []wmOrder `json:"orders,omitempty"`
	ModMax int       `json:"mod_max"`
	ItemEN string    `json:"item_en"`
	Expire int64     `json:"expire"`
}

// wmPendingKey 按群与用户隔离选择状态。
func wmPendingKey(c sdk.Call) string {
	if c.Event.UserID == "" {
		return ""
	}
	return "wm:" + c.Event.GroupID + ":" + c.Event.UserID
}

// continueWM 处理市场查价的编号选择；handled 为 false 时继续走普通指令。
func continueWM(ctx context.Context, c sdk.Call, key, text string) (bool, error) {
	var p wmPending
	if err := sdk.LoadJSON(ctx, c.State, key, &p); err != nil {
		return true, err
	}
	if p.Stage == "" {
		return false, nil
	}
	if nowFunc().Unix() >= p.Expire {
		if err := clearWM(ctx, c, key); err != nil {
			return true, err
		}
		if isWMControl(text) {
			return true, c.Reply(ctx, "会话已超时!")
		}
		return false, nil
	}
	if text == "c" {
		if err := clearWM(ctx, c, key); err != nil {
			return true, err
		}
		return true, c.Reply(ctx, "会话已结束!")
	}
	idx, ok := parseWMIndex(text)
	if !ok {
		return false, nil
	}
	switch p.Stage {
	case "items":
		if idx < 0 || idx >= len(p.Items) {
			return true, c.Reply(ctx, "ERROR: 编号超出范围")
		}
		item := p.Items[idx]
		if err := clearWM(ctx, c, key); err != nil {
			return true, err
		}
		return true, showWMOrders(ctx, c, key, item)
	case "orders":
		if idx < 0 || idx >= len(p.Orders) {
			return true, c.Reply(ctx, "ERROR: 编号超出范围")
		}
		order := p.Orders[idx]
		if err := clearWM(ctx, c, key); err != nil {
			return true, err
		}
		if p.ModMax > 0 {
			return true, c.Reply(ctx, fmt.Sprintf("/w %s Hi! I want to buy: %s (Rank:%d) for %d platinum. (warframe.market)",
				order.User.IngameName, p.ItemEN, order.ModRank, order.Platinum))
		}
		return true, c.Reply(ctx, fmt.Sprintf("/w %s Hi! I want to buy: %s for %d platinum. (warframe.market)",
			order.User.IngameName, p.ItemEN, order.Platinum))
	default:
		return false, nil
	}
}

// clearWM 清空选择会话。
func clearWM(ctx context.Context, c sdk.Call, key string) error {
	return sdk.SaveJSON(ctx, c.State, key, wmPending{})
}

// isWMControl 判断文本是否像选择会话的输入。
func isWMControl(text string) bool {
	if text == "c" || text == "r" {
		return true
	}
	_, ok := parseWMIndex(text)
	return ok
}

// parseWMIndex 解析纯数字序号。
func parseWMIndex(text string) (int, bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// wmQuery 按名称模糊搜索物品，多结果时进入限时选择。
func wmQuery(ctx context.Context, c sdk.Call, arg string) error {
	query := strings.TrimSpace(arg)
	if query == "" {
		return c.Reply(ctx, "用法：.wm 物品名称")
	}
	items, err := fetchWMItems(ctx)
	if err != nil {
		return c.Reply(ctx, "ERROR: 获取Warframe市场物品列表失败: "+err.Error())
	}
	matches := searchWMItems(items, query)
	switch len(matches) {
	case 0:
		return c.Reply(ctx, "无法查询到该物品")
	case 1:
		return showWMOrders(ctx, c, wmPendingKey(c), matches[0])
	}
	if len(matches) > wmMaxResults {
		matches = matches[:wmMaxResults]
	}
	lines := []string{"包含多个结果, 请输入编号查看(30s内),输入c直接结束会话"}
	for i, item := range matches {
		lines = append(lines, fmt.Sprintf("[%d] %s", i, item.ItemName))
	}
	if key := wmPendingKey(c); key != "" {
		if err := sdk.SaveJSON(ctx, c.State, key, wmPending{
			Stage:  "items",
			Items:  matches,
			Expire: nowFunc().Add(wmTTL).Unix(),
		}); err != nil {
			return err
		}
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// searchWMItems 做大小写不敏感的子串匹配，失败时退化为按序字符匹配。
func searchWMItems(items []wmItem, query string) []wmItem {
	q := normalizeWM(query)
	var exact []wmItem
	for _, item := range items {
		if strings.Contains(normalizeWM(item.ItemName), q) {
			exact = append(exact, item)
		}
	}
	if len(exact) > 0 {
		return sortWMItems(exact)
	}
	var fuzzy []wmItem
	for _, item := range items {
		if subsequenceWM(normalizeWM(item.ItemName), q) {
			fuzzy = append(fuzzy, item)
		}
	}
	return sortWMItems(fuzzy)
}

// sortWMItems 让更短的名称优先，保证结果稳定。
func sortWMItems(items []wmItem) []wmItem {
	sort.SliceStable(items, func(i, j int) bool {
		li, lj := len([]rune(items[i].ItemName)), len([]rune(items[j].ItemName))
		if li != lj {
			return li < lj
		}
		return items[i].ItemName < items[j].ItemName
	})
	return items
}

// normalizeWM 统一大小写与空白，便于匹配。
func normalizeWM(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "")
}

// subsequenceWM 判断 query 的字符是否按顺序出现在 name 中。
func subsequenceWM(name, query string) bool {
	if query == "" {
		return false
	}
	runes := []rune(query)
	i := 0
	for _, r := range name {
		if r == runes[i] {
			i++
			if i == len(runes) {
				return true
			}
		}
	}
	return false
}

// fetchWMItems 拉取市场物品表。
func fetchWMItems(ctx context.Context) ([]wmItem, error) {
	var resp wmItemsResp
	header := map[string]string{"Accept": "application/json", "Language": "zh-hans"}
	if err := getWM(ctx, marketItemsURL, header, &resp); err != nil {
		return nil, err
	}
	return resp.Payload.Items, nil
}

// showWMOrders 拉取订单并展示满级卖单，同时保存选择会话。
func showWMOrders(ctx context.Context, c sdk.Call, key string, item wmItem) error {
	orders, modMax, itemEN, err := fetchWMOrders(ctx, item.URLName)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	filtered := filterWMOrders(orders, modMax)
	if len(filtered) == 0 {
		return c.Reply(ctx, "无可购买对象")
	}
	if len(filtered) > wmMaxOrders {
		filtered = filtered[:wmMaxOrders]
	}
	lines := []string{"【Warframe Market】" + item.ItemName}
	for i, order := range filtered {
		line := fmt.Sprintf("[%d] %dP - %s", i, order.Platinum, order.User.IngameName)
		if modMax > 0 {
			line = fmt.Sprintf("[%d] (Rank:%d/%d) %dP - %s", i, order.ModRank, modMax, order.Platinum, order.User.IngameName)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "请输入编号选择(30s内)", "输入c直接结束会话")
	if key != "" {
		if err := sdk.SaveJSON(ctx, c.State, key, wmPending{
			Stage:  "orders",
			Item:   item,
			Orders: filtered,
			ModMax: modMax,
			ItemEN: itemEN,
			Expire: nowFunc().Add(wmTTL).Unix(),
		}); err != nil {
			return err
		}
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// filterWMOrders 只保留在线卖家的卖单；有满级时只保留满级报价。
func filterWMOrders(orders []wmOrder, modMax int) []wmOrder {
	out := make([]wmOrder, 0, len(orders))
	for _, order := range orders {
		if order.OrderType != "sell" || order.User.Status == "offline" {
			continue
		}
		if modMax > 0 && order.ModRank != modMax {
			continue
		}
		out = append(out, order)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Platinum < out[j].Platinum })
	return out
}

// fetchWMOrders 拉取指定物品的订单与物品信息。
func fetchWMOrders(ctx context.Context, urlName string) ([]wmOrder, int, string, error) {
	var resp wmOrdersResp
	header := map[string]string{"Accept": "application/json", "Platform": "pc"}
	rawURL := fmt.Sprintf(marketOrdersURL, url.PathEscape(urlName))
	if err := getWM(ctx, rawURL, header, &resp); err != nil {
		return nil, 0, "", err
	}
	info := wmItemInfo{}
	for _, v := range resp.Include.Item.ItemsInSet {
		if v.URLName == urlName {
			info = v
			break
		}
	}
	if info.URLName == "" && len(resp.Include.Item.ItemsInSet) > 0 {
		info = resp.Include.Item.ItemsInSet[0]
	}
	if len(resp.Payload.Orders) == 0 {
		return nil, info.ModMaxRank, info.En.ItemName, nil
	}
	return resp.Payload.Orders, info.ModMaxRank, info.En.ItemName, nil
}

// getWM 发起市场接口请求并解析 JSON。
func getWM(ctx context.Context, rawURL string, header map[string]string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := marketClient.Do(req)
	if err != nil {
		return fmt.Errorf("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("响应解析失败")
	}
	return nil
}

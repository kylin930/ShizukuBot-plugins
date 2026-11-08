package gloryofkings

// P2：皮肤 —— #皮肤墙（#全部皮肤）、#缺皮肤、#查皮肤。
// 数据口径与上游 apps/skinWall.js、apps/skinMissing.js、apps/heroSkin.js、
// utils/skinCatalog.js 对齐：
//   - 已拥有判据：heroSkinList 条目含 iBuy 字段且 szClass 非空；
//   - 全量清单：同一响应的 heroSkinConfList（isHidden==1 是经典原皮，不计）；
//   - 排序：tierRank（高价值品质档）→ SZ_ORDER（营地评级）→ skin_worth → iPrice；
//   - 品质计数只认 classTypeName 品质名（荣耀典藏/无双/传说三档）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

/* ------------------------------ 品质口径 ------------------------------ */

// szOrder 营地评级价值序（下标越小越高）。SSR 高于 SR，SP 介于 SR 与 S++ 之间。
var szOrder = []string{"SSR", "SR", "SP", "S++", "S+", "S", "A", "B", "C", "D"}

// tierPriority 高价值品质优先级（下标越小价值越高）。
var tierPriority = []string{"荣耀典藏", "珍品无双", "无双至尊", "珍品传说", "传说限定"}

// qualityLabels 品质名展示优先级：classTypeName 常混主题名，按价值品质优先挑一个展示。
var qualityLabels = []string{
	"荣耀典藏", "珍品无双", "无双至尊", "珍品传说", "传说限定",
	"无双", "珍品限定", "传说品质", "史诗品质", "勇者品质", "限定",
}

type qualityStat struct {
	Key     string
	Label   string
	Aliases []string
}

var qualityStats = []qualityStat{
	{Key: "gloryNum", Label: "荣耀典藏", Aliases: []string{"荣耀典藏"}},
	{Key: "wushuangNum", Label: "无双", Aliases: []string{"珍品无双", "无双至尊", "无双"}},
	{Key: "legendNum", Label: "传说", Aliases: []string{"珍品传说", "传说限定", "传说品质"}},
}

// normalizeSzClass 归一化营地评级：全角转半角、去空白、转大写。
// 营地真实返回里存在 " A"（前导空格）这种写法，不归一会掉到末档。
func normalizeSzClass(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			builder.WriteRune(r - 0xFEE0)
		case r == 0x3000:
			builder.WriteRune(' ')
		default:
			builder.WriteRune(r)
		}
	}
	return strings.ToUpper(strings.TrimSpace(builder.String()))
}

func szLevel(value string) int {
	cls := normalizeSzClass(value)
	for i, item := range szOrder {
		if item == cls {
			return i
		}
	}
	return len(szOrder)
}

// tierRank 命中的最高价值品质档下标；未命中返回末尾档。
func tierRank(classTypeName []string) int {
	best := len(tierPriority)
	for _, name := range classTypeName {
		for i, tier := range tierPriority {
			if name == tier && i < best {
				best = i
			}
		}
	}
	return best
}

// pickTierText 挑一个用于展示的品质名。
func pickTierText(classTypeName []string) string {
	names := make([]string, 0, len(classTypeName))
	for _, name := range classTypeName {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	if len(names) == 0 {
		return ""
	}
	for _, label := range qualityLabels {
		for _, name := range names {
			if name == label {
				return label
			}
		}
	}
	return names[0]
}

// countQuality 一张皮肤只计入最先命中的品质格。
func countQuality(classTypeName []string, counters map[string]int) {
	names := make([]string, 0, len(classTypeName))
	for _, name := range classTypeName {
		names = append(names, strings.TrimSpace(name))
	}
	if len(names) == 0 {
		return
	}
	for _, stat := range qualityStats {
		for _, alias := range stat.Aliases {
			for _, name := range names {
				if name == alias {
					counters[stat.Key]++
					return
				}
			}
		}
	}
}

// isClassicSkin isHidden==1 的是经典原皮（人人都有），不计入缺失。
func isClassicSkin(conf skinConf) bool {
	return conf.IsHidden.Int() == 1
}

// ownedSkinIDs 已拥有的皮肤ID集合。判据不是「出现在 heroSkinList 里」，
// 那份列表混了展示项；有 iBuy 字段且 szClass 非空才是真拥有。
func ownedSkinIDs(list []skinOwnedItem) map[string]bool {
	out := map[string]bool{}
	for _, skin := range list {
		if skin.IBuy.String() != "" && strings.TrimSpace(skin.SzClass) != "" {
			out[skin.SkinID.String()] = true
		}
	}
	return out
}

/* ------------------------------------------------------------------ *
 * #皮肤墙 / #全部皮肤
 * ------------------------------------------------------------------ */

const (
	skinWallDefaultTop = 50
	skinWallPageSize   = 50
	skinWallGridCols   = 6
)

type skinWallItem struct {
	SkinID   string
	SkinName string
	HeroName string
	ImgURL   string
	LabelURL string
	TierText string
	Big      bool
	Row      int
	Col      int
	// 排序键
	tier   int
	iClass int
	worth  int
	iPrice int
}

type qualityStatView struct {
	Label string
	Num   int
}

type skinWallView struct {
	YdID         string
	Nickname     string
	Avatar       string
	Owned        string
	TotalSkinNum string
	NotForSell   string
	TotalValue   string
	QualityStats []qualityStatView
	PageInfo     string
	SkinList     []skinWallItem
}

// parseSkinWallArgs 数量（≤4 位纯数字）与营地ID 可混写。
func parseSkinWallArgs(input string) (campID string, topCount int) {
	topCount = skinWallDefaultTop
	for _, tok := range strings.Fields(input) {
		if len(tok) <= 4 && digitsRe.MatchString(tok) {
			if n, err := strconv.Atoi(tok); err == nil && n >= 1 {
				topCount = n
			}
			continue
		}
		campID = tok
	}
	return campID, topCount
}

func skinWallCommand(ctx context.Context, c sdk.Call, rest string, all bool) error {
	campIDArg, topCount := parseSkinWallArgs(rest)
	if all {
		topCount = 0 // 0 表示不截断
		campIDArg = strings.TrimSpace(rest)
	}
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID := campIDArg
	if campID == "" {
		if len(state.IDs) == 0 {
			return c.Reply(ctx, "未查询到营地ID，请先使用 #绑定营地 绑定营地ID，或在指令后附带营地ID")
		}
		campID = state.currentID()
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	client := newCampClient()
	data, err := client.getSkinList(ctx, campID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	if len(data.HeroSkinConfList) == 0 {
		return c.Reply(ctx, "获取皮肤数据失败，可能该召唤师隐藏了资料或登录态失效")
	}

	nickname := state.Names[campID]
	if nickname == "" {
		if name := refreshRoleName(ctx, client, state, campID); name != "" {
			nickname = name
			_ = saveUserState(ctx, c.State, c.Event.UserID, state)
		}
	}
	if nickname == "" {
		nickname = "召唤师"
	}

	counters := map[string]int{}
	var items []skinWallItem
	for _, skin := range data.HeroSkinList {
		if skin.IBuy.String() == "" || strings.TrimSpace(skin.SzClass) == "" {
			continue
		}
		conf, ok := data.HeroSkinConfList[skin.SkinID.String()]
		if !ok {
			continue
		}
		countQuality(conf.ClassTypeName, counters)
		items = append(items, skinWallItem{
			SkinID:   conf.ISkinID.String(),
			SkinName: conf.SzTitle,
			HeroName: conf.SzHeroTitle,
			ImgURL:   firstNonEmpty(conf.SzLargeIcon, conf.BigCover, conf.SzSmallIcon),
			LabelURL: conf.ClassLabel,
			TierText: pickTierText(conf.ClassTypeName),
			tier:     tierRank(conf.ClassTypeName),
			iClass:   szLevel(conf.SzClass),
			worth:    conf.SkinWorth.Int(),
			iPrice:   conf.IPrice.Int(),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].tier != items[j].tier {
			return items[i].tier < items[j].tier
		}
		if items[i].iClass != items[j].iClass {
			return items[i].iClass < items[j].iClass
		}
		if items[i].worth != items[j].worth {
			return items[i].worth > items[j].worth
		}
		return items[i].iPrice > items[j].iPrice
	})

	totalAvailable := len(items)
	limited := items
	if topCount > 0 && topCount < len(items) {
		limited = items[:topCount]
	}
	if len(limited) == 0 {
		return c.Reply(ctx, "该账号暂无可展示的皮肤，或资料未公开")
	}

	info := data.SkinCountInfo
	view := skinWallView{
		YdID:         campID,
		Nickname:     nickname,
		Avatar:       state.campAvatar(campID),
		Owned:        info.Owned.String(),
		TotalSkinNum: info.TotalSkinNum.String(),
		NotForSell:   info.NotForSell.String(),
		TotalValue:   info.TotalValue.String(),
	}
	for _, stat := range qualityStats {
		view.QualityStats = append(view.QualityStats, qualityStatView{Label: stat.Label, Num: counters[stat.Key]})
	}

	// 分页渲染：每页 PAGE_SIZE 个，逐张发送。
	totalPages := (len(limited) + skinWallPageSize - 1) / skinWallPageSize
	if totalPages > 1 {
		scope := fmt.Sprintf("共 %d 个皮肤", len(limited))
		if topCount > 0 && topCount < totalAvailable {
			scope = fmt.Sprintf("按价值 TOP %d", len(limited))
		}
		if err := c.Reply(ctx, fmt.Sprintf("%s，将分 %d 张图发送，请稍候...", scope, totalPages)); err != nil {
			return err
		}
	}
	sent := false
	for page := 0; page < totalPages; page++ {
		start := page * skinWallPageSize
		end := start + skinWallPageSize
		if end > len(limited) {
			end = len(limited)
		}
		pageItems := append([]skinWallItem(nil), limited[start:end]...)
		assignBandLayout(pageItems, skinWallGridCols)
		pageView := view
		pageView.SkinList = pageItems
		if totalPages > 1 {
			pageView.PageInfo = fmt.Sprintf("第 %d/%d 页", page+1, totalPages)
		}
		image, err := renderSkinWall(ctx, pageView)
		if err != nil {
			if !sent {
				return c.Reply(ctx, "皮肤图渲染失败，请稍后再试")
			}
			return nil
		}
		if err := c.ReplyImageBytes(ctx, image); err != nil {
			return err
		}
		sent = true
	}
	return nil
}

// assignBandLayout 为一页皮肤分配大图(2×2)与精确网格坐标。
// 以两行为一段：每段 1 张大图 + 铺满其余格子的小图，恰好填满 cols 的两整行。
func assignBandLayout(pageSkins []skinWallItem, cols int) {
	smallPerBand := cols*2 - 4
	bandSize := 1 + smallPerBand
	idx := 0
	baseRow := 0
	for idx < len(pageSkins) {
		remaining := len(pageSkins) - idx
		if remaining >= bandSize {
			bc := 0 // 大图列 [0, cols-2]，固定取随机等效中最左边避免渲染不可复现
			pageSkins[idx].Big = true
			pageSkins[idx].Row = baseRow + 1
			pageSkins[idx].Col = bc + 1
			type cell struct{ r, c int }
			var free []cell
			for r := baseRow; r < baseRow+2; r++ {
				for cc := 0; cc < cols; cc++ {
					inBig := (r == baseRow || r == baseRow+1) && (cc == bc || cc == bc+1)
					if !inBig {
						free = append(free, cell{r, cc})
					}
				}
			}
			for k := 0; k < smallPerBand; k++ {
				pageSkins[idx+1+k].Big = false
				pageSkins[idx+1+k].Row = free[k].r + 1
				pageSkins[idx+1+k].Col = free[k].c + 1
			}
			idx += bandSize
			baseRow += 2
		} else {
			for k := 0; k < remaining; k++ {
				pageSkins[idx+k].Big = false
				pageSkins[idx+k].Row = baseRow + k/cols + 1
				pageSkins[idx+k].Col = k%cols + 1
			}
			idx += remaining
		}
	}
}

/* ------------------------------------------------------------------ *
 * #缺皮肤
 * ------------------------------------------------------------------ */

const (
	missingTopCount  = 12
	missingTopHero   = 6
	missingHeroSkins = 30
)

type missingTier struct {
	Label string
	Has   int
	Lack  int
	Pct   int
}

type missingSkinCard struct {
	Name  string
	Sub   string
	Price string
	Cover string
	Owned bool
}

type missingHeroTop struct {
	Hero string
	Num  int
}

type skinMissingView struct {
	Title     string
	SubText   string
	Username  string
	Avatar    string
	ScopeText string
	WorthText string
	Has       int
	Missing   int
	Total     int
	TotalKey  string
	Pct       string
	Tiers     []missingTier
	SkinTitle string
	SkinTip   string
	Skins     []missingSkinCard
	More      int
	HeroTop   []missingHeroTop
	FullText  string
	FootText  string
}

// parseMissingArgs 5 位以上数字是营地ID，4 位以内是绑定序号，其余当英雄名。
func parseMissingArgs(input string) (campID string, index int, heroName string) {
	index = -1
	var rest []string
	for _, tok := range strings.FieldsFunc(input, func(r rune) bool {
		return r == ' ' || r == ',' || r == '，' || r == '、'
	}) {
		if digitsRe.MatchString(tok) {
			if len(tok) >= 5 {
				campID = tok
				continue
			}
			if index < 0 {
				index, _ = strconv.Atoi(tok)
				continue
			}
		}
		rest = append(rest, tok)
	}
	heroName = strings.Join(rest, "")
	return campID, index, heroName
}

var yuanAbbr = map[string]string{"法": "法师", "射": "射手", "辅": "辅助", "坦": "坦克", "刺": "刺客"}

// expandYuanAbbr 元法/元射/元辅/元坦/元刺 → 元流之子(X)。
func expandYuanAbbr(name string) string {
	if matched := regexp.MustCompile(`^元(.)$`).FindStringSubmatch(name); matched != nil {
		if full, ok := yuanAbbr[matched[1]]; ok {
			return "元流之子(" + full + ")"
		}
	}
	return name
}

// normalizeHeroName 比对前统一去掉全半角括号与空白。
func normalizeHeroName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch r {
		case ' ', '（', '）', '(', ')':
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func skinMissingCommand(ctx context.Context, c sdk.Call, rest string) error {
	campIDArg, index, heroName := parseMissingArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID := campIDArg
	if campID == "" && index > 0 {
		if index > len(state.IDs) {
			return c.Reply(ctx, fmt.Sprintf("你没有第 %d 个绑定的营地ID，发送 #营地ID 看看列表", index))
		}
		campID = state.IDs[index-1]
	}
	if campID == "" {
		if len(state.IDs) == 0 {
			return c.Reply(ctx, "未查询到营地ID，请先使用 #绑定营地 绑定营地ID，或在指令后附带营地ID")
		}
		campID = state.currentID()
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	client := newCampClient()
	data, err := client.getSkinList(ctx, campID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}

	var conf []skinConf
	for _, item := range data.HeroSkinConfList {
		if !isClassicSkin(item) {
			conf = append(conf, item)
		}
	}
	if len(conf) == 0 {
		return c.Reply(ctx, "营地没返回皮肤配置表，稍后再试试")
	}
	owned := ownedSkinIDs(data.HeroSkinList)

	name := state.Names[campID]
	if name == "" {
		if refreshed := refreshRoleName(ctx, client, state, campID); refreshed != "" {
			name = refreshed
			_ = saveUserState(ctx, c.State, c.Event.UserID, state)
		}
	}
	if name == "" {
		name = "召唤师"
	}

	avatar := state.campAvatar(campID)
	if heroName != "" {
		hero, list, candidates := matchHeroSkins(conf, heroName)
		if len(list) == 0 {
			if len(candidates) > 0 {
				return c.Reply(ctx, fmt.Sprintf("没找到「%s」，你是想查：%s", heroName, strings.Join(candidates, "、")))
			}
			return c.Reply(ctx, fmt.Sprintf("没找到英雄「%s」，英雄名要写全，比如 #缺皮肤 妲己", heroName))
		}
		view := buildMissingHeroView(hero, list, owned, name)
		view.Avatar = avatar
		image, err := renderSkinMissing(ctx, view)
		if err != nil {
			return c.Reply(ctx, renderMissingHeroText(hero, list, owned, name))
		}
		return c.ReplyImageBytes(ctx, image)
	}

	view := buildMissingOverview(conf, owned, data.SkinCountInfo, name)
	view.Avatar = avatar
	image, err := renderSkinMissing(ctx, view)
	if err != nil {
		return c.Reply(ctx, renderMissingOverviewText(conf, owned, data.SkinCountInfo, name))
	}
	return c.ReplyImageBytes(ctx, image)
}

// matchHeroSkins 从配置表里认英雄名：先精确匹配，不中再按包含关系给候选。
func matchHeroSkins(conf []skinConf, input string) (string, []skinConf, []string) {
	want := normalizeHeroName(expandYuanAbbr(input))
	var exact []skinConf
	for _, item := range conf {
		if normalizeHeroName(item.SzHeroTitle) == want {
			exact = append(exact, item)
		}
	}
	if len(exact) > 0 {
		sort.SliceStable(exact, func(i, j int) bool { return exact[i].ISkinID.Int() < exact[j].ISkinID.Int() })
		return exact[0].SzHeroTitle, exact, nil
	}

	seen := map[string]bool{}
	var names []string
	for _, item := range conf {
		title := item.SzHeroTitle
		if title == "" || seen[title] {
			continue
		}
		if strings.Contains(normalizeHeroName(title), want) {
			seen[title] = true
			names = append(names, title)
		}
	}
	if len(names) == 1 {
		var list []skinConf
		for _, item := range conf {
			if item.SzHeroTitle == names[0] {
				list = append(list, item)
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].ISkinID.Int() < list[j].ISkinID.Int() })
		return names[0], list, nil
	}
	if len(names) > 8 {
		names = names[:8]
	}
	return "", nil, names
}

func missingValueLess(a, b skinConf) bool {
	ta, tb := tierRank(a.ClassTypeName), tierRank(b.ClassTypeName)
	if ta != tb {
		return ta < tb
	}
	sa, sb := szLevel(a.SzClass), szLevel(b.SzClass)
	if sa != sb {
		return sa < sb
	}
	return a.IPrice.Int() > b.IPrice.Int()
}

func missingSkinCardView(conf skinConf, owned bool, sub string) missingSkinCard {
	if sub == "" {
		sub = pickTierText(conf.ClassTypeName)
	}
	price := ""
	if conf.IPrice.Int() > 0 {
		price = fmt.Sprintf("%d点券", conf.IPrice.Int())
	}
	name := conf.SzTitle
	if name == "" {
		name = "皮肤" + conf.ISkinID.String()
	}
	return missingSkinCard{
		Name:  name,
		Sub:   sub,
		Price: price,
		Cover: firstNonEmpty(conf.BigCover, conf.SzSmallIcon),
		Owned: owned,
	}
}

func buildMissingOverview(conf []skinConf, owned map[string]bool, info skinCountInfo, name string) skinMissingView {
	var miss []skinConf
	for _, item := range conf {
		if !owned[item.ISkinID.String()] {
			miss = append(miss, item)
		}
	}
	total := len(conf)
	has := total - len(miss)

	view := skinMissingView{
		Title:    "皮肤缺失",
		SubText:  "不含经典皮肤",
		Username: name,
		Has:      has,
		Missing:  len(miss),
		Total:    total,
		TotalKey: "全部皮肤",
	}
	view.ScopeText = fmt.Sprintf("已有 %d / %d 款", has, total)
	worth := info.TotalValue.Int()
	notForSell := info.NotForSell.Int()
	if worth > 0 {
		view.WorthText = fmt.Sprintf("估值 %d 点券", worth)
		if notForSell > 0 {
			view.WorthText += fmt.Sprintf(" · 绝版 %d", notForSell)
		}
	}
	if total > 0 {
		view.Pct = fmt.Sprintf("%.1f", float64(has)/float64(total)*100)
	} else {
		view.Pct = "0"
	}

	for _, stat := range qualityStats {
		hit := func(item skinConf) bool {
			for _, n := range item.ClassTypeName {
				for _, alias := range stat.Aliases {
					if strings.TrimSpace(n) == alias {
						return true
					}
				}
			}
			return false
		}
		all := 0
		lack := 0
		for _, item := range conf {
			if hit(item) {
				all++
			}
		}
		if all == 0 {
			continue
		}
		for _, item := range miss {
			if hit(item) {
				lack++
			}
		}
		view.Tiers = append(view.Tiers, missingTier{
			Label: stat.Label,
			Has:   all - lack,
			Lack:  lack,
			Pct:   int(float64(all-lack) / float64(all) * 100),
		})
	}

	sorted := append([]skinConf(nil), miss...)
	sort.SliceStable(sorted, func(i, j int) bool { return missingValueLess(sorted[i], sorted[j]) })
	top := sorted
	if len(top) > missingTopCount {
		top = top[:missingTopCount]
	}
	view.SkinTitle = fmt.Sprintf("最值钱的缺失（前 %d）", len(top))
	view.SkinTip = "先按品质档，再按营地评级和标价"
	for _, item := range top {
		view.Skins = append(view.Skins, missingSkinCardView(item, false, item.SzHeroTitle))
	}

	byHero := map[string]int{}
	var heroOrder []string
	for _, item := range miss {
		hero := item.SzHeroTitle
		if hero == "" {
			hero = "未知"
		}
		if _, ok := byHero[hero]; !ok {
			heroOrder = append(heroOrder, hero)
		}
		byHero[hero]++
	}
	sort.SliceStable(heroOrder, func(i, j int) bool { return byHero[heroOrder[i]] > byHero[heroOrder[j]] })
	if len(heroOrder) > missingTopHero {
		heroOrder = heroOrder[:missingTopHero]
	}
	for _, hero := range heroOrder {
		view.HeroTop = append(view.HeroTop, missingHeroTop{Hero: hero, Num: byHero[hero]})
	}

	view.FullText = "全部皮肤都到手了"
	view.FootText = "不含经典皮肤（原皮人人都有）\n#缺皮肤 妲己 能看单个英雄缺哪几款"
	return view
}

func buildMissingHeroView(hero string, list []skinConf, owned map[string]bool, name string) skinMissingView {
	has := 0
	for _, item := range list {
		if owned[item.ISkinID.String()] {
			has++
		}
	}
	shown := list
	if len(shown) > missingHeroSkins {
		shown = shown[:missingHeroSkins]
	}
	view := skinMissingView{
		Title:     "皮肤缺失",
		SubText:   hero,
		Username:  name,
		Has:       has,
		Missing:   len(list) - has,
		Total:     len(list),
		TotalKey:  "该英雄皮肤",
		SkinTitle: fmt.Sprintf("%s 的皮肤（%d）", hero, len(list)),
		SkinTip:   "按皮肤序号排，压暗的是还没有的",
		More:      len(list) - len(shown),
		FullText:  fmt.Sprintf("「%s」的皮肤全齐了", hero),
		FootText:  "不含经典皮肤（原皮人人都有，列出来没意义）",
	}
	view.ScopeText = fmt.Sprintf("「%s」已有 %d / %d 款", hero, has, len(list))
	if len(list) > 0 {
		view.Pct = fmt.Sprintf("%.1f", float64(has)/float64(len(list))*100)
	} else {
		view.Pct = "0"
	}
	for _, item := range shown {
		view.Skins = append(view.Skins, missingSkinCardView(item, owned[item.ISkinID.String()], ""))
	}
	return view
}

/* ------------------------------ 缺皮肤文字兜底 ------------------------------ */

func skinLineText(conf skinConf) string {
	name := conf.SzTitle
	if name == "" {
		name = "皮肤" + conf.ISkinID.String()
	}
	tier := pickTierText(conf.ClassTypeName)
	price := conf.IPrice.Int()
	var builder strings.Builder
	builder.WriteString(name)
	if tier != "" {
		builder.WriteString("[" + tier + "]")
	}
	if price > 0 {
		fmt.Fprintf(&builder, " %d点券", price)
	}
	return builder.String()
}

func renderMissingHeroText(hero string, list []skinConf, owned map[string]bool, name string) string {
	has := 0
	for _, item := range list {
		if owned[item.ISkinID.String()] {
			has++
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s 的「%s」皮肤\n共 %d 款，已有 %d，还缺 %d\n\n", name, hero, len(list), has, len(list)-has)
	shown := list
	if len(shown) > missingHeroSkins {
		shown = shown[:missingHeroSkins]
	}
	for _, item := range shown {
		mark := "[有]"
		if !owned[item.ISkinID.String()] {
			mark = "[缺]"
		}
		fmt.Fprintf(&builder, "%s %s\n", mark, skinLineText(item))
	}
	if len(list) > len(shown) {
		fmt.Fprintf(&builder, "…… 还有 %d 款\n", len(list)-len(shown))
	}
	builder.WriteString("\n不含经典皮肤（原皮人人都有，列出来没意义）")
	return builder.String()
}

func renderMissingOverviewText(conf []skinConf, owned map[string]bool, info skinCountInfo, name string) string {
	var miss []skinConf
	for _, item := range conf {
		if !owned[item.ISkinID.String()] {
			miss = append(miss, item)
		}
	}
	total := len(conf)
	has := total - len(miss)
	pct := "0.0"
	if total > 0 {
		pct = fmt.Sprintf("%.1f", float64(has)/float64(total)*100)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s 的皮肤收集进度\n已有 %d / %d 款（%s%%），还缺 %d 款\n", name, has, total, pct, len(miss))
	if worth := info.TotalValue.Int(); worth > 0 {
		fmt.Fprintf(&builder, "已有皮肤估值 %d 点券", worth)
		if n := info.NotForSell.Int(); n > 0 {
			fmt.Fprintf(&builder, "，其中绝版 %d 款", n)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\n按品质：\n")
	for _, stat := range qualityStats {
		hit := func(item skinConf) bool {
			for _, n := range item.ClassTypeName {
				for _, alias := range stat.Aliases {
					if strings.TrimSpace(n) == alias {
						return true
					}
				}
			}
			return false
		}
		all, lack := 0, 0
		for _, item := range conf {
			if hit(item) {
				all++
			}
		}
		if all == 0 {
			continue
		}
		for _, item := range miss {
			if hit(item) {
				lack++
			}
		}
		fmt.Fprintf(&builder, "· %s：有 %d / 缺 %d\n", stat.Label, all-lack, lack)
	}

	sorted := append([]skinConf(nil), miss...)
	sort.SliceStable(sorted, func(i, j int) bool { return missingValueLess(sorted[i], sorted[j]) })
	top := sorted
	if len(top) > missingTopCount {
		top = top[:missingTopCount]
	}
	if len(top) > 0 {
		fmt.Fprintf(&builder, "\n最值钱的缺失（前 %d）：\n", len(top))
		for _, item := range top {
			fmt.Fprintf(&builder, "· %s —— %s\n", item.SzHeroTitle, skinLineText(item))
		}
	}

	byHero := map[string]int{}
	var heroOrder []string
	for _, item := range miss {
		hero := item.SzHeroTitle
		if hero == "" {
			hero = "未知"
		}
		if _, ok := byHero[hero]; !ok {
			heroOrder = append(heroOrder, hero)
		}
		byHero[hero]++
	}
	sort.SliceStable(heroOrder, func(i, j int) bool { return byHero[heroOrder[i]] > byHero[heroOrder[j]] })
	if len(heroOrder) > missingTopHero {
		heroOrder = heroOrder[:missingTopHero]
	}
	if len(heroOrder) > 0 {
		builder.WriteString("\n缺得最多的英雄：\n")
		var parts []string
		for _, hero := range heroOrder {
			parts = append(parts, fmt.Sprintf("%s %d", hero, byHero[hero]))
		}
		builder.WriteString(strings.Join(parts, " · ") + "\n")
	}
	builder.WriteString("\n发送 #缺皮肤 妲己 看单个英雄缺哪几款\n总数按营地全量配置表算，不含经典皮肤")
	return builder.String()
}

/* ------------------------------------------------------------------ *
 * #查皮肤
 * ------------------------------------------------------------------ */

const (
	pvpHeroListURL = "https://pvp.qq.com/web201605/js/herolist.json"
	pvpCacheTTL    = 6 * time.Hour
)

// pvpHero 官网 herolist.json 的单条：ename=英雄ID、cname=全名、skin_name 按 | 分隔。
type pvpHero struct {
	EName    json.Number `json:"ename"`
	CName    string      `json:"cname"`
	SkinName string      `json:"skin_name"`
}

var (
	pvpHeroListMu sync.Mutex
	pvpHeroList   []pvpHero
	pvpFetchedAt  time.Time
)

// fetchPvpHeroList 拉官网英雄列表，内存缓存 6 小时。
func fetchPvpHeroList(ctx context.Context) ([]pvpHero, error) {
	pvpHeroListMu.Lock()
	defer pvpHeroListMu.Unlock()
	if len(pvpHeroList) > 0 && time.Since(pvpFetchedAt) < pvpCacheTTL {
		return pvpHeroList, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pvpHeroListURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var list []pvpHero
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	pvpHeroList = list
	pvpFetchedAt = time.Now()
	return list, nil
}

type heroSkinItem struct {
	Name  string
	URL   string
	Index int
}

type heroSkinView struct {
	HeroName  string
	SkinCount int
	SkinData  []heroSkinItem
}

func heroSkinCommand(ctx context.Context, c sdk.Call, rest string) error {
	heroName := strings.Join(strings.Fields(rest), "")
	if heroName == "" {
		return c.Reply(ctx, "请输入要查询的英雄名称，示例：#查皮肤 妲己")
	}
	fullName := expandYuanAbbr(heroName)

	var hero *pvpHero
	if list, err := fetchPvpHeroList(ctx); err == nil {
		for i := range list {
			if list[i].CName == fullName {
				hero = &list[i]
				break
			}
		}
	}

	// herolist 的皮肤名：「皮肤序号 → 名字」对照表，按 | 分隔。
	var heroSkinNames []string
	if hero != nil && hero.SkinName != "" {
		heroSkinNames = strings.Split(hero.SkinName, "|")
	}

	// 皮肤清单：优先营地全量配置表（含原皮与联动皮），兜底官网 herolist。
	skins := collectSkins(ctx, c, hero, fullName)
	if len(skins) == 0 && hero != nil {
		for idx, name := range heroSkinNames {
			skins = append(skins, skinConf{
				ISkinID: scalarValue(fmt.Sprintf("%s%02d", hero.EName.String(), idx)),
				SzTitle: name,
			})
		}
	}
	if len(skins) == 0 {
		return c.Reply(ctx, "未找到该英雄的皮肤信息")
	}

	ename := ""
	if hero != nil {
		ename = hero.EName.String()
	} else if id := skins[0].ISkinID.String(); len(id) > 2 {
		ename = id[:len(id)-2]
	}

	displayName := fullName
	if hero != nil {
		displayName = simplifyHeroName(hero.CName)
	}

	view := heroSkinView{HeroName: displayName, SkinCount: len(skins)}
	for idx, skin := range skins {
		url := heroSkinImageURL(skin, ename, heroSkinNames)
		view.SkinData = append(view.SkinData, heroSkinItem{
			Name:  simplifyHeroName(skin.SzTitle),
			URL:   url,
			Index: idx + 1,
		})
	}

	image, err := renderHeroSkin(ctx, view)
	if err != nil {
		return c.Reply(ctx, "英雄皮肤出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

// collectSkins 营地全量配置表里该英雄的皮肤（需要任意一个可用登录态）。
func collectSkins(ctx context.Context, c sdk.Call, hero *pvpHero, fullName string) []skinConf {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return nil
	}
	campID := state.currentID()
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return nil
	}
	data, err := newCampClient().getSkinList(ctx, campID, auth)
	if err != nil {
		return nil
	}
	want := fullName
	if hero != nil {
		want = hero.CName
	}
	target := normalizeHeroName(want)
	var out []skinConf
	for _, item := range data.HeroSkinConfList {
		if normalizeHeroName(item.SzHeroTitle) == target {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ISkinID.Int() < out[j].ISkinID.Int() })
	return out
}

// gtimgBigSkin 官方横版大图(1920x882)，路径里的序号是「皮肤序号+1」，原皮为 1。
func gtimgBigSkin(ename string, seq int) string {
	return fmt.Sprintf("https://game.gtimg.cn/images/yxzj/img201606/skin/hero-info/%s/%s-bigskin-%d.jpg", ename, ename, seq)
}

// heroSkinImageURL 单张皮肤的图源回退链：
//  1. gtimg bigskin 横版大图（仅当序号位置的皮肤名与 herolist 对得上，避免跳号配错图）
//  2. 营地 szLargeIcon 竖版大图
//  3. 营地 bigCover 卡面图
func heroSkinImageURL(skin skinConf, ename string, heroSkinNames []string) string {
	seq := skin.ISkinID.Int() % 100
	nameMatched := seq >= 0 && seq < len(heroSkinNames) && heroSkinNames[seq] == skin.SzTitle
	if nameMatched && ename != "" {
		return gtimgBigSkin(ename, seq+1)
	}
	if skin.SzLargeIcon != "" {
		return skin.SzLargeIcon
	}
	return firstNonEmpty(skin.BigCover, skin.SzSmallIcon)
}

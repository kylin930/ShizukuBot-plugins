package gloryofkings

// P2：英雄查询 —— #我的英雄、#常用英雄（#英雄战力榜）。
// 数据口径与上游 apps/myHeroList.js、apps/heroList.js 对齐：
//   - 我的英雄：主表 getGameHeroList（生涯累计），历史最高战力与称号
//     由 getSeasonUsuallyHeroList(seasonId=0) 按 heroId 合并；
//   - 常用英雄：当前赛季 seasonpage 的 rankInfo/masterInfo heros 合并取前 5，
//     无数据时兜底 getProfileHeroList 生涯累计。

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"shizukubot/sdk"
)

// heroImgBase 英雄战斗页特写图，和上游共用一套地址。
const heroImgBase = "https://game-1255653016.file.myqcloud.com/battle_skin_1250-326"

// skilledNames 熟练度等级在营地里的叫法（8=神话 7=传说 6=巅峰 5=超凡）。
var skilledNames = map[int]string{8: "神话", 7: "传说", 6: "巅峰", 5: "超凡"}

// fightColor 战力配色，与营地方案一致。
func fightColor(power int) string {
	switch {
	case power <= 2500:
		return "#c8c8c8"
	case power <= 5000:
		return "#6f8ef5"
	case power <= 7500:
		return "#a24bff"
	case power <= 10000:
		return "#f5d76e"
	default:
		return "#ff5b7c"
	}
}

// splitHeroName 「元流之子(射手)」→ 主名 + 副标。
func splitHeroName(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	open := strings.IndexAny(text, "(（")
	if open < 0 {
		return text, ""
	}
	closeIdx := strings.LastIndexAny(text, ")）")
	if closeIdx <= open {
		return text, ""
	}
	return strings.TrimSpace(text[:open]), strings.TrimSpace(text[open+1 : closeIdx])
}

// simplifyHeroName 荣誉标空间有限，元流之子(法师) → 元法。
func simplifyHeroName(text string) string {
	name, sub := splitHeroName(text)
	if sub != "" && strings.HasPrefix(name, "元流之子") && sub != "" {
		runes := []rune(sub)
		if len(runes) > 0 {
			return "元" + string(runes[0])
		}
	}
	return text
}

/* ------------------------------------------------------------------ *
 * #我的英雄
 * ------------------------------------------------------------------ */

const (
	myHeroShowCount = 10
	myHeroMaxCount  = 30
)

type myHeroCard struct {
	Name         string
	SubName      string
	HeroType     string
	SkilledText  string
	HonorText    string
	ImgURL       string
	PlayNum      int
	WinRate      string
	FightPower   int
	FightColor   string
	HistoryPower int
	HistoryColor string
	SeasonPower  int
	SeasonColor  string
}

type myHeroView struct {
	YdID       string
	HeroCount  int
	OwnCount   int
	ShownCount int
	TotalPlay  int
	TotalRate  string
	Heroes     []myHeroCard
}

// parseMyHeroArgs 营地ID（≥5 位数字）与展示数量（≤4 位）可混写。
func parseMyHeroArgs(input string) (campID string, limit int) {
	limit = myHeroShowCount
	for _, tok := range strings.FieldsFunc(input, func(r rune) bool {
		return r == ' ' || r == ',' || r == '，' || r == '、'
	}) {
		if !digitsRe.MatchString(tok) {
			continue
		}
		if len(tok) >= 5 {
			campID = tok
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil && n >= 1 {
			if n > myHeroMaxCount {
				n = myHeroMaxCount
			}
			limit = n
		}
	}
	return campID, limit
}

func myHeroCommand(ctx context.Context, c sdk.Call, rest string) error {
	campIDArg, limit := parseMyHeroArgs(rest)
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
	data, err := client.getGameHeroList(ctx, campID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}

	var played []gameHero
	for _, hero := range data.HeroList {
		if hero.PlayNum.Int() > 0 {
			played = append(played, hero)
		}
	}
	if len(played) == 0 {
		return c.Reply(ctx, "未获取到英雄数据，请前往王者营地开启「陌生人可见」后重试")
	}

	// 历史最高战力 + 称号（seasonId=0 跨赛季峰值），取不到自动退回当前战力。
	history := fetchHistoryPower(ctx, client, campID, auth)

	type mergedHero struct {
		hero         gameHero
		historyPower int
		displayPower int
		honorText    string
	}
	merged := make([]mergedHero, 0, len(played))
	for _, hero := range played {
		item := mergedHero{hero: hero, displayPower: hero.HeroFightPower.Int()}
		if hit, ok := history[hero.HeroID.String()]; ok {
			item.historyPower = hit.power
			item.honorText = hit.honor
			if hit.power > 0 {
				item.displayPower = hit.power
			}
		}
		merged = append(merged, item)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].displayPower != merged[j].displayPower {
			return merged[i].displayPower > merged[j].displayPower
		}
		return merged[i].hero.PlayNum.Int() > merged[j].hero.PlayNum.Int()
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}

	view := myHeroView{
		YdID:       campID,
		HeroCount:  len(played),
		OwnCount:   data.HasData.HeroNum.Int(),
		ShownCount: len(merged),
	}
	if view.OwnCount <= 0 {
		view.OwnCount = len(played)
	}
	totalWin := 0
	for _, hero := range played {
		view.TotalPlay += hero.PlayNum.Int()
		totalWin += hero.WinNum.Int()
	}
	if view.TotalPlay > 0 {
		view.TotalRate = fmt.Sprintf("%.1f%%", float64(totalWin)/float64(view.TotalPlay)*100)
	} else {
		view.TotalRate = "—"
	}

	for _, item := range merged {
		hero := item.hero
		name, sub := splitHeroName(hero.Name)
		level := hero.SkilledLevel.Int()
		skilled := ""
		if level > 0 {
			if label, ok := skilledNames[level]; ok {
				skilled = label
			} else {
				skilled = fmt.Sprintf("Lv.%d", level)
			}
		}
		winRate := hero.WinRate
		if winRate == "" {
			winRate = "—"
		}
		view.Heroes = append(view.Heroes, myHeroCard{
			Name:         name,
			SubName:      sub,
			HeroType:     strings.Join(hero.HeroTypes, "/"),
			SkilledText:  skilled,
			HonorText:    item.honorText,
			ImgURL:       heroImageURL(hero.HeroID.String(), hero.URL, hero.HeroIcon),
			PlayNum:      hero.PlayNum.Int(),
			WinRate:      winRate,
			FightPower:   item.displayPower,
			FightColor:   fightColor(item.displayPower),
			HistoryPower: item.historyPower,
			HistoryColor: fightColor(item.historyPower),
			SeasonPower:  hero.HeroFightPower.Int(),
			SeasonColor:  fightColor(hero.HeroFightPower.Int()),
		})
	}

	image, err := renderMyHeroList(ctx, view)
	if err != nil {
		return c.Reply(ctx, "我的英雄出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

type historyPower struct {
	power int
	honor string
}

// fetchHistoryPower 拉历史最高战力与配套称号；失败返回空表，调用方退回当前战力。
func fetchHistoryPower(ctx context.Context, client *campClient, campID string, a *campAuth) map[string]historyPower {
	out := map[string]historyPower{}
	profile, err := client.getProfile(ctx, campID, a)
	if err != nil {
		return out
	}
	roleID := profile.TargetRoleID.String()
	if roleID == "" {
		return out
	}
	list, err := client.getSeasonUsuallyHeroList(ctx, roleID, 0, a)
	if err != nil {
		return out
	}
	for _, item := range list {
		heroID := item.HeroID.String()
		if heroID == "" {
			continue
		}
		honor := ""
		if item.HonorTitle != nil {
			honor = item.HonorTitle.Desc.Full
		}
		out[heroID] = historyPower{power: item.MaxHeroFightPower.Int(), honor: honor}
	}
	return out
}

// heroImageURL 头像优先战斗页特写图，取不到回退接口给的横版图。
func heroImageURL(heroID, url, icon string) string {
	if heroID != "" {
		return fmt.Sprintf("%s/%s00.jpg?imageMogr2/thumbnail/x170/crop/270x170/gravity/east", heroImgBase, heroID)
	}
	if url != "" {
		return url
	}
	return icon
}

/* ------------------------------------------------------------------ *
 * #常用英雄 / #英雄战力榜
 * ------------------------------------------------------------------ */

const heroListShowCount = 5

type heroListCard struct {
	Name       string
	SubName    string
	ImgURL     string
	RankCnt    string
	RankRate   string
	PeakCnt    string
	PeakRate   string
	FightPower string
	FightColor string
	HonorIcon  string
	HonorText  string
}

type heroListView struct {
	YdID      string
	RoleName  string
	RoleIcon  string
	RoleArea  string
	ScopeName string
	ShowModes bool
	HeroCount int
	Heroes    []heroListCard
}

// honorIconFile 称号标：1=县 2=市 3=省 4=国服（注意 contry 是上游资源名拼写）。
func honorIconFile(honorType int) string {
	switch honorType {
	case 1:
		return "icon_honor_county.png"
	case 2:
		return "icon_honor_city.png"
	case 3:
		return "icon_honor_province.png"
	case 4:
		return "icon_honor_contry.png"
	}
	return ""
}

func heroListCommand(ctx context.Context, c sdk.Call, rest string) error {
	campID := strings.TrimSpace(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if campID == "" || !digitsRe.MatchString(campID) {
		if campID != "" && !digitsRe.MatchString(campID) {
			return c.Reply(ctx, "常用英雄只支持附带营地ID，示例：#常用英雄 123456")
		}
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
	profile, err := client.getProfile(ctx, campID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	roleID := profile.TargetRoleID.String()
	if roleID == "" {
		return c.Reply(ctx, "获取角色信息失败，请稍后重试")
	}

	var role profileRole
	if len(profile.RoleList) > 0 {
		role = profile.RoleList[0]
	}
	for _, item := range profile.RoleList {
		if item.RoleID.String() == roleID {
			role = item
			break
		}
	}
	roleName := cleanRoleName(role.RoleName)
	if roleName == "" {
		roleName = campID
	}
	roleArea := strings.TrimSpace(strings.Join([]string{role.AreaName, role.RoleText}, " · "))
	roleArea = strings.Trim(roleArea, " ·")

	// 当前赛季排位 + 巅峰常用英雄；赛季没打过则兜底生涯累计。
	picked, scopeName, showModes, err := fetchSeasonHeroes(ctx, client, roleID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	if len(picked) == 0 {
		picked, err = fetchCareerHeroes(ctx, client, campID, roleID, auth)
		if err != nil {
			return c.Reply(ctx, seasonErrorText(err))
		}
		scopeName = "生涯累计"
		showModes = false
	}
	if len(picked) == 0 {
		return c.Reply(ctx, "未获取到常用英雄数据，请前往王者营地开启「陌生人可见」后重试")
	}

	view := heroListView{
		YdID:      campID,
		RoleName:  roleName,
		RoleIcon:  role.RoleIcon,
		RoleArea:  roleArea,
		ScopeName: scopeName,
		ShowModes: showModes,
		HeroCount: len(picked),
	}
	for _, hero := range picked {
		view.Heroes = append(view.Heroes, buildHeroListCard(hero))
	}

	image, err := renderHeroList(ctx, view)
	if err != nil {
		return c.Reply(ctx, "常用英雄出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

// commonHero 是赛季榜/生涯榜合并后的中间结构。
type commonHero struct {
	HeroID     string
	RawName    string
	ImgURL     string
	FightPower int
	Honor      *honorTitle
	RankCnt    string
	RankRate   string
	PeakCnt    string
	PeakRate   string
	TotalCnt   int
}

// fetchSeasonHeroes 取当前赛季排位/巅峰场次前 5 的英雄，按 heroId 合并去重。
func fetchSeasonHeroes(ctx context.Context, client *campClient, roleID string, a *campAuth) ([]commonHero, string, bool, error) {
	summary, err := client.getSeasonpage(ctx, roleID, 0, a)
	if err != nil {
		return nil, "", true, err
	}
	if len(summary.HistoryList) == 0 {
		return nil, "", true, nil
	}
	current := summary.HistoryList[0]
	seasonID := current.SeasonID.Int()
	if seasonID == 0 {
		return nil, current.SeasonName, true, nil
	}
	detail, err := client.getSeasonpage(ctx, roleID, seasonID, a)
	if err != nil {
		return nil, "", true, err
	}

	merged := map[string]*commonHero{}
	var order []string
	collect := func(heros []seasonHero, mode string) {
		for _, hero := range heros {
			heroID := hero.HeroID.String()
			if heroID == "" {
				continue
			}
			// 上游 heroList.js 优先用 heroLandscapeIcon 做展示图，称号取第一个非空的 honorTitle。
			icon := firstNonEmpty(hero.HeroLandscapeIcon, hero.HeroIcon)
			item, ok := merged[heroID]
			if !ok {
				item = &commonHero{HeroID: heroID, RawName: hero.HeroName, ImgURL: icon}
				merged[heroID] = item
				order = append(order, heroID)
			}
			if power := hero.HeroFightPower.Int(); power > item.FightPower {
				item.FightPower = power
			}
			if item.ImgURL == "" {
				item.ImgURL = icon
			}
			if item.Honor == nil && hero.HonorTitle != nil {
				item.Honor = hero.HonorTitle
			}
			gameCnt := strconv.Itoa(hero.GameCnt.Int())
			winRate := percentText(hero.WinRate.Float(), 1)
			if mode == "rank" {
				item.RankCnt = gameCnt
				item.RankRate = winRate
			} else {
				item.PeakCnt = gameCnt
				item.PeakRate = winRate
			}
			item.TotalCnt += hero.GameCnt.Int()
		}
	}
	collect(detail.Behavior.RankInfo.Heros, "rank")
	collect(detail.Behavior.MasterInfo.Heros, "peak")

	out := make([]commonHero, 0, len(merged))
	for _, heroID := range order {
		out = append(out, *merged[heroID])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FightPower != out[j].FightPower {
			return out[i].FightPower > out[j].FightPower
		}
		return out[i].TotalCnt > out[j].TotalCnt
	})
	if len(out) > heroListShowCount {
		out = out[:heroListShowCount]
	}
	return out, current.SeasonName, true, nil
}

// fetchCareerHeroes 赛季无数据时的兜底：生涯累计前 5。
func fetchCareerHeroes(ctx context.Context, client *campClient, campID, roleID string, a *campAuth) ([]commonHero, error) {
	list, err := client.getProfileHeroList(ctx, campID, roleID, a)
	if err != nil {
		return nil, err
	}
	out := make([]commonHero, 0, len(list))
	for _, hero := range list {
		basic := hero.BasicInfo
		winRate := basic.WinRate
		if winRate == "" {
			winRate = "-"
		}
		out = append(out, commonHero{
			HeroID:     basic.HeroID.String(),
			RawName:    basic.Title,
			FightPower: basic.HeroFightPower.Int(),
			Honor:      hero.HonorTitle,
			RankCnt:    strconv.Itoa(basic.PlayNum.Int()),
			RankRate:   winRate,
			TotalCnt:   basic.PlayNum.Int(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FightPower != out[j].FightPower {
			return out[i].FightPower > out[j].FightPower
		}
		return out[i].TotalCnt > out[j].TotalCnt
	})
	if len(out) > heroListShowCount {
		out = out[:heroListShowCount]
	}
	return out, nil
}

func buildHeroListCard(hero commonHero) heroListCard {
	name, sub := splitHeroName(hero.RawName)
	card := heroListCard{
		Name:       name,
		SubName:    sub,
		ImgURL:     heroImageURL(hero.HeroID, hero.ImgURL, ""),
		RankCnt:    hero.RankCnt,
		RankRate:   hero.RankRate,
		PeakCnt:    hero.PeakCnt,
		PeakRate:   hero.PeakRate,
		FightColor: fightColor(hero.FightPower),
	}
	if hero.FightPower > 0 {
		card.FightPower = strconv.Itoa(hero.FightPower)
	} else {
		card.FightPower = "-"
	}
	if hero.Honor != nil {
		card.HonorIcon = imageDataURI(honorIconFile(hero.Honor.Type.Int()))
		text := hero.Honor.Desc.Full
		if text == "" {
			text = hero.Honor.Desc.Name
		}
		if text == "" {
			text = hero.Honor.Desc.Abbr
		}
		card.HonorText = simplifyHeroName(text)
	}
	return card
}

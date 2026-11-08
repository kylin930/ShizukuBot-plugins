package gloryofkings

// 趋势图：#巅峰趋势（巅峰分折线）与 #段位趋势（段位阶梯）。
// 口径与上游 apps/scoreTrend.js、apps/rankTrend.js 及 utils/scoreTrend.js、
// utils/rankTrend.js 一致：
//   - 数据来自本地归档（archive.go，最多 35 天），库里够点时一次营地请求都不发，
//     凑不够 10 场才去补拉战绩（最多 8 页，下限为整个归档保留期）；
//   - 只画会改变对应指标的场次：巅峰趋势只取 mapName 含「巅峰」的局
//     （排位局 old/newMasterMatchScore 恒等，混进来是水平噪声），
//     段位趋势只取 mapName 含「排位」的局（巅峰/娱乐局段位不动，会挤扁真实升降）；
//   - 天数只是首选窗口，窗口内不足 10 场放宽到全库最近 10 场，并在图上如实标注；
//   - 段位高低按「大段位 → roleJob 编号 → stars」三级排序：王者段编号恒 16，
//     比星耀的 22~25 小，不能直接比编号；相邻点编号跳变 ≥3（切游戏角色）
//     或「大段降 1 级 + 编号反而大涨」（赛季重置）时折线断开成独立线段。

import (
	"context"
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	trendDefaultDays  = 14 // 默认窗口天数（上游 TREND_DEFAULT_DAYS）
	trendTargetPoints = 10 // 窗口内想凑够的场数，不足放宽到全库最近 10 场
	trendMaxPoints    = 60 // 折线最多点数，超出等距抽稀（首尾必留）
	scoreRecentCount  = 8  // 巅峰趋势「最近战况」列几场
	rankRecentCount   = 10 // 段位趋势「最近战况」列几场
	trendHeroTop      = 6  // 带分英雄列前几名

	rankJumpTolerance = 3 // roleJob 编号跳变多少算「不是正常升降段」
)

/* ------------------------------------------------------------------ *
 * 公共小工具
 * ------------------------------------------------------------------ */

// isPeakBattle 是不是巅峰赛的场次（mapName 实测为「巅峰赛」，Contains 兜住可能的后缀）。
func isPeakBattle(battle archiveBattle) bool {
	return strings.Contains(battle.MapName, "巅峰")
}

// isRankedBattle 是不是排位赛。巅峰赛改的是巅峰分，娱乐局什么都不改。
func isRankedBattle(battle archiveBattle) bool {
	return strings.Contains(battle.MapName, "排位")
}

// thinSlice 等距抽稀，首尾必留。
func thinSlice[T any](list []T, max int) []T {
	if len(list) <= max {
		return list
	}
	step := float64(len(list)-1) / float64(max-1)
	out := make([]T, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, list[int(math.Round(float64(i)*step))])
	}
	return out
}

func trendDayKey(sec int64) string { return time.Unix(sec, 0).Format("2006-01-02") }
func trendMD(sec int64) string     { return time.Unix(sec, 0).Format("1-2") }
func trendHM(sec int64) string     { return time.Unix(sec, 0).Format("15:04") }

// dayLabel "2026-11-07" → "11/07"（上游 key.slice(5).replace('-', '/')）。
func dayLabel(key string) string {
	if len(key) < 7 {
		return key
	}
	return strings.Replace(key[5:], "-", "/", 1)
}

func signed(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func deltaClass(diff int) string {
	switch {
	case diff > 0:
		return "up"
	case diff < 0:
		return "down"
	default:
		return "flat"
	}
}

// trendResult 胜负文案与配色；逃跑（既不是 1 也不是 2）显示「—」。
func trendResult(result int) (text, class string) {
	switch result {
	case 1:
		return "胜", "win"
	case 2:
		return "负", "lose"
	default:
		return "—", "flat"
	}
}

func kdaText(kill, dead, assist int) string {
	return fmt.Sprintf("%d/%d/%d", kill, dead, assist)
}

// spanText 跨天数的展示（最多一位小数，整数不带小数点）。
func spanText(spanDays float64) string {
	return strconv.FormatFloat(spanDays, 'f', -1, 64)
}

// coverText 用户要 days 天而库里只有 spanDays 天，两个口径如实区分：
// 放宽过窗口时一律说实际覆盖，否则「近 2 天」会和旁边的日期区间自相矛盾。
func coverText(spanDays float64, days int, relaxed bool) string {
	if !relaxed && spanDays >= float64(days) {
		return fmt.Sprintf("近 %d 天", days)
	}
	return fmt.Sprintf("实际覆盖 %s 天", spanText(spanDays))
}

// battleStreaks 最长连胜 / 连败；逃跑算断。
func battleStreaks(battles []archiveBattle) (win, lose int) {
	curWin, curLose := 0, 0
	for _, battle := range battles {
		if battle.Result == 1 {
			curWin++
		} else {
			curWin = 0
		}
		if battle.Result == 2 {
			curLose++
		} else {
			curLose = 0
		}
		if curWin > win {
			win = curWin
		}
		if curLose > lose {
			lose = curLose
		}
	}
	return win, lose
}

// heroNameMap 英雄ID → 英雄名（官网总表，内存缓存 6 小时；失败返回 nil，调用方按 ID 兜底显示）。
func heroNameMap(ctx context.Context) map[string]string {
	list, err := fetchPvpHeroList(ctx)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(list))
	for _, hero := range list {
		out[hero.EName.String()] = hero.CName
	}
	return out
}

// heroIconURL 英雄头像地址（上游 reportStore.heroIconUrl 同款）。
func heroIconURL(heroID string) string {
	id := strings.TrimSpace(heroID)
	if id == "" {
		return ""
	}
	return "https://game.gtimg.cn/images/yxzj/img201606/heroimg/" + id + "/" + id + ".jpg"
}

func trendHeroName(heroMap map[string]string, heroID string) string {
	if name := heroMap[heroID]; name != "" {
		return html.EscapeString(name)
	}
	return "英雄" + html.EscapeString(heroID)
}

// trendHeader 图头的展示名与头像：缓存的营地昵称 → 群名片/昵称 → QQ 号。
func trendHeader(c sdk.Call, state *userState, campID string) (name, avatar string) {
	name = state.Names[campID]
	if name == "" {
		name = c.Event.Nickname
	}
	if name == "" {
		name = c.Event.UserID
	}
	if cred := state.bestCredential(campID); cred != nil {
		avatar = cred.Avatar
	}
	return html.EscapeString(name), avatar
}

// trendArgs 是趋势类指令的参数（上游 apps/scoreTrend.js:140 parseArgs、rankTrend 同款）：
// 5 位以上纯数字是营地号，1-2 位是观察天数，3-4 位是绑定序号。
type trendArgs struct {
	campID string
	days   int
	index  int
}

func parseTrendArgs(input string) trendArgs {
	var args trendArgs
	for _, token := range strings.FieldsFunc(input, func(r rune) bool {
		return r == ' ' || r == '，' || r == ',' || r == '、'
	}) {
		token = strings.TrimSpace(token)
		if !digitsRe.MatchString(token) {
			continue
		}
		switch {
		case len(token) >= 5:
			args.campID = token
		case len(token) <= 2 && args.days == 0:
			args.days, _ = strconv.Atoi(token)
		case args.index == 0:
			args.index, _ = strconv.Atoi(token)
		}
	}
	return args
}

// resolveTrendCamp 解析趋势指令的目标营地号：显式营地号 > 绑定序号 > 当前绑定。
// 趋势指令的 3-4 位数字是绑定序号（与表现类指令的赛季号口径不同）。
func resolveTrendCamp(state *userState, args trendArgs) (string, error) {
	if args.campID != "" {
		return args.campID, nil
	}
	if len(state.IDs) == 0 {
		return "", fmt.Errorf("你还没有绑定营地ID\n发送 #绑定营地 [营地ID] 后就能查数据")
	}
	if args.index > 0 {
		if args.index > len(state.IDs) {
			return "", fmt.Errorf("你没有第 %d 个绑定的营地ID，发送 #营地ID 看看列表", args.index)
		}
		return state.IDs[args.index-1], nil
	}
	return state.currentID(), nil
}

// trendWindowDays 把用户给的天数夹到 [1, 归档保留期]，未给则用默认值。
func trendWindowDays(days int) int {
	if days <= 0 {
		days = trendDefaultDays
	}
	if days < 1 {
		days = 1
	}
	if days > archiveKeepDays {
		days = archiveKeepDays
	}
	return days
}

/* ------------------------------------------------------------------ *
 * 巅峰趋势
 * ------------------------------------------------------------------ */

type scoreTrendPoint struct {
	Score int   `json:"score"`
	Time  int64 `json:"time"`
}

type scoreTrendDayRow struct {
	Date       string
	Count      int
	Win        int
	Lose       int
	Score      int
	DeltaText  string
	DeltaClass string
}

type scoreTrendRecentRow struct {
	HeroName    string
	HeroIcon    string
	ResultText  string
	ResultClass string
	KDA         string
	Score       int
	DeltaText   string
	DeltaClass  string
	TimeText    string
}

type scoreTrendHeroRow struct {
	HeroName string
	HeroID   string
	HeroIcon string
	Count    int
	WinRate  int
	NetText  string
	NetClass string
}

// scoreTrendView 是 ScoreTrend 模板的完整变量树（字段名对应上游模板变量）。
type scoreTrendView struct {
	Title    string
	SubText  string
	Username string
	Avatar   string
	FootText string

	Count         int
	Win           int
	Lose          int
	WinRate       float64
	WinRateClass  string
	StartScore    int
	Current       int
	Peak          int
	Low           int
	DeltaText     string
	DeltaClass    string
	MaxWinStreak  int
	MaxLoseStreak int
	SpanDays      float64
	RangeText     string
	CoverText     string

	Trend      []scoreTrendPoint
	TrendJson  string
	DayRows    []scoreTrendDayRow
	RecentRows []scoreTrendRecentRow
	HeroRows   []scoreTrendHeroRow
}

// pickPeakBattles 挑出区间内的巅峰局，返回正序（旧 → 新），去掉没有巅峰分的场次。
func pickPeakBattles(battles []archiveBattle, fromSec int64) []archiveBattle {
	var out []archiveBattle
	for _, battle := range battles {
		if !isPeakBattle(battle) || battle.Time < fromSec || battle.NewMasterMatchScore <= 0 {
			continue
		}
		out = append(out, battle)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// pickPeakWindow 取「要画的那一窗巅峰局」：窗口内不足 target 场放宽到全库最近 target 场。
// relaxed=true 表示突破了天数窗口。
func pickPeakWindow(battles []archiveBattle, fromSec int64) (list []archiveBattle, relaxed bool) {
	inRange := pickPeakBattles(battles, fromSec)
	if len(inRange) >= trendTargetPoints {
		return inRange, false
	}
	all := pickPeakBattles(battles, 0)
	if len(all) <= len(inRange) {
		return inRange, false
	}
	if len(all) > trendTargetPoints {
		all = all[len(all)-trendTargetPoints:]
	}
	return all, true
}

// buildScoreTrendView 把巅峰局序列整理成模板数据；少于 2 场返回 nil（一个点画不出趋势）。
func buildScoreTrendView(picked []archiveBattle, heroMap map[string]string, days int, relaxed bool) *scoreTrendView {
	if len(picked) < 2 {
		return nil
	}
	first := picked[0]
	last := picked[len(picked)-1]
	// 起点用最早一场的「打之前」分数：这样涨跌把第一场本身也算进去。
	startScore := first.OldMasterMatchScore
	if startScore == 0 {
		startScore = first.NewMasterMatchScore
	}
	current := last.NewMasterMatchScore
	peak, low := current, current
	win, lose := 0, 0
	for _, battle := range picked {
		if battle.NewMasterMatchScore > peak {
			peak = battle.NewMasterMatchScore
		}
		if battle.NewMasterMatchScore < low {
			low = battle.NewMasterMatchScore
		}
		switch battle.Result {
		case 1:
			win++
		case 2:
			lose++
		}
	}
	played := win + lose
	winRate := 0.0
	if played > 0 {
		winRate = math.Round(float64(win)/float64(played)*1000) / 10
	}
	delta := current - startScore

	// 折线点：抽稀后的每场一个点。
	shown := thinSlice(picked, trendMaxPoints)
	trend := make([]scoreTrendPoint, 0, len(shown))
	for _, battle := range shown {
		trend = append(trend, scoreTrendPoint{Score: battle.NewMasterMatchScore, Time: battle.Time})
	}

	// 逐日战况：一天一行，净涨跌 = 当天最后一场的 new − 第一场的 old。
	type dayGroup struct {
		key   string
		items []archiveBattle
	}
	groupsByDay := map[string]*dayGroup{}
	var groups []*dayGroup
	for _, battle := range picked {
		key := trendDayKey(battle.Time)
		group := groupsByDay[key]
		if group == nil {
			group = &dayGroup{key: key}
			groupsByDay[key] = group
			groups = append(groups, group)
		}
		group.items = append(group.items, battle)
	}
	dayRows := make([]scoreTrendDayRow, 0, len(groups))
	for i := len(groups) - 1; i >= 0; i-- {
		list := groups[i].items
		dayStart := list[0].OldMasterMatchScore
		if dayStart == 0 {
			dayStart = list[0].NewMasterMatchScore
		}
		dayEnd := list[len(list)-1].NewMasterMatchScore
		diff := dayEnd - dayStart
		dayWin, dayLose := 0, 0
		for _, battle := range list {
			switch battle.Result {
			case 1:
				dayWin++
			case 2:
				dayLose++
			}
		}
		dayRows = append(dayRows, scoreTrendDayRow{
			Date:       dayLabel(groups[i].key),
			Count:      len(list),
			Win:        dayWin,
			Lose:       dayLose,
			Score:      dayEnd,
			DeltaText:  signed(diff),
			DeltaClass: deltaClass(diff),
		})
	}

	// 最近几场：倒序列出，分差用这一场自己的 old→new。
	recent := picked
	if len(recent) > scoreRecentCount {
		recent = recent[len(recent)-scoreRecentCount:]
	}
	recentRows := make([]scoreTrendRecentRow, 0, len(recent))
	for i := len(recent) - 1; i >= 0; i-- {
		battle := recent[i]
		diff := battle.NewMasterMatchScore - battle.OldMasterMatchScore
		resultText, resultClass := trendResult(battle.Result)
		recentRows = append(recentRows, scoreTrendRecentRow{
			HeroName:    trendHeroName(heroMap, battle.HeroID),
			HeroIcon:    heroIconURL(battle.HeroID),
			ResultText:  resultText,
			ResultClass: resultClass,
			KDA:         kdaText(battle.Kill, battle.Dead, battle.Assist),
			Score:       battle.NewMasterMatchScore,
			DeltaText:   signed(diff),
			DeltaClass:  deltaClass(diff),
			TimeText:    trendMD(battle.Time) + " " + trendHM(battle.Time),
		})
	}

	// 带分英雄：按净分贡献排序，看清是谁在带分。
	// old 缺 key / 为 0 时退回 new（该场净分为 0），别把绝对巅峰分当净得分累加。
	type heroStat struct {
		id    string
		count int
		win   int
		net   int
	}
	statsByHero := map[string]*heroStat{}
	var heroOrder []*heroStat
	for _, battle := range picked {
		id := battle.HeroID
		if id == "" {
			continue
		}
		row := statsByHero[id]
		if row == nil {
			row = &heroStat{id: id}
			statsByHero[id] = row
			heroOrder = append(heroOrder, row)
		}
		row.count++
		if battle.Result == 1 {
			row.win++
		}
		old := battle.OldMasterMatchScore
		if old == 0 {
			old = battle.NewMasterMatchScore
		}
		row.net += battle.NewMasterMatchScore - old
	}
	sort.SliceStable(heroOrder, func(i, j int) bool {
		if heroOrder[i].net != heroOrder[j].net {
			return heroOrder[i].net > heroOrder[j].net
		}
		return heroOrder[i].count > heroOrder[j].count
	})
	var heroRows []scoreTrendHeroRow
	for index, row := range heroOrder {
		if index >= trendHeroTop {
			break
		}
		heroWinRate := 0
		if row.count > 0 {
			heroWinRate = int(math.Round(float64(row.win) / float64(row.count) * 100))
		}
		heroRows = append(heroRows, scoreTrendHeroRow{
			HeroName: trendHeroName(heroMap, row.id),
			HeroID:   row.id,
			HeroIcon: heroIconURL(row.id),
			Count:    row.count,
			WinRate:  heroWinRate,
			NetText:  signed(row.net),
			NetClass: deltaClass(row.net),
		})
	}

	spanDays := math.Max(1, math.Round(float64(last.Time-first.Time)/8640)/10)
	streakWin, streakLose := battleStreaks(picked)

	view := &scoreTrendView{
		Count:         len(picked),
		Win:           win,
		Lose:          lose,
		WinRate:       winRate,
		StartScore:    startScore,
		Current:       current,
		Peak:          peak,
		Low:           low,
		DeltaText:     signed(delta),
		DeltaClass:    deltaClass(delta),
		MaxWinStreak:  streakWin,
		MaxLoseStreak: streakLose,
		SpanDays:      spanDays,
		RangeText:     trendMD(first.Time) + " ~ " + trendMD(last.Time),
		CoverText:     coverText(spanDays, days, relaxed),
		Trend:         trend,
		DayRows:       dayRows,
		RecentRows:    recentRows,
		HeroRows:      heroRows,
	}
	if winRate >= 50 {
		view.WinRateClass = "good"
	} else {
		view.WinRateClass = "bad"
	}
	view.TrendJson = mustJSON(view.Trend)
	return view
}

/* ------------------------------------------------------------------ *
 * 段位趋势
 * ------------------------------------------------------------------ */

// rankBands 大段位层级表，低 → 高。测试顺序有讲究：
// 「荣耀黄金III」和「荣耀王者」都含「荣耀」，黄金必须排在王者前面先命中；
// 王者那条只认「王者」二字，把最强/非凡/绝世/至圣/荣耀/传奇王者一网打尽。
var rankBands = []struct {
	keywords []string
	band     int
	short    string
}{
	{[]string{"青铜"}, 1, "青铜"},
	{[]string{"白银"}, 2, "白银"},
	{[]string{"黄金"}, 3, "黄金"},
	{[]string{"铂金", "白金"}, 4, "铂金"},
	{[]string{"钻石"}, 5, "钻石"},
	{[]string{"星耀"}, 6, "星耀"},
	{[]string{"王者"}, 7, "王者"},
}

// rankSegOrder 小段罗马数字从低到高；各大段的小段数不同，见 rankSegCount。
var rankSegOrder = []string{"V", "IV", "III", "II", "I"}

// rankBase 各大段第一小段之前的累计星数（青铜III 0 星起算）。
var rankBase = map[int]int{1: 0, 2: 9, 3: 18, 4: 34, 5: 50, 6: 75, 7: 100}

// rankSegFull 每小段满星：青铜/白银 3 星、黄金/铂金 4 星、钻石/星耀 5 星。
var rankSegFull = map[int]int{1: 3, 2: 3, 3: 4, 4: 4, 5: 5, 6: 5}

// rankSegCount 各大段有几个小段：青铜/白银 III~I、黄金/铂金 IV~I、钻石/星耀 V~I。
var rankSegCount = map[int]int{1: 3, 2: 3, 3: 4, 4: 4, 5: 5, 6: 5}

var (
	rankSegRe  = regexp.MustCompile(`(IV|V|III|II|I)$`)
	rankTailRe = regexp.MustCompile(`(I{1,3}|IV|V)$`)
)

// rankBand 段位名 → 大段位层级；认不出来返回 0（参与排序时当最低，但照常画点）。
func rankBand(jobName string) int {
	name := strings.TrimSpace(jobName)
	for _, item := range rankBands {
		for _, keyword := range item.keywords {
			if strings.Contains(name, keyword) {
				return item.band
			}
		}
	}
	return 0
}

// rankBandShort 段位名 → 「星耀」这种两字简称，图上做分层标签用。
func rankBandShort(jobName string) string {
	name := strings.TrimSpace(jobName)
	for _, item := range rankBands {
		for _, keyword := range item.keywords {
			if strings.Contains(name, keyword) {
				return item.short
			}
		}
	}
	runes := []rune(name)
	if len(runes) > 2 {
		runes = runes[:2]
	}
	if len(runes) == 0 {
		return "未知"
	}
	return string(runes)
}

// rankSegLabel 段位名里的小段标签（「至尊星耀III」→「III」，「最强王者」→ 空串）。
func rankSegLabel(jobName string) string {
	matched := rankSegRe.FindStringSubmatch(strings.TrimSpace(jobName))
	if matched == nil {
		return ""
	}
	return matched[1]
}

// rankStarTotal 把「大段 + 小段 + 段内星」折算成从青铜III 0 星起算的连续刻度，
// 让跨段的净升星可以相减（星耀I 4星 → 王者1星 = 99 → 101 = +2 星）。
// 段位认不出或缺少小段时返回 false。
func rankStarTotal(jobName string, stars int) (int, bool) {
	band := rankBand(jobName)
	if band == 0 {
		return 0, false
	}
	if band == 7 {
		return rankBase[7] + stars, true
	}
	seg := rankSegLabel(jobName)
	if seg == "" {
		return 0, false
	}
	segIndex := -1
	for i, item := range rankSegOrder {
		if item == seg {
			segIndex = i
			break
		}
	}
	// 各段的小段数不同（青铜/白银从 III 起、钻石/星耀从 V 起），先对齐到「离本段底几格」。
	segIndex -= 5 - rankSegCount[band]
	if segIndex < 0 {
		return 0, false
	}
	return rankBase[band] + segIndex*rankSegFull[band] + stars, true
}

// rankPoint 是一场战绩里的段位快照。
// Value 是可比较的高低标量：大段位层级 → roleJob 编号 → stars 三级排序压成一个数
// （编号实测最大 26 占 1e4 档，星数王者段实测到 97 占 1e0 档）。
// Value 不能直接当画图的 y 值（跨大段差 1e7 会压成两条贴边直线），画图另用离散刻度。
type rankPoint struct {
	JobName string
	JobNum  int
	Stars   int
	Band    int
	Value   int64
	Time    int64
	Ranked  bool
	Win     bool
	HeroID  string
	Mode    string
	Result  int
	KDA     string
	Seg     int
}

func rankPointOf(battle archiveBattle) rankPoint {
	jobName := strings.TrimSpace(battle.RoleJobName)
	jobNum := battle.RoleJob
	band := rankBand(jobName)
	return rankPoint{
		JobName: jobName,
		JobNum:  jobNum,
		Stars:   battle.Stars,
		Band:    band,
		Value:   int64(band)*1e7 + int64(jobNum)*1e4 + int64(battle.Stars),
		Time:    battle.Time,
		Ranked:  isRankedBattle(battle),
		Win:     battle.Result == 1,
		HeroID:  battle.HeroID,
		Mode:    strings.TrimSpace(battle.MapName),
		Result:  battle.Result,
		KDA:     kdaText(battle.Kill, battle.Dead, battle.Assist),
	}
}

// isRankJump 相邻两点之间是不是「不可比」的跳变（切了游戏角色 / 赛季重置）：
//   - 大段跨 2 级以上：一晚上从钻石打到王者不可能；
//   - 同一大段内编号跳 rankJumpTolerance 以上（实测同号 12 ↔ 20 交替）；
//   - 大段降 1 级 + 编号反而大涨：赛季重置（荣耀王者 j16 → 至尊星耀 j22~24）。
//
// 「星耀 → 王者」是 band 6→7、差 1，不断开——那正是最该连起来报喜的一段。
// 编号只在两边都有值时才比：一边是 0 时硬减会把 0 ↔ 20 当成跳 20 段。
func isRankJump(prev, next rankPoint) bool {
	bandGap := next.Band - prev.Band
	if bandGap < 0 {
		bandGap = -bandGap
	}
	if bandGap >= 2 {
		return true
	}
	if bandGap == 0 {
		if prev.JobNum == 0 || next.JobNum == 0 {
			return false
		}
		diff := next.JobNum - prev.JobNum
		if diff < 0 {
			diff = -diff
		}
		return diff >= rankJumpTolerance
	}
	if prev.JobNum == 0 || next.JobNum == 0 {
		return false
	}
	return next.Band < prev.Band &&
		next.JobNum > prev.JobNum &&
		(next.JobNum-prev.JobNum) >= rankJumpTolerance
}

// describeDelta 区间首尾之间怎么描述这段变化，分三种口径：
//  1. 大段和小段都没变 → 说星数差（「+3 星」），最常见；
//  2. 同一大段内换了小段 → 说段数差（「+2 段」），升段星数会重置，相减没有意义；
//  3. 跨了大段 → 说大段名（「星耀 → 王者」），最该报喜的一句，别压成数字。
func describeDelta(first, last rankPoint) (text, class string) {
	if last.Band != first.Band {
		class = "down"
		if last.Band > first.Band {
			class = "up"
		}
		return rankBandShort(first.JobName) + " → " + rankBandShort(last.JobName), class
	}
	if last.JobNum != first.JobNum {
		diff := last.JobNum - first.JobNum
		class = "down"
		if diff > 0 {
			class = "up"
		}
		return signed(diff) + " 段", class
	}
	diff := last.Stars - first.Stars
	if diff == 0 {
		return "持平", "flat"
	}
	class = "down"
	if diff > 0 {
		class = "up"
	}
	return signed(diff) + " 星", class
}

// countSteps 升段 / 降段各发生了几次，以及涨星 / 掉星各几次；跳变处不算。
// 王者段必须看涨星掉星：band 恒 7、编号恒 16，段位全靠累积星数体现。
func countSteps(points []rankPoint) (up, down, starUp, starDown int) {
	for i := 1; i < len(points); i++ {
		prev, cur := points[i-1], points[i]
		if isRankJump(prev, cur) {
			continue
		}
		if cur.Value > prev.Value {
			starUp++
		} else if cur.Value < prev.Value {
			starDown++
		}
		if cur.Band != prev.Band {
			if cur.Band > prev.Band {
				up++
			} else {
				down++
			}
		} else if cur.JobNum != prev.JobNum {
			if cur.JobNum > prev.JobNum {
				up++
			} else {
				down++
			}
		}
	}
	return up, down, starUp, starDown
}

// pointStreaks 最长连胜 / 连败；只传排位局进来，娱乐局的胜负和段位无关。
func pointStreaks(points []rankPoint) (win, lose int) {
	curWin, curLose := 0, 0
	for _, point := range points {
		if point.Result == 1 {
			curWin++
		} else {
			curWin = 0
		}
		if point.Result == 2 {
			curLose++
		} else {
			curLose = 0
		}
		if curWin > win {
			win = curWin
		}
		if curLose > lose {
			lose = curLose
		}
	}
	return win, lose
}

// rankLabelText 「至尊星耀III 4星」这种完整标签。
func rankLabelText(point rankPoint) string {
	if point.JobName == "" {
		return "未知段位"
	}
	return fmt.Sprintf("%s %d星", point.JobName, point.Stars)
}

// shortRankLabel 「星耀III 4★」这种短标签，图上的 y 轴刻度用，太长会顶出去。
func shortRankLabel(point rankPoint) string {
	if point.JobName == "" {
		return "未知"
	}
	tail := ""
	if matched := rankTailRe.FindStringSubmatch(point.JobName); matched != nil {
		tail = matched[1]
	}
	return fmt.Sprintf("%s%s %d★", rankBandShort(point.JobName), tail, point.Stars)
}

type rankTrendJSONPoint struct {
	Level  int    `json:"level"`
	Time   int64  `json:"time"`
	Label  string `json:"label"`
	Full   string `json:"full"`
	Ranked bool   `json:"ranked"`
	Seg    int    `json:"seg"`
}

type rankLevelJSONPoint struct {
	Level int    `json:"level"`
	Label string `json:"label"`
}

type rankTrendDayRow struct {
	Date        string
	Count       int
	RankedCount int
	Win         int
	Lose        int
	Label       string
	DeltaText   string
	DeltaClass  string
}

type rankTrendRecentRow struct {
	HeroName    string
	HeroIcon    string
	ResultText  string
	ResultClass string
	KDA         string
	Mode        string
	Ranked      bool
	Label       string
	DeltaText   string
	DeltaClass  string
	TimeText    string
}

type rankTrendModeRow struct {
	Mode    string
	Count   int
	Win     int
	WinRate int
	Ranked  bool
}

// rankTrendView 是 RankTrend 模板的完整变量树（字段名对应上游模板变量）。
type rankTrendView struct {
	Title    string
	SubText  string
	Username string
	Avatar   string
	FootText string

	Count         int
	RankedCount   int
	Win           int
	Lose          int
	WinRate       float64
	WinRateClass  string
	StartLabel    string
	CurrentLabel  string
	CurrentShort  string
	PeakLabel     string
	LowLabel      string
	IsKing        bool
	StarText      string
	DeltaText     string
	DeltaClass    string
	UpSteps       int
	DownSteps     int
	StarUpCount   int
	StarDownCount int
	StepText      string
	StarStepText  string
	MaxWinStreak  int
	MaxLoseStreak int
	SpanDays      float64
	RangeText     string
	CoverText     string
	SegCount      int
	SegNote       string

	Trend      []rankTrendJSONPoint
	TrendJson  string
	Levels     []rankLevelJSONPoint
	LevelJson  string
	DayRows    []rankTrendDayRow
	RecentRows []rankTrendRecentRow
	ModeRows   []rankTrendModeRow
}

// pickRankBattles 挑出区间内的排位局，正序（旧 → 新）。
// 没有段位名 / 编号的场次扔掉——那种记录连大段都定不了。
func pickRankBattles(battles []archiveBattle, fromSec int64) []archiveBattle {
	var out []archiveBattle
	for _, battle := range battles {
		if !isRankedBattle(battle) || battle.Time < fromSec {
			continue
		}
		if strings.TrimSpace(battle.RoleJobName) == "" || battle.RoleJob <= 0 {
			continue
		}
		out = append(out, battle)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

// pickRankWindow 取「要画的那一窗排位局」：窗口内不足 target 场放宽到全库最近 target 场。
func pickRankWindow(battles []archiveBattle, fromSec int64) (list []archiveBattle, relaxed bool) {
	inRange := pickRankBattles(battles, fromSec)
	if len(inRange) >= trendTargetPoints {
		return inRange, false
	}
	all := pickRankBattles(battles, 0)
	if len(all) <= len(inRange) {
		return inRange, false
	}
	if len(all) > trendTargetPoints {
		all = all[len(all)-trendTargetPoints:]
	}
	return all, true
}

// buildRankTrendView 把段位快照序列整理成模板数据；少于 2 场返回 nil。
//
// y 轴用「离散刻度」而不是 rankPoint.Value：Value 跨大段差 1e7，一张图里混了
// 星耀和王者就压成贴着上下边的两条直线。取区间内出现过的所有 Value 去重升序，
// 索引即 y 值，一格一档；精确幅度看文案和逐日行，图只负责表达走向。
func buildRankTrendView(picked []archiveBattle, heroMap map[string]string, days int, relaxed bool) *rankTrendView {
	if len(picked) < 2 {
		return nil
	}
	all := make([]rankPoint, 0, len(picked))
	for _, battle := range picked {
		all = append(all, rankPointOf(battle))
	}
	// 进来的已经全是排位局（pickRankBattles 过滤过），留一道过滤是让
	// 「只有排位改变段位」这个前提在数据层写明。
	var rankedOnly []rankPoint
	for _, point := range all {
		if point.Ranked {
			rankedOnly = append(rankedOnly, point)
		}
	}

	// 先给每一场标上「属于第几段折线」，跳变处开新段。
	// 抽稀之后再算段号不行：抽稀可能正好丢掉跳变的那两场之一，跳变就凭空消失了。
	segIndex := 0
	for i := range all {
		if i > 0 && isRankJump(all[i-1], all[i]) {
			segIndex++
		}
		all[i].Seg = segIndex
	}
	segCount := segIndex + 1
	// 段位高低类统计（区间变化 / 最高 / 最低）只看最后一段：跨角色比高低会得出荒谬结论。
	// 场次、胜率、逐日这些「这个营地号打了多少」的统计仍然按全区间算。
	var tail []rankPoint
	for _, point := range all {
		if point.Seg == segIndex {
			tail = append(tail, point)
		}
	}

	first := all[0]
	last := all[len(all)-1]
	highest, lowest := tail[0], tail[0]
	for _, point := range tail[1:] {
		if point.Value > highest.Value {
			highest = point
		}
		if point.Value < lowest.Value {
			lowest = point
		}
	}
	deltaText, deltaCls := describeDelta(tail[0], last)
	upSteps, downSteps, starUp, starDown := countSteps(rankedOnly)

	// 折线点。抽稀后再建刻度表，刻度就只覆盖真的画出来的档位，不留空排。
	shown := thinSlice(all, trendMaxPoints)
	levelValues := make([]int64, 0, len(shown))
	labelOfValue := map[int64]string{}
	seenValue := map[int64]bool{}
	for _, point := range shown {
		if !seenValue[point.Value] {
			seenValue[point.Value] = true
			levelValues = append(levelValues, point.Value)
		}
		if _, ok := labelOfValue[point.Value]; !ok {
			labelOfValue[point.Value] = shortRankLabel(point)
		}
	}
	sort.Slice(levelValues, func(i, j int) bool { return levelValues[i] < levelValues[j] })
	levelOfValue := make(map[int64]int, len(levelValues))
	for index, value := range levelValues {
		levelOfValue[value] = index
	}

	trend := make([]rankTrendJSONPoint, 0, len(shown))
	for _, point := range shown {
		trend = append(trend, rankTrendJSONPoint{
			Level:  levelOfValue[point.Value],
			Time:   point.Time,
			Label:  shortRankLabel(point),
			Full:   rankLabelText(point),
			Ranked: point.Ranked,
			Seg:    point.Seg,
		})
	}

	// y 轴刻度：档位可能有十几档，全标会糊成一片，等距挑最多 5 档（首尾必留）。
	levelItems := make([]rankLevelJSONPoint, 0, len(levelValues))
	for index, value := range levelValues {
		levelItems = append(levelItems, rankLevelJSONPoint{Level: index, Label: labelOfValue[value]})
	}
	levels := thinSlice(levelItems, 5)

	// 逐日战况：一天一行。段位取当天最后一场的快照，变化和前一天的收盘比。
	type dayGroup struct {
		key   string
		items []rankPoint
	}
	groupsByDay := map[string]*dayGroup{}
	var groups []*dayGroup
	for _, point := range all {
		key := trendDayKey(point.Time)
		group := groupsByDay[key]
		if group == nil {
			group = &dayGroup{key: key}
			groupsByDay[key] = group
			groups = append(groups, group)
		}
		group.items = append(group.items, point)
	}
	dayRows := make([]rankTrendDayRow, 0, len(groups))
	for i := len(groups) - 1; i >= 0; i-- {
		list := groups[i].items
		dayEnd := list[len(list)-1]
		// 前一天的收盘快照；第一天没有前一天可比，就用当天第一场的快照当起点。
		base := list[0]
		if i > 0 {
			prevList := groups[i-1].items
			base = prevList[len(prevList)-1]
		}
		// 跨了角色切换就别给数字了，「钻石 +8 段」这种结论比不给更糟。
		diffText, diffCls := "—", "flat"
		if !isRankJump(base, dayEnd) {
			diffText, diffCls = describeDelta(base, dayEnd)
		}
		rankedCount, dayWin, dayLose := 0, 0, 0
		for _, point := range list {
			if !point.Ranked {
				continue
			}
			rankedCount++
			switch point.Result {
			case 1:
				dayWin++
			case 2:
				dayLose++
			}
		}
		dayRows = append(dayRows, rankTrendDayRow{
			Date:        dayLabel(groups[i].key),
			Count:       len(list),
			RankedCount: rankedCount,
			Win:         dayWin,
			Lose:        dayLose,
			Label:       shortRankLabel(dayEnd),
			DeltaText:   diffText,
			DeltaClass:  diffCls,
		})
	}

	// 最近几场：倒序列出，变化是这一场自己造成的（和上一场的快照比）。
	// 段位快照是「打这局时的段位」而不是这局的结果，所以严格说这里的差值是
	// 「上一场之后到这一场之前」的变化，对连续对局来说就是上一场的战果。
	recentStart := 0
	if len(all) > rankRecentCount {
		recentStart = len(all) - rankRecentCount
	}
	recentRows := make([]rankTrendRecentRow, 0, len(all)-recentStart)
	for i := len(all) - 1; i >= recentStart; i-- {
		point := all[i]
		diffText, diffCls := "—", "flat"
		if i > 0 && !isRankJump(all[i-1], point) {
			diffText, diffCls = describeDelta(all[i-1], point)
		}
		mode := point.Mode
		if mode == "" {
			mode = "对局"
		}
		resultText, resultClass := trendResult(point.Result)
		recentRows = append(recentRows, rankTrendRecentRow{
			HeroName:    trendHeroName(heroMap, point.HeroID),
			HeroIcon:    heroIconURL(point.HeroID),
			ResultText:  resultText,
			ResultClass: resultClass,
			KDA:         point.KDA,
			Mode:        html.EscapeString(mode),
			Ranked:      point.Ranked,
			Label:       shortRankLabel(point),
			DeltaText:   diffText,
			DeltaClass:  diffCls,
			TimeText:    trendMD(point.Time) + " " + trendHM(point.Time),
		})
	}

	// 模式分布：只取排位局之后这里剩的是「排位赛 单排 / 双排 / 五排」这类开黑形态。
	type modeStat struct {
		mode   string
		count  int
		win    int
		ranked bool
	}
	statsByMode := map[string]*modeStat{}
	var modeOrder []*modeStat
	for _, point := range all {
		key := point.Mode
		if key == "" {
			key = "其它"
		}
		row := statsByMode[key]
		if row == nil {
			row = &modeStat{mode: key, ranked: point.Ranked}
			statsByMode[key] = row
			modeOrder = append(modeOrder, row)
		}
		row.count++
		if point.Result == 1 {
			row.win++
		}
	}
	sort.SliceStable(modeOrder, func(i, j int) bool { return modeOrder[i].count > modeOrder[j].count })
	modeRows := make([]rankTrendModeRow, 0, len(modeOrder))
	for _, row := range modeOrder {
		modeWinRate := 0
		if row.count > 0 {
			modeWinRate = int(math.Round(float64(row.win) / float64(row.count) * 100))
		}
		modeRows = append(modeRows, rankTrendModeRow{
			Mode:    html.EscapeString(row.mode),
			Count:   row.count,
			Win:     row.win,
			WinRate: modeWinRate,
			Ranked:  row.ranked,
		})
	}

	rankedWin, rankedLose := 0, 0
	for _, point := range rankedOnly {
		switch point.Result {
		case 1:
			rankedWin++
		case 2:
			rankedLose++
		}
	}
	played := rankedWin + rankedLose
	winRate := 0.0
	if played > 0 {
		winRate = math.Round(float64(rankedWin)/float64(played)*1000) / 10
	}
	streakWin, streakLose := pointStreaks(rankedOnly)
	spanDays := math.Max(1, math.Round(float64(last.Time-first.Time)/8640)/10)

	// 王者段没有小段可升，升降段恒 0，展示时改用涨星掉星。
	stepText := "未升降段"
	if upSteps != 0 || downSteps != 0 {
		stepText = fmt.Sprintf("升 %d / 降 %d 段", upSteps, downSteps)
	}
	// 断点如实交代：多半是同一个营地账号下切了游戏角色，不说清楚用户会以为图画错了。
	segNote := ""
	if segCount > 1 {
		segNote = fmt.Sprintf("检测到 %d 处段位跳变，已断成 %d 段（多为切换游戏角色或赛季重置）；区间变化与最高最低只统计最后一段", segCount-1, segCount)
	}

	view := &rankTrendView{
		Count:         len(all),
		RankedCount:   len(rankedOnly),
		Win:           rankedWin,
		Lose:          rankedLose,
		WinRate:       winRate,
		StartLabel:    rankLabelText(tail[0]),
		CurrentLabel:  rankLabelText(last),
		CurrentShort:  shortRankLabel(last),
		PeakLabel:     rankLabelText(highest),
		LowLabel:      rankLabelText(lowest),
		IsKing:        rankBand(last.JobName) == 7,
		StarText:      fmt.Sprintf("%d 星", last.Stars),
		DeltaText:     deltaText,
		DeltaClass:    deltaCls,
		UpSteps:       upSteps,
		DownSteps:     downSteps,
		StarUpCount:   starUp,
		StarDownCount: starDown,
		StepText:      stepText,
		StarStepText:  fmt.Sprintf("+%d / -%d", starUp, starDown),
		MaxWinStreak:  streakWin,
		MaxLoseStreak: streakLose,
		SpanDays:      spanDays,
		RangeText:     trendMD(first.Time) + " ~ " + trendMD(last.Time),
		CoverText:     coverText(spanDays, days, relaxed),
		SegCount:      segCount,
		SegNote:       segNote,
		Trend:         trend,
		Levels:        levels,
		DayRows:       dayRows,
		RecentRows:    recentRows,
		ModeRows:      modeRows,
	}
	if winRate >= 50 {
		view.WinRateClass = "good"
	} else {
		view.WinRateClass = "bad"
	}
	view.TrendJson = mustJSON(view.Trend)
	view.LevelJson = mustJSON(view.Levels)
	return view
}

/* ------------------------------------------------------------------ *
 * 指令入口
 * ------------------------------------------------------------------ */

func scoreTrendCommand(ctx context.Context, c sdk.Call, rest string) error {
	args := parseTrendArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID, err := resolveTrendCamp(state, args)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	days := trendWindowDays(args.days)
	fromSec := time.Now().Unix() - int64(days)*86400

	battles := loadCampArchive(ctx, c.State, campID).Battles
	picked, relaxed := pickPeakWindow(battles, fromSec)
	// 库里凑不够 10 场巅峰：现拉几页补上。补拉下限用整个归档保留期而不是 days
	// ——要凑的是场次，天数窗口不够就往更早翻。失败沿用旧库（上游只记 debug）。
	if len(picked) < trendTargetPoints {
		if result, err := collectBattles(ctx, c.State, campID, auth, time.Now().Unix()-int64(archiveKeepDays)*86400, 8); err == nil && result != nil {
			battles = result.Battles
			picked, relaxed = pickPeakWindow(battles, fromSec)
		}
	}
	if len(picked) < 2 {
		return c.Reply(ctx, scoreTrendEmptyText(len(picked), days, relaxed))
	}

	view := buildScoreTrendView(picked, heroNameMap(ctx), days, relaxed)
	if view == nil {
		return c.Reply(ctx, "巅峰赛场次太少，攒几天再看吧")
	}
	view.Title = "巅峰趋势"
	// 放宽过窗口就别再写「近 N 天」了，那是假的。
	if relaxed {
		view.SubText = fmt.Sprintf("最近 %d 场巅峰", len(picked))
	} else {
		view.SubText = fmt.Sprintf("近 %d 天", days)
	}
	view.Username, view.Avatar = trendHeader(c, state, campID)
	// 归档只留 35 天，且只覆盖轮询跑过的那段，别让人以为是全赛季。
	view.FootText = fmt.Sprintf("数据取自本地战绩存档（最多 %d 天）· 只统计巅峰赛", archiveKeepDays)

	image, err := renderScoreTrend(ctx, *view)
	if err != nil {
		return c.Reply(ctx, "趋势图渲染失败了，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

func rankTrendCommand(ctx context.Context, c sdk.Call, rest string) error {
	args := parseTrendArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID, err := resolveTrendCamp(state, args)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	days := trendWindowDays(args.days)
	fromSec := time.Now().Unix() - int64(days)*86400

	battles := loadCampArchive(ctx, c.State, campID).Battles
	picked, relaxed := pickRankWindow(battles, fromSec)
	// 排位局在战绩里本来就只占一部分（实测 48 场里 15 场），页数给到 8 才有把握凑够。
	if len(picked) < trendTargetPoints {
		if result, err := collectBattles(ctx, c.State, campID, auth, time.Now().Unix()-int64(archiveKeepDays)*86400, 8); err == nil && result != nil {
			battles = result.Battles
			picked, relaxed = pickRankWindow(battles, fromSec)
		}
	}
	if len(picked) < 2 {
		return c.Reply(ctx, rankTrendEmptyText(len(picked), days, relaxed))
	}

	view := buildRankTrendView(picked, heroNameMap(ctx), days, relaxed)
	if view == nil {
		return c.Reply(ctx, "排位赛场次太少，攒几天再看吧")
	}
	view.Title = "段位趋势"
	if relaxed {
		view.SubText = fmt.Sprintf("最近 %d 场排位", len(picked))
	} else {
		view.SubText = fmt.Sprintf("近 %d 天", days)
	}
	view.Username, view.Avatar = trendHeader(c, state, campID)
	view.FootText = fmt.Sprintf("数据取自本地战绩存档（最多 %d 天）· 只统计排位赛，巅峰赛与娱乐局不计入", archiveKeepDays)

	image, err := renderRankTrend(ctx, *view)
	if err != nil {
		return c.Reply(ctx, "趋势图渲染失败了，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

// scoreTrendEmptyText 数据画不出图时的说明文案（上游原文）。
// relaxed=true 表示已经翻到归档最早，再说「近 N 天」与数据矛盾，
// 而「试试更长的区间」必然无效，两种口径必须分开。
func scoreTrendEmptyText(count, days int, relaxed bool) string {
	where := fmt.Sprintf("近 %d 天", days)
	if relaxed {
		where = "本地存档里"
	}
	text := fmt.Sprintf("%s只找到 %d 场巅峰赛，画不出趋势。\n", where, count) +
		"· 排位赛不算：排位不影响巅峰分\n"
	if relaxed {
		text += "· 已经翻到存档最早，攒几天再来\n"
	} else {
		text += fmt.Sprintf("· 试试更长的区间，比如 #巅峰趋势 %d\n", min(days*2, archiveKeepDays))
	}
	return text + "· 开了 #开启战绩推送 之后每天的对局会自动存档，往后趋势会越来越全"
}

func rankTrendEmptyText(count, days int, relaxed bool) string {
	where := fmt.Sprintf("近 %d 天", days)
	if relaxed {
		where = "本地存档里"
	}
	text := fmt.Sprintf("%s只找到 %d 场排位赛，画不出趋势。\n", where, count) +
		"· 巅峰赛和娱乐局不算：只有排位赛会改变段位\n"
	if relaxed {
		text += "· 已经翻到存档最早，攒几天再来\n"
	} else {
		text += fmt.Sprintf("· 试试更长的区间，比如 #段位趋势 %d\n", min(days*2, archiveKeepDays))
	}
	return text + "· 开了 #开启战绩推送 之后每天的对局会自动存档，往后趋势会越来越全"
}

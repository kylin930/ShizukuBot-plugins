package gloryofkings

// 赛季表现 / 巅峰表现 / 全部赛季表现的视图构建。
// 数据口径与上游 apps/seasonPage.js、peakPerformance.js、allSeasonPerformance.js 对齐。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

// 分路颜色，顺序与上游一致。
var rankBranchColors = []string{"#f5d76e", "#f0932b", "#6ab0f5", "#57c98a", "#c97bdb"}
var masterBranchColors = []string{"#f5d76e", "#f0932b", "#6ab0f5", "#57c98a", "#c97bdb", "#e0708a"}

// branchTypeNames 是 getFightData branchType 的分路名（0=全部）。
var branchTypeNames = map[int]string{1: "对抗路", 2: "中路", 3: "发育路", 4: "打野", 5: "游走"}

const radarMax = 12000.0 // 五维满分（万分制），pct = min(value/12000, 1)

// perfArgs 是赛季类指令的参数解析结果（上游 parsePerfArgs 口径）。
type perfArgs struct {
	season int    // s40 / 40 → 40
	campID string // ≥5 位数字
	count  int    // ≤4 位数字
	all    bool   // all / 全部
}

// perfSeasonRe 匹配赛季号 s40 / S40，且允许和营地号连写（#排位表现1580886057s40）。
// Go 的 regexp 不支持 (?!\d)，改成先匹配再人工检查后面不是数字，
// 避免把 s1580 之类更长的数字误当赛季号。
var perfSeasonRe = regexp.MustCompile(`[sS]\d{1,3}`)

// extractSeason 从整串中抽出第一个完整的赛季号，返回赛季号与「挖掉它」的剩余串。
func extractSeason(input string) (season int, rest string) {
	for _, loc := range perfSeasonRe.FindAllStringIndex(input, -1) {
		end := loc[1]
		if end < len(input) && input[end] >= '0' && input[end] <= '9' {
			continue
		}
		season, _ = strconv.Atoi(input[loc[0]+1 : end])
		rest = input[:loc[0]] + " " + input[end:]
		return season, rest
	}
	return 0, input
}

// parsePerfArgs 拆分指令尾部：s 开头的是赛季号，5 位以上纯数字是营地号，
// 4 位以内纯数字是数量（只取第一个，也兜底当赛季号），all/全部 单独识别。
// 口径与上游 utils/index.js:176 parsePerfArgs 一致。
func parsePerfArgs(input string) perfArgs {
	var args perfArgs
	countSet := false
	rest := strings.TrimSpace(input)
	if rest == "" {
		return args
	}
	// 先在整串里抽赛季号，允许与营地号连写，抽走后剩余部分再按分隔符切。
	args.season, rest = extractSeason(rest)
	for _, token := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == ' ' || r == '，' || r == ',' || r == '、'
	}) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		lower := strings.ToLower(token)
		if lower == "all" || token == "全部" {
			args.all = true
			continue
		}
		if digitsRe.MatchString(token) {
			switch {
			case len(token) >= 5:
				args.campID = token
			case !countSet:
				args.count, _ = strconv.Atoi(token)
				countSet = true
			}
		}
	}
	return args
}

// seasonNumber 从 "S44" 提取 44。
func seasonNumber(name string) int {
	var digits strings.Builder
	for _, r := range name {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	value, _ := strconv.Atoi(digits.String())
	return value
}

type honorItem struct {
	Val string
	Key string
}

type seasonHeroView struct {
	HeroName string
	HeroIcon string
	WinRate  string
	GameCnt  string
}

type seasonBranchView struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	// GameCnt 必须输出成数值：模板环形图用 b.gameCnt 求和与除算，
	// 若序列化成字符串，JS 的 + 会退化成字符串拼接、除法得到 NaN，环形图整块空白。
	GameCnt int    `json:"gameCnt"`
	WinNum  string `json:"winNum"`
	LoseNum string `json:"loseNum"`
	WinRate string `json:"winRate"`
}

type radarItem struct {
	Label string  `json:"label"`
	Value string  `json:"value"`
	Pct   float64 `json:"pct"`
}

type laneView struct {
	Type     int         `json:"type"`
	Name     string      `json:"name"`
	Color    string      `json:"color"`
	GameCnt  string      `json:"gameCnt"`
	WinRate  string      `json:"winRate"`
	AvgScore string      `json:"avgScore"`
	Radar    []radarItem `json:"radar"`
}

type seasonTrendView struct {
	Star     int    `json:"star"`
	Stars    int    `json:"stars"`
	JobName  string `json:"jobName"`
	JobColor string `json:"jobColor"`
	Time     int64  `json:"time"`
}

// seasonPageView 是 SeasonPage.html 的完整模板变量树。
type seasonPageView struct {
	RoleName   string
	RoleIcon   string
	ServerName string
	SeasonName string
	TitleLabel string
	SubLabel   string

	JobName     string
	JobLabel    string
	RankingStar string
	MasterScore string
	MasterRank  string
	Score       string
	WinRate     string
	GameCnt     string
	Branch      string
	RankTag     string

	Heros      []seasonHeroView
	TotalGames string

	Branches       []seasonBranchView
	BranchesJson   string
	Radar          []radarItem
	RadarJson      string
	HasBattleStats bool

	Trend     []seasonTrendView
	TrendJson string

	Lanes     []laneView
	LanesJson string
	HasLanes  bool

	Honor    []honorItem
	HasHonor bool

	HasMaster          bool
	MasterStats        []honorItem
	MasterHeros        []seasonHeroView
	MasterBranches     []seasonBranchView
	MasterBranchesJson string
	MasterTotalGames   string
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// pickSeason 在 historyList 里按赛季号匹配；wantSeason=0 取最新赛季。
func pickSeason(history []seasonHistory, wantSeason int) (*seasonHistory, int) {
	if len(history) == 0 {
		return nil, -1
	}
	if wantSeason <= 0 {
		return &history[0], 0
	}
	for index := range history {
		if seasonNumber(history[index].SeasonName) == wantSeason {
			return &history[index], index
		}
	}
	return nil, -1
}

func buildSeasonHeroViews(heros []seasonHero, limit int) []seasonHeroView {
	var views []seasonHeroView
	for index, hero := range heros {
		if index >= limit {
			break
		}
		views = append(views, seasonHeroView{
			HeroName: html.EscapeString(hero.HeroName),
			HeroIcon: html.EscapeString(hero.HeroIcon),
			WinRate:  percentText(hero.WinRate.Float(), 0),
			GameCnt:  hero.GameCnt.String(),
		})
	}
	return views
}

// percentText 把 0~1 小数格式化成百分数；本身已是百分数（>1）时直接取整。
func percentText(value float64, digits int) string {
	if value <= 1 && value >= 0 {
		value *= 100
	}
	if digits == 0 {
		return fmt.Sprintf("%.0f%%", value)
	}
	return fmt.Sprintf("%.*f%%", digits, value)
}

// branchWinRate 优先用接口给的 winRate 字符串，缺失时用胜负场数算。
func branchWinRate(branch seasonBranch) string {
	rate := strings.TrimSpace(branch.WinRate.String())
	if rate != "" {
		if !strings.HasSuffix(rate, "%") {
			if value, err := strconv.ParseFloat(rate, 64); err == nil {
				return percentText(value, 0)
			}
		}
		return html.EscapeString(rate)
	}
	win, lose := branch.WinNum.Int(), branch.LoseNum.Int()
	if win+lose == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(win)*100/float64(win+lose))
}

func buildSeasonBranchViews(branches []seasonBranch, colors []string, filterEmpty bool) []seasonBranchView {
	var views []seasonBranchView
	colorIndex := 0
	for _, branch := range branches {
		gameCnt := branch.GameCnt.Int()
		if gameCnt == 0 {
			gameCnt = branch.WinNum.Int() + branch.LoseNum.Int()
		}
		if filterEmpty && gameCnt == 0 {
			continue
		}
		color := colors[colorIndex%len(colors)]
		colorIndex++
		views = append(views, seasonBranchView{
			Name:    html.EscapeString(firstNonEmpty(branch.BranchName, branchTypeNames[branch.BranchType.Int()])),
			Color:   color,
			GameCnt: gameCnt,
			WinNum:  branch.WinNum.String(),
			LoseNum: branch.LoseNum.String(),
			WinRate: branchWinRate(branch),
		})
	}
	return views
}

func buildRadarItems(stats *battleStats) []radarItem {
	if stats == nil {
		return nil
	}
	entries := []struct {
		label string
		value scalarValue
	}{
		{"输出", stats.HurtHero},
		{"生存", stats.Survive},
		{"团战", stats.Battle},
		{"发育", stats.Grow},
		{"KDA", stats.Kda},
	}
	items := make([]radarItem, 0, len(entries))
	for _, entry := range entries {
		value := entry.value.Float()
		pct := value / radarMax
		if pct > 1 {
			pct = 1
		}
		items = append(items, radarItem{Label: entry.label, Value: entry.value.String(), Pct: pct})
	}
	return items
}

func hasRadarValue(items []radarItem) bool {
	for _, item := range items {
		if item.Pct > 0 {
			return true
		}
	}
	return false
}

// filterGameTrend 按赛季窗口过滤跨赛季快照，映射成模板点（star=totalRankStar+stars）。
func filterGameTrend(points []gameTrendPoint, start, end int64) []seasonTrendView {
	ordered := make([]gameTrendPoint, len(points))
	copy(ordered, points)
	// 上游先 reverse：接口返回是倒序，这里统一按时间正序。
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time.Int() < ordered[j].Time.Int() })
	var views []seasonTrendView
	for _, point := range ordered {
		timestamp := int64(point.Time.Int())
		if start > 0 && end > 0 && (timestamp < start || timestamp > end) {
			continue
		}
		color := point.JobColor
		if color == "" {
			color = "#f5d76e"
		}
		views = append(views, seasonTrendView{
			Star:     point.TotalRankStar.Int() + point.Stars.Int(),
			Stars:    point.Stars.Int(),
			JobName:  point.JobName,
			JobColor: color,
			Time:     timestamp,
		})
	}
	return views
}

// buildSeasonPageView 组装 #排位表现 / #赛季表现 的模板数据。
// detail 是 getSeasonpage(roleId, target.seasonId) 的响应；summary 是 seasonId=0 的响应（取 historyList）。
func buildSeasonPageView(summary, detail *seasonPageData, target *seasonHistory, isCurrent bool, mode string, lanes []laneView) seasonPageView {
	head := detail.HeadCard
	view := seasonPageView{
		RoleName:    html.EscapeString(head.RoleName),
		RoleIcon:    html.EscapeString(head.RoleIcon),
		ServerName:  html.EscapeString(head.ServerName),
		SeasonName:  html.EscapeString(target.SeasonName),
		JobName:     html.EscapeString(head.JobName),
		RankingStar: head.RankingStar.String(),
		MasterScore: head.MasterScore.String(),
		MasterRank:  head.MasterRank.String(),
		Score:       head.Score.String(),
		Branch:      html.EscapeString(head.Branch),
	}
	if mode == "赛季" {
		view.TitleLabel = "赛季表现"
		view.RankTag = "排位 · "
	} else {
		view.TitleLabel = "排位表现"
	}
	if isCurrent {
		view.JobLabel = "当前段位"
	} else {
		view.JobLabel = "赛季最终段位"
		view.SubLabel = "历史赛季 · 无五维与上分趋势数据"
	}
	if head.WinRate.Float() > 0 {
		view.WinRate = percentText(head.WinRate.Float(), 0)
	}
	view.GameCnt = head.GameCnt.String()

	rankInfo := detail.Behavior.RankInfo
	// 历史赛季 behavior 为空时，heros/branches 回落到 historyList 里的快照。
	heros := rankInfo.Heros
	if len(heros) == 0 {
		heros = target.RankInfo.Heros
	}
	view.Heros = buildSeasonHeroViews(heros, 3)

	branches := rankInfo.Branches
	if len(branches) == 0 {
		branches = target.RankInfo.Branches
	}
	view.Branches = buildSeasonBranchViews(branches, rankBranchColors, true)
	view.BranchesJson = mustJSON(view.Branches)
	if view.WinRate == "" && len(view.Branches) > 0 {
		win, total := 0, 0
		for _, branch := range branches {
			win += branch.WinNum.Int()
			total += branch.WinNum.Int() + branch.LoseNum.Int()
		}
		if total > 0 {
			view.WinRate = fmt.Sprintf("%.0f%%", float64(win)*100/float64(total))
		}
	}

	view.TotalGames = head.GameCnt.String()
	if view.TotalGames == "" || view.TotalGames == "0" {
		total := 0
		for _, branch := range view.Branches {
			total += branch.GameCnt
		}
		if total > 0 {
			view.TotalGames = strconv.Itoa(total)
		}
	}

	view.Radar = buildRadarItems(detail.BattleStats)
	view.RadarJson = mustJSON(view.Radar)
	view.HasBattleStats = hasRadarValue(view.Radar)

	if isCurrent {
		view.Trend = filterGameTrend(rankInfo.GameTrend, int64(target.StartTime.Int()), int64(target.EndTime.Int()))
	}
	view.TrendJson = mustJSON(view.Trend)

	if len(lanes) > 0 {
		view.Lanes = lanes
		view.LanesJson = mustJSON(lanes)
		view.HasLanes = true
	}

	if isCurrent && detail.BattleStats != nil {
		stats := detail.BattleStats
		view.Honor = []honorItem{
			{stats.Mvp.String(), "全场最佳"},
			{stats.LoseMvp.String(), "败方最佳"},
			{stats.ThreeKill.String(), "三杀"},
			{stats.FourKill.String(), "四杀"},
			{stats.FiveKill.String(), "五杀"},
			{stats.GodLike.String(), "超神"},
		}
	} else {
		rank := target.RankInfo
		view.Honor = []honorItem{
			{rank.TotalWinCnt.String(), "胜场"},
			{strconv.Itoa(rank.TotalCnt.Int() - rank.TotalWinCnt.Int()), "负场"},
			{rank.MaxContinuousWinCnt.String(), "最高连胜"},
			{rank.GoldCnt.String(), "金牌"},
			{rank.SilverCnt.String(), "银牌"},
		}
	}
	view.HasHonor = len(view.Honor) > 0

	if mode == "赛季" {
		buildMasterPart(&view, summary, detail, target)
	}
	return view
}

// buildMasterPart 拼赛季表现的巅峰半区（#赛季表现 专属，不额外请求）。
func buildMasterPart(view *seasonPageView, summary, detail *seasonPageData, target *seasonHistory) {
	master := detail.Behavior.MasterInfo
	games := target.MasterInfo.TotalCnt.Int()
	if games == 0 {
		games = master.TotalGameCnt.Int()
	}
	if games == 0 {
		return
	}
	view.HasMaster = true
	view.MasterTotalGames = strconv.Itoa(games)

	score := view.MasterScore
	if score == "" || score == "0" {
		score = target.MasterInfo.MasterScore.String()
	}
	wins := target.MasterInfo.TotalWinCnt.Int()
	winRate := ""
	if target.MasterInfo.WinRate.Float() > 0 {
		winRate = percentText(target.MasterInfo.WinRate.Float(), 0)
	} else if games > 0 {
		winRate = fmt.Sprintf("%.0f%%", float64(wins)*100/float64(games))
	}
	avgScore := target.MasterInfo.AverageScore.String()
	avgLabel := "平均得分"
	if target.MasterInfo.AverageScore.Float() > 0 && target.MasterInfo.AverageScore.Float() <= 20 {
		avgLabel = "平均分数 · 旧10分制"
	}
	view.MasterStats = []honorItem{
		{score, "巅峰赛积分"},
		{strconv.Itoa(games), "巅峰赛场次"},
		{winRate, "巅峰赛胜率"},
		{avgScore, avgLabel},
		{target.MasterInfo.MaxContinuousWinCnt.String(), "最高连胜"},
	}

	heros := master.Heros
	if len(heros) == 0 {
		heros = target.MasterInfo.Heros
	}
	view.MasterHeros = buildSeasonHeroViews(heros, 3)

	branches := append([]seasonBranch(nil), master.Branches...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].GameCnt.Int() > branches[j].GameCnt.Int() })
	view.MasterBranches = buildSeasonBranchViews(branches, masterBranchColors, true)
	view.MasterBranchesJson = mustJSON(view.MasterBranches)
}

/* ------------------------------------------------------------------ *
 * 巅峰表现
 * ------------------------------------------------------------------ */

type peakStatView struct {
	Stats           []honorItem
	GameCnt         string
	WinRate         string
	WinNum          string
	LoseNum         string
	AvgScore        string
	Branch          string
	Honor           []honorItem
	Branches        []seasonBranchView
	Lanes           []laneView
	HasOverallRadar bool
	Overall         *laneView
	Heros           []seasonHeroView
}

type peakPageView struct {
	RoleName    string
	RoleIcon    string
	ServerName  string
	SeasonLabel string
	SubLabel    string
	TrendNote   string

	GameCnt  string
	WinRate  string
	WinNum   string
	LoseNum  string
	AvgScore string
	Branch   string
	Stats    []honorItem

	Honor    []honorItem
	HasHonor bool

	Branches     []seasonBranchView
	BranchesJson string
	HasBranches  bool

	Lanes     []laneView
	LanesJson string
	HasLanes  bool

	HasOverallRadar bool
	OverallJson     string

	Trend     []peakTrendPoint
	TrendJson string

	Heros []seasonHeroView
}

type peakTrendPoint struct {
	Score    int    `json:"score"`
	Time     int64  `json:"time"`
	JobName  string `json:"jobName,omitempty"`
	JobColor string `json:"jobColor,omitempty"`
}

const peakBranchRadarOrder = "输出,KDA,发育,团战,生存"

// buildLaneFromFight 把 getFightData 的一路响应组装成分路卡片。
// order 为五维展示顺序：排位表现用「输出/生存/团战/发育/KDA」，巅峰表现用「输出/KDA/发育/团战/生存」。
func buildLaneFromFight(branchType int, data *fightDataSelf, peakOrder bool) *laneView {
	gameCnt := data.WinNum.Int() + data.LoseNum.Int()
	lane := &laneView{
		Type:     branchType,
		Name:     branchTypeNames[branchType],
		GameCnt:  strconv.Itoa(gameCnt),
		WinRate:  branchWinRate(seasonBranch{WinNum: data.WinNum, LoseNum: data.LoseNum, WinRate: data.WinRate}),
		AvgScore: data.AvgScore.String(),
	}
	if branchType > 0 && branchType <= len(rankBranchColors) {
		lane.Color = rankBranchColors[branchType-1]
	} else {
		lane.Color = rankBranchColors[0]
	}
	type entry struct {
		label string
		value scalarValue
	}
	var entries []entry
	if peakOrder {
		entries = []entry{{"输出", data.HurtHero}, {"KDA", data.Kda}, {"发育", data.Grow}, {"团战", data.Battle}, {"生存", data.Survive}}
	} else {
		entries = []entry{{"输出", data.HurtHero}, {"生存", data.Survive}, {"团战", data.Battle}, {"发育", data.Grow}, {"KDA", data.Kda}}
	}
	for _, item := range entries {
		value := item.value.Float()
		pct := value / radarMax
		if pct > 1 {
			pct = 1
		}
		lane.Radar = append(lane.Radar, radarItem{Label: item.label, Value: item.value.String(), Pct: pct})
	}
	return lane
}

// fetchLanes 并发拉 5 条分路的 getFightData；失败的分路跳过。
func fetchLanes(ctx context.Context, client *campClient, roleID string, battleType int, a *campAuth, peakOrder bool) []laneView {
	type result struct {
		index int
		lane  *laneView
	}
	results := make(chan result, 5)
	for branchType := 1; branchType <= 5; branchType++ {
		go func(branchType int) {
			data, err := client.getFightData(ctx, roleID, battleType, branchType, 2, a)
			if err != nil || (data.WinNum.Int() == 0 && data.LoseNum.Int() == 0) {
				results <- result{branchType, nil}
				return
			}
			results <- result{branchType, buildLaneFromFight(branchType, data, peakOrder)}
		}(branchType)
	}
	lanes := make([]laneView, 0, 5)
	collected := map[int]*laneView{}
	for i := 0; i < 5; i++ {
		item := <-results
		if item.lane != nil {
			collected[item.index] = item.lane
		}
	}
	for branchType := 1; branchType <= 5; branchType++ {
		if lane := collected[branchType]; lane != nil {
			lanes = append(lanes, *lane)
		}
	}
	return lanes
}

/* ------------------------------------------------------------------ *
 * 全部赛季表现
 * ------------------------------------------------------------------ */

type allSeasonHeroView struct {
	HeroName string
	HeroIcon string
	GameCnt  string
	WinCnt   string
	WinRate  string
}

type allSeasonItem struct {
	SeasonID     string
	SeasonName   string
	IsCurrent    bool
	DateRange    string
	Games        string
	Wins         string
	WinRate      string
	Streak       string
	Gold         string
	Silver       string
	AvgScore     string
	ScoreLabel   string
	JobName      string
	ShortJobName string
	RankingStar  string
	JobColor     string
	MasterScore  string
	Star         int
	Heros        []allSeasonHeroView
}

type allSeasonTrendPoint struct {
	Label string `json:"label"`
	Value int    `json:"value"`
	Tip   string `json:"tip"`
}

type allSeasonView struct {
	Mode      string
	ModeLabel string
	IsPeak    bool

	RoleName       string
	RoleIcon       string
	ServerName     string
	TopJob         string
	TopMasterScore string
	WangzheCnt     string
	TotalGameCnt   string
	TotalWinRate   string
	Branch         string

	SeasonCnt int
	ShownCnt  int
	RangeText string

	Seasons   []allSeasonItem
	Trend     []allSeasonTrendPoint
	TrendJson string
}

// jobColor 按段位名取色（上游 JOB_COLORS 正则表）。
func jobColor(jobName string) string {
	switch {
	case strings.Contains(jobName, "王者"):
		return "#f5d76e"
	case strings.Contains(jobName, "星耀"):
		return "#c97bdb"
	case strings.Contains(jobName, "钻石"):
		return "#6ab0f5"
	case strings.Contains(jobName, "铂金"), strings.Contains(jobName, "白金"):
		return "#57c98a"
	case strings.Contains(jobName, "黄金"):
		return "#f0932b"
	case strings.Contains(jobName, "白银"):
		return "#c3cddd"
	case strings.Contains(jobName, "青铜"):
		return "#b07d5a"
	default:
		return "#9aa7bd"
	}
}

func fmtMonth(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(int64(seconds), 0).Format("2006.1")
}

const peakBaseScore = 1200 // 巅峰起始分：没打过的赛季也回 1200，需要按场次过滤

// buildAllSeasonView 组装 #全部排位表现 / #全部巅峰表现（只用 seasonId=0 一次请求）。
func buildAllSeasonView(head seasonHeadCard, history []seasonHistory, peak bool, limit int) allSeasonView {
	view := allSeasonView{
		IsPeak:         peak,
		RoleName:       html.EscapeString(head.RoleName),
		RoleIcon:       html.EscapeString(head.RoleIcon),
		ServerName:     html.EscapeString(head.ServerName),
		TopMasterScore: head.MasterScore.String(),
		WangzheCnt:     head.WangzheCnt.String(),
		TotalGameCnt:   head.GameCnt.String(),
		Branch:         html.EscapeString(head.Branch),
	}
	if peak {
		view.ModeLabel = "巅峰表现"
	} else {
		view.ModeLabel = "排位表现"
	}
	if head.WinRate.Float() > 0 {
		view.TotalWinRate = fmt.Sprintf("%.1f%%", head.WinRate.Float()*100)
	}
	if head.JobName != "" {
		view.TopJob = head.JobName
		if head.RankingStar.String() != "" {
			view.TopJob += " " + head.RankingStar.String() + "星"
		}
	}

	var played []allSeasonItem
	for index := range history {
		item := history[index]
		rank := item.RankInfo
		master := item.MasterInfo
		games := rank.TotalCnt.Int()
		masterScore := master.MasterScore.Int()
		if peak {
			games = master.TotalCnt.Int()
		}
		if games == 0 && !(peak && masterScore > peakBaseScore) {
			continue // 没打过的赛季（巅峰 1200 分 0 场是噪音）
		}
		entry := allSeasonItem{
			SeasonID:     item.SeasonID.String(),
			SeasonName:   html.EscapeString(item.SeasonName),
			IsCurrent:    index == 0,
			DateRange:    fmtMonth(item.StartTime.Int()) + " - " + fmtMonth(item.EndTime.Int()),
			JobName:      html.EscapeString(rank.JobName),
			ShortJobName: html.EscapeString(firstNonEmpty(rank.ShortJobName, rank.JobName)),
			RankingStar:  rank.RankingStar.String(),
			JobColor:     jobColor(rank.JobName),
			MasterScore:  master.MasterScore.String(),
			Star:         rank.TotalRankStar.Int() + rank.RankingStar.Int(),
		}
		if peak {
			entry.Games = strconv.Itoa(games)
			entry.Wins = master.TotalWinCnt.String()
			if master.WinRate.Float() > 0 {
				entry.WinRate = percentText(master.WinRate.Float(), 0)
			} else if games > 0 {
				entry.WinRate = fmt.Sprintf("%.0f%%", float64(master.TotalWinCnt.Int())*100/float64(games))
			}
			entry.Streak = master.MaxContinuousWinCnt.String()
			entry.Gold = master.GoldCnt.String()
			entry.Silver = master.SilverCnt.String()
			entry.AvgScore = master.AverageScore.String()
			entry.Heros = buildAllSeasonHeroViews(master.Heros, 5)
		} else {
			entry.Games = strconv.Itoa(games)
			entry.Wins = rank.TotalWinCnt.String()
			if rank.WinRate.Float() > 0 {
				entry.WinRate = percentText(rank.WinRate.Float(), 0)
			} else if games > 0 {
				entry.WinRate = fmt.Sprintf("%.0f%%", float64(rank.TotalWinCnt.Int())*100/float64(games))
			}
			entry.Streak = rank.MaxContinuousWinCnt.String()
			entry.Gold = rank.GoldCnt.String()
			entry.Silver = rank.SilverCnt.String()
			entry.AvgScore = rank.AverageScore.String()
			entry.Heros = buildAllSeasonHeroViews(rank.Heros, 5)
		}
		if entry.WinRate == "" {
			entry.WinRate = "—"
		}
		entry.ScoreLabel = "平均得分"
		if value := entry.AvgScore; value != "" && value != "0" {
			if number, err := strconv.ParseFloat(value, 64); err == nil && number <= 20 {
				entry.ScoreLabel = "平均分数 · 旧10分制"
			}
		}
		played = append(played, entry)
	}

	// 跨赛季趋势不受展示数量限制（从旧到新）。
	for index := len(played) - 1; index >= 0; index-- {
		entry := played[index]
		if peak {
			view.Trend = append(view.Trend, allSeasonTrendPoint{
				Label: entry.SeasonName,
				Value: parseIntDefault(entry.MasterScore),
				Tip:   entry.MasterScore,
			})
		} else {
			view.Trend = append(view.Trend, allSeasonTrendPoint{
				Label: entry.SeasonName,
				Value: entry.Star,
				Tip:   entry.ShortJobName + " " + entry.RankingStar + "星",
			})
		}
	}
	view.TrendJson = mustJSON(view.Trend)

	view.SeasonCnt = len(played)
	if limit > 0 && len(played) > limit {
		played = played[:limit]
	}
	view.ShownCnt = len(played)
	view.Seasons = played
	if len(played) > 0 {
		view.RangeText = played[len(played)-1].SeasonName + " ~ " + played[0].SeasonName
	}
	return view
}

func buildAllSeasonHeroViews(heros []seasonHero, limit int) []allSeasonHeroView {
	var views []allSeasonHeroView
	for index, hero := range heros {
		if index >= limit {
			break
		}
		views = append(views, allSeasonHeroView{
			HeroName: html.EscapeString(hero.HeroName),
			HeroIcon: html.EscapeString(hero.HeroIcon),
			GameCnt:  hero.GameCnt.String(),
			WinCnt:   hero.WinCnt.String(),
			WinRate:  percentText(hero.WinRate.Float(), 0),
		})
	}
	return views
}

func parseIntDefault(text string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(text))
	return value
}

/* ------------------------------------------------------------------ *
 * 指令入口
 * ------------------------------------------------------------------ */

// resolveCamp 解析指令里的目标营地号：显式 campID > 当前绑定。
// 表现类指令里的数字是赛季号（上游 seasonPage.js:54 wantSeason = args.season ?? args.count），
// 不能当绑定序号，否则多绑定用户发送 #排位表现3 会切错账号。
func resolveCamp(state *userState, args perfArgs) (string, error) {
	if args.campID != "" {
		return args.campID, nil
	}
	if len(state.IDs) == 0 {
		return "", errors.New("你还没有绑定营地ID\n发送 #绑定营地 [营地ID] 后就能查数据")
	}
	return state.currentID(), nil
}

func seasonCredential(state *userState, campID string) (*campAuth, error) {
	cred := state.bestCredential(campID)
	if cred == nil {
		return nil, errors.New(noCredentialHint)
	}
	return cred.auth(), nil
}

// seasonErrorText 把接口错误翻译成用户可读文案。
func seasonErrorText(err error) string {
	switch {
	case errors.Is(err, ErrAuthInvalid):
		return "营地登录态已失效，请重新发送 #营地wx全局登录 或 #营地QQ全局登录"
	case errors.Is(err, ErrRateLimited):
		return ErrRateLimited.Error()
	case errors.Is(err, ErrSeasonHidden), errors.Is(err, ErrProfileHidden):
		return err.Error()
	default:
		return "查询失败：" + err.Error()
	}
}

// querySeasonData 拉赛季表现需要的两份响应：summary（seasonId=0）+ detail（目标赛季）。
func querySeasonData(ctx context.Context, client *campClient, campID string, a *campAuth, wantSeason int) (*profileData, *seasonPageData, *seasonPageData, *seasonHistory, bool, error) {
	profile, err := client.getProfile(ctx, campID, a)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	roleID := profile.TargetRoleID.String()
	if roleID == "" {
		return nil, nil, nil, nil, false, errors.New("营地没有返回角色信息，请确认该账号绑定了王者荣耀角色")
	}
	summary, err := client.getSeasonpage(ctx, roleID, 0, a)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	if len(summary.HistoryList) == 0 {
		return nil, nil, nil, nil, false, ErrSeasonHidden
	}
	target, index := pickSeason(summary.HistoryList, wantSeason)
	if target == nil {
		var available []string
		for _, item := range summary.HistoryList {
			available = append(available, item.SeasonName)
		}
		return nil, nil, nil, nil, false, fmt.Errorf("未找到 S%d 的赛季记录，可查询：%s", wantSeason, strings.Join(available, "、"))
	}
	detail := summary
	if target.SeasonID.Int() != 0 {
		detail, err = client.getSeasonpage(ctx, roleID, target.SeasonID.Int(), a)
		if err != nil {
			return nil, nil, nil, nil, false, err
		}
	}
	isCurrent := index == 0
	return profile, summary, detail, target, isCurrent, nil
}

func seasonCommand(ctx context.Context, c sdk.Call, mode string, rest string) error {
	args := parsePerfArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID, err := resolveCamp(state, args)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	// count 小于等于绑定数时已被 resolveCamp 当序号吃掉；否则当赛季号。
	wantSeason := args.season
	if wantSeason == 0 && args.count > len(state.IDs) {
		wantSeason = args.count
	}

	client := newCampClient()
	profile, summary, detail, target, isCurrent, err := querySeasonData(ctx, client, campID, auth, wantSeason)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}

	var lanes []laneView
	if isCurrent {
		lanes = fetchLanes(ctx, client, profile.TargetRoleID.String(), 3, auth, false)
	}
	view := buildSeasonPageView(summary, detail, target, isCurrent, mode, lanes)
	image, err := renderSeasonPage(ctx, view)
	if err != nil {
		return c.Reply(ctx, "赛季表现出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

func peakCommand(ctx context.Context, c sdk.Call, rest string) error {
	args := parsePerfArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID, err := resolveCamp(state, args)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	auth, err := seasonCredential(state, campID)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	wantSeason := args.season
	if wantSeason == 0 && args.count > len(state.IDs) {
		wantSeason = args.count
	}

	client := newCampClient()
	profile, err := client.getProfile(ctx, campID, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	roleID := profile.TargetRoleID.String()
	if roleID == "" {
		return c.Reply(ctx, errors.New("营地没有返回角色信息").Error())
	}

	if wantSeason > 0 {
		return peakSeasonCommand(ctx, c, client, campID, roleID, auth, wantSeason)
	}
	return peakRecentCommand(ctx, c, client, campID, roleID, auth)
}

// peakSeasonCommand 是 #巅峰表现s40：整赛季巅峰数据（seasonpage 路径）。
func peakSeasonCommand(ctx context.Context, c sdk.Call, client *campClient, campID, roleID string, a *campAuth, wantSeason int) error {
	summary, err := client.getSeasonpage(ctx, roleID, 0, a)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	target, index := pickSeason(summary.HistoryList, wantSeason)
	if target == nil {
		var available []string
		for _, item := range summary.HistoryList {
			available = append(available, item.SeasonName)
		}
		return c.Reply(ctx, fmt.Sprintf("未找到 S%d 的赛季记录，可查询：%s", wantSeason, strings.Join(available, "、")))
	}
	detail := summary
	if target.SeasonID.Int() != 0 {
		if detail, err = client.getSeasonpage(ctx, roleID, target.SeasonID.Int(), a); err != nil {
			return c.Reply(ctx, seasonErrorText(err))
		}
	}
	master := target.MasterInfo
	games := master.TotalCnt.Int()
	if games == 0 {
		games = detail.Behavior.MasterInfo.TotalGameCnt.Int()
	}
	if games == 0 {
		return c.Reply(ctx, target.SeasonName+" 暂无巅峰赛数据")
	}
	_ = index

	head := detail.HeadCard
	view := peakPageView{
		RoleName:    html.EscapeString(head.RoleName),
		RoleIcon:    html.EscapeString(head.RoleIcon),
		ServerName:  html.EscapeString(head.ServerName),
		SeasonLabel: html.EscapeString(target.SeasonName),
		SubLabel:    "赛季巅峰表现",
		Branch:      html.EscapeString(head.Branch),
		GameCnt:     strconv.Itoa(games),
	}
	score := head.MasterScore.String()
	if score == "" || score == "0" {
		score = master.MasterScore.String()
	}
	wins := master.TotalWinCnt.Int()
	if master.WinRate.Float() > 0 {
		view.WinRate = percentText(master.WinRate.Float(), 0)
	} else if games > 0 {
		view.WinRate = fmt.Sprintf("%.0f%%", float64(wins)*100/float64(games))
	}
	view.WinNum = strconv.Itoa(wins)
	view.LoseNum = strconv.Itoa(games - wins)
	view.AvgScore = master.AverageScore.String()
	view.Stats = []honorItem{
		{score, "巅峰赛积分"},
		{view.GameCnt, "巅峰赛场次"},
		{view.WinRate, "巅峰赛胜率"},
		{view.AvgScore, avgScoreLabel(master.AverageScore.String())},
		{master.MaxContinuousWinCnt.String(), "最高连胜"},
		{master.GoldCnt.String() + " / " + master.SilverCnt.String(), "金牌 / 银牌"},
	}
	heros := detail.Behavior.MasterInfo.Heros
	if len(heros) == 0 {
		heros = master.Heros
	}
	view.Heros = buildSeasonHeroViews(heros, 3)
	branches := append([]seasonBranch(nil), detail.Behavior.MasterInfo.Branches...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].GameCnt.Int() > branches[j].GameCnt.Int() })
	view.Branches = buildSeasonBranchViews(branches, masterBranchColors, true)
	view.BranchesJson = mustJSON(view.Branches)
	view.HasBranches = len(view.Branches) > 0
	view.TrendJson = "[]"
	view.OverallJson = "null"

	image, err := renderPeakPage(ctx, view)
	if err != nil {
		return c.Reply(ctx, "巅峰表现出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

// peakRecentCommand 是 #巅峰表现：近 30 天口径（getFightData 路径）。
func peakRecentCommand(ctx context.Context, c sdk.Call, client *campClient, campID, roleID string, a *campAuth) error {
	// 并发：整体 + 5 条分路，巅峰赛 battleType=10。
	type laneResult struct {
		branch int
		data   *fightDataSelf
		err    error
	}
	results := make(chan laneResult, 6)
	for branch := 0; branch <= 5; branch++ {
		go func(branch int) {
			data, err := client.getFightData(ctx, roleID, 10, branch, 2, a)
			results <- laneResult{branch, data, err}
		}(branch)
	}
	var overall *fightDataSelf
	laneData := map[int]*fightDataSelf{}
	var firstErr error
	for i := 0; i < 6; i++ {
		item := <-results
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			continue
		}
		if item.branch == 0 {
			overall = item.data
		} else {
			laneData[item.branch] = item.data
		}
	}
	if overall == nil {
		return c.Reply(ctx, seasonErrorText(firstErr))
	}
	games := overall.WinNum.Int() + overall.LoseNum.Int()
	if games == 0 {
		return c.Reply(ctx, "近 30 天暂无巅峰赛数据，快去巅峰赛大展身手吧")
	}

	// 头部角色信息与巅峰分：复用赛季页当前赛季数据（与上游一致串行两跳）。
	summary, summaryErr := client.getSeasonpage(ctx, roleID, 0, a)
	var detail *seasonPageData
	var target *seasonHistory
	if summaryErr == nil && len(summary.HistoryList) > 0 {
		target = &summary.HistoryList[0]
		if target.SeasonID.Int() != 0 {
			detail, _ = client.getSeasonpage(ctx, roleID, target.SeasonID.Int(), a)
		} else {
			detail = summary
		}
	}

	view := peakPageView{
		SeasonLabel: "近30天巅峰表现",
		SubLabel:    "近 30 天",
		GameCnt:     strconv.Itoa(games),
		WinNum:      overall.WinNum.String(),
		LoseNum:     overall.LoseNum.String(),
		WinRate:     branchWinRate(seasonBranch{WinNum: overall.WinNum, LoseNum: overall.LoseNum, WinRate: overall.WinRate}),
		AvgScore:    overall.AvgScore.String(),
	}
	if detail != nil {
		head := detail.HeadCard
		view.RoleName = html.EscapeString(head.RoleName)
		view.RoleIcon = html.EscapeString(head.RoleIcon)
		view.ServerName = html.EscapeString(head.ServerName)
		view.Branch = html.EscapeString(head.Branch)
		view.Stats = []honorItem{
			{head.MasterScore.String(), "当前巅峰分"},
			{view.GameCnt, "巅峰赛场次"},
			{view.WinRate, "巅峰赛胜率"},
			{view.AvgScore, "平均得分"},
			{overall.GoldMedal.String() + " / " + overall.SliverMedal.String(), "金牌 / 银牌"},
		}
		heros := detail.Behavior.MasterInfo.Heros
		view.Heros = buildSeasonHeroViews(heros, 3)
	} else {
		view.Stats = []honorItem{
			{view.GameCnt, "巅峰赛场次"},
			{view.WinRate, "巅峰赛胜率"},
			{view.AvgScore, "平均得分"},
		}
	}
	view.Honor = []honorItem{
		{overall.Mvp.String(), "全场最佳"},
		{overall.LoseMvp.String(), "败方最佳"},
		{overall.ThreeKill.String(), "三杀"},
		{overall.FourKill.String(), "四杀"},
		{overall.FiveKill.String(), "五杀"},
		{overall.GodLike.String(), "超神"},
		{overall.GoldMedal.String(), "金牌"},
		{overall.SliverMedal.String(), "银牌"},
	}
	view.HasHonor = true

	overallLane := buildLaneFromFight(0, overall, true)
	view.HasOverallRadar = hasRadarValue(overallLane.Radar)
	view.OverallJson = mustJSON(overallLane)

	var lanes []laneView
	for branch := 1; branch <= 5; branch++ {
		data := laneData[branch]
		if data == nil || (data.WinNum.Int() == 0 && data.LoseNum.Int() == 0) {
			continue
		}
		lanes = append(lanes, *buildLaneFromFight(branch, data, true))
	}
	view.Lanes = lanes
	view.LanesJson = mustJSON(lanes)
	view.HasLanes = len(lanes) > 0

	// 上分趋势：优先本地归档逐日聚合，否则回落 seasonpage 的 gameTrend。
	trend, note := buildPeakTrend(ctx, c, campID, target, detail)
	view.Trend = trend
	view.TrendJson = mustJSON(trend)
	view.TrendNote = note

	image, err := renderPeakPage(ctx, view)
	if err != nil {
		return c.Reply(ctx, "巅峰表现出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

func avgScoreLabel(avg string) string {
	if number, err := strconv.ParseFloat(strings.TrimSpace(avg), 64); err == nil && number > 0 && number <= 20 {
		return "平均分数 · 旧10分制"
	}
	return "平均得分"
}

// buildPeakTrend 组装巅峰表现页的上分趋势：归档逐日 ≥3 点优先，否则 seasonpage 快照。
func buildPeakTrend(ctx context.Context, c sdk.Call, campID string, target *seasonHistory, detail *seasonPageData) ([]peakTrendPoint, string) {
	if archive := loadCampArchive(ctx, c.State, campID); archive != nil {
		daily := map[int64]int{} // 日期(天) → 当天最后一场巅峰分
		var days []int64
		cutoff := time.Now().Unix() - 30*86400
		for _, battle := range archive.Battles { // 倒序，先看到的是当天最后一场
			if battle.Time < cutoff || battle.NewMasterMatchScore <= 0 {
				continue
			}
			if !strings.Contains(battle.MapName, "巅峰") {
				continue
			}
			day := battle.Time / 86400
			if _, ok := daily[day]; !ok {
				daily[day] = battle.NewMasterMatchScore
				days = append(days, day)
			}
		}
		if len(days) >= 3 {
			sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
			points := make([]peakTrendPoint, 0, len(days))
			for _, day := range days {
				points = append(points, peakTrendPoint{Score: daily[day], Time: day * 86400})
			}
			return points, fmt.Sprintf("逐日 · 覆盖 %d 天", len(days))
		}
	}
	if detail != nil && target != nil {
		var points []peakTrendPoint
		for _, item := range filterGameTrend(detail.Behavior.RankInfo.GameTrend, int64(target.StartTime.Int()), int64(target.EndTime.Int())) {
			// gameTrend 的 score 是巅峰分；排位变化点 score 为 0，跳过。
			score := 0
			for _, raw := range detail.Behavior.RankInfo.GameTrend {
				if int64(raw.Time.Int()) == item.Time {
					score = raw.Score.Int()
					break
				}
			}
			if score <= 0 {
				continue
			}
			points = append(points, peakTrendPoint{Score: score, Time: item.Time, JobName: item.JobName, JobColor: item.JobColor})
		}
		if len(points) > 0 {
			return points, "按段位变化"
		}
	}
	return nil, ""
}

func allSeasonCommand(ctx context.Context, c sdk.Call, peak bool, rest string) error {
	args := parsePerfArgs(rest)
	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID, err := resolveCamp(state, args)
	if err != nil {
		return c.Reply(ctx, err.Error())
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
		return c.Reply(ctx, "营地没有返回角色信息，请确认该账号绑定了王者荣耀角色")
	}
	summary, err := client.getSeasonpage(ctx, roleID, 0, auth)
	if err != nil {
		return c.Reply(ctx, seasonErrorText(err))
	}
	if len(summary.HistoryList) == 0 {
		return c.Reply(ctx, ErrSeasonHidden.Error())
	}

	limit := 3
	if args.all {
		limit = 30
	} else if args.count > 0 && args.count > len(state.IDs) {
		limit = args.count
		if limit > 30 {
			limit = 30
		}
	}
	view := buildAllSeasonView(summary.HeadCard, summary.HistoryList, peak, limit)
	image, err := renderAllSeasonPage(ctx, view)
	if err != nil {
		return c.Reply(ctx, "赛季表现汇总出图失败，稍后再试")
	}
	return c.ReplyImageBytes(ctx, image)
}

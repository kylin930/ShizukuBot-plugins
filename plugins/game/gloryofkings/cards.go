package gloryofkings

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"shizukubot/sdk"
)

//go:embed resources/html/accountManage.html resources/html/QueryGameRecordList.html resources/html/SeasonPage.html resources/html/PeakPerformance.html resources/html/AllSeasonPerformance.html resources/html/ScoreTrend.html resources/html/RankTrend.html resources/html/MyHeroList.html resources/html/HeroList.html resources/html/SkinWall.html resources/html/SkinMissing.html resources/html/HeroSkin.html resources/html/CampSign.html resources/html/CampExchange.html resources/html/CampPrivacy.html resources/html/CampOnline.html resources/html/CampBattleDetail.html resources/img/*.png resources/img/*.jpg
var assets embed.FS

var (
	tplAccount    = template.Must(template.New("account").Parse(mustAsset("resources/html/accountManage.html")))
	tplBattleList = template.Must(template.New("battleList").Parse(mustAsset("resources/html/QueryGameRecordList.html")))

	// P2 模板
	tplSeasonPage   = template.Must(template.New("seasonPage").Parse(mustAsset("resources/html/SeasonPage.html")))
	tplPeakPage     = template.Must(template.New("peakPage").Parse(mustAsset("resources/html/PeakPerformance.html")))
	tplAllSeason    = template.Must(template.New("allSeason").Parse(mustAsset("resources/html/AllSeasonPerformance.html")))
	tplScoreTrend   = template.Must(template.New("scoreTrend").Parse(mustAsset("resources/html/ScoreTrend.html")))
	tplRankTrend    = template.Must(template.New("rankTrend").Parse(mustAsset("resources/html/RankTrend.html")))
	tplMyHeroList   = template.Must(template.New("myHeroList").Parse(mustAsset("resources/html/MyHeroList.html")))
	tplHeroList     = template.Must(template.New("heroList").Parse(mustAsset("resources/html/HeroList.html")))
	tplSkinWall     = template.Must(template.New("skinWall").Parse(mustAsset("resources/html/SkinWall.html")))
	tplSkinMissing  = template.Must(template.New("skinMissing").Parse(mustAsset("resources/html/SkinMissing.html")))
	tplHeroSkin     = template.Must(template.New("heroSkin").Parse(mustAsset("resources/html/HeroSkin.html")))
	tplCampSign     = template.Must(template.New("campSign").Parse(mustAsset("resources/html/CampSign.html")))
	tplCampExchange = template.Must(template.New("campExchange").Parse(mustAsset("resources/html/CampExchange.html")))
	tplCampPrivacy  = template.Must(template.New("campPrivacy").Parse(mustAsset("resources/html/CampPrivacy.html")))
	tplCampOnline   = template.Must(template.New("campOnline").Parse(mustAsset("resources/html/CampOnline.html")))
	tplBattleDetail = template.Must(template.New("battleDetail").Parse(mustAsset("resources/html/CampBattleDetail.html")))

	imgRef = regexp.MustCompile(`\.\./img/[A-Za-z0-9_.-]+`)
)

func mustAsset(name string) string {
	data, err := assets.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("glory-of-kings: 缺少模板资源 %s: %v", name, err))
	}
	return string(data)
}

func imageDataURI(name string) string {
	data, err := assets.ReadFile("resources/img/" + name)
	if err != nil {
		return ""
	}
	mime := "image/jpeg"
	if strings.HasSuffix(name, ".png") {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// inlineImages 把模板里的 ../img/xxx 换成内嵌 data URI，渲染时就无需临时目录。
func inlineImages(source string) string {
	return imgRef.ReplaceAllStringFunc(source, func(ref string) string {
		return imageDataURI(strings.TrimPrefix(ref, "../img/"))
	})
}

type cardFunc struct {
	Cmd     string
	Example string
}

type accountCardData struct {
	Type      string
	WzryID    string
	WzryName  string
	IDList    string
	Funcs     []cardFunc
	Timestamp string
}

func renderAccountCard(ctx context.Context, data accountCardData) ([]byte, error) {
	body, err := executeTemplate(tplAccount, data)
	if err != nil {
		return nil, err
	}
	return sdk.RenderHTML(ctx, []byte(inlineImages(body)), sdk.RenderOptions{Width: 800, Timeout: 30 * time.Second})
}

// accountCardText 生成卡片里的账号列表文案，格式与上游 accountManage 一致：
// 只列已绑定的营地号，当前使用的那个后面标「当前」。
func accountCardText(state *userState) (string, string) {
	if len(state.IDs) == 0 {
		return "", "还未绑定营地号\n发送 #绑定营地 + 营地号 即可绑定\n示例：#绑定营地 123456"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "当前使用：第 %d 个\n\n", state.Current+1)
	for index, id := range state.IDs {
		name := state.Names[id]
		line := fmt.Sprintf("%d. %s", index+1, id)
		if name != "" {
			line += "（" + html.EscapeString(name) + "）"
		}
		if index == state.Current {
			line += " ← 当前"
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return builder.String(), state.currentID()
}

var accountCardFuncs = []cardFunc{
	{Cmd: "#绑定营地", Example: "示例: #绑定营地 123456"},
	{Cmd: "#营地ID / #王者ID / #我的王者ID", Example: "示例: #我的王者ID"},
	{Cmd: "#切换营地", Example: "示例: #切换营地2"},
	{Cmd: "#删除营地", Example: "示例: #删除营地2"},
	{Cmd: "#营地wx全局登录 / #营地QQ全局登录", Example: "示例: #营地wx全局登录"},
	{Cmd: "#查询战绩 / #查询N战绩", Example: "示例: #查询2战绩"},
	{Cmd: "#我的英雄 / #常用英雄", Example: "示例: #我的英雄 10"},
	{Cmd: "#皮肤墙 / #缺皮肤 / #查皮肤", Example: "示例: #皮肤墙 50"},
	{Cmd: "#赛季表现 / #巅峰表现", Example: "示例: #赛季表现s40"},
	{Cmd: "#巅峰趋势 / #段位趋势", Example: "示例: #段位趋势"},
}

type killTag struct {
	Icon string
	Text string
}

type battleRow struct {
	Win         bool
	Result      string
	HeroIcon    string
	SpecialTags []string
	GameType    string
	KillCnt     int
	DeadCnt     int
	AssistCnt   int
	KillTags    []killTag
	MvpIcon     string
	MvpLabel    string
	EvalIcon    string
	EvalLabel   string
	Tags        []string
	Duration    string
	Time        string
	Grade       string
}

type battleListView struct {
	Avatar           string
	HeroLabel        string
	RoleJobName      string
	ModeLabel        string
	WinningStreak    int
	EmptyState       bool
	EmptyTitle       string
	EmptyDescription string
	Battles          []battleRow
}

var specialBattleTags = []string{"实力局", "翻盘局", "暴走局", "尽力局", "带飞局", "翻车局", "伯仲局"}

func renderBattleList(ctx context.Context, view battleListView) ([]byte, error) {
	body, err := executeTemplate(tplBattleList, view)
	if err != nil {
		return nil, err
	}
	return sdk.RenderHTML(ctx, []byte(inlineImages(body)), sdk.RenderOptions{
		Width:    760,
		FullPage: true,
		Timeout:  30 * time.Second,
	})
}

func executeTemplate(tpl *template.Template, data any) (string, error) {
	var buffer bytes.Buffer
	if err := tpl.Execute(&buffer, data); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

/* ------------------------------------------------------------------ *
 * P2 渲染入口
 * ------------------------------------------------------------------ */

// renderCard 是 P2 模板的通用渲染：执行模板、内联本地图片、整页截图。
func renderCard(ctx context.Context, tpl *template.Template, data any, width int) ([]byte, error) {
	body, err := executeTemplate(tpl, data)
	if err != nil {
		return nil, err
	}
	return sdk.RenderHTML(ctx, []byte(inlineImages(body)), sdk.RenderOptions{
		Width:    width,
		FullPage: true,
		Timeout:  60 * time.Second,
	})
}

func renderSeasonPage(ctx context.Context, view seasonPageView) ([]byte, error) {
	return renderCard(ctx, tplSeasonPage, view, 1050)
}

func renderPeakPage(ctx context.Context, view peakPageView) ([]byte, error) {
	return renderCard(ctx, tplPeakPage, view, 1050)
}

func renderAllSeasonPage(ctx context.Context, view allSeasonView) ([]byte, error) {
	return renderCard(ctx, tplAllSeason, view, 1050)
}

func renderScoreTrend(ctx context.Context, view scoreTrendView) ([]byte, error) {
	return renderCard(ctx, tplScoreTrend, view, 1050)
}

func renderRankTrend(ctx context.Context, view rankTrendView) ([]byte, error) {
	return renderCard(ctx, tplRankTrend, view, 1050)
}

func renderMyHeroList(ctx context.Context, view myHeroView) ([]byte, error) {
	return renderCard(ctx, tplMyHeroList, view, 1000)
}

func renderHeroList(ctx context.Context, view heroListView) ([]byte, error) {
	return renderCard(ctx, tplHeroList, view, 1000)
}

func renderSkinWall(ctx context.Context, view skinWallView) ([]byte, error) {
	return renderCard(ctx, tplSkinWall, view, 1400)
}

func renderSkinMissing(ctx context.Context, view skinMissingView) ([]byte, error) {
	return renderCard(ctx, tplSkinMissing, view, 1050)
}

func renderHeroSkin(ctx context.Context, view heroSkinView) ([]byte, error) {
	return renderCard(ctx, tplHeroSkin, view, 1400)
}

func renderCampSign(ctx context.Context, view signPageView) ([]byte, error) {
	return renderCard(ctx, tplCampSign, view, 1050)
}

func renderCampExchange(ctx context.Context, view exchangePageView) ([]byte, error) {
	return renderCard(ctx, tplCampExchange, view, 1050)
}

func renderPrivacy(ctx context.Context, view privacyPageView) ([]byte, error) {
	return renderCard(ctx, tplCampPrivacy, view, 1050)
}

func renderOnline(ctx context.Context, view onlinePageView) ([]byte, error) {
	return renderCard(ctx, tplCampOnline, view, 1050)
}

func renderBattleDetail(ctx context.Context, view battleDetailView) ([]byte, error) {
	return renderCard(ctx, tplBattleDetail, view, 1050)
}

// buildBattleView 把营地返回的战绩列表映射成模板数据。
func buildBattleView(data *battleListData, avatar, roleJobName string, option int) battleListView {
	view := battleListView{
		Avatar:        avatar,
		RoleJobName:   html.EscapeString(roleJobName),
		WinningStreak: winStreak(data.List),
	}
	switch option {
	case 1:
		view.ModeLabel = "排位赛"
	case 4:
		view.ModeLabel = "巅峰赛"
	}
	if len(data.List) == 0 {
		view.EmptyState = true
		view.EmptyTitle = "暂无战绩"
		view.EmptyDescription = "当前没有可展示的战绩数据"
		return view
	}
	for _, item := range data.List {
		view.Battles = append(view.Battles, buildBattleRow(item))
	}
	return view
}

// winStreak 返回列表中的最高连胜（模板文案是「最高 N 连胜」），
// 不能只数开头那段，否则中途输一局就会把后面的连胜丢掉。
func winStreak(items []battleItem) int {
	best, current := 0, 0
	for _, item := range items {
		if item.GameResult == 1 {
			current++
			if current > best {
				best = current
			}
		} else {
			current = 0
		}
	}
	return best
}

func buildBattleRow(item battleItem) battleRow {
	row := battleRow{
		Win:       item.GameResult == 1,
		HeroIcon:  html.EscapeString(item.HeroIcon),
		GameType:  html.EscapeString(item.MapName),
		KillCnt:   item.KillCnt,
		DeadCnt:   item.DeadCnt,
		AssistCnt: item.AssistCnt,
		Duration:  formatDuration(item.UsedTime),
		Time:      formatGameTime(item.GameTime),
		Grade:     item.GradeGame.String(),
	}
	if row.Win {
		row.Result = "胜利"
	} else {
		row.Result = "失败"
	}
	descs := strings.FieldsFunc(item.Desc, func(r rune) bool { return r == ',' || r == '，' || r == '|' })
	for _, tag := range descs {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if isSpecialTag(tag) {
			row.SpecialTags = append(row.SpecialTags, html.EscapeString(tag))
		} else {
			row.Tags = append(row.Tags, html.EscapeString(tag))
		}
	}
	for _, tag := range killTags(item) {
		row.KillTags = append(row.KillTags, killTag{Icon: tag.Icon, Text: tag.Text})
	}
	row.MvpIcon = html.EscapeString(firstNonEmpty(item.MvpURLV3, item.MvpURLV2))
	row.EvalIcon = html.EscapeString(firstNonEmpty(item.EvaluateURLV3, item.EvaluateURLV2, item.EvaluateURL))
	return row
}

func isSpecialTag(tag string) bool {
	for _, special := range specialBattleTags {
		if tag == special {
			return true
		}
	}
	return false
}

func killTags(item battleItem) []killTag {
	var tags []killTag
	if item.GodLikeCnt > 0 {
		tags = append(tags, killTag{Icon: imageDataURI("kill_legend.png"), Text: "超神"})
	}
	if item.RampageCnt > 0 {
		tags = append(tags, killTag{Icon: imageDataURI("kill_5.png"), Text: "五杀"})
	}
	if item.UltraKillCnt > 0 {
		tags = append(tags, killTag{Icon: imageDataURI("kill_4.png"), Text: "四杀"})
	}
	if item.TripleKillCnt > 0 {
		tags = append(tags, killTag{Icon: imageDataURI("kill_3.png"), Text: "三杀"})
	}
	return tags
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func formatGameTime(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if number, err := strconv.ParseInt(text, 10, 64); err == nil {
		if number > 1e12 {
			number /= 1000
		}
		return time.Unix(number, 0).Format("01-02 15:04")
	}
	return html.EscapeString(text)
}

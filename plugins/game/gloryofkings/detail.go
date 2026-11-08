package gloryofkings

// 单场战绩详情（#查询战绩 3 / #排位战绩 3）。
// 上游用 44KB 的 QueryGameRecordDetails.html 出图，这里按同样的信息口径
// （双方阵容、KDA、评分、经济、输出、承伤、装备）重建了一张精简卡片。

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

var (
	roleIDFromURLRe = regexp.MustCompile(`toAppRoleId=(\d+)`)
	branchHashRe    = regexp.MustCompile(`custom_wzry_battledetail_tags/([0-9a-f]{32})\.png`)
	medalIconRe     = regexp.MustCompile(`/(gold|silver)_(warrior|archer|mage|support|assassin|tank)(?:_\d+)?\.png`)
)

// 分路评价图标文件名是无规律哈希，逐张核对后登记（与上游 BRANCH_TAGS 同源）。
var branchTags = map[string]string{
	"5db4fef1bfc72dd2c5ae71b01ef3951b": "顶级对抗路",
	"4b4f396f8e6d18bdf8bf699b8c5d9be4": "顶级中路",
	"926ba0111984464ad46e72dc93157fcd": "顶级发育路",
	"9eb904626303912a65d9b69bc8d88aa9": "顶级打野",
	"a8b5101bc81ae64cf96c67ed1ab21975": "顶级游走",
	"c30089b8daf9a4792f85c8a6d97a3e9c": "金牌对抗路",
	"f63de8a7f98863ab3a34aada6bc4bd6f": "金牌中路",
	"c717aab51e99a4bfa9c6d2e024e97512": "金牌发育路",
	"5142af35177e111837efbf85071f373b": "金牌打野",
	"1147db2cd2a46031783a9a0fc34f7f3c": "金牌游走",
	"116bb42c52b7d83b9d80ac9dd9580607": "银牌对抗路",
	"a2c96893471637e5cf5c0a1e2c9829f3": "银牌中路",
	"7577421618c781e7a59b81904937a8a0": "银牌发育路",
	"39d8211165f3730700fc6db10abd170e": "银牌打野",
	"977937945942799fd618773e5c378d3a": "银牌游走",
	"e8602ae4b427f06dd1349438fbeab68f": "铜牌对抗路",
	"029706c958a71f2aa5c187e2ef021430": "铜牌中路",
	"af6fd95b08fd1b58340b48374707262c": "铜牌发育路",
	"c8a09fe55b4614d0307a6161f32ae479": "铜牌打野",
	"3159d2f1733203167a9a3d5d3e4656ad": "铜牌游走",
}

var medalRoles = map[string]string{
	"warrior": "战士", "archer": "射手", "mage": "法师",
	"support": "辅助", "assassin": "刺客", "tank": "坦克",
}

// ---------- 视图 ----------

type detailPlayerView struct {
	Name       string
	HeroName   string
	HeroIcon   string
	IsMe       bool
	KDA        string
	Grade      string
	GradeKey   string
	Money      string
	Hurt       string
	HurtRate   int
	BeHurt     string
	JoinRate   int
	Ctrl       string
	Soldier    string
	MvpTag     string
	EvalTag    string
	EvalIcon   string
	MaxTags    []string
	EquipIcons []string
}

type detailTeamView struct {
	Side           string
	Win            bool
	KillDeadAssist string
	Money          string
	Tower          string
	Players        []detailPlayerView
}

type battleDetailView struct {
	Result       string
	ResultEn     string
	MapName      string
	MatchDesc    string
	Tips         string
	StartTime    string
	UsedTime     string
	MySide       string
	EnemySide    string
	MyDragons    string
	EnemyDragons string
	MyTeam       detailTeamView
	EnemyTeam    detailTeamView
	GeneratedAt  string
}

// buildBattleDetailView 把营地详情接口的数据整理成模板字段。
func buildBattleDetailView(detail *battleDetailData, item battleItem) battleDetailView {
	isBlue := teamOf(detail.BlueTeam.AcntCamp) == teamOf(detail.Head.AcntCamp)
	myTeam, enemyTeam := detail.RedTeam, detail.BlueTeam
	myRoles, enemyRoles := detail.RedRoles, detail.BlueRoles
	if isBlue {
		myTeam, enemyTeam = detail.BlueTeam, detail.RedTeam
		myRoles, enemyRoles = detail.BlueRoles, detail.RedRoles
	}

	myWin := detail.Head.GameResult.Int() != 0
	view := battleDetailView{
		Result:       "失败",
		ResultEn:     "DEFEAT",
		MapName:      detail.Head.MapName,
		MatchDesc:    detail.Head.MatchDesc,
		Tips:         detail.Head.Tips,
		StartTime:    formatStartTime(item.eventTime()),
		UsedTime:     fmt.Sprintf("%d 分钟", detail.Battle.UsedTime.Int()/60),
		MySide:       sideLabel(myTeam.AcntCamp),
		EnemySide:    sideLabel(enemyTeam.AcntCamp),
		MyDragons:    dragonText(myTeam),
		EnemyDragons: dragonText(enemyTeam),
		GeneratedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	if myWin {
		view.Result = "胜利"
		view.ResultEn = "VICTORY"
	}
	view.MyTeam = buildDetailTeam(myTeam, myRoles, myWin)
	view.EnemyTeam = buildDetailTeam(enemyTeam, enemyRoles, !myWin)
	return view
}

func buildDetailTeam(team battleDetailTeam, roles []battleDetailRole, win bool) detailTeamView {
	view := detailTeamView{
		Side:           sideLabel(team.AcntCamp),
		Win:            win,
		KillDeadAssist: fmt.Sprintf("%d/%d/%d", team.KillCnt.Int(), team.DeadCnt.Int(), team.AssistCnt.Int()),
		Money:          formatMoney(team.Money.Int()),
		Tower:          strconv.Itoa(team.TowerCnt.Int()),
	}
	totalHurt := 0
	for _, role := range roles {
		totalHurt += role.BattleStats.TotalHeroHurtCnt.Int()
	}
	for _, role := range roles {
		view.Players = append(view.Players, buildDetailPlayer(role, win, totalHurt))
	}
	return view
}

func buildDetailPlayer(role battleDetailRole, win bool, teamHurt int) detailPlayerView {
	stats := role.BattleStats
	grade := stats.GradeGame.Int()
	view := detailPlayerView{
		Name:     html.EscapeString(role.BasicInfo.RoleName),
		HeroName: html.EscapeString(role.BattleRecords.UsedHero.HeroName),
		HeroIcon: html.EscapeString(role.BattleRecords.UsedHero.HeroIcon),
		IsMe:     bool(role.BasicInfo.IsMe),
		KDA:      fmt.Sprintf("%d / %d / %d", stats.KillCnt.Int(), stats.DeadCnt.Int(), stats.AssistCnt.Int()),
		Grade:    stats.GradeGame.String(),
		GradeKey: gradeKey(grade),
		Money:    formatMoney(stats.Money.Int()),
		Hurt:     formatDamage(stats.TotalHeroHurtCnt.Int()),
		BeHurt:   formatDamage(stats.TotalBeHurtCnt.Int()),
		Ctrl:     stats.CtrlTime.String(),
		JoinRate: percentOf(stats.JoinGamePercent),
	}
	if hurt := stats.TotalHeroHurtCnt.Int(); teamHurt > 0 && hurt > 0 {
		view.HurtRate = int(float64(hurt)/float64(teamHurt)*100 + 0.5)
	}
	if soldier := stats.KillSoldier.Int(); soldier > 0 {
		view.Soldier = strconv.Itoa(soldier)
	}
	if bool(stats.Mvp) {
		if win {
			view.MvpTag = "MVP"
		} else {
			view.MvpTag = "SVP"
		}
	}
	view.EvalTag, view.EvalIcon = resolveEvaluate(stats.EvaluateIconV3, stats.EvaluateIconV2, stats.EvaluateIcon)
	for _, tag := range []struct {
		on   bool
		text string
	}{
		{bool(stats.MaxMoney), "经济最高"},
		{bool(stats.MaxJoinGame), "参团最高"},
		{bool(stats.MaxBeHurt), "承伤最高"},
	} {
		if tag.on {
			view.MaxTags = append(view.MaxTags, tag.text)
		}
	}
	if icon := role.BattleRecords.Skill.SkillIcon; icon != "" {
		view.EquipIcons = append(view.EquipIcons, html.EscapeString(icon))
	}
	for _, equip := range role.BattleRecords.FinalEquips {
		if equip.EquipIcon != "" {
			view.EquipIcons = append(view.EquipIcons, html.EscapeString(equip.EquipIcon))
		}
	}
	return view
}

func resolveEvaluate(urls ...string) (label, icon string) {
	for _, candidate := range urls {
		if candidate == "" {
			continue
		}
		if matched := branchHashRe.FindStringSubmatch(candidate); matched != nil {
			if text, ok := branchTags[matched[1]]; ok {
				return text, candidate
			}
			return "分路评价", candidate
		}
		if matched := medalIconRe.FindStringSubmatch(candidate); matched != nil {
			medal := "金牌"
			if matched[1] == "silver" {
				medal = "银牌"
			}
			return medal + medalRoles[matched[2]], candidate
		}
	}
	return "", ""
}

func gradeKey(grade int) string {
	switch {
	case grade >= 10:
		return "s"
	case grade >= 8:
		return "a"
	case grade >= 6:
		return "b"
	default:
		return "c"
	}
}

func formatDamage(value int) string {
	if value <= 0 {
		return ""
	}
	if value >= 10000 {
		return fmt.Sprintf("%.1f万", float64(value)/10000)
	}
	return strconv.Itoa(value)
}

func formatMoney(value int) string {
	if value <= 0 {
		return "0"
	}
	if value > 1000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return strconv.Itoa(value)
}

// percentOf 参团率接口给的是 0~1 的小数。
func percentOf(value scalarValue) int {
	raw, err := value.Float64()
	if err != nil || raw <= 0 {
		return 0
	}
	return int(raw*100 + 0.5)
}

func formatStartTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}

func sideLabel(camp scalarValue) string {
	if camp.String() == "1" {
		return "红方"
	}
	if camp.String() == "2" {
		return "蓝方"
	}
	return "对局"
}

func teamOf(camp scalarValue) string { return camp.String() }

func dragonText(team battleDetailTeam) string {
	tyrant := team.Bdragon1.Int() + team.Bdragon2.Int() + team.Bdragon3.Int()
	overlord := team.Ldragon1.Int() + team.Ldragon2.Int()
	if tyrant == 0 && overlord == 0 {
		return ""
	}
	return fmt.Sprintf("暴君 %d · 主宰 %d", tyrant, overlord)
}

// ---------- 指令 ----------

// battleDetailCommand 拉取并渲染单场详情。
func battleDetailCommand(ctx context.Context, c sdk.Call, id string, cred *credential, item battleItem) error {
	client := newCampClient()
	var detail *battleDetailData
	var err error
	// 刚打完的局详情可能还没落库（head.acntCamp 为空），重试几次再说。
	for attempt := 0; attempt < 3; attempt++ {
		detail, err = client.getBattleDetail(ctx, id, cred.auth(), item)
		if err != nil || detail.Head.AcntCamp.String() != "" {
			break
		}
		time.Sleep(900 * time.Millisecond)
	}
	if err != nil {
		if errors.Is(err, ErrAuthInvalid) {
			return c.Reply(ctx, "营地登录态已失效，请重新发送 #营地wx全局登录 或 #营地QQ全局登录")
		}
		if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrProfileHidden) {
			return c.Reply(ctx, err.Error())
		}
		return c.Reply(ctx, "查询单场详情失败："+err.Error())
	}
	if detail.Head.AcntCamp.String() == "" {
		return c.Reply(ctx, "这一局的详情还没生成，稍后再试")
	}

	view := buildBattleDetailView(detail, item)
	image, renderErr := renderBattleDetail(ctx, view)
	if renderErr != nil {
		return c.Reply(ctx, "详情出图失败，稍后再试\n\n"+detailSummary(view))
	}
	return c.ReplyImageBytes(ctx, image)
}

func detailSummary(view battleDetailView) string {
	lines := []string{
		fmt.Sprintf("%s · %s（%s）", view.Result, view.MapName, view.UsedTime),
		fmt.Sprintf("%s %s / %s", view.MySide, view.MyTeam.KillDeadAssist, view.EnemyTeam.KillDeadAssist),
	}
	for _, player := range view.MyTeam.Players {
		lines = append(lines, fmt.Sprintf("%s %s %s", player.HeroName, player.KDA, player.Grade))
	}
	return strings.Join(lines, "\n")
}

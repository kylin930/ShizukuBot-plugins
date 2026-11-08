package gloryofkings

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"
	"time"

	"shizukubot/sdk"
)

const previewHeroIcon = "https://game.gtimg.cn/images/yxzj/img201606/heroimg/105/105.jpg"

func previewSkinImage(index int) string {
	return "https://game.gtimg.cn/images/yxzj/img201606/skin/hero-info/105/105-bigskin-1.jpg"
}

// resetJSONFields 把 fillSlices 填进去的占位字符串换成合法的空 JSON，
// 避免模板里的 JSON.parse 抛异常导致整段脚本中断。
func resetJSONFields(value reflect.Value, depth int) {
	if depth > 5 || !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Ptr:
		if value.IsNil() {
			return
		}
		resetJSONFields(value.Elem(), depth+1)
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if !field.CanSet() {
				continue
			}
			if strings.HasSuffix(typ.Field(i).Name, "Json") && field.Kind() == reflect.String {
				field.SetString("[]")
				continue
			}
			resetJSONFields(field, depth+1)
		}
	}
}

func fillSeasonPreview(v any) {
	page := v.(*seasonPageView)
	page.RoleName = "测试玩家"
	page.RoleIcon = previewHeroIcon
	page.ServerName = "微信区·安卓"
	page.SeasonName = "S40 赛季"
	page.TitleLabel = "赛季表现"
	page.SubLabel = "排位赛"
	page.JobName = "王者"
	page.JobLabel = "最强王者 25 星"
	page.RankingStar = "25"
	page.MasterScore = "1850"
	page.MasterRank = "第 1024 名"
	page.Score = "78.5"
	page.WinRate = "56.8%"
	page.GameCnt = "88"
	page.Branch = "对抗路"
	page.RankTag = "排位"
	page.TotalGames = "88"
	page.Heros = []seasonHeroView{
		{HeroName: "吕布", HeroIcon: previewHeroIcon, WinRate: "61.2%", GameCnt: "36"},
		{HeroName: "关羽", HeroIcon: previewHeroIcon, WinRate: "55.0%", GameCnt: "20"},
		{HeroName: "马超", HeroIcon: previewHeroIcon, WinRate: "48.6%", GameCnt: "18"},
	}
	page.BranchesJson = `[{"name":"对抗路","color":"#f5d76e","gameCnt":12,"winNum":"8","loseNum":"4","winRate":"66.7%"}]`
	page.RadarJson = `[{"label":"输出","value":"8200","pct":0.68},{"label":"生存","value":"7000","pct":0.58},{"label":"团战","value":"9100","pct":0.75},{"label":"发育","value":"6400","pct":0.53},{"label":"KDA","value":"7600","pct":0.63}]`
	page.Trend = []seasonTrendView{
		{Star: 18, Stars: 0, JobName: "星耀", JobColor: "#c97bdb", Time: 1695000000},
		{Star: 22, Stars: 2, JobName: "王者", JobColor: "#f5d76e", Time: 1695600000},
		{Star: 25, Stars: 0, JobName: "王者", JobColor: "#f5d76e", Time: 1696200000},
	}
	page.TrendJson = `[{"star":18,"stars":0,"jobName":"星耀","jobColor":"#c97bdb","time":1695000000},{"star":22,"stars":2,"jobName":"王者","jobColor":"#f5d76e","time":1695600000},{"star":25,"stars":0,"jobName":"王者","jobColor":"#f5d76e","time":1696200000}]`
	page.LanesJson = `[{"type":1,"name":"对抗路","color":"#f5d76e","gameCnt":"12","winRate":"66.7%","avgScore":"9.8","radar":[{"label":"输出","value":"8.2","pct":0.82}]}]`
	page.MasterBranchesJson = `[{"name":"对抗路","color":"#e0708a","gameCnt":10,"winNum":"6","loseNum":"4","winRate":"60.0%"}]`
}

func fillPeakPreview(v any) {
	page := v.(*peakPageView)
	page.RoleName = "测试玩家"
	page.RoleIcon = previewHeroIcon
	page.ServerName = "微信区·安卓"
	page.SeasonLabel = "S40 赛季"
	page.SubLabel = "巅峰赛"
	page.TrendNote = "近 30 场"
	page.GameCnt = "66"
	page.WinRate = "60.6%"
	page.WinNum = "40"
	page.LoseNum = "26"
	page.AvgScore = "96.3"
	page.Branch = "中路"
	page.HasHonor = true
	page.Honor = []honorItem{{Val: "1863", Key: "巅峰分"}, {Val: "66", Key: "场次"}}
	page.Stats = []honorItem{{Val: "96.3", Key: "场均评分"}, {Val: "4.2", Key: "KDA"}}
	page.HasBranches = true
	page.HasLanes = true
	page.HasOverallRadar = true
	page.BranchesJson = `[{"name":"中路","color":"#f5d76e","gameCnt":20,"winNum":"12","loseNum":"8","winRate":"60.0%"}]`
	page.LanesJson = `[{"type":2,"name":"中路","color":"#f5d76e","gameCnt":"20","winRate":"60.0%","avgScore":"9.6","radar":[{"label":"输出","value":"9.1","pct":0.91},{"label":"生存","value":"7.2","pct":0.60},{"label":"团战","value":"8.8","pct":0.73},{"label":"发育","value":"6.1","pct":0.51},{"label":"KDA","value":"7.9","pct":0.66}]}]`
	page.OverallJson = `{"type":0,"name":"全部","color":"#f5d76e","gameCnt":"66","winRate":"60.6%","avgScore":"9.63","radar":[{"label":"输出","value":"8600","pct":0.72},{"label":"生存","value":"7200","pct":0.60},{"label":"团战","value":"9200","pct":0.76},{"label":"发育","value":"6100","pct":0.51},{"label":"KDA","value":"7900","pct":0.66}]}`
	page.Trend = []peakTrendPoint{
		{Score: 1680, Time: 1695000000, JobName: "王者", JobColor: "#f5d76e"},
		{Score: 1725, Time: 1695600000, JobName: "王者", JobColor: "#f5d76e"},
		{Score: 1863, Time: 1696200000, JobName: "王者", JobColor: "#f5d76e"},
	}
	page.TrendJson = `[{"score":1680,"time":1695000000,"jobName":"王者","jobColor":"#f5d76e"},{"score":1725,"time":1695600000,"jobName":"王者","jobColor":"#f5d76e"},{"score":1863,"time":1696200000,"jobName":"王者","jobColor":"#f5d76e"}]`
	page.Heros = []seasonHeroView{{HeroName: "貂蝉", HeroIcon: previewHeroIcon, WinRate: "64.0%", GameCnt: "25"}}
}

func fillAllSeasonPreview(v any) {
	page := v.(*allSeasonView)
	page.Mode = "rank"
	page.ModeLabel = "排位赛"
	page.RoleName = "测试玩家"
	page.RoleIcon = previewHeroIcon
	page.ServerName = "微信区·安卓"
	page.TopJob = "王者"
	page.TopMasterScore = "1863"
	page.WangzheCnt = "3"
	page.TotalGameCnt = "520"
	page.TotalWinRate = "55.2%"
	page.Branch = "对抗路"
	page.SeasonCnt = 3
	page.ShownCnt = 3
	page.RangeText = "S38 - S40"
	page.Seasons = []allSeasonItem{
		{SeasonID: "40", SeasonName: "S40", IsCurrent: true, DateRange: "9.20 - 10.11", Games: "88", Wins: "50", WinRate: "56.8%", Streak: "7 连胜", Gold: "12", Silver: "4", AvgScore: "78.5", ScoreLabel: "78.5", JobName: "王者", ShortJobName: "王者", RankingStar: "25", JobColor: "#f5d76e", MasterScore: "1863", Star: 25},
		{SeasonID: "39", SeasonName: "S39", DateRange: "6.1 - 9.19", Games: "180", Wins: "102", WinRate: "56.7%", Streak: "5 连胜", Gold: "20", Silver: "8", AvgScore: "75.1", ScoreLabel: "75.1", JobName: "王者", ShortJobName: "王者", RankingStar: "31", JobColor: "#f5d76e", MasterScore: "1820", Star: 31},
	}
	page.Trend = []allSeasonTrendPoint{
		{Label: "S38", Value: 12, Tip: "星耀 12 星"},
		{Label: "S39", Value: 31, Tip: "最强王者 31 星"},
		{Label: "S40", Value: 25, Tip: "最强王者 25 星"},
	}
	page.TrendJson = `[{"label":"S38","value":12,"tip":"星耀 12 星"},{"label":"S39","value":31,"tip":"最强王者 31 星"},{"label":"S40","value":25,"tip":"最强王者 25 星"}]`
}

func fillScoreTrendPreview(v any) {
	page := v.(*scoreTrendView)
	page.Title = "巅峰趋势"
	page.SubText = "近 30 场巅峰赛"
	page.Username = "测试玩家"
	page.Avatar = previewHeroIcon
	page.FootText = "数据来自王者营地"
	page.Count = 30
	page.Win = 18
	page.Lose = 12
	page.WinRate = 60
	page.WinRateClass = "win"
	page.StartScore = 1600
	page.Current = 1863
	page.Peak = 1902
	page.Low = 1585
	page.DeltaText = "+263"
	page.DeltaClass = "up"
	page.MaxWinStreak = 7
	page.MaxLoseStreak = 3
	page.SpanDays = 21
	page.RangeText = "9.20 - 10.11"
	page.CoverText = "21 天"
	page.TrendJson = `[{"score":1600,"time":1695000000},{"score":1628,"time":1695300000},{"score":1710,"time":1695600000},{"score":1688,"time":1695900000},{"score":1863,"time":1696200000}]`
	page.DayRows = []scoreTrendDayRow{
		{Date: "10-11", Count: 4, Win: 3, Lose: 1, Score: 1863, DeltaText: "+46", DeltaClass: "up"},
		{Date: "10-10", Count: 5, Win: 2, Lose: 3, Score: 1817, DeltaText: "-22", DeltaClass: "down"},
	}
	page.RecentRows = []scoreTrendRecentRow{
		{HeroName: "貂蝉", HeroIcon: previewHeroIcon, ResultText: "胜", ResultClass: "win", KDA: "9/2/10", Score: 1863, DeltaText: "+18", DeltaClass: "up", TimeText: "10-11 21:30"},
		{HeroName: "不知火舞", HeroIcon: previewHeroIcon, ResultText: "负", ResultClass: "lose", KDA: "3/6/4", Score: 1845, DeltaText: "-15", DeltaClass: "down", TimeText: "10-11 20:55"},
	}
	page.HeroRows = []scoreTrendHeroRow{
		{HeroName: "貂蝉", HeroID: "141", HeroIcon: previewHeroIcon, Count: 12, WinRate: 67, NetText: "+168", NetClass: "up"},
		{HeroName: "不知火舞", HeroID: "157", HeroIcon: previewHeroIcon, Count: 8, WinRate: 50, NetText: "+32", NetClass: "up"},
	}
}

func fillRankTrendPreview(v any) {
	page := v.(*rankTrendView)
	page.Title = "段位趋势"
	page.SubText = "近 30 场排位赛"
	page.Username = "测试玩家"
	page.Avatar = previewHeroIcon
	page.FootText = "数据来自王者营地"
	page.Count = 30
	page.RankedCount = 24
	page.Win = 17
	page.Lose = 13
	page.WinRate = 56.7
	page.WinRateClass = "win"
	page.StartLabel = "钻石 I"
	page.CurrentLabel = "最强王者 25 星"
	page.CurrentShort = "王者 25"
	page.PeakLabel = "最强王者 31 星"
	page.LowLabel = "星耀 III"
	page.IsKing = true
	page.StarText = "25 星"
	page.DeltaText = "+8 星"
	page.DeltaClass = "up"
	page.UpSteps = 3
	page.DownSteps = 1
	page.StarUpCount = 12
	page.StarDownCount = 4
	page.StepText = "净升 2 档"
	page.StarStepText = "净赚 8 星"
	page.MaxWinStreak = 6
	page.MaxLoseStreak = 2
	page.SpanDays = 21
	page.RangeText = "9.20 - 10.11"
	page.CoverText = "21 天"
	page.SegCount = 3
	page.SegNote = "跨 3 个大段"
	page.TrendJson = `[{"level":100,"time":1695000000,"label":"钻石 I","full":"钻石 I","ranked":true,"seg":0},{"level":110,"time":1695300000,"label":"星耀 V","full":"星耀 V","ranked":true,"seg":"星耀"},{"level":118,"time":1695600000,"label":"星耀 I","full":"星耀 I","ranked":true,"seg":"星耀"},{"level":120,"time":1695900000,"label":"王者","full":"最强王者 17 星","ranked":true,"seg":"王者"},{"level":124,"time":1696200000,"label":"王者","full":"最强王者 25 星","ranked":true,"seg":"王者"}]`
	page.LevelJson = `[{"level":100,"label":"钻石 I"},{"level":110,"label":"星耀 V"},{"level":118,"label":"星耀 I"},{"level":120,"label":"王者"},{"level":124,"label":"王者 25"}]`
	page.DayRows = []rankTrendDayRow{
		{Date: "10-11", Count: 4, RankedCount: 3, Win: 3, Lose: 1, Label: "王者 25", DeltaText: "+2 星", DeltaClass: "up"},
		{Date: "10-10", Count: 5, RankedCount: 4, Win: 2, Lose: 3, Label: "王者 23", DeltaText: "-1 星", DeltaClass: "down"},
	}
	page.RecentRows = []rankTrendRecentRow{
		{HeroName: "吕布", HeroIcon: previewHeroIcon, ResultText: "胜", ResultClass: "win", KDA: "7/3/9", Mode: "排位", Ranked: true, Label: "王者 25", DeltaText: "+1 星", DeltaClass: "up", TimeText: "10-11 21:30"},
		{HeroName: "关羽", HeroIcon: previewHeroIcon, ResultText: "负", ResultClass: "lose", KDA: "2/5/3", Mode: "排位", Ranked: true, Label: "王者 24", DeltaText: "-1 星", DeltaClass: "down", TimeText: "10-11 20:50"},
	}
	page.ModeRows = []rankTrendModeRow{
		{Mode: "排位赛", Count: 24, Win: 14, WinRate: 58, Ranked: true},
		{Mode: "巅峰赛", Count: 6, Win: 3, WinRate: 50},
	}
}

func fillMyHeroPreview(v any) {
	page := v.(*myHeroView)
	page.YdID = "12345678"
	page.HeroCount = 118
	page.OwnCount = 96
	page.ShownCount = 6
	page.TotalPlay = 980
	page.TotalRate = "55.6%"
	names := []string{"吕布", "关羽", "马超", "貂蝉", "不知火舞", "妲己"}
	for index, name := range names {
		page.Heroes = append(page.Heroes, myHeroCard{
			Name:         name,
			SubName:      "",
			HeroType:     "战士",
			SkilledText:  "传说",
			HonorText:    "省级",
			ImgURL:       previewHeroIcon,
			PlayNum:      120 - index*12,
			WinRate:      "58.3%",
			FightPower:   9800 - index*450,
			FightColor:   fightColor(9800 - index*450),
			HistoryPower: 10200 - index*400,
			HistoryColor: fightColor(10200 - index*400),
			SeasonPower:  6800 - index*300,
			SeasonColor:  fightColor(6800 - index*300),
		})
	}
}

func fillHeroListPreview(v any) {
	page := v.(*heroListView)
	page.YdID = "12345678"
	page.RoleName = "测试玩家"
	page.RoleIcon = previewHeroIcon
	page.RoleArea = "微信区·安卓"
	page.ScopeName = "当前赛季"
	page.ShowModes = true
	page.HeroCount = 5
	names := []string{"吕布", "关羽", "马超", "貂蝉", "妲己"}
	for index, name := range names {
		page.Heroes = append(page.Heroes, heroListCard{
			Name:       name,
			ImgURL:     previewHeroIcon,
			RankCnt:    "36 场",
			RankRate:   "58.3%",
			PeakCnt:    "8 场",
			PeakRate:   "50.0%",
			FightPower: "9800",
			FightColor: fightColor(9800 - index*450),
			HonorIcon:  honorIconFile(3),
			HonorText:  "省级",
		})
	}
}

func fillSkinWallPreview(v any) {
	page := v.(*skinWallView)
	page.YdID = "12345678"
	page.Nickname = "测试玩家"
	page.Owned = "320"
	page.TotalSkinNum = "610"
	page.NotForSell = "12"
	page.TotalValue = "268880"
	page.PageInfo = "第 1/7 页"
	page.QualityStats = []qualityStatView{{Label: "荣耀典藏", Num: 2}, {Label: "无双至尊", Num: 5}, {Label: "传说限定", Num: 18}}
	names := []string{"吕布", "关羽", "马超", "貂蝉", "不知火舞", "妲己", "鲁班七号", "孙尚香", "李白"}
	for band := 0; band < 3; band++ {
		row := 1 + band*2
		page.SkinList = append(page.SkinList, skinWallItem{
			SkinID: "1", SkinName: names[band] + "·典藏", HeroName: names[band],
			ImgURL: previewSkinImage(band), TierText: "荣耀典藏", Big: true, Row: row, Col: 1,
		})
		for i := 0; i < 8; i++ {
			col := 3 + i%4
			line := row + i/4
			page.SkinList = append(page.SkinList, skinWallItem{
				SkinID: "2", SkinName: names[i] + "·传说", HeroName: names[i],
				ImgURL: previewSkinImage(i), TierText: "传说限定", Row: line, Col: col,
			})
		}
	}
}

func fillSkinMissingPreview(v any) {
	page := v.(*skinMissingView)
	page.Title = "缺皮肤"
	page.SubText = "还差 290 款"
	page.Username = "测试玩家"
	page.ScopeText = "微信区·安卓"
	page.WorthText = "价值 128880 点券"
	page.Has = 320
	page.Missing = 290
	page.Total = 610
	page.TotalKey = "290"
	page.Pct = "47.5%"
	page.Tiers = []missingTier{{Label: "荣耀典藏", Has: 1, Lack: 4, Pct: 20}, {Label: "传说限定", Has: 18, Lack: 22, Pct: 45}}
	page.SkinTitle = "重点缺失"
	page.SkinTip = "按品质与价值排序"
	page.Skins = []missingSkinCard{
		{Name: "吕布·天魔缭乱", Sub: "吕布", Price: "1688", Cover: previewSkinImage(0)},
		{Name: "关羽·冰锋战神", Sub: "关羽", Price: "888", Cover: previewSkinImage(1)},
		{Name: "妲己·女仆咖啡", Sub: "妲己", Price: "888", Cover: previewSkinImage(2)},
	}
	page.More = 12
	page.HeroTop = []missingHeroTop{{Hero: "吕布", Num: 6}, {Hero: "貂蝉", Num: 5}}
	page.FullText = "完整清单略"
	page.FootText = "数据来自王者营地"
}

func fillHeroSkinPreview(v any) {
	page := v.(*heroSkinView)
	page.HeroName = "妲己"
	page.SkinCount = 6
	names := []string{"原皮", "女仆咖啡", "魅力维加斯", "少女阿狸", "仙境爱丽丝", "热情桑巴"}
	for index, name := range names {
		page.SkinData = append(page.SkinData, heroSkinItem{Name: name, URL: previewSkinImage(index), Index: index})
	}
}

func fillCampSignPreview(v any) {
	page := v.(*signPageView)
	page.Avatar = previewHeroIcon
	page.Username = "测试玩家"
	page.DateText = "2026-11-07 周五"
	page.AccountCount = 2
	page.Title = "营地签到"
	page.SubText = "王者荣耀 · 营地福利中心"
	page.TodayClass = "ok"
	page.TodayVal = "2/2"
	page.MaxSeq = "3 天"
	page.TotalSign = "12 天"
	page.GiftCount = "2"
	page.GiftKey = "今日奖励"
	page.FootText = "数据来自王者营地 · 每天 0 点重置"
	page.Accounts = []signAccountView{
		{
			Name:       "测试玩家",
			CampID:     "1234567890",
			StateClass: "ok",
			StateText:  "已签到",
			HasWeek:    true,
			RoleName:   "· 最强王者",
			Days: []signDayView{
				{Week: "一", Mark: "√", Gift: "铭文碎片", Cls: "done", Tag: "已领"},
				{Week: "二", Mark: "√", Gift: "钻石", Cls: "done", Tag: "已领"},
				{Week: "三", Mark: "√", Gift: "营地币", Cls: "today"},
				{Week: "四", Mark: "·", Gift: "皮肤碎片"},
				{Week: "五", Mark: "·", Gift: "英雄碎片"},
				{Week: "六", Mark: "·", Gift: "钻石"},
				{Week: "日", Mark: "礼", Gift: "宝箱", Cls: "gift"},
			},
			Gifts: []signGiftView{
				{Name: "铭文碎片", Desc: "×5", Icon: previewHeroIcon, Num: "5"},
				{Name: "钻石", Desc: "×20", Icon: previewHeroIcon, Num: "20"},
			},
			Totals: []signTotalView{
				{Days: "3", Percent: 42, Cls: "active", Name: "钻石", Desc: "累计 3 天", StatusText: "进行中", StatusClass: "doing"},
				{Days: "7", Percent: 0, Name: "皮肤碎片", Desc: "累计 7 天", StatusText: "未开始", StatusClass: "idle"},
			},
			ActedText:   "浏览资讯、点赞、分享",
			ClaimedText: "每日任务 ×3",
			CoinText:    "25 营地币",
			Note:        "签到成功",
		},
	}
}

func fillCampExchangePreview(v any) {
	page := v.(*exchangePageView)
	page.Avatar = previewHeroIcon
	page.Who = "测试玩家"
	page.CoinText = "225"
	page.EndTime = "2026-11-30"
	page.Groups = []exchangeGroupView{
		{
			Title: "本期",
			Tip:   "活动周期内限兑",
			Gifts: []exchangeGiftView{
				{No: 1, Icon: previewHeroIcon, Name: "改名卡", LimitText: "限兑 1 份", Price: 100, ShowLeft: true, LeftText: "剩 12 份"},
				{No: 2, SoldOut: true, Icon: previewHeroIcon, Name: "皮肤碎片 ×5", LimitText: "限兑 1 份", Price: 60, ShowLeft: true, LeftText: "剩 0 份"},
				{No: 3, MineUsed: true, Icon: previewHeroIcon, Name: "英雄碎片 ×3", LimitText: "限兑 1 份", Price: 40},
				{No: 4, Icon: previewHeroIcon, Name: "铭文碎片 ×200", LimitText: "限兑 2 份", Price: 30},
			},
		},
		{
			Title: "本日",
			Tip:   "每天 00:00 重置",
			Gifts: []exchangeGiftView{
				{No: 5, Icon: previewHeroIcon, Name: "金币 ×200", LimitText: "限兑 1 份", Price: 10, ShowLeft: true, LeftText: "剩 99 份"},
				{No: 6, Icon: previewHeroIcon, Name: "体验卡", LimitText: "限兑 1 份", Price: 20},
			},
		},
	}
}

func fillCampPrivacyPreview(v any) {
	page := v.(*privacyPageView)
	page.Avatar = previewHeroIcon
	page.Who = "测试玩家"
	page.CampID = "1234567890"
	page.BattleText = "别人现在看不到你的战绩"
	page.BattleHidden = true
	page.GeneratedAt = "2026-11-07 21:30:00"
	page.Classes = []privacyClassView{
		{
			ClassName: "战绩",
			Items: []privacyCardRow{
				{No: 9, Name: "我的比赛", Verified: true, Details: []privacyTag{{Name: "全部联系人"}, {Name: "陌生人"}, {Name: "好友"}}},
				{No: 10, Name: "我的比赛详情", Verified: true, Details: []privacyTag{{Name: "全部联系人"}, {Name: "陌生人"}, {Name: "好友"}}},
				{No: 12, Name: "精彩时刻", Details: []privacyTag{{Name: "全部联系人"}, {Name: "陌生人"}}},
				{No: 14, Name: "观战", Details: []privacyTag{{Name: "全部联系人"}, {Name: "允许观战", Locked: true}}},
			},
		},
		{
			ClassName: "社交",
			Items: []privacyCardRow{
				{No: 6, Name: "个人主页", Verified: true, Details: []privacyTag{{Name: "全部联系人"}, {Name: "陌生人"}}},
				{No: 13, Name: "亲密关系", Details: []privacyTag{{Name: "全部联系人"}, {Name: "好友"}, {Name: "仅自己"}}},
			},
		},
	}
}

func fillCampOnlinePreview(v any) {
	page := v.(*onlinePageView)
	page.Avatar = previewHeroIcon
	page.Who = "测试玩家"
	page.GeneratedAt = "2026-11-07 21:30:00"
	page.Rows = []onlineRow{
		{No: 1, CampID: "1234567890", Name: "测试玩家", Key: onlineKeyPlaying, Label: "对局中", HeroIcon: previewHeroIcon, Mode: "王者峡谷", Minutes: "已进行 12 分钟"},
		{No: 2, CampID: "1234567891", Name: "另一个号", Key: onlineKeyIdle, Label: "游戏中"},
		{No: 3, CampID: "1234567892", Name: "小号", Key: onlineKeyOnline, Label: "在线"},
		{No: 4, CampID: "1234567893", Name: "尘封的号", Key: onlineKeyOffline, Label: "离线"},
		{No: 5, CampID: "1234567894", Name: "隐藏号", Key: onlineKeyUnknown, Label: "未公开在线状态", Note: "营地没有返回在线状态"},
	}
}

func fillCampBattleDetailPreview(v any) {
	page := v.(*battleDetailView)
	page.Result = "胜利"
	page.ResultEn = "VICTORY"
	page.MapName = "王者峡谷"
	page.MatchDesc = "排位赛"
	page.Tips = "实力局 · 逆风翻盘"
	page.StartTime = "2026-11-07 21:30"
	page.UsedTime = "12 分钟"
	page.MySide = "蓝方"
	page.EnemySide = "红方"
	page.MyDragons = "暴君 3 · 主宰 1"
	page.EnemyDragons = "暴君 1 · 主宰 0"
	page.MyTeam.Players = nil
	page.EnemyTeam.Players = nil

	names := []string{"测试玩家", "队友甲", "队友乙", "队友丙", "队友丁"}
	heroes := []string{"妲己", "吕布", "关羽", "马超", "貂蝉"}
	for index := range names {
		player := detailPlayerView{
			Name: names[index], HeroName: heroes[index], HeroIcon: previewHeroIcon,
			IsMe:  index == 0,
			KDA:   "8 / 2 / 10",
			Grade: "12.5", GradeKey: gradeKey(12),
			Money: formatMoney(12800), Hurt: formatDamage(42800), HurtRate: 32,
			BeHurt: formatDamage(18600), JoinRate: 75,
			MaxTags:    []string{"经济最高", "参团最高"},
			EquipIcons: []string{previewHeroIcon, previewHeroIcon, previewHeroIcon, previewHeroIcon, previewHeroIcon, previewHeroIcon},
			EvalTag:    "顶级中路",
		}
		if index == 0 {
			player.MvpTag = "MVP"
		}
		if index == 1 {
			player.GradeKey = "a"
		}
		page.MyTeam.Players = append(page.MyTeam.Players, player)
	}
	for index := range names {
		player := detailPlayerView{
			Name: "对手" + names[index], HeroName: heroes[(index+2)%len(heroes)], HeroIcon: previewHeroIcon,
			KDA:   "3 / 6 / 4",
			Grade: "7.2", GradeKey: "b",
			Money: formatMoney(9600), Hurt: formatDamage(19800), HurtRate: 18,
			BeHurt: formatDamage(24600), JoinRate: 55,
			EquipIcons: []string{previewHeroIcon, previewHeroIcon, previewHeroIcon, previewHeroIcon, previewHeroIcon},
		}
		if index == 0 {
			player.MvpTag = "SVP"
		}
		page.EnemyTeam.Players = append(page.EnemyTeam.Players, player)
	}
	page.MyTeam.Side = "蓝方"
	page.MyTeam.Win = true
	page.EnemyTeam.Win = false
	page.MyTeam.KillDeadAssist = "32 / 12 / 76"
	page.MyTeam.Money = "72.6k"
	page.MyTeam.Tower = "9"
	page.EnemyTeam.Side = "红方"
	page.EnemyTeam.KillDeadAssist = "12 / 32 / 30"
	page.EnemyTeam.Money = "58.1k"
	page.EnemyTeam.Tower = "3"
	page.GeneratedAt = "2026-11-07 22:00:00"
}

// TestRenderPreview 用样例数据把 P2/P3 模板真实渲染成图片，仅用于人工核对排版。
// 运行：GOK_PREVIEW=1 go test -run TestRenderPreview -v
func TestRenderPreview(t *testing.T) {
	if os.Getenv("GOK_PREVIEW") == "" {
		t.Skip("set GOK_PREVIEW=1 to render previews")
	}
	outDir := filepath.Join(os.TempDir(), "gok_preview")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer sdk.CloseRenderBrowser()

	cases := []struct {
		name  string
		tpl   *template.Template
		view  any
		width int
		fill  func(any)
	}{
		{"SeasonPage", tplSeasonPage, seasonPageView{}, 1050, fillSeasonPreview},
		{"PeakPerformance", tplPeakPage, peakPageView{}, 1050, fillPeakPreview},
		{"AllSeasonPerformance", tplAllSeason, allSeasonView{}, 1050, fillAllSeasonPreview},
		{"ScoreTrend", tplScoreTrend, scoreTrendView{}, 1050, fillScoreTrendPreview},
		{"RankTrend", tplRankTrend, rankTrendView{}, 1050, fillRankTrendPreview},
		{"MyHeroList", tplMyHeroList, myHeroView{}, 1000, fillMyHeroPreview},
		{"HeroList", tplHeroList, heroListView{}, 1000, fillHeroListPreview},
		{"SkinWall", tplSkinWall, skinWallView{}, 1400, fillSkinWallPreview},
		{"SkinMissing", tplSkinMissing, skinMissingView{}, 1050, fillSkinMissingPreview},
		{"HeroSkin", tplHeroSkin, heroSkinView{}, 1400, fillHeroSkinPreview},
		{"CampSign", tplCampSign, signPageView{}, 1050, fillCampSignPreview},
		{"CampExchange", tplCampExchange, exchangePageView{}, 1050, fillCampExchangePreview},
		{"CampPrivacy", tplCampPrivacy, privacyPageView{}, 1050, fillCampPrivacyPreview},
		{"CampOnline", tplCampOnline, onlinePageView{}, 1050, fillCampOnlinePreview},
		{"CampBattleDetail", tplBattleDetail, battleDetailView{}, 1050, fillCampBattleDetailPreview},
	}

	only := os.Getenv("GOK_PREVIEW_ONLY")
	for _, item := range cases {
		if only != "" && !strings.Contains(item.name, only) {
			continue
		}
		value := reflect.New(reflect.TypeOf(item.view))
		fillSlices(value.Elem(), 0)
		resetJSONFields(value.Elem(), 0)
		item.fill(value.Interface())

		body, err := executeTemplate(item.tpl, value.Elem().Interface())
		if err != nil {
			t.Errorf("%s: %v", item.name, err)
			continue
		}
		if strings.Contains(body, "{{") {
			t.Errorf("%s: 模板残留未渲染标记", item.name)
		}
		if err := os.WriteFile(filepath.Join(outDir, item.name+".html"), []byte(inlineImages(body)), 0o644); err != nil {
			t.Error(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		img, err := renderCard(ctx, item.tpl, value.Elem().Interface(), item.width)
		cancel()
		if err != nil {
			t.Errorf("%s 渲染失败: %v", item.name, err)
			continue
		}
		path := filepath.Join(outDir, item.name+".jpg")
		if err := os.WriteFile(path, img, 0o644); err != nil {
			t.Error(err)
			continue
		}
		t.Logf("%s -> %s (%d bytes)", item.name, path, len(img))
	}
}

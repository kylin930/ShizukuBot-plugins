package toutai

import (
	"sort"
	"strconv"
	"strings"
)

var blessings = []string{
	"愿你此生，长安顺遂。",
	"愿你所往之处，皆有暖灯。",
	"山高水远，愿有归处。",
	"这一趟人间，好好去过。",
	"愿你被这世界温柔以待。",
}

var laments = []string{
	"魂灯未燃，已随风散。",
	"尚未睁眼，人间已远。",
	"来路太短，未及道别。",
	"命簿之上，添了一笔空白。",
}

// entry 生成「标签　内容」；标签字间插入全角空格以对齐。
func entry(label, value string) string {
	return "　" + strings.Join(strings.Split(label, ""), "　") + "　" + value
}

func textTitle(title string) string {
	return strings.TrimSuffix(title, "榜") + "排行榜"
}

func rankingsText(records []record, count int, options rankingOptions) string {
	scored := rankRows(records, options.pick)
	if len(scored) == 0 {
		return strings.Join([]string{
			"📋 " + textTitle(options.title),
			"尚无人在此留下痕迹。",
			"发送「" + options.tip + "」走出第一个名字。",
		}, "\n")
	}
	if count < 1 {
		count = 1
	}
	rows := scored
	if len(rows) > count {
		rows = rows[:count]
	}
	selfRank := 0
	for _, row := range scored {
		if row.UserID == options.selfUserID {
			selfRank = row.Rank
			break
		}
	}
	standing := "你尚未上榜"
	if selfRank > 0 {
		standing = "你位居第 " + strconv.Itoa(selfRank)
	}
	lines := []string{"📋 " + textTitle(options.title)}
	for _, row := range rows {
		lines = append(lines, "• "+strconv.Itoa(row.Rank)+" "+esc(trimUsername(row.Username))+" "+strconv.Itoa(row.Value)+" "+options.valueLabel)
	}
	lines = append(lines, standing+" · 发送「"+options.tip+"」刷新你的名次。")
	return strings.Join(lines, "\n")
}

func chinaOverviewText(username string, analysis chinaAnalysis, userRank, stillbirthsRank, stillbirths int) string {
	attempts := analysis.totalCount + stillbirths
	lines := []string{
		"📋 中国投胎 · 记录总览",
		"　命主 " + esc(username) + " · 降生 " + strconv.Itoa(analysis.totalCount) + " 次 · 夭折 " + strconv.Itoa(stillbirths) + " 次",
		"　存活率 " + percentText(analysis.totalCount, attempts) + " · 履及 " + strconv.Itoa(analysis.uniqueProvinces) + " 省 · 最常降生 " + orDash(analysis.favourite.Name),
	}
	if userRank > 0 || stillbirthsRank > 0 {
		lines = append(lines, "　降生 "+rankText(userRank)+" · 夭折 "+rankText(stillbirthsRank))
	}
	lines = append(lines, "发送「toutai.投胎中国」再走一遭。")
	return strings.Join(lines, "\n")
}

func worldOverviewText(username string, analysis worldAnalysis, userRank, stillbirthsRank, stillbirths int) string {
	attempts := analysis.totalCount + stillbirths
	lines := []string{
		"📋 世界投胎 · 记录总览",
		"　命主 " + esc(username) + " · 降生 " + strconv.Itoa(analysis.totalCount) + " 次 · 夭折 " + strconv.Itoa(stillbirths) + " 次",
		"　存活率 " + percentText(analysis.totalCount, attempts) + " · 履及 " + strconv.Itoa(analysis.uniqueCountries) + " 国 · 最常降生 " + orDash(analysis.favourite.Name),
	}
	if userRank > 0 || stillbirthsRank > 0 {
		lines = append(lines, "　降生 "+rankText(userRank)+" · 夭折 "+rankText(stillbirthsRank))
	}
	lines = append(lines, "发送「toutai.投胎世界」再走一遭。")
	return strings.Join(lines, "\n")
}

func chinaBirthHistoryText(username string, results []birthResultInChina) string {
	rows := append([]birthResultInChina(nil), results...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index > rows[j].Index })
	if len(rows) > 2 {
		rows = rows[:2]
	}
	lines := []string{
		"📋 中国投胎 · 降生纪年",
		"　命主 " + esc(username) + " · 共 " + strconv.Itoa(len(results)) + " 次",
	}
	for _, rec := range rows {
		parts := []string{rec.Province}
		if rec.Category != "" {
			parts = append(parts, rec.Category)
		}
		if rec.Order != "" {
			parts = append(parts, orderText(rec.Order))
		}
		lines = append(lines, "• 第 "+strconv.Itoa(rec.Index)+" 次 "+translateGender(rec.Gender)+" · "+strings.Join(parts, " · "))
	}
	lines = append(lines, "发送「toutai.中国投胎记录.总览」查看全貌。")
	return strings.Join(lines, "\n")
}

func worldBirthHistoryText(username string, results []birthResultInWorld) string {
	rows := append([]birthResultInWorld(nil), results...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index > rows[j].Index })
	if len(rows) > 2 {
		rows = rows[:2]
	}
	lines := []string{
		"📋 世界投胎 · 降生纪年",
		"　命主 " + esc(username) + " · 共 " + strconv.Itoa(len(results)) + " 次",
	}
	for _, rec := range rows {
		lines = append(lines, "• 第 "+strconv.Itoa(rec.Index)+" 次 "+rec.DictContinent+" · "+rec.DictName)
	}
	lines = append(lines, "发送「toutai.世界投胎记录.总览」查看全貌。")
	return strings.Join(lines, "\n")
}

func worldDemiseHistoryText(username string, records []demiseRecord) string {
	rows := append([]demiseRecord(nil), records...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index > rows[j].Index })
	if len(rows) > 2 {
		rows = rows[:2]
	}
	lines := []string{
		"📋 世界投胎 · 夭折历史",
		"　命主 " + esc(username) + " · 共 " + strconv.Itoa(len(records)) + " 笔",
	}
	for _, rec := range rows {
		lines = append(lines, "• 第 "+strconv.Itoa(rec.Index)+" 次 "+rec.DictContinent+" · "+rec.DictName)
	}
	lines = append(lines, "发送「toutai.世界投胎记录.总览」查看全貌。")
	return strings.Join(lines, "\n")
}

func regionDistributionText(username string, results []birthResultInChina) string {
	total := len(results)
	sorted := provinceCounts(results)
	lines := []string{
		"📋 中国投胎 · 地区分布",
		"　命主 " + esc(username) + " · 共 " + strconv.Itoa(total) + " 次 · " + strconv.Itoa(len(sorted)) + " 省",
	}
	for i, pair := range sorted {
		if i >= 2 {
			break
		}
		lines = append(lines, "• "+pair[0].(string)+" "+strconv.Itoa(pair[1].(int))+" 次 · 占 "+percentText(pair[1].(int), total))
	}
	lines = append(lines, "发送「toutai.中国投胎记录.总览」查看全貌。")
	return strings.Join(lines, "\n")
}

func genderDistributionText(username string, results []birthResultInChina) string {
	total := len(results)
	male := 0
	for _, rec := range results {
		if rec.Gender == "male" {
			male++
		}
	}
	female := total - male
	ratio := "—"
	if female > 0 {
		ratio = formatFloat(float64(male)/float64(female), 2)
	}
	return strings.Join([]string{
		"📋 中国投胎 · 性别分布",
		"　命主 " + esc(username) + " · 共 " + strconv.Itoa(total) + " 次 · 性别比 " + ratio,
		"• 男孩 " + strconv.Itoa(male) + " 次 · 占 " + percentText(male, total),
		"• 女孩 " + strconv.Itoa(female) + " 次 · 占 " + percentText(female, total),
		"发送「toutai.中国投胎记录.总览」查看全貌。",
	}, "\n")
}

func firstAppearanceText(username string, results []birthResultInChina, totalProvinces int) string {
	provinces := provinceFirstAppearances(results)
	cell := func(value *int, gender string) string {
		if value == nil {
			return translateGender(gender) + " 未逢"
		}
		return translateGender(gender) + " 第 " + strconv.Itoa(*value) + " 次"
	}
	lines := []string{
		"📋 中国投胎 · 初见图鉴",
		"　命主 " + esc(username) + " · 已踏足 " + strconv.Itoa(len(provinces)) + " / " + strconv.Itoa(totalProvinces) + " 省",
	}
	for i, item := range provinces {
		if i >= 2 {
			break
		}
		lines = append(lines, "• "+item.Province+" "+cell(item.Entry.Male, "male")+" · "+cell(item.Entry.Female, "female"))
	}
	lines = append(lines, "发送「toutai.中国投胎记录.总览」查看全貌。")
	return strings.Join(lines, "\n")
}

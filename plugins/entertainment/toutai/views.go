package toutai

import (
	"fmt"
	"strconv"
	"strings"
)

// renderGenderDistribution 性别分布：环图加两张数据卡。
func renderGenderDistribution(username string, results []birthResultInChina) string {
	total := len(results)
	male := 0
	for _, result := range results {
		if result.Gender == "male" {
			male++
		}
	}
	female := total - male
	ratio := "—"
	if female > 0 {
		ratio = formatFloat(float64(male)/float64(female), 2)
	}

	maleRatio := 0.0
	femaleRatio := 0.0
	if total > 0 {
		maleRatio = float64(male) / float64(total)
		femaleRatio = float64(female) / float64(total)
	}
	body := `<div class="gender">
    ` + donut([]donutPart{
		{value: male, color: toneSolid("azure")},
		{value: female, color: toneSolid("rose")},
	}, strconv.Itoa(total), "次降生") + `
    <div class="gender-side">
        ` + statCard(statOptions{label: "男孩", value: strconv.Itoa(male), unit: "次", tone: "azure", note: "占 " + percentText(male, total), ratio: &maleRatio}) + `
        ` + statCard(statOptions{label: "女孩", value: strconv.Itoa(female), unit: "次", tone: "rose", note: "占 " + percentText(female, total), ratio: &femaleRatio}) + `
        <p class="ratio">性别比　男 <b>` + ratio + `</b> ： 女 <b>1.00</b></p>
    </div>
</div>`

	return buildPage(pageOptions{
		docTitle:      "中国投胎性别分布",
		title:         "中国投胎 · 性别分布",
		subtitle:      subtitleOf(username, "阴阳各半，皆是缘法"),
		seal:          "阴阳",
		body:          section("男女之数", body, "合计 "+strconv.Itoa(total)+" 次"),
		colophonRight: "男 " + strconv.Itoa(male) + " · 女 " + strconv.Itoa(female),
		style:         genderStyle,
	})
}

const genderStyle = `
.gender { display: flex; align-items: center; gap: 32px; }
.donut { position: relative; flex: none; width: 248px; height: 248px; }
.donut svg { display: block; width: 100%; height: 100%; }
.donut .c { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.donut .c b { font-size: 56px; line-height: 60px; font-weight: 600; letter-spacing: -.01em; }
.donut .c span { font-size: 16px; line-height: 22px; color: var(--md-sys-color-on-surface-variant); }
.gender-side { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 12px; }
.ratio { margin: 4px 0 0; text-align: center; font-size: 16px; line-height: 24px; color: var(--md-sys-color-on-surface-variant); }
.ratio b { font-size: 20px; font-weight: 600; color: var(--md-sys-color-on-surface); }
`

type rankingOptions struct {
	title      string
	seal       string
	valueLabel string
	tone       string
	pick       func(record) int
	selfUserID string
	tip        string
}

// renderRankings 排行榜：名次、玩家、条形长短与次数。
func renderRankings(records []record, count int, options rankingOptions) string {
	scored := rankRows(records, options.pick)
	rows := scored
	if len(rows) > count {
		rows = rows[:count]
	}
	top := 1
	if len(rows) > 0 && rows[0].Value > 0 {
		top = rows[0].Value
	}
	selfRank := 0
	for _, row := range scored {
		if row.UserID == options.selfUserID {
			selfRank = row.Rank
			break
		}
	}

	var list strings.Builder
	for _, row := range rows {
		isSelf := row.UserID == options.selfUserID
		selfClass := ""
		if isSelf {
			selfClass = " self"
		}
		meTag := ""
		if isSelf {
			meTag = `<i class="me-tag">你</i>`
		}
		fmt.Fprintf(&list, `<tr class="t-%s%s">
    <td class="no">%s</td>
    <td class="name">%s%s</td>
    <td class="bar">%s</td>
    <td class="val">%d<small>%s</small></td>
</tr>`, options.tone, selfClass, medal(row.Rank), esc(trimUsername(row.Username)), meTag,
			track(float64(row.Value)/float64(top)), row.Value, options.valueLabel)
	}

	body := ""
	aside := ""
	if len(rows) > 0 {
		body = `<table class="ledger bars"><tbody>` + list.String() + `</tbody></table>`
		aside = "前 " + strconv.Itoa(len(rows)) + " 位"
	} else {
		body = `<div class="empty"><b>榜上无名</b>尚无人在此留下痕迹。<br>发送「` + options.tip + `」走出第一个名字。</div>`
	}
	standing := "阁下尚未上榜"
	if selfRank > 0 {
		standing = "阁下位居第 " + strconv.Itoa(selfRank)
	}
	subtitle := strings.Join([]string{"列位共 " + strconv.Itoa(len(scored)) + " 人", standing}, `<span class="sep">·</span>`)
	colophon := "虚位以待"
	if len(rows) > 0 {
		colophon = "榜首 " + esc(trimUsername(rows[0].Username)) + " · " + strconv.Itoa(rows[0].Value) + " " + options.valueLabel
	}

	return buildPage(pageOptions{
		docTitle:      options.title,
		title:         options.title,
		subtitle:      subtitle,
		seal:          options.seal,
		body:          section("名次录", body, aside),
		colophonRight: colophon,
	})
}

// distributionRow 分布表的一行：序号、名称、比例条、次数、占比。
func distributionRow(index int, name string, count, top, total int) string {
	ratio := 0.0
	if top > 0 {
		ratio = float64(count) / float64(top)
	}
	return fmt.Sprintf(`<tr class="t-ink">
    <td class="no">%d</td>
    <td class="name">%s</td>
    <td class="bar">%s</td>
    <td class="val">%d<small>次</small></td>
    <td class="pct">%s</td>
</tr>`, index, name, track(ratio), count, percentText(count, total))
}

// renderRegionDistribution 地区分布：省份条形榜。
func renderRegionDistribution(username string, results []birthResultInChina) string {
	total := len(results)
	sorted := provinceCounts(results)
	top := 1
	if len(sorted) > 0 {
		top = sorted[0][1].(int)
	}
	var bars strings.Builder
	for i, pair := range sorted {
		bars.WriteString(distributionRow(i+1, pair[0].(string), pair[1].(int), top, total))
	}
	most := "—"
	if len(sorted) > 0 {
		most = sorted[0][0].(string)
	}
	return buildPage(pageOptions{
		docTitle:      "中国投胎地区分布",
		title:         "中国投胎 · 地区分布",
		subtitle:      subtitleOf(username, "足迹遍及 "+strconv.Itoa(len(sorted))+" 省"),
		seal:          "山河",
		body:          section("省份之分", `<table class="ledger bars"><tbody>`+bars.String()+`</tbody></table>`, "共 "+strconv.Itoa(total)+" 次 · 最常降生于 "+most),
		colophonRight: strconv.Itoa(len(sorted)) + " 省 · " + strconv.Itoa(total) + " 次",
	})
}

// renderWorldDemiseHistory 世界投胎夭折历史。
func renderWorldDemiseHistory(username string, records []demiseRecord) string {
	var rows strings.Builder
	for _, rec := range records {
		fmt.Fprintf(&rows, `<tr>
    <td class="idx">%d</td>
    <td>%s</td>
    <td class="l">%s</td>
</tr>`, rec.Index, chip(rec.DictContinent, "cinnabar", false), rec.DictName)
	}
	return buildPage(pageOptions{
		docTitle: "世界投胎夭折历史",
		title:    "世界投胎 · 夭折历史",
		subtitle: subtitleOf(username, "未及睁眼，已别人间"),
		seal:     "长夜",
		body: section("殁者名录", `<table class="ledger">
    <thead><tr><th width="22%">次第</th><th width="34%">大洲</th><th class="l" width="44%">国度</th></tr></thead>
    <tbody>`+rows.String()+`</tbody>
</table>`, "近 "+strconv.Itoa(len(records))+" 笔"),
		colophonLeft:  "轮回簿 · 殁",
		colophonRight: "愿来世安稳",
	})
}

// renderWorldBirthHistory 世界投胎成功历史。
func renderWorldBirthHistory(username string, records []birthResultInWorld) string {
	var rows strings.Builder
	for _, rec := range records {
		fmt.Fprintf(&rows, `<tr>
    <td class="idx">%d</td>
    <td>%s</td>
    <td class="l">%s</td>
</tr>`, rec.Index, chip(rec.DictContinent, "jade", false), rec.DictName)
	}
	return buildPage(pageOptions{
		docTitle: "世界投胎成功历史",
		title:    "世界投胎 · 降生纪年",
		subtitle: subtitleOf(username, "山南水北，皆曾为家"),
		seal:     "寰宇",
		body: section("降生名录", `<table class="ledger">
    <thead><tr><th width="22%">次第</th><th width="34%">大洲</th><th class="l" width="44%">国度</th></tr></thead>
    <tbody>`+rows.String()+`</tbody>
</table>`, "近 "+strconv.Itoa(len(records))+" 笔"),
		colophonRight: "自新至旧，依序而列",
	})
}

// renderChinaBirthHistory 中国投胎成功历史。
func renderChinaBirthHistory(username string, records []birthResultInChina) string {
	var rows strings.Builder
	for _, rec := range records {
		genderTone := "rose"
		if rec.Gender == "male" {
			genderTone = "azure"
		}
		category := "—"
		if rec.Category != "" {
			category = chip(rec.Category, "ink", false)
		}
		order := "—"
		if rec.Order != "" {
			order = orderText(rec.Order)
		}
		fmt.Fprintf(&rows, `<tr>
    <td class="idx">%d</td>
    <td>%s</td>
    <td>%s</td>
    <td>%s</td>
    <td>%s</td>
</tr>`, rec.Index, chip(translateGender(rec.Gender), genderTone, true), rec.Province, category, order)
	}
	return buildPage(pageOptions{
		docTitle: "中国投胎成功历史",
		title:    "中国投胎 · 降生纪年",
		subtitle: subtitleOf(username, "一纸命簿，半生浮沉"),
		seal:     "降生",
		body: section("降生名录", `<table class="ledger">
    <thead><tr><th width="14%">次第</th><th width="17%">性别</th><th width="25%">省份</th><th width="22%">城乡</th><th width="22%">胎次</th></tr></thead>
    <tbody>`+rows.String()+`</tbody>
</table>`, "近 "+strconv.Itoa(len(records))+" 笔"),
		colophonRight: "自新至旧，依序而列",
	})
}

// lifeAndDeathCards 降生 / 夭折 / 存活率三张大卡。
func lifeAndDeathCards(totalCount, stillbirths, userRank, stillbirthsRank int) string {
	attempts := totalCount + stillbirths
	return `<div class="grid c3">
    ` + statCard(statOptions{label: "降生", value: strconv.Itoa(totalCount), unit: "次", tone: "jade", note: rankText(userRank), hero: true}) + `
    ` + statCard(statOptions{label: "夭折", value: strconv.Itoa(stillbirths), unit: "次", tone: "cinnabar", note: rankText(stillbirthsRank), hero: true}) + `
    ` + statCard(statOptions{label: "存活率", value: percentText(totalCount, attempts), tone: "gold", note: "共叩门 " + strconv.Itoa(attempts) + " 次", hero: true}) + `
</div>`
}

// renderWorldOverview 世界投胎记录总览。
func renderWorldOverview(username string, analysis worldAnalysis, userRank, stillbirthsRank, stillbirths int) string {
	visited := 0
	for _, count := range analysis.continentCounts {
		if count > 0 {
			visited++
		}
	}
	favouriteNote := ""
	if analysis.favourite.Count > 0 {
		favouriteNote = strconv.Itoa(analysis.favourite.Count) + " 次 · 占 " + percentText(analysis.favourite.Count, analysis.totalCount)
	}
	footprint := `<div class="grid c2">
    ` + statCard(statOptions{label: "履及之国", value: strconv.Itoa(analysis.uniqueCountries), unit: "国", tone: "ink", note: "遍及 " + strconv.Itoa(visited) + " 洲"}) + `
    ` + statCard(statOptions{label: "最常降生", value: orDash(analysis.favourite.Name), tone: "ink", note: favouriteNote}) + `
</div>`

	type pair = nameCount
	sorted := make([]pair, 0, len(analysis.continentCounts))
	for _, name := range continentOrder {
		sorted = append(sorted, pair{name, analysis.continentCounts[name]})
	}
	sortNameCounts(sorted)
	top := 0
	if len(sorted) > 0 {
		top = sorted[0].count
	}
	var continents strings.Builder
	for i, item := range sorted {
		continents.WriteString(distributionRow(i+1, item.name, item.count, top, analysis.totalCount))
	}

	return buildPage(pageOptions{
		docTitle: "世界投胎记录总览",
		title:    "世界投胎 · 记录总览",
		subtitle: subtitleOf(username, "七洲之内，已履 "+strconv.Itoa(visited)+" 洲"),
		seal:     "寰宇",
		body: section("生死之数", lifeAndDeathCards(analysis.totalCount, stillbirths, userRank, stillbirthsRank)) +
			section("行迹", footprint) +
			section("大洲分布", `<table class="ledger bars"><tbody>`+continents.String()+`</tbody></table>`, "共 "+strconv.Itoa(analysis.totalCount)+" 次"),
		colophonRight: "降生 " + strconv.Itoa(analysis.totalCount) + " · 夭折 " + strconv.Itoa(stillbirths),
	})
}

// renderChinaOverview 中国投胎记录总览。
func renderChinaOverview(username string, analysis chinaAnalysis, userRank, stillbirthsRank, stillbirths, totalProvinces int) string {
	var region strings.Builder
	region.WriteString(`<div class="grid c3">`)
	for _, key := range []string{"城市", "城镇", "乡村"} {
		count := analysis.categoryCounts[key]
		ratio := safeRatio(count, analysis.totalCount)
		region.WriteString(statCard(statOptions{label: key, value: strconv.Itoa(count), unit: "次", tone: "ink", note: "占 " + percentText(count, analysis.totalCount), ratio: &ratio}))
	}
	region.WriteString(`</div><div class="grid c2">`)
	provinceNote := ""
	var provinceRatio *float64
	if totalProvinces > 0 {
		provinceNote = "共 " + strconv.Itoa(totalProvinces) + " 省 · 已踏足 " + percentText(analysis.uniqueProvinces, totalProvinces)
		value := float64(analysis.uniqueProvinces) / float64(totalProvinces)
		provinceRatio = &value
	}
	region.WriteString(statCard(statOptions{label: "履及之省", value: strconv.Itoa(analysis.uniqueProvinces), unit: "省", tone: "ink", note: provinceNote, ratio: provinceRatio}))
	favouriteNote := ""
	if analysis.favourite.Count > 0 {
		favouriteNote = strconv.Itoa(analysis.favourite.Count) + " 次 · 占 " + percentText(analysis.favourite.Count, analysis.totalCount)
	}
	favouriteRatio := safeRatio(analysis.favourite.Count, analysis.totalCount)
	region.WriteString(statCard(statOptions{label: "最常降生", value: orDash(analysis.favourite.Name), tone: "ink", note: favouriteNote, ratio: &favouriteRatio}))
	region.WriteString(`</div>`)

	var gender strings.Builder
	gender.WriteString(`<div class="grid c2">`)
	for _, item := range []struct {
		key  string
		tone string
	}{{"male", "azure"}, {"female", "rose"}} {
		count := analysis.genderCounts[item.key]
		ratio := safeRatio(count, analysis.totalCount)
		gender.WriteString(statCard(statOptions{label: translateGenderChild(item.key), value: strconv.Itoa(count), unit: "次", tone: item.tone, note: "占 " + percentText(count, analysis.totalCount), ratio: &ratio}))
	}
	gender.WriteString(`</div>`)

	var orders strings.Builder
	orders.WriteString(`<div class="grid c5">`)
	for _, key := range []string{"一", "二", "三", "四", "五及以上"} {
		label := "第" + key + "胎"
		if key == "五及以上" {
			label = key
		}
		count := analysis.orderCounts[key]
		ratio := safeRatio(count, analysis.totalCount)
		orders.WriteString(statCard(statOptions{label: label, value: strconv.Itoa(count), unit: "次", tone: "ink", note: percentText(count, analysis.totalCount), ratio: &ratio}))
	}
	orders.WriteString(`</div>`)

	return buildPage(pageOptions{
		docTitle: "中国投胎记录总览",
		title:    "中国投胎 · 记录总览",
		subtitle: subtitleOf(username, "生死有数，去来有痕"),
		seal:     "命簿",
		body: section("生死之数", lifeAndDeathCards(analysis.totalCount, stillbirths, userRank, stillbirthsRank)) +
			section("城乡与山河", region.String()) +
			section("男女之数", gender.String()) +
			section("胎次", orders.String()),
		colophonRight: "降生 " + strconv.Itoa(analysis.totalCount) + " · 夭折 " + strconv.Itoa(stillbirths),
	})
}

// renderFirstAppearance 中国投胎初见图鉴。
func renderFirstAppearance(username string, results []birthResultInChina, totalProvinces int) string {
	provinces := provinceFirstAppearances(results)
	cell := func(value *int, tone string) string {
		if value == nil {
			return `<span class="chip ghost">未逢</span>`
		}
		return chip("第 "+strconv.Itoa(*value)+" 次", tone, true)
	}
	var rows strings.Builder
	for _, item := range provinces {
		first := "—"
		if earliestAppearance(item.Entry) < 1<<29 {
			first = strconv.Itoa(earliestAppearance(item.Entry))
		}
		fmt.Fprintf(&rows, `<tr>
    <td class="idx">%s</td>
    <td>%s</td>
    <td>%s</td>
    <td>%s</td>
</tr>`, first, item.Province, cell(item.Entry.Male, "azure"), cell(item.Entry.Female, "rose"))
	}
	unlocked := len(provinces)
	remaining := totalProvinces - unlocked
	if remaining < 0 {
		remaining = 0
	}
	note := "三十四省，尽数踏遍"
	if remaining > 0 {
		note = "尚余 " + strconv.Itoa(remaining) + " 省未至"
	}
	ratio := safeRatio(unlocked, totalProvinces)
	progress := `<div class="grid c1">` + statCard(statOptions{
		label: "图鉴进度",
		value: strconv.Itoa(unlocked),
		unit:  "/ " + strconv.Itoa(totalProvinces) + " 省",
		tone:  "jade",
		note:  note,
		ratio: &ratio,
	}) + `</div>`

	return buildPage(pageOptions{
		docTitle: "中国投胎第一次出现",
		title:    "中国投胎 · 初见图鉴",
		subtitle: subtitleOf(username, "已踏足 "+strconv.Itoa(unlocked)+" / "+strconv.Itoa(totalProvinces)+" 省"),
		seal:     "初见",
		body: section("初见之序", progress+`<table class="ledger dex">
    <thead><tr><th width="16%">初见</th><th width="24%">省份</th><th width="30%">男孩</th><th width="30%">女孩</th></tr></thead>
    <tbody>`+rows.String()+`</tbody>
</table>`),
		colophonRight: strconv.Itoa(unlocked) + " / " + strconv.Itoa(totalProvinces),
		style: `
.grid.c1 { grid-template-columns: 1fr; margin-bottom: 20px; }
.grid.c1 .stat .v { font-size: 48px; }
`,
	})
}

func safeRatio(count, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(count) / float64(total)
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

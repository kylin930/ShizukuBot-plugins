package endfield

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

func init() {
	registerRoute(80, exactMatch("成就列表"), handleAchievementList)
	registerRoute(75, prefixMatch("影拓丰碑"), handleIndieHard)
}

type achieveMedal struct {
	ID       string
	Name     string
	Cate     string
	CateName string
	Icon     string
	IsPlated bool
	Level    int
}

type achieveGroup struct {
	Cate     string
	CateName string
	Items    []achieveMedal
}

func handleAchievementList(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	res, err := session.Client.getData(ctx, "achieve", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !res.ok() {
		return replyText(ctx, c, getMessage("achieve.get_failed"))
	}
	var data map[string]any
	if err := res.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("achieve.get_failed"))
	}
	role := pexMap(data, "role")
	achieve := pexMap(data, "achieve")
	medals := achievementMedals(achieve)
	if len(medals) == 0 {
		return replyText(ctx, c, getMessage("achieve.not_found_info"))
	}
	groups := achievementGroups(medals)
	roleName := pexString(role, "name")
	if roleName == "" {
		roleName = getMessage("common.unknown")
	}
	roleID := pexString(role, "roleId")
	level := pexField(role, "level")
	serverID := pexField(role, "serverId")

	var body strings.Builder
	body.WriteString(`<div class="section"><h2>账号</h2><div class="grid c3">`)
	body.WriteString(playerStatCard("角色名", roleName, ""))
	body.WriteString(playerStatCard("角色 ID", roleID, ""))
	body.WriteString(playerStatCard("等级", playerFormatValue(level), "服务器 "+playerFormatValue(serverID)))
	body.WriteString(`</div></div>`)
	for _, group := range groups {
		body.WriteString(`<div class="section"><h2>` + htmlEscape(fmt.Sprintf("%s（%d）", group.CateName, len(group.Items))) + `</h2><div class="tags">`)
		for _, medal := range group.Items {
			label := medal.Name
			if medal.Level > 0 {
				label += fmt.Sprintf(" Lv.%d", medal.Level)
			}
			if medal.IsPlated {
				label += " · 镀层"
			}
			body.WriteString(`<span class="chip gold">` + htmlEscape(label) + `</span>`)
		}
		body.WriteString(`</div></div>`)
	}
	html := buildPage(pageOptions{
		title:    getMessage("achieve.title"),
		eyebrow:  "终末地 · 成就",
		subtitle: htmlEscape(fmt.Sprintf("%s · 共 %d 个成就", roleName, len(medals))),
		seal:     "成就",
		body:     body.String(),
		width:    900,
	})

	lines := []string{getMessage("achieve.text_header", map[string]any{"name": roleName})}
	for _, group := range groups {
		lines = append(lines, fmt.Sprintf("【%s】(%d)", group.CateName, len(group.Items)))
		for index, medal := range group.Items {
			plated := ""
			if medal.IsPlated {
				plated = "（镀层）"
			}
			levelText := ""
			if medal.Level > 0 {
				levelText = fmt.Sprintf(" Lv.%d", medal.Level)
			}
			lines = append(lines, fmt.Sprintf("%d. %s%s%s", index+1, medal.Name, levelText, plated))
		}
		lines = append(lines, "")
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return replyCard(ctx, c, html, 900, strings.Join(lines, "\n"))
}

func achievementMedals(achieve map[string]any) []achieveMedal {
	rawMedals := pexMapList(pexField(achieve, "achieveMedals"))
	sort.SliceStable(rawMedals, func(i, j int) bool {
		left, _ := pexNumber(rawMedals[i], "obtainTs")
		right, _ := pexNumber(rawMedals[j], "obtainTs")
		return left > right
	})
	out := make([]achieveMedal, 0, len(rawMedals))
	for _, item := range rawMedals {
		data := pexMap(item, "achievementData")
		if data == nil {
			data = item
		}
		level := pexInt(item, 0, "level")
		if level == 0 {
			level = pexInt(data, 0, "initLevel")
		}
		out = append(out, achieveMedal{
			ID:       pexString(data, "id"),
			Name:     pexString(data, "name"),
			Cate:     pexString(data, "cate"),
			CateName: pexString(data, "cateName"),
			Icon:     pexString(data, "icon"),
			IsPlated: pexBool(item, "isPlated"),
			Level:    level,
		})
	}
	return out
}

func achievementGroups(medals []achieveMedal) []achieveGroup {
	order := []string{
		"achv_type_quest", "achv_type_battle", "achv_type_factory",
		"achv_type_growth", "achv_type_adventure", "achv_type_social", "achv_type_hide",
	}
	orderMap := map[string]int{}
	for index, cate := range order {
		orderMap[cate] = index
	}
	byName := map[string]*achieveGroup{}
	keys := make([]string, 0)
	for _, medal := range medals {
		cateName := medal.CateName
		if cateName == "" {
			cateName = "未分类"
		}
		group, ok := byName[cateName]
		if !ok {
			group = &achieveGroup{Cate: medal.Cate, CateName: cateName}
			byName[cateName] = group
			keys = append(keys, cateName)
		}
		group.Items = append(group.Items, medal)
	}
	out := make([]achieveGroup, 0, len(keys))
	for _, key := range keys {
		group := byName[key]
		sort.SliceStable(group.Items, func(i, j int) bool {
			if group.Items[i].Level != group.Items[j].Level {
				return group.Items[i].Level > group.Items[j].Level
			}
			return group.Items[i].Name < group.Items[j].Name
		})
		out = append(out, *group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, okLeft := orderMap[out[i].Cate]
		right, okRight := orderMap[out[j].Cate]
		if !okLeft {
			left = 999
		}
		if !okRight {
			right = 999
		}
		if left != right {
			return left < right
		}
		return out[i].CateName < out[j].CateName
	})
	return out
}

// ---------- 影拓丰碑 ----------

type indieEnemy struct {
	Name  string
	Level any
}

type indieTeamMember struct {
	Text string
}

type indieBest struct {
	Summary string
	Team    []indieTeamMember
}

type indieDungeon struct {
	Label          string
	Name           string
	IsPass         bool
	Status         string
	RecommendLevel any
	Best           *indieBest
	FeatureText    string
	Enemies        []indieEnemy
}

type indieDungeonGroup struct {
	Name   string
	Desc   string
	Normal indieDungeon
	Hard   indieDungeon
}

type indieGroup struct {
	Name              string
	ActivityName      string
	IsInActivity      bool
	TimeRange         string
	AchievementName   string
	AchievementStatus string
	IsPlated          bool
	Level             int
	NormalPass        int
	HardPass          int
	Total             int
	Dungeons          []indieDungeonGroup
}

func handleIndieHard(ctx context.Context, c sdk.Call, cfg config, keyword string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	res, err := session.Client.getData(ctx, "indie_hard", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !res.ok() {
		return replyText(ctx, c, getMessage("indie_hard.get_failed"))
	}
	var data map[string]any
	if err := res.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("indie_hard.get_failed"))
	}
	cardNameMap := map[string]string{}
	if cardRes, err := session.Client.getData(ctx, "endfield_card_detail", nil); err == nil && cardRes.ok() {
		var cardData map[string]any
		if cardRes.decode(&cardData) == nil {
			detail := pexMap(cardData, "detail")
			if detail == nil {
				detail = cardData
			}
			for _, raw := range pexSlice(detail["chars"]) {
				char := pexAsMap(raw)
				if char == nil {
					continue
				}
				c := pexMap(char, "charData")
				if c == nil {
					c = char
				}
				id := pexString(char, "id", "instId")
				if id == "" {
					id = pexString(c, "id", "charId")
				}
				name := pexString(c, "name")
				if name == "" {
					name = pexString(pexMap(c, "template"), "name_cn")
				}
				if id != "" && name != "" {
					cardNameMap[id] = name
				}
			}
		}
	}

	indie := pexMap(data, "indieHard")
	if indie == nil {
		indie = data
	}
	groupsRaw := pexSlice(indie["indieHardGroups"])
	if len(groupsRaw) == 0 {
		return replyText(ctx, c, getMessage("indie_hard.not_found_info"))
	}
	groups := make([]indieGroup, 0, len(groupsRaw))
	for _, raw := range groupsRaw {
		group := pexAsMap(raw)
		if group == nil {
			continue
		}
		if keyword != "" && !indieMatchGroup(group, keyword) {
			continue
		}
		groups = append(groups, indieBuildGroup(group, cardNameMap))
	}
	if len(groups) == 0 {
		return replyText(ctx, c, getMessage("indie_hard.no_match", map[string]any{"keyword": keyword}))
	}

	var body strings.Builder
	for index, group := range groups {
		body.WriteString(`<div class="section"><h2>` + htmlEscape(fmt.Sprintf("%d. %s", index+1, group.Name)) + `</h2>`)
		body.WriteString(`<div class="grid c3">`)
		body.WriteString(playerStatCard("活动", group.ActivityName, map[bool]string{true: "进行中", false: "已结束"}[group.IsInActivity]))
		body.WriteString(playerStatCard("时间", group.TimeRange, ""))
		body.WriteString(playerStatCard("奖章", group.AchievementName, group.AchievementStatus))
		body.WriteString(playerStatCard("进度", fmt.Sprintf("普通 %d/%d", group.NormalPass, group.Total), fmt.Sprintf("苦难 %d/%d", group.HardPass, group.Total)))
		body.WriteString(`</div>`)
		for dungeonIndex, dungeon := range group.Dungeons {
			body.WriteString(`<div class="card" style="margin-top:12px"><div class="v" style="font-size:18px">` + htmlEscape(fmt.Sprintf("%d. %s", dungeonIndex+1, dungeon.Name)) + `</div>`)
			body.WriteString(`<div class="row"><span class="label">普通</span><span class="value">` + htmlEscape(dungeon.Normal.Status) + `｜推荐Lv.` + htmlEscape(playerFormatValue(dungeon.Normal.RecommendLevel)) + `</span></div>`)
			if dungeon.Normal.Best != nil {
				body.WriteString(`<div class="note">最佳：` + htmlEscape(dungeon.Normal.Best.Summary) + `</div>`)
				for _, member := range dungeon.Normal.Best.Team {
					body.WriteString(`<div class="muted">· ` + htmlEscape(member.Text) + `</div>`)
				}
			}
			body.WriteString(`<div class="row"><span class="label">苦难</span><span class="value">` + htmlEscape(dungeon.Hard.Status) + `｜推荐Lv.` + htmlEscape(playerFormatValue(dungeon.Hard.RecommendLevel)) + `</span></div>`)
			if dungeon.Hard.Best != nil {
				body.WriteString(`<div class="note">最佳：` + htmlEscape(dungeon.Hard.Best.Summary) + `</div>`)
				for _, member := range dungeon.Hard.Best.Team {
					body.WriteString(`<div class="muted">· ` + htmlEscape(member.Text) + `</div>`)
				}
			}
			if len(dungeon.Normal.Enemies) > 0 {
				body.WriteString(`<div class="note">敌人：`)
				names := make([]string, 0, len(dungeon.Normal.Enemies))
				for _, enemy := range dungeon.Normal.Enemies {
					names = append(names, fmt.Sprintf("%s Lv.%s", enemy.Name, playerFormatValue(enemy.Level)))
				}
				body.WriteString(htmlEscape(strings.Join(names, "、")))
				body.WriteString(`</div>`)
			}
			body.WriteString(`</div>`)
		}
		body.WriteString(`</div>`)
	}

	html := buildPage(pageOptions{
		title:    "影拓丰碑",
		eyebrow:  "终末地 · 影拓丰碑",
		subtitle: htmlEscape(fmt.Sprintf("共 %d 个记录", len(groups))),
		seal:     "丰碑",
		body:     body.String(),
		width:    980,
	})

	texts := make([]string, 0, len(groups))
	for index, group := range groups {
		texts = append(texts, indieFormatGroup(group, index, len(groups)))
	}
	return replyCard(ctx, c, html, 980, strings.Join(texts, "\n\n"))
}

func indieBuildGroup(group map[string]any, charNameMap map[string]string) indieGroup {
	achievement := pexMap(group, "achieve")
	achievementData := pexMap(achievement, "achievementData")
	dungeonsRaw := pexSlice(group["dungeonGroups"])
	total := len(dungeonsRaw)
	normalPass, hardPass := 0, 0
	dungeons := make([]indieDungeonGroup, 0, total)
	for _, raw := range dungeonsRaw {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		normal := pexMap(item, "normalDungeon")
		hard := pexMap(item, "hardDungeon")
		if pexBool(normal, "isPass") {
			normalPass++
		}
		if pexBool(hard, "isPass") {
			hardPass++
		}
		name := pexString(normal, "name")
		if name == "" {
			name = pexString(hard, "name")
		}
		if name == "" {
			name = "未知"
		}
		dungeons = append(dungeons, indieDungeonGroup{
			Name:   name,
			Desc:   pexString(normal, "desc", "description"),
			Normal: indieBuildDungeon("普通", normal, charNameMap),
			Hard:   indieBuildDungeon("苦难", hard, charNameMap),
		})
	}
	name := pexString(group, "name")
	if name == "" {
		name = pexString(group, "activityName")
	}
	if name == "" {
		name = "未知"
	}
	activityName := pexString(group, "activityName")
	if activityName == "" {
		activityName = "未知"
	}
	achievementName := pexString(achievementData, "name")
	if achievementName == "" {
		achievementName = "未知"
	}
	return indieGroup{
		Name:              name,
		ActivityName:      activityName,
		IsInActivity:      pexBool(group, "isInActivity"),
		TimeRange:         indieFormatRange(pexField(group, "activityStartTs"), pexField(group, "activityEndTs")),
		AchievementName:   achievementName,
		AchievementStatus: indieFormatAchievement(achievement),
		IsPlated:          pexBool(achievement, "isPlated"),
		Level:             pexInt(achievement, 0, "level"),
		NormalPass:        normalPass,
		HardPass:          hardPass,
		Total:             total,
		Dungeons:          dungeons,
	}
}

func indieBuildDungeon(label string, dungeon map[string]any, charNameMap map[string]string) indieDungeon {
	if dungeon == nil {
		return indieDungeon{Label: label, Name: "未知", Status: "暂无", RecommendLevel: "未知"}
	}
	status := "未通关"
	if pexBool(dungeon, "isPass") {
		status = "已通关"
	}
	out := indieDungeon{
		Label:          label,
		Name:           pexString(dungeon, "name"),
		IsPass:         pexBool(dungeon, "isPass"),
		Status:         status,
		RecommendLevel: pexField(dungeon, "recommendLevel"),
		FeatureText:    indieFeatureText(pexString(dungeon, "feature")),
		Enemies:        indieBuildEnemies(pexSlice(dungeon["enemies"])),
	}
	if out.Name == "" {
		out.Name = "未知"
	}
	if out.RecommendLevel == nil {
		out.RecommendLevel = "未知"
	}
	if best := pexMap(dungeon, "bestRecord"); best != nil {
		out.Best = indieFormatBestRecord(best, charNameMap)
	}
	return out
}

func indieBuildEnemies(raw []any) []indieEnemy {
	out := make([]indieEnemy, 0)
	for _, item := range raw {
		if len(out) >= 5 {
			break
		}
		enemy := pexAsMap(item)
		if enemy == nil {
			continue
		}
		name := pexString(enemy, "name")
		if name == "" {
			name = "未知"
		}
		level := pexField(enemy, "level")
		if level == nil {
			level = "?"
		}
		out = append(out, indieEnemy{Name: name, Level: level})
	}
	return out
}

func indieFormatBestRecord(record map[string]any, charNameMap map[string]string) *indieBest {
	seconds := pexFloatOrZero(pexField(record, "passTs"))
	duration := "耗时未知"
	if seconds > 0 {
		duration = indieFormatDuration(seconds)
	}
	ts := pexFloatOrZero(pexField(record, "ts"))
	summary := duration
	if ts > 0 {
		summary = duration + "｜" + indieFormatDateTime(ts)
	}
	team := make([]indieTeamMember, 0)
	for _, raw := range pexSlice(record["chars"]) {
		if len(team) >= 4 {
			break
		}
		char := pexAsMap(raw)
		if char == nil {
			continue
		}
		charID := pexString(char, "charId", "char_id", "id")
		name := charNameMap[charID]
		rarity := "?星"
		if value := pexString(pexMap(char, "rarity"), "value"); value != "" {
			rarity = value + "星"
		}
		property := pexString(pexMap(char, "property"), "value")
		if property == "" {
			property = "未知"
		}
		level := "Lv.?"
		if value := pexField(char, "level"); value != nil {
			level = "Lv." + playerFormatValue(value)
		}
		phase := ""
		if value := pexField(char, "evolvePhase"); value != nil {
			phase = "精" + playerFormatValue(value)
		}
		text := ""
		if name != "" {
			text = name + " "
		}
		text += rarity + property + level + phase
		team = append(team, indieTeamMember{Text: text})
	}
	return &indieBest{Summary: summary, Team: team}
}

func indieMatchGroup(group map[string]any, keyword string) bool {
	key := indieNormalize(keyword)
	if key == "" {
		return true
	}
	texts := []string{
		pexString(group, "name"),
		pexString(group, "activityName"),
		pexString(pexMap(pexMap(group, "achieve"), "achievementData"), "name"),
	}
	for _, raw := range pexSlice(group["dungeonGroups"]) {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		texts = append(texts, pexString(pexMap(item, "normalDungeon"), "name"))
		texts = append(texts, pexString(pexMap(item, "hardDungeon"), "name"))
	}
	for _, text := range texts {
		if strings.Contains(indieNormalize(text), key) {
			return true
		}
	}
	return false
}

func indieNormalize(text string) string {
	return strings.ToLower(crisisNormalizeRe.ReplaceAllString(text, ""))
}

func indieCleanRichText(text string) string {
	text = strings.ReplaceAll(text, "</>", "")
	text = crisisTagRe.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func indieFeatureLines(text string) []string {
	lines := strings.Split(indieCleanRichText(text), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func indieFeatureText(text string) string {
	lines := indieFeatureLines(text)
	if len(lines) == 0 {
		return ""
	}
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "。；;，,、 ")
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, "；") + "。"
}

func indieFormatAchievement(achievement map[string]any) string {
	level := pexInt(achievement, 0, "level")
	obtainTs := pexFloatOrZero(pexField(achievement, "obtainTs"))
	plated := "未镀层"
	if pexBool(achievement, "isPlated") {
		plated = "镀层"
	}
	if level <= 0 {
		return "未获得"
	}
	obtained := ""
	if obtainTs > 0 {
		obtained = "｜" + indieFormatDateTime(obtainTs)
	}
	return fmt.Sprintf("Lv.%d｜%s%s", level, plated, obtained)
}

func indieFormatGroup(group indieGroup, index, total int) string {
	titlePrefix := "【影拓丰碑】"
	if total > 1 {
		titlePrefix = fmt.Sprintf("【影拓丰碑 %d/%d】", index+1, total)
	}
	activitySuffix := ""
	if group.IsInActivity {
		activitySuffix = "（进行中）"
	}
	lines := []string{
		titlePrefix + group.Name,
		fmt.Sprintf("活动：%s%s", group.ActivityName, activitySuffix),
		fmt.Sprintf("时间：%s", group.TimeRange),
		fmt.Sprintf("奖章：%s｜%s", group.AchievementName, group.AchievementStatus),
		fmt.Sprintf("进度：普通 %d/%d｜苦难 %d/%d", group.NormalPass, group.Total, group.HardPass, group.Total),
		"",
	}
	for dungeonIndex, dungeon := range group.Dungeons {
		lines = append(lines, fmt.Sprintf("%d. %s", dungeonIndex+1, dungeon.Name))
		lines = append(lines, indieFormatDungeonLines(dungeon.Normal)...)
		lines = append(lines, indieFormatDungeonLines(dungeon.Hard)...)
		enemies := dungeon.Normal.Enemies
		if len(enemies) == 0 {
			enemies = dungeon.Hard.Enemies
		}
		if len(enemies) > 0 {
			lines = append(lines, "   敌人：")
			for _, enemy := range enemies {
				lines = append(lines, fmt.Sprintf("     - %s Lv.%s", enemy.Name, playerFormatValue(enemy.Level)))
			}
		}
		if dungeonIndex < len(group.Dungeons)-1 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func indieFormatDungeonLines(dungeon indieDungeon) []string {
	lines := []string{
		fmt.Sprintf("   %s：%s｜推荐Lv.%s", dungeon.Label, dungeon.Status, playerFormatValue(dungeon.RecommendLevel)),
	}
	if dungeon.Best != nil {
		lines = append(lines, "     最佳："+dungeon.Best.Summary)
		if len(dungeon.Best.Team) > 0 {
			lines = append(lines, "     队伍：")
			for _, member := range dungeon.Best.Team {
				lines = append(lines, "       - "+member.Text)
			}
		}
	} else {
		lines = append(lines, "     最佳：无记录")
	}
	return lines
}

func indieFormatRange(startRaw, endRaw any) string {
	start, _ := pexFloatValue(startRaw)
	end, _ := pexFloatValue(endRaw)
	if start == 0 && end == 0 {
		return "长期"
	}
	if start != 0 && end == 0 {
		return indieFormatDate(start) + " 起"
	}
	if start == 0 && end != 0 {
		return "至 " + indieFormatDate(end)
	}
	return indieFormatDate(start) + " ~ " + indieFormatDate(end)
}

func indieFormatDate(ts float64) string {
	return time.Unix(int64(ts), 0).Format("2006-01-02")
}

func indieFormatDateTime(ts any) string {
	value, _ := pexFloatValue(ts)
	if value == 0 {
		return ""
	}
	return time.Unix(int64(value), 0).Format("01-02 15:04")
}

func indieFormatDuration(seconds float64) string {
	total := int(mathMax0(seconds))
	minutes := total / 60
	rest := total % 60
	if minutes > 0 {
		return strconv.Itoa(minutes) + "分" + strconv.Itoa(rest) + "秒"
	}
	return strconv.Itoa(rest) + "秒"
}

func mathMax0(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

package endfield

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

func init() {
	registerRoute(70, exactMatch("便签", "账号便签"), func(ctx context.Context, c sdk.Call, cfg config, args string, text string) error {
		return handleNote(ctx, c, cfg, false)
	})
	registerRoute(70, exactMatch("角色"), func(ctx context.Context, c sdk.Call, cfg config, args string, text string) error {
		return handleNote(ctx, c, cfg, true)
	})
}

type noteCharacter struct {
	Name         string
	Level        int
	Rarity       int
	Potential    int
	EvolvePhase  *int
	ElitePhase   int
	Profession   string
	Property     string
	AvatarRT     string
	AvatarSQ     string
	WeaponName   string
	WeaponLevel  int
	WeaponType   string
	GemName      string
	ProfessionIx string
	PropertyIx   string
}

type noteMedal struct {
	Name     string
	Level    int
	IsPlated bool
}

// handleNote 对齐上游 apps/note.js 的账号便签与角色视图。
func handleNote(ctx context.Context, c sdk.Call, cfg config, charsView bool) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	res, err := session.Client.getData(ctx, "note", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !res.ok() {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	var data map[string]any
	if err := res.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}

	unknown := getMessage("common.unknown")
	placeholder := getMessage("note.placeholder")
	base := pexMap(data, "base")
	charsRaw := pexSlice(data["chars"])
	achieve := pexMap(data, "achieve")
	bpSystem := pexMap(data, "bpSystem")

	chars := make([]noteCharacter, 0, len(charsRaw))
	for _, item := range charsRaw {
		raw := pexAsMap(item)
		if raw == nil {
			continue
		}
		profession := pexString(pexMap(raw, "profession"), "value")
		if profession == "" {
			profession = pexString(raw, "profession")
		}
		property := pexString(pexMap(raw, "property"), "value")
		if property == "" {
			property = pexString(raw, "property")
		}
		rarity := pexInt(raw, 1, "rarity")
		if rarityVal, ok := pexNumber(pexMap(raw, "rarity"), "value"); ok {
			rarity = int(rarityVal)
		}
		if rarity < 1 {
			rarity = 1
		}
		if rarity > 6 {
			rarity = 6
		}
		potential := pexInt(raw, 0, "potentialLevel", "potential_level")
		if potential < 0 {
			potential = 0
		}
		if potential > 5 {
			potential = 5
		}
		level := pexInt(raw, 1, "level")
		if level < 1 {
			level = 1
		}
		var evolvePhase *int
		rawEvolve := pexField(raw, "evolvePhase", "evolve_phase", "eliteLevel", "elite_level")
		if value, ok := pexFloatValue(rawEvolve); ok {
			phase := int(value)
			if phase < 0 {
				phase = 0
			}
			evolvePhase = &phase
		}
		weapon := pexMap(raw, "weapon")
		weaponName := pexString(weapon, "name")
		weaponLevel := pexInt(weapon, 0, "level")
		weaponBreak := pexInt(weapon, 0, "breakthroughLevel", "breakthrough_level")
		elitePhase := weaponBreak
		if evolvePhase != nil {
			elitePhase = *evolvePhase
		}
		name := pexString(raw, "name")
		if name == "" {
			name = unknown
		}
		chars = append(chars, noteCharacter{
			Name:         name,
			Level:        level,
			Rarity:       rarity,
			Potential:    potential,
			EvolvePhase:  evolvePhase,
			ElitePhase:   elitePhase,
			Profession:   profession,
			Property:     property,
			AvatarRT:     pexString(raw, "avatarRtUrl", "avatar_rt_url"),
			AvatarSQ:     pexString(raw, "avatarSqUrl", "avatar_sq_url"),
			WeaponName:   weaponName,
			WeaponLevel:  weaponLevel,
			WeaponType:   pexString(pexMap(weapon, "type"), "value"),
			GemName:      pexString(pexMap(pexMap(weapon, "gem"), "gemData"), "name"),
			ProfessionIx: playerMetaIcon("class", profession),
			PropertyIx:   playerMetaIcon("attrpanle", property),
		})
	}

	totalCharNum := len(chars)
	if value, ok := pexNumber(base, "charNum", "char_num"); ok {
		totalCharNum = int(value)
	} else if value, ok := pexNumber(data, "charCount", "char_count"); ok {
		totalCharNum = int(value)
	}
	achieveCount := int(0)
	hasAchieve := false
	if value, ok := pexNumber(achieve, "count"); ok {
		achieveCount = int(value)
		hasAchieve = true
	}
	bpCur, hasBPCur := pexNumber(bpSystem, "curLevel", "cur_level")
	bpMax, hasBPMax := pexNumber(bpSystem, "maxLevel", "max_level")

	serverName := pexString(base, "serverName", "server_name")
	if serverName == "" {
		serverName = unknown
	}
	name := pexString(base, "name")
	if name == "" {
		name = unknown
	}
	roleID := pexString(base, "roleId", "role_id")
	if roleID == "" {
		roleID = unknown
	}
	createTimeStr := unknown
	if ts, ok := pexNumber(base, "createTime", "create_time"); ok && ts > 0 {
		createTimeStr = time.Unix(int64(ts), 0).Format("2006-01-02 15:04:05")
	}
	lastLoginStr := unknown
	if ts, ok := pexNumber(base, "lastLoginTime", "last_login_time"); ok && ts > 0 {
		lastLoginStr = time.Unix(int64(ts), 0).Format("2006-01-02 15:04:05")
	}
	mainMission := pexString(pexMap(base, "mainMission", "main_mission"), "description")
	if mainMission == "" {
		mainMission = unknown
	}

	medals := noteMedals(achieve)
	levelText := playerFormatValue(pexField(base, "level"))
	expText := playerFormatValue(pexField(base, "exp"))
	worldLevelText := playerFormatValue(pexField(base, "worldLevel", "world_level"))
	weaponNumText := playerFormatValue(pexField(base, "weaponNum", "weapon_num"))
	docNumText := playerFormatValue(pexField(base, "docNum", "doc_num"))
	sectionTitle := "已拥有干员"
	if charsView {
		sectionTitle = "角色"
	}
	title := "账号便签"
	if charsView {
		title = "角色"
	}

	achieveText := placeholder
	if hasAchieve {
		achieveText = strconv.Itoa(achieveCount)
	}
	bpCurText := placeholder
	bpMaxText := placeholder
	if hasBPCur {
		bpCurText = playerFormatValue(bpCur)
	}
	if hasBPMax {
		bpMaxText = playerFormatValue(bpMax)
	}
	bpText := placeholder
	if hasBPCur && hasBPMax {
		bpText = fmt.Sprintf("%s/%s", bpCurText, bpMaxText)
	} else if hasBPCur {
		bpText = bpCurText
	}

	body := ""
	if !charsView {
		body += `<div class="section"><h2>账号信息</h2><div class="grid c2">` +
			playerStatCard("角色名", name, "") +
			playerStatCard("角色 ID", roleID, "") +
			playerStatCard("等级 / 世界等级", fmt.Sprintf("%s / %s", levelText, worldLevelText), "") +
			playerStatCard("经验", expText, "") +
			playerStatCard("服务器", serverName, "") +
			playerStatCard("创建时间", createTimeStr, "") +
			playerStatCard("最后登录", lastLoginStr, "") +
			playerStatCard("主线任务", mainMission, "") +
			`</div></div>`
		body += `<div class="section"><h2>收集统计</h2><div class="grid c3">` +
			playerStatCard("角色数", strconv.Itoa(totalCharNum), "") +
			playerStatCard("武器数", weaponNumText, "") +
			playerStatCard("文档数", docNumText, "") +
			playerStatCard("成就", achieveText, "") +
			playerStatCard("通行证", bpText, "") +
			`</div></div>`
		if len(medals) > 0 {
			var b strings.Builder
			b.WriteString(`<div class="section"><h2>成就展示</h2><div class="tags">`)
			for _, medal := range medals {
				label := medal.Name
				if medal.Level > 0 {
					label += fmt.Sprintf(" Lv.%d", medal.Level)
				}
				if medal.IsPlated {
					label += " · 镀层"
				}
				b.WriteString(`<span class="chip gold">` + htmlEscape(label) + `</span>`)
			}
			b.WriteString(`</div></div>`)
			body += b.String()
		}
	}

	body += `<div class="section"><h2>` + htmlEscape(fmt.Sprintf("%s（%d 个）", sectionTitle, len(chars))) + `</h2>`
	if len(chars) == 0 {
		body += `<p class="muted">暂无干员数据</p>`
	} else {
		body += `<div class="grid c2">`
		limit := len(chars)
		if !charsView && limit > 12 {
			limit = 12
		}
		for _, char := range chars[:limit] {
			body += noteCharCard(char)
		}
		body += `</div>`
		if limit < len(chars) {
			body += `<p class="muted">仍有 ` + strconv.Itoa(len(chars)-limit) + ` 名干员未展示，发送「:角色」查看完整角色列表。</p>`
		}
	}
	body += `</div>`

	html := buildPage(pageOptions{
		title:    title,
		eyebrow:  "终末地 · 账号便签",
		subtitle: htmlEscape(fmt.Sprintf("%s · %s", name, serverName)),
		seal:     sectionTitle,
		tags:     []string{fmt.Sprintf("UID %s", roleID), fmt.Sprintf("Lv.%s", levelText)},
		body:     body,
		width:    900,
	})

	// 与上游一致的纯文本回退。
	var msg strings.Builder
	if !charsView {
		msg.WriteString(getMessage("note.text_base", map[string]any{
			"name":         name,
			"role_id":      roleID,
			"level":        levelText,
			"exp":          expText,
			"world_level":  worldLevelText,
			"server":       serverName,
			"create_time":  createTimeStr,
			"last_login":   lastLoginStr,
			"main_mission": mainMission,
		}))
		msg.WriteString("\n\n")
		msg.WriteString(getMessage("note.text_stats", map[string]any{
			"char_num":      totalCharNum,
			"weapon_num":    weaponNumText,
			"doc_num":       docNumText,
			"achieve_count": achieveText,
			"bp_cur":        bpCurText,
			"bp_max":        bpMaxText,
		}))
		msg.WriteString("\n\n")
	}
	msg.WriteString(fmt.Sprintf("【%s】(%d个)\n", sectionTitle, len(chars)))
	for _, char := range chars {
		eliteText := fmt.Sprintf(" 精英化%d", char.ElitePhase)
		if charsView && char.WeaponName != "" {
			msg.WriteString(fmt.Sprintf("• %s%s - %s Lv.%d\n", char.Name, eliteText, char.WeaponName, char.WeaponLevel))
		} else {
			msg.WriteString(getMessage("note.text_owned_item", map[string]any{"name": char.Name + eliteText}) + "\n")
		}
	}

	return replyCard(ctx, c, html, 900, strings.TrimSpace(msg.String()))
}

func noteCharCard(char noteCharacter) string {
	avatar := char.AvatarRT
	if avatar == "" {
		avatar = char.AvatarSQ
	}
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	b.WriteString(`<div style="display:flex;gap:12px;align-items:center">`)
	if avatar != "" {
		b.WriteString(`<img src="` + htmlEscape(avatar) + `" alt="" style="width:56px;height:56px;border-radius:12px;object-fit:cover">`)
	}
	b.WriteString(`<div><div class="v" style="font-size:20px">` + htmlEscape(char.Name) + `</div>`)
	b.WriteString(`<div class="note">Lv.` + strconv.Itoa(char.Level) + ` · ★` + strconv.Itoa(char.Rarity) + ` · 潜能` + strconv.Itoa(char.Potential))
	if char.EvolvePhase != nil {
		b.WriteString(` · 精英化` + strconv.Itoa(*char.EvolvePhase))
	}
	b.WriteString(`</div></div></div>`)
	if char.WeaponName != "" {
		b.WriteString(`<div class="row"><span class="label">武器</span><span class="value">` + htmlEscape(char.WeaponName) + ` Lv.` + strconv.Itoa(char.WeaponLevel) + `</span></div>`)
	}
	if char.Profession != "" || char.Property != "" {
		var chips strings.Builder
		if char.ProfessionIx != "" {
			chips.WriteString(`<img src="` + char.ProfessionIx + `" alt="" style="width:18px;height:18px;border-radius:4px;vertical-align:middle">`)
		}
		if char.Profession != "" {
			chips.WriteString(`<span>` + htmlEscape(char.Profession) + `</span>`)
		}
		if char.PropertyIx != "" {
			chips.WriteString(`<img src="` + char.PropertyIx + `" alt="" style="width:18px;height:18px;border-radius:4px;vertical-align:middle">`)
		}
		if char.Property != "" {
			chips.WriteString(`<span>` + htmlEscape(char.Property) + `</span>`)
		}
		b.WriteString(`<div class="tags" style="margin-top:10px"><span class="chip">` + chips.String() + `</span></div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func noteMedals(achieve map[string]any) []noteMedal {
	rawMedals := pexSlice(achieve["achieveMedals"])
	byID := map[string]map[string]any{}
	ordered := make([]map[string]any, 0, len(rawMedals))
	for _, raw := range rawMedals {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		data := pexMap(item, "achievementData")
		if data == nil {
			data = item
		}
		id := pexString(data, "id")
		if id != "" {
			byID[id] = item
		}
		ordered = append(ordered, item)
	}

	display := pexMap(achieve, "display")
	var displayIDs []string
	if display != nil {
		keys := make([]string, 0, len(display))
		for key := range display {
			keys = append(keys, key)
		}
		sort.SliceStable(keys, func(i, j int) bool {
			left, _ := strconv.Atoi(keys[i])
			right, _ := strconv.Atoi(keys[j])
			return left < right
		})
		for _, key := range keys {
			if id := pexStringValue(display[key]); id != "" {
				displayIDs = append(displayIDs, id)
			}
		}
	}

	source := make([]map[string]any, 0, len(ordered))
	if len(displayIDs) > 0 {
		for _, id := range displayIDs {
			if item, ok := byID[id]; ok {
				source = append(source, item)
			}
		}
	} else {
		sorted := append([]map[string]any(nil), ordered...)
		sort.SliceStable(sorted, func(i, j int) bool {
			left, _ := pexNumber(sorted[i], "obtainTs")
			right, _ := pexNumber(sorted[j], "obtainTs")
			return left > right
		})
		if len(sorted) > 10 {
			sorted = sorted[:10]
		}
		source = sorted
	}

	medals := make([]noteMedal, 0, len(source))
	for _, item := range source {
		data := pexMap(item, "achievementData")
		if data == nil {
			data = item
		}
		level := pexInt(item, 0, "level")
		if level == 0 {
			level = pexInt(data, 0, "initLevel")
		}
		medals = append(medals, noteMedal{
			Name:     pexString(data, "name"),
			Level:    level,
			IsPlated: pexBool(item, "isPlated"),
		})
	}
	return medals
}

// ---------- 数据访问辅助 ----------

func playerQueryFailed(ctx context.Context, c sdk.Call, err error) error {
	return replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
}

func playerMetaIcon(dir, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	candidates := []struct {
		ext  string
		mime string
	}{
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".png", "image/png"},
		{".webp", "image/webp"},
	}
	for _, candidate := range candidates {
		path := "resources/meta/" + dir + "/" + name + candidate.ext
		if data := assetDataURL(path, candidate.mime); data != "" {
			return data
		}
	}
	return ""
}

func playerStatCard(label, value, note string) string {
	html := `<div class="card"><div class="k">` + htmlEscape(label) + `</div><div class="v">` + htmlEscape(value) + `</div>`
	if note != "" {
		html += `<div class="note">` + htmlEscape(note) + `</div>`
	}
	return html + `</div>`
}

func playerFormatValue(value any) string {
	if value == nil {
		return "0"
	}
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "0"
		}
		return v
	case float64:
		return playerFormatNumber(v, 2)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func playerFormatNumber(value float64, digits int) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	text := strconv.FormatFloat(value, 'f', digits, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text
}

// pexField 对齐上游 pick：空字符串视为缺失。
func pexField(m map[string]any, keys ...string) any {
	if m == nil {
		return nil
	}
	for _, key := range keys {
		value, ok := m[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		return value
	}
	return nil
}

func pexAsMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func pexMap(m map[string]any, keys ...string) map[string]any {
	return pexAsMap(pexField(m, keys...))
}

func pexSlice(value any) []any {
	if value == nil {
		return nil
	}
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func pexString(m map[string]any, keys ...string) string {
	return pexStringValue(pexField(m, keys...))
}

func pexStringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return playerFormatNumber(v, 2)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func pexNumber(m map[string]any, keys ...string) (float64, bool) {
	return pexFloatValue(pexField(m, keys...))
}

func pexFloatValue(value any) (float64, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		number, err := v.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func pexInt(m map[string]any, fallback int, keys ...string) int {
	if value, ok := pexNumber(m, keys...); ok {
		return int(value)
	}
	return fallback
}

func pexBool(m map[string]any, keys ...string) bool {
	return pexBoolValue(pexField(m, keys...))
}

func pexBoolValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case json.Number:
		number, err := v.Float64()
		return err == nil && number != 0
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
		return strings.TrimSpace(v) != ""
	default:
		return false
	}
}

func pexStringMap(value any) map[string]string {
	raw := pexAsMap(value)
	if raw == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(raw))
	for key, item := range raw {
		out[key] = pexStringValue(item)
	}
	return out
}

func pexMapList(value any) []map[string]any {
	list := pexSlice(value)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m := pexAsMap(item); m != nil {
			out = append(out, m)
		}
	}
	return out
}

package endfield

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

var (
	opDescExprRe      = regexp.MustCompile(`\{([^}:]+):([^}]+)\}`)
	opHTMLTagRe       = regexp.MustCompile(`<[^>]+>`)
	opSpaceNewlineRe  = regexp.MustCompile(`[ \t]+\n`)
	opTripleNewlineRe = regexp.MustCompile(`\n{3,}`)
	opRarityKeyRe     = regexp.MustCompile(`equip_rarity_(\d)|rarity_(\d)`)
	opAffixPercentRe  = regexp.MustCompile(`(?i)Scalar|Rate|Ratio|Multiplier`)
)

func init() {
	registerRoute(82, exactMatch("同步面板", "更新面板"), handlePanelSync)
	registerRoute(82, exactMatch("练度统计"), handleTrainingStats)
	registerRoute(10, func(text string) (string, bool) {
		if text == "面板" || !strings.HasSuffix(text, "面板") {
			return "", false
		}
		name := strings.TrimSpace(strings.TrimSuffix(text, "面板"))
		if name == "" {
			return "", false
		}
		return name, true
	}, handleOperatorPanel)
}

type opAffix struct {
	Name  string
	Value string
	Empty bool
}

type opSkill struct {
	ID       string
	Name     string
	Type     string
	Property string
	IconURL  string
	Desc     string
	Level    int
	MaxLevel any
}

type opTalent struct {
	Name     string
	IconURL  string
	Desc     string
	Category string
}

type opSuit struct {
	Name   string
	Count  int
	Effect string
}

type opChip struct {
	Label   string
	Value   string
	IconURL string
}

type opGear struct {
	Type              string
	SlotLabel         string
	Name              string
	IconURL           string
	Level             any
	Rarity            int
	RarityClass       string
	Stars             []int
	Meta              string
	Note              string
	Desc              string
	EffectTag         string
	SuitName          string
	SuitEffect        string
	Affixes           []opAffix
	Chips             []string
	GemName           string
	GemIcon           string
	GemLevel          any
	Skills            []string
	BreakthroughLevel any
}

type opPanelData struct {
	Name               string
	Level              any
	Stars              []int
	Profession         string
	Property           string
	ProfessionIcon     string
	PropertyIcon       string
	PotentialLevel     int
	EvolvePhase        any
	WeaponType         string
	Tags               []string
	Skills             []opSkill
	DisplaySkills      []opSkill
	Weapon             *opGear
	BodyEquip          *opGear
	ArmEquip           *opGear
	FirstAccessory     *opGear
	SecondAccessory    *opGear
	TacticalItem       *opGear
	CombatTalents      []opTalent
	AbilityTalents     []opTalent
	CultivationTalents []opTalent
	EquipSuits         []opSuit
	IdentityChips      []opChip
}

type opFriendCore struct {
	HP     int
	ATK    int
	DEF    int
	HPSub  string
	ATKSub string
	DEFSub string
}

type opFriendMini struct {
	AGI  int
	STR  int
	WISD int
	WILL int
	CRT  string
	CDMG string
}

type opFriendResist struct {
	Physical  string
	Spell     string
	HealTaken string
}

type opMatrixTerm struct {
	NameCN string
	Cost   string
}

type opMatrix struct {
	GemNameCN string
	Terms     []opMatrixTerm
}

type opFriendPanel struct {
	NameCN             string
	Level              any
	Stars              []int
	PotentialLevel     int
	PromotionName      string
	Core               opFriendCore
	Mini               opFriendMini
	Resist             opFriendResist
	ChargeEfficiency   string
	ArtsStrength       string
	Matrix             *opMatrix
	EquipAffixesBySlot [4][]opAffix
}

type opTrainingSeed struct {
	InstID      string
	Index       int
	Name        string
	Rarity      int
	Level       any
	WeaponName  string
	WeaponLevel any
	MatrixName  string
}

type opTrainingRow struct {
	Name        string
	Rarity      int
	Level       string
	SkillLevels [4]string
	WeaponName  string
	WeaponLevel string
	MatrixName  string
	MatrixLevel string
	EquipLevels [4]string
	ItemName    string
}

func opStars(count int) []int {
	if count < 1 {
		count = 1
	}
	if count > 6 {
		count = 6
	}
	out := make([]int, 0, count)
	for i := 1; i <= count; i++ {
		out = append(out, i)
	}
	return out
}

func opIntFromAny(value any, fallback int) int {
	if number, ok := pexFloatValue(value); ok {
		return int(number)
	}
	return fallback
}

func opFormatPanelNumber(value float64, digits int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	if math.Abs(value-math.Round(value)) < 1e-6 {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	if digits <= 0 {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	text := strconv.FormatFloat(value, 'f', digits, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text
}

func opFormatOptional(m map[string]any, key string, digits int) string {
	if value, ok := pexNumber(m, key); ok {
		return opFormatPanelNumber(value, digits)
	}
	return ""
}

func opResolveDescExpr(expr string, params map[string]float64) (float64, bool) {
	parts := strings.Split(expr, "*")
	result := 1.0
	hasValue := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var value float64
		if number, err := strconv.ParseFloat(part, 64); err == nil {
			value = number
		} else if param, ok := params[part]; ok {
			value = param
		} else {
			return 0, false
		}
		result *= value
		hasValue = true
	}
	return result, hasValue
}

func opRenderPanelText(text string, params map[string]float64) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	out := opDescExprRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := opDescExprRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return ""
		}
		value, ok := opResolveDescExpr(sub[1], params)
		if !ok {
			return ""
		}
		needsPercent := strings.Contains(sub[2], "%")
		if needsPercent && math.Abs(value) <= 1 {
			value *= 100
		}
		digits := 0
		if math.Abs(value) < 10 {
			digits = 1
		}
		formatted := opFormatPanelNumber(value, digits)
		if needsPercent {
			formatted += "%"
		}
		return formatted
	})
	out = opHTMLTagRe.ReplaceAllString(out, "")
	out = opSpaceNewlineRe.ReplaceAllString(out, "\n")
	out = opTripleNewlineRe.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func opParamMap(value any) map[string]float64 {
	out := map[string]float64{}
	if m := pexAsMap(value); m != nil {
		for key, item := range m {
			if number, ok := pexFloatValue(item); ok {
				out[key] = number
			}
		}
		return out
	}
	for _, item := range pexSlice(value) {
		entry := pexAsMap(item)
		if entry == nil {
			continue
		}
		key := pexString(entry, "key", "name", "id", "param", "paramKey", "param_key")
		if key == "" {
			continue
		}
		if number, ok := pexNumber(entry, "value", "val", "v", "num", "count", "raw"); ok {
			out[key] = number
		}
	}
	return out
}

func opBuildTalentCards(items []any, category string) []opTalent {
	out := make([]opTalent, 0, len(items))
	for index, raw := range items {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		name := pexString(item, "name")
		if name == "" {
			name = fmt.Sprintf("%s%d", category, index+1)
		}
		descRaw := pexString(item, "desc")
		paramsRaw := pexField(item, "descParams")
		if descRaw == "" {
			descRaw = pexString(item, "activeEffect")
			paramsRaw = pexField(item, "activeEffectParams")
		}
		if descRaw == "" {
			descRaw = pexString(item, "passiveEffect")
			paramsRaw = pexField(item, "passiveEffectParams")
		}
		icon := pexString(item, "iconUrl")
		if icon == "" {
			icon = pexString(item, "lockedIconUrl")
		}
		talent := opTalent{
			Name:     name,
			IconURL:  icon,
			Desc:     opRenderPanelText(descRaw, opParamMap(paramsRaw)),
			Category: category,
		}
		if talent.Name != "" || talent.Desc != "" {
			out = append(out, talent)
		}
	}
	return out
}

func opBuildSkillCards(charData, userSkills map[string]any) []opSkill {
	source := pexSlice(charData["skills"])
	out := make([]opSkill, 0, len(source))
	for _, raw := range source {
		skill := pexAsMap(raw)
		if skill == nil {
			continue
		}
		id := pexString(skill, "id")
		userInfo := pexMap(userSkills, id)
		level := pexInt(userInfo, 1, "level")
		maxLevel := pexField(userInfo, "maxLevel")
		desc := opRenderPanelText(pexString(skill, "desc"), opParamMap(pexField(skill, "descParams")))
		out = append(out, opSkill{
			ID:       id,
			Name:     pexString(skill, "name"),
			Type:     pexString(pexMap(skill, "type"), "value"),
			Property: pexString(pexMap(skill, "property"), "value"),
			IconURL:  pexString(skill, "iconUrl"),
			Desc:     desc,
			Level:    level,
			MaxLevel: maxLevel,
		})
	}
	return out
}

func opParseRarity(raw map[string]any) (int, string) {
	key := pexString(raw, "key")
	match := opRarityKeyRe.FindStringSubmatch(key)
	rarity := 1
	if len(match) > 0 {
		for index := 1; index < len(match); index++ {
			if match[index] != "" {
				if value, err := strconv.Atoi(match[index]); err == nil {
					rarity = value
				}
				break
			}
		}
	}
	if value := opIntFromAny(pexField(raw, "value"), 0); value >= 1 && value <= 6 {
		rarity = value
	}
	if rarity < 1 {
		rarity = 1
	}
	if rarity > 6 {
		rarity = 6
	}
	return rarity, fmt.Sprintf("equip_rarity_%d", rarity)
}

func opPickEquip(slot any) *opGear {
	if slot == nil {
		return nil
	}
	raw := pexMap(pexAsMap(slot), "equipData")
	if raw == nil {
		raw = pexAsMap(slot)
	}
	if raw == nil || pexString(raw, "name") == "" {
		return nil
	}
	level := pexField(raw, "level")
	if levelMap := pexMap(raw, "level"); levelMap != nil {
		level = pexField(levelMap, "value")
	}
	rarity, rarityClass := opParseRarity(pexMap(raw, "rarity"))
	suit := pexMap(raw, "suit")
	properties := pexSlice(raw["properties"])
	effectTag := ""
	if len(properties) > 0 {
		effectTag = fmt.Sprintf("%d词条", len(properties))
	}
	return &opGear{
		Name:        pexString(raw, "name"),
		IconURL:     pexString(raw, "iconUrl"),
		Level:       level,
		Rarity:      rarity,
		RarityClass: rarityClass,
		Stars:       opStars(rarity),
		SlotLabel:   pexString(pexMap(raw, "type"), "value"),
		Meta:        pexString(pexMap(raw, "type"), "value"),
		SuitName:    pexString(suit, "name"),
		SuitEffect:  opRenderPanelText(pexString(suit, "skillDesc"), opParamMap(pexField(suit, "skillDescParams"))),
		Desc:        opRenderPanelText(pexString(raw, "pkg", "function"), nil),
		EffectTag:   effectTag,
	}
}

func opBuildWeapon(operator, container map[string]any) *opGear {
	weaponRaw := pexMap(operator, "weapon")
	if weaponRaw == nil {
		weaponRaw = pexMap(container, "weapon")
	}
	weaponData := pexMap(weaponRaw, "weaponData")
	if weaponData == nil {
		return nil
	}
	rarityValue := pexMap(weaponData, "rarity")
	rarity := opIntFromAny(pexField(rarityValue, "value"), 1)
	if rarity < 1 {
		rarity = 1
	}
	if rarity > 6 {
		rarity = 6
	}
	gemRaw := pexMap(weaponRaw, "gem")
	gemData := pexMap(gemRaw, "gemData")
	if gemData == nil {
		gemData = gemRaw
	}
	gemName := ""
	gemIcon := ""
	if gemData != nil && (pexString(gemData, "icon") != "" || pexString(gemRaw, "id") != "") {
		gemName = pexString(gemData, "name")
		if gemName == "" {
			gemName = pexString(gemRaw, "name")
		}
		gemIcon = pexString(gemData, "icon")
		if gemIcon == "" {
			gemIcon = pexString(gemRaw, "icon")
		}
	}
	skills := make([]string, 0)
	for _, raw := range pexSlice(weaponData["skills"]) {
		if text := pexString(pexAsMap(raw), "value"); text != "" {
			skills = append(skills, text)
		}
	}
	return &opGear{
		Name:              pexString(weaponData, "name"),
		Level:             pexField(weaponRaw, "level"),
		Rarity:            rarity,
		RarityClass:       fmt.Sprintf("equip_rarity_%d", rarity),
		Stars:             opStars(rarity),
		IconURL:           pexString(weaponData, "iconUrl"),
		Meta:              pexString(pexMap(weaponData, "type"), "value"),
		Desc:              opRenderPanelText(pexString(weaponData, "description", "function"), nil),
		Skills:            skills,
		GemName:           gemName,
		GemIcon:           gemIcon,
		GemLevel:          opGemLevel(gemRaw, gemData),
		BreakthroughLevel: pexField(weaponRaw, "breakthroughLevel", "breakthrough"),
	}
}

func opGemLevel(gemRaw, gemData map[string]any) any {
	candidates := []any{
		pexField(gemRaw, "level", "lv", "rank", "tier", "breakthroughLevel", "refineLevel", "intensifyLevel"),
		pexField(gemData, "level", "lv"),
	}
	for _, candidate := range candidates {
		if text := opTrainingLevel(candidate, ""); text != "" {
			return text
		}
	}
	return nil
}

func opBuildPanelData(operator, charData, userSkills, container map[string]any) *opPanelData {
	rarity := opIntFromAny(pexField(pexMap(charData, "rarity"), "value"), 1)
	profession := pexString(pexMap(charData, "profession"), "value")
	property := pexString(pexMap(charData, "property"), "value")
	potentialLevel := pexInt(operator, 0, "potentialLevel", "potential_level")
	if potentialLevel < 0 {
		potentialLevel = 0
	}
	if potentialLevel > 5 {
		potentialLevel = 5
	}
	skills := opBuildSkillCards(charData, userSkills)
	weapon := opBuildWeapon(operator, container)
	bodyEquip := opPickEquip(pexField(operator, "bodyEquip"))
	if bodyEquip == nil {
		bodyEquip = opPickEquip(pexField(container, "bodyEquip"))
	}
	armEquip := opPickEquip(pexField(operator, "armEquip"))
	if armEquip == nil {
		armEquip = opPickEquip(pexField(container, "armEquip"))
	}
	firstAccessory := opPickEquip(pexField(operator, "firstAccessory"))
	if firstAccessory == nil {
		firstAccessory = opPickEquip(pexField(container, "firstAccessory"))
	}
	secondAccessory := opPickEquip(pexField(operator, "secondAccessory"))
	if secondAccessory == nil {
		secondAccessory = opPickEquip(pexField(container, "secondAccessory"))
	}

	var tacticalItem *opGear
	tacticalRaw := pexMap(pexMap(operator, "tacticalItem"), "tacticalItemData")
	if tacticalRaw == nil {
		tacticalRaw = pexMap(pexMap(container, "tacticalItem"), "tacticalItemData")
	}
	if tacticalRaw != nil && pexString(tacticalRaw, "name") != "" {
		activeEffect := opRenderPanelText(pexString(tacticalRaw, "activeEffect"), opParamMap(pexField(tacticalRaw, "activeEffectParams")))
		passiveEffect := opRenderPanelText(pexString(tacticalRaw, "passiveEffect"), opParamMap(pexField(tacticalRaw, "passiveEffectParams")))
		effectText := passiveEffect
		if effectText == "" {
			effectText = activeEffect
		}
		rarity, _ := opParseRarity(pexMap(tacticalRaw, "rarity"))
		tacticalItem = &opGear{
			Name:      pexString(tacticalRaw, "name"),
			IconURL:   pexString(tacticalRaw, "iconUrl"),
			Rarity:    rarity,
			Stars:     opStars(rarity),
			Meta:      pexString(pexMap(tacticalRaw, "activeEffectType"), "value"),
			SlotLabel: "战术物品",
			EffectTag: pexString(pexMap(tacticalRaw, "activeEffectType"), "value"),
			Desc:      effectText,
		}
	}

	displaySkills := make([]opSkill, 0, 4)
	for _, skill := range skills {
		if len(displaySkills) >= 4 {
			break
		}
		displaySkills = append(displaySkills, skill)
	}
	for len(displaySkills) < 4 {
		displaySkills = append(displaySkills, opSkill{})
	}

	evolvePhase := pexField(container, "evolvePhase")
	if evolvePhase == nil {
		evolvePhase = pexField(operator, "evolvePhase")
	}
	if evolvePhase == nil {
		evolvePhase = 1
	}
	weaponType := pexString(pexMap(charData, "weaponType"), "value")
	combatTalents := opBuildTalentCards(pexSlice(charData["combatTalents"]), "作战天赋")
	abilityTalents := opBuildTalentCards(pexSlice(charData["abilityTalents"]), "能力扩延")
	cultivationTalents := opBuildTalentCards(pexSlice(charData["cultivationTalents"]), "驻舰天赋")
	equipSuits := opBuildEquipSuits(bodyEquip, armEquip, firstAccessory, secondAccessory)
	tags := make([]string, 0)
	for _, tag := range pexSlice(charData["tags"]) {
		if text := pexStringValue(tag); text != "" {
			tags = append(tags, text)
		}
	}

	name := pexString(charData, "name")
	if name == "" {
		name = "未知"
	}
	identityChips := []opChip{
		{Label: "职业", Value: profession, IconURL: playerMetaIcon("class", profession)},
		{Label: "属性", Value: property, IconURL: playerMetaIcon("attrpanle", property)},
		{Label: "武器", Value: weaponType, IconURL: ""},
		{Label: "阶段", Value: fmt.Sprintf("精英化 %s", playerFormatValue(evolvePhase)), IconURL: ""},
	}
	chips := make([]opChip, 0, len(identityChips))
	for _, chip := range identityChips {
		if chip.Value != "" {
			chips = append(chips, chip)
		}
	}

	return &opPanelData{
		Name:               name,
		Level:              pexField(operator, "level"),
		Stars:              opStars(rarity),
		Profession:         profession,
		Property:           property,
		ProfessionIcon:     playerMetaIcon("class", profession),
		PropertyIcon:       playerMetaIcon("attrpanle", property),
		PotentialLevel:     potentialLevel,
		EvolvePhase:        evolvePhase,
		WeaponType:         weaponType,
		Tags:               tags,
		Skills:             skills,
		DisplaySkills:      displaySkills,
		Weapon:             weapon,
		BodyEquip:          bodyEquip,
		ArmEquip:           armEquip,
		FirstAccessory:     firstAccessory,
		SecondAccessory:    secondAccessory,
		TacticalItem:       tacticalItem,
		CombatTalents:      combatTalents,
		AbilityTalents:     abilityTalents,
		CultivationTalents: cultivationTalents,
		EquipSuits:         equipSuits,
		IdentityChips:      chips,
	}
}

func opBuildEquipSuits(equips ...*opGear) []opSuit {
	order := make([]string, 0)
	byName := map[string]*opSuit{}
	for _, equip := range equips {
		if equip == nil || equip.SuitName == "" {
			continue
		}
		suit, ok := byName[equip.SuitName]
		if !ok {
			suit = &opSuit{Name: equip.SuitName}
			byName[equip.SuitName] = suit
			order = append(order, equip.SuitName)
		}
		suit.Count++
		if suit.Effect == "" {
			if equip.SuitEffect != "" {
				suit.Effect = equip.SuitEffect
			} else {
				suit.Effect = equip.Desc
			}
		}
	}
	out := make([]opSuit, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

func opExtractDetail(data map[string]any) (operator, charData, userSkills, container map[string]any) {
	container = pexMap(data, "detail")
	if container == nil {
		container = data
	}
	operator = pexMap(container, "char")
	if operator == nil {
		operator = pexMap(container, "operator")
	}
	if operator == nil {
		operator = container
	}
	charData = pexMap(operator, "charData")
	if charData == nil {
		charData = pexMap(container, "charData")
	}
	if charData == nil {
		charData = pexMap(pexMap(operator, "char"), "charData")
	}
	userSkills = pexMap(operator, "userSkills")
	if userSkills == nil {
		userSkills = pexMap(container, "userSkills")
	}
	if userSkills == nil {
		userSkills = pexMap(pexMap(operator, "char"), "userSkills")
	}
	return operator, charData, userSkills, container
}

func opFindAgg(aggregated []any, rawName string) map[string]any {
	for _, raw := range aggregated {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		if pexString(pexMap(item, "attr_type"), "raw_name") == rawName {
			return item
		}
	}
	return nil
}

func opFormatAffixValue(mod map[string]any, rawName string) string {
	value, ok := pexNumber(mod, "value")
	if !ok {
		return ""
	}
	raw := rawName
	if raw == "" {
		raw = pexString(mod, "attr_name")
	}
	mode := pexString(mod, "mode")
	if mode == "ratio" || (opAffixPercentRe.MatchString(raw) && value > 0 && value <= 1) {
		return fmt.Sprintf("%.1f%%", value*100)
	}
	return strconv.FormatInt(int64(math.Round(value)), 10)
}

func opBuildFriendPanelData(friendCharData map[string]any) *opFriendPanel {
	if friendCharData == nil {
		return nil
	}
	payload := pexMap(friendCharData, "data")
	if payload == nil {
		payload = friendCharData
	}
	char := pexMap(payload, "char")
	processed := pexMap(payload, "processed")
	template := pexMap(char, "template")
	coreStats := pexMap(processed, "core_stats")
	panelSummary := pexMap(pexMap(payload, "panel"), "summary")
	aggregated := pexSlice(processed["aggregated_attributes"])

	hp := int(math.Round(pexFloatOrZero(pexField(coreStats, "hp"))))
	atk := int(math.Round(pexFloatOrZero(pexField(coreStats, "atk"))))
	def := int(math.Round(pexFloatOrZero(pexField(coreStats, "def"))))
	hpAgg := opFindAgg(aggregated, "MaxHp")
	atkAgg := opFindAgg(aggregated, "Atk")
	defAgg := opFindAgg(aggregated, "Def")

	hpSub := ""
	if hpAgg != nil {
		hpSub = fmt.Sprintf("%d + %d", opIntFromAny(pexField(hpAgg, "base"), 0), opIntFromAny(pexField(hpAgg, "flat"), 0))
	}
	atkSub := ""
	if atkAgg != nil {
		base := pexFloatOrZero(pexField(atkAgg, "base"))
		final := pexFloatOrZero(pexField(atkAgg, "final"))
		atkSub = fmt.Sprintf("%d + %d", int(math.Round(base)), int(math.Round(final-base)))
	}
	defSub := ""
	if defAgg != nil {
		defSub = fmt.Sprintf("%d + %d", opIntFromAny(pexField(defAgg, "base"), 0), opIntFromAny(pexField(defAgg, "flat"), 0))
	}

	crt := ""
	if value, ok := pexNumber(opFindAgg(aggregated, "CriticalRate"), "final"); ok {
		crt = fmt.Sprintf("%.1f%%", value*100)
	}
	cdmg := ""
	derived := pexMap(processed, "derived_stats")
	if derived == nil {
		derived = pexMap(processed, "summary_stats")
	}
	if derived == nil {
		derived = pexMap(processed, "ui")
	}
	cdmgRaw := pexField(derived, "critical_damage_pct", "critical_damage")
	if cdmgRaw == nil {
		cdmgRaw = pexField(panelSummary, "critical_damage_pct")
	}
	if value, ok := pexFloatValue(cdmgRaw); ok {
		cdmg = fmt.Sprintf("%.1f%%", value)
	}

	charge := ""
	if value, ok := pexNumber(opFindAgg(aggregated, "UltimateSpGainScalar"), "final"); ok {
		charge = opFormatPanelNumber(value*100, 1) + "%"
	}
	arts := ""
	if value, ok := pexNumber(opFindAgg(aggregated, "PhysicalAndSpellInflictionEnhance"), "final"); ok {
		arts = opFormatPanelNumber(value, 1)
	}

	var matrix *opMatrix
	gems := pexSlice(char["gems"])
	if len(gems) > 0 {
		gem := pexAsMap(gems[0])
		gemTemplate := pexMap(gem, "template")
		gemName := pexString(gemTemplate, "name_cn")
		if gemName == "" {
			gemName = pexString(gemTemplate, "name")
		}
		terms := make([]opMatrixTerm, 0)
		for _, raw := range pexSlice(gem["terms"]) {
			term := pexAsMap(raw)
			termData := pexMap(term, "term")
			name := pexString(termData, "name_cn")
			if name == "" {
				name = pexString(termData, "name")
			}
			if name == "" {
				continue
			}
			cost := ""
			if value := pexField(term, "cost"); value != nil {
				cost = pexStringValue(value)
			}
			terms = append(terms, opMatrixTerm{NameCN: name, Cost: cost})
		}
		if gemName != "" || len(terms) > 0 {
			matrix = &opMatrix{GemNameCN: gemName, Terms: terms}
		}
	}

	attrNames := map[string]string{}
	for _, source := range [][]any{aggregated, pexSlice(pexMap(processed, "base_attributes")["attributes"])} {
		for _, raw := range source {
			item := pexAsMap(raw)
			if item == nil {
				continue
			}
			rawName := pexString(pexMap(item, "attr_type"), "raw_name")
			nameCN := pexString(pexMap(item, "attr_type"), "name_cn")
			if rawName != "" && nameCN != "" {
				attrNames[rawName] = nameCN
			}
		}
	}
	var affixes [4][]opAffix
	for slot := range affixes {
		affixes[slot] = []opAffix{}
	}
	for _, raw := range pexSlice(processed["runtime_modifiers"]) {
		mod := pexAsMap(raw)
		if mod == nil {
			continue
		}
		slot := pexInt(mod, -1, "slot")
		if slot < 0 || slot > 3 {
			continue
		}
		rawName := pexString(mod, "attr_name")
		name := attrNames[rawName]
		if name == "" {
			name = rawName
		}
		value := opFormatAffixValue(mod, rawName)
		if name == "" || value == "" {
			continue
		}
		affixes[slot] = append(affixes[slot], opAffix{Name: name, Value: value})
	}
	for slot := range affixes {
		seen := map[string]bool{}
		filtered := make([]opAffix, 0, 4)
		for _, affix := range affixes[slot] {
			key := affix.Name + ":" + affix.Value
			if seen[key] {
				continue
			}
			seen[key] = true
			filtered = append(filtered, affix)
			if len(filtered) >= 4 {
				break
			}
		}
		for len(filtered) < 4 {
			filtered = append(filtered, opAffix{Empty: true})
		}
		affixes[slot] = filtered
	}

	potentialLevel := pexInt(char, 0, "potential_level", "potentialLevel")
	if potentialLevel < 0 {
		potentialLevel = 0
	}
	if potentialLevel > 5 {
		potentialLevel = 5
	}
	return &opFriendPanel{
		NameCN:         pexString(template, "name_cn"),
		Level:          pexField(char, "level"),
		Stars:          opStars(6),
		PotentialLevel: potentialLevel,
		PromotionName:  pexString(pexMap(pexMap(char, "talent"), "latest_break_node_named"), "name_cn"),
		Core:           opFriendCore{HP: hp, ATK: atk, DEF: def, HPSub: hpSub, ATKSub: atkSub, DEFSub: defSub},
		Mini: opFriendMini{
			AGI:  int(math.Round(pexFloatOrZero(pexField(opFindAgg(aggregated, "Agi"), "final")))),
			STR:  int(math.Round(pexFloatOrZero(pexField(opFindAgg(aggregated, "Str"), "final")))),
			WISD: int(math.Round(pexFloatOrZero(pexField(opFindAgg(aggregated, "Wisd"), "final")))),
			WILL: int(math.Round(pexFloatOrZero(pexField(opFindAgg(aggregated, "Will"), "final")))),
			CRT:  crt,
			CDMG: cdmg,
		},
		Resist: opFriendResist{
			Physical:  opFormatOptional(panelSummary, "physical_resist", 1),
			Spell:     opFormatOptional(panelSummary, "spell_resist", 1),
			HealTaken: opFormatOptional(panelSummary, "heal_taken_bonus_pct", 1),
		},
		ChargeEfficiency:   charge,
		ArtsStrength:       arts,
		Matrix:             matrix,
		EquipAffixesBySlot: affixes,
	}
}

func opPadAffixes(items []opAffix) []opAffix {
	out := make([]opAffix, 0, 4)
	for _, item := range items {
		if len(out) >= 4 {
			break
		}
		out = append(out, item)
	}
	for len(out) < 4 {
		out = append(out, opAffix{Empty: true})
	}
	return out
}

func opBuildGearCards(panel *opPanelData, friend *opFriendPanel) []opGear {
	cards := make([]opGear, 0, 6)
	weapon := panel.Weapon
	if weapon == nil {
		weapon = &opGear{Name: "武器"}
	}
	weaponAffixes := make([]opAffix, 0)
	if friend != nil && friend.Matrix != nil {
		for _, term := range friend.Matrix.Terms {
			value := ""
			if term.Cost != "" {
				value = "+" + term.Cost
			}
			weaponAffixes = append(weaponAffixes, opAffix{Name: term.NameCN, Value: value})
		}
	}
	weaponCard := *weapon
	weaponCard.Type = "weapon"
	weaponCard.SlotLabel = "武器"
	weaponCard.Note = weapon.GemName
	weaponCard.Affixes = opPadAffixes(weaponAffixes)
	cards = append(cards, weaponCard)

	entries := []struct {
		key   string
		slot  int
		gear  *opGear
		label string
	}{
		{"bodyEquip", 1, panel.BodyEquip, "躯干装备"},
		{"armEquip", 0, panel.ArmEquip, "手部装备"},
		{"firstAccessory", 2, panel.FirstAccessory, "配件一"},
		{"secondAccessory", 3, panel.SecondAccessory, "配件二"},
		{"tacticalItem", -1, panel.TacticalItem, "战术物品"},
	}
	for _, entry := range entries {
		card := opGear{Type: entry.key, SlotLabel: entry.label, Name: "—"}
		if entry.gear != nil {
			card = *entry.gear
			card.Type = entry.key
			if card.SlotLabel == "" {
				card.SlotLabel = entry.label
			}
			card.Meta = entry.gear.SuitName
			if card.Meta == "" {
				card.Meta = entry.gear.Meta
			}
			card.Note = entry.gear.EffectTag
		}
		if friend != nil && entry.slot >= 0 && entry.slot < len(friend.EquipAffixesBySlot) {
			card.Affixes = friend.EquipAffixesBySlot[entry.slot]
		} else {
			card.Affixes = opPadAffixes(nil)
		}
		cards = append(cards, card)
	}
	return cards
}

// handlePanelSync 触发面板同步并短暂轮询状态。
func handlePanelSync(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	res, err := session.Client.getData(ctx, "panel_sync", map[string]any{})
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !res.ok() {
		return replyText(ctx, c, "面板同步触发失败。")
	}
	status := ""
	reason := ""
	for attempt := 0; attempt < 6; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		statusRes, err := session.Client.getData(ctx, "panel_sync_status", nil)
		if err != nil || !statusRes.ok() {
			continue
		}
		var data map[string]any
		if statusRes.decode(&data) != nil {
			continue
		}
		status = pexString(data, "status")
		if status == "completed" || status == "idle" {
			return replyText(ctx, c, "面板同步已完成。")
		}
		if status == "failed" {
			reason = opPanelSyncFailedReason(data)
			break
		}
	}
	if reason != "" {
		return replyText(ctx, c, "面板同步失败："+reason)
	}
	if status == "" {
		status = "未知"
	}
	return replyText(ctx, c, "面板同步已触发，当前状态："+status+"。同步在后台继续执行，请稍后在「:练度统计」或「:<干员名>面板」中查看。")
}

func opPanelSyncFailedReason(data map[string]any) string {
	for _, value := range []any{
		pexField(data, "error_message"),
		pexField(data, "error"),
		pexField(data, "reason"),
		pexField(data, "fail_reason"),
		pexField(data, "message"),
	} {
		text := strings.TrimSpace(pexStringValue(value))
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		if lower == "成功" || lower == "success" || lower == "ok" {
			continue
		}
		return text
	}
	failed := pexSlice(data["failed_ids"])
	if len(failed) > 0 {
		return fmt.Sprintf("同步失败，失败角色 %d 个", len(failed))
	}
	return "同步失败"
}

// handleTrainingStats 对齐上游练度统计。
func handleTrainingStats(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	detailRes, err := session.Client.getData(ctx, "endfield_card_detail", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !detailRes.ok() {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	var data map[string]any
	if err := detailRes.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	detail := pexMap(data, "detail")
	if detail == nil {
		detail = data
	}
	base := pexMap(detail, "base")
	chars := pexSlice(detail["chars"])
	if len(chars) == 0 {
		return replyText(ctx, c, getMessage("operator.training_empty"))
	}

	seeds := make([]opTrainingSeed, 0, len(chars))
	operatorNames := map[string]string{}
	_ = loadAssetJSON("data/operatorMap.json", &operatorNames)
	for index, raw := range chars {
		char := pexAsMap(raw)
		if char == nil {
			continue
		}
		c := pexMap(char, "charData")
		if c == nil {
			c = char
		}
		instID := pexString(char, "id", "instId")
		if instID == "" {
			instID = pexString(c, "id")
		}
		name := pexString(c, "name")
		if name == "" {
			name = pexString(char, "name")
		}
		if name == "" {
			name = pexString(pexMap(c, "template"), "name_cn")
		}
		if name == "" {
			name = operatorNames[pexString(char, "templateId", "template_id")]
		}
		if name == "" {
			name = fmt.Sprintf("角色%d", index+1)
		}
		rarity := opIntFromAny(pexField(pexMap(c, "rarity"), "value"), 0)
		weapon := pexMap(c, "weapon")
		if weapon == nil {
			weapon = pexMap(char, "weapon")
		}
		matrixName := pexString(pexMap(pexMap(weapon, "gem"), "gemData"), "name")
		level := pexField(c, "level")
		if level == nil {
			level = pexField(char, "level")
		}
		seeds = append(seeds, opTrainingSeed{
			InstID:      instID,
			Index:       index,
			Name:        name,
			Rarity:      rarity,
			Level:       level,
			WeaponName:  pexString(weapon, "name"),
			WeaponLevel: pexField(weapon, "level"),
			MatrixName:  matrixName,
		})
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].Rarity != seeds[j].Rarity {
			return seeds[i].Rarity > seeds[j].Rarity
		}
		left := opIntFromAny(seeds[i].Level, 0)
		right := opIntFromAny(seeds[j].Level, 0)
		if left != right {
			return left > right
		}
		return seeds[i].Index < seeds[j].Index
	})

	rows := opCollectTrainingRows(ctx, session, seeds)
	if len(rows) == 0 {
		return replyText(ctx, c, getMessage("operator.training_failed"))
	}
	opSortTrainingRows(rows)

	var body strings.Builder
	body.WriteString(`<div class="section"><h2>练度统计（共 ` + strconv.Itoa(len(rows)) + ` 名）</h2>`)
	body.WriteString(`<table><thead><tr><th>#</th><th>干员</th><th>Lv</th><th>技能</th><th>武器</th><th>基质</th><th>装备</th><th>物品</th></tr></thead><tbody>`)
	for index, row := range rows {
		body.WriteString(`<tr>`)
		body.WriteString(`<td>` + strconv.Itoa(index+1) + `</td>`)
		body.WriteString(`<td>★` + strconv.Itoa(row.Rarity) + ` ` + htmlEscape(row.Name) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(row.Level) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(strings.Join(row.SkillLevels[:], "/")) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(row.WeaponName) + ` Lv.` + htmlEscape(row.WeaponLevel) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(row.MatrixName) + ` Lv.` + htmlEscape(row.MatrixLevel) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(strings.Join(row.EquipLevels[:], "/")) + `</td>`)
		body.WriteString(`<td>` + htmlEscape(row.ItemName) + `</td>`)
		body.WriteString(`</tr>`)
	}
	body.WriteString(`</tbody></table></div>`)

	userName := pexString(base, "name")
	if userName == "" {
		userName = getMessage("common.unknown")
	}
	html := buildPage(pageOptions{
		title:    "练度统计",
		eyebrow:  "终末地 · 干员练度",
		subtitle: htmlEscape(fmt.Sprintf("%s · 共 %d 名", userName, len(rows))),
		seal:     "练度",
		body:     body.String(),
		width:    1200,
	})

	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, fmt.Sprintf("练度统计（共 %d 名）", len(rows)))
	for index, row := range rows {
		lines = append(lines, fmt.Sprintf("%d. %s Lv.%s | 技能 %s | 武器 %s Lv.%s | 基质 %s Lv.%s | 装备 %s | 物品 %s",
			index+1, row.Name, row.Level, strings.Join(row.SkillLevels[:], "/"), row.WeaponName, row.WeaponLevel,
			row.MatrixName, row.MatrixLevel, strings.Join(row.EquipLevels[:], "/"), row.ItemName))
	}
	return replyCard(ctx, c, html, 1200, strings.Join(lines, "\n"))
}

func opCollectTrainingRows(ctx context.Context, session *userSession, seeds []opTrainingSeed) []opTrainingRow {
	rows := make([]opTrainingRow, len(seeds))
	var wait sync.WaitGroup
	sem := make(chan struct{}, 4)
	for index, seed := range seeds {
		if seed.InstID == "" {
			rows[index] = opTrainingFallbackRow(seed)
			continue
		}
		wait.Add(1)
		go func(index int, seed opTrainingSeed) {
			defer wait.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := session.Client.getData(ctx, "endfield_card_char", map[string]any{
				"instId":   seed.InstID,
				"roleId":   session.Binding.roleID(),
				"serverId": session.Binding.serverID(),
			})
			if err != nil || !res.ok() {
				rows[index] = opTrainingFallbackRow(seed)
				return
			}
			var data map[string]any
			if err := res.decode(&data); err != nil {
				rows[index] = opTrainingFallbackRow(seed)
				return
			}
			operator, charData, userSkills, container := opExtractDetail(data)
			if operator == nil || charData == nil {
				rows[index] = opTrainingFallbackRow(seed)
				return
			}
			panel := opBuildPanelData(operator, charData, userSkills, container)
			skillLevels := opExtractTrainingSkillLevels(panel, operator, userSkills, container)
			matrixName, matrixLevel := opExtractTrainingMatrixInfo(panel, operator, container)
			weaponName := seed.WeaponName
			weaponLevel := seed.WeaponLevel
			if panel.Weapon != nil {
				if panel.Weapon.Name != "" {
					weaponName = panel.Weapon.Name
				}
				if panel.Weapon.Level != nil {
					weaponLevel = panel.Weapon.Level
				}
			}
			rows[index] = opTrainingRow{
				Name:        opTrainingName(panel.Name, seed.Name),
				Rarity:      seed.Rarity,
				Level:       opTrainingLevel(panel.Level, "-"),
				SkillLevels: skillLevels,
				WeaponName:  opTrainingName(weaponName, "-"),
				WeaponLevel: opTrainingLevel(weaponLevel, "-"),
				MatrixName:  opTrainingName(matrixName, "-"),
				MatrixLevel: opTrainingLevel(matrixLevel, "-"),
				EquipLevels: [4]string{
					opTrainingLevel(opGearLevel(panel.BodyEquip), "-"),
					opTrainingLevel(opGearLevel(panel.ArmEquip), "-"),
					opTrainingLevel(opGearLevel(panel.FirstAccessory), "-"),
					opTrainingLevel(opGearLevel(panel.SecondAccessory), "-"),
				},
				ItemName: opTrainingName(opExtractTrainingItemName(data, panel), "-"),
			}
		}(index, seed)
	}
	wait.Wait()
	out := make([]opTrainingRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}

func opGearLevel(gear *opGear) any {
	if gear == nil {
		return nil
	}
	return gear.Level
}

func opTrainingName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func opTrainingLevel(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return fallback
	}
	if number, ok := pexFloatValue(value); ok {
		return strconv.FormatInt(int64(math.Max(0, math.Floor(number))), 10)
	}
	text := pexStringValue(value)
	if text == "" {
		return fallback
	}
	return text
}

func opTrainingFallbackRow(seed opTrainingSeed) opTrainingRow {
	return opTrainingRow{
		Name:        opTrainingName(seed.Name, "未知"),
		Rarity:      seed.Rarity,
		Level:       opTrainingLevel(seed.Level, "-"),
		SkillLevels: [4]string{"-", "-", "-", "-"},
		WeaponName:  opTrainingName(seed.WeaponName, "-"),
		WeaponLevel: opTrainingLevel(seed.WeaponLevel, "-"),
		MatrixName:  opTrainingName(seed.MatrixName, "-"),
		MatrixLevel: "-",
		EquipLevels: [4]string{"-", "-", "-", "-"},
		ItemName:    "-",
	}
}

func opExtractTrainingSkillLevels(panel *opPanelData, operator, userSkills, container map[string]any) [4]string {
	levels := make([]string, 0, 4)
	push := func(value any) {
		if len(levels) >= 4 {
			return
		}
		if text := opTrainingLevel(value, ""); text != "" {
			levels = append(levels, text)
		}
	}
	for _, skill := range panel.DisplaySkills {
		if skill.Name == "" && skill.Level == 0 {
			continue
		}
		push(skill.Level)
	}
	sources := []any{
		operator["skills"],
		pexMap(operator, "charData")["skills"],
		container["skills"],
		pexMap(container, "charData")["skills"],
	}
	for _, source := range sources {
		if len(levels) >= 4 {
			break
		}
		for _, raw := range pexSlice(source) {
			skill := pexAsMap(raw)
			if skill == nil {
				continue
			}
			skillID := pexString(skill, "id", "skillId", "skill_id")
			level := pexField(skill, "level", "skillLevel", "skill_level")
			if level == nil {
				level = pexField(pexMap(userSkills, skillID), "level")
			}
			push(level)
			if len(levels) >= 4 {
				break
			}
		}
	}
	for len(levels) < 4 {
		levels = append(levels, "-")
	}
	var out [4]string
	copy(out[:], levels[:4])
	return out
}

func opExtractTrainingMatrixInfo(panel *opPanelData, operator, container map[string]any) (string, any) {
	name := ""
	var level any
	if panel.Weapon != nil {
		name = panel.Weapon.GemName
		level = panel.Weapon.GemLevel
	}
	if name == "" {
		gem := pexMap(pexMap(operator, "weapon"), "gem")
		if gem == nil {
			gem = pexMap(pexMap(container, "weapon"), "gem")
		}
		gemData := pexMap(gem, "gemData")
		if gemData == nil {
			gemData = gem
		}
		name = pexString(gemData, "name")
		if name == "" {
			name = pexString(pexMap(gemData, "template"), "name_cn", "name")
		}
		if level == nil {
			for _, key := range []string{"level", "lv", "rank", "tier", "phase", "breakthroughLevel", "refineLevel", "intensifyLevel", "enhanceLevel"} {
				if value := pexField(gem, key); value != nil {
					level = value
					break
				}
			}
			if level == nil {
				for _, key := range []string{"level", "lv"} {
					if value := pexField(gemData, key); value != nil {
						level = value
						break
					}
				}
			}
		}
	}
	if strings.TrimSpace(name) == "" {
		name = "-"
	}
	return name, level
}

func opExtractTrainingItemName(data map[string]any, panel *opPanelData) string {
	detail := pexMap(data, "detail")
	if detail == nil {
		detail = data
	}
	char := pexMap(detail, "char")
	equipMedicine := pexMap(char, "equip_medicine")
	if equipMedicine == nil {
		equipMedicine = pexMap(char, "equipMedicine")
	}
	name := pexString(equipMedicine, "name_cn", "name")
	if name == "" && panel.TacticalItem != nil {
		name = panel.TacticalItem.Name
	}
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	return name
}

func opTrainingMetrics(row opTrainingRow) (rarity, level, skillTotal, weaponLevel, equipTotal, matrixLevel int) {
	rarity = row.Rarity
	level = opIntFromAny(row.Level, 0)
	for _, value := range row.SkillLevels {
		skillTotal += opIntFromAny(value, 0)
	}
	weaponLevel = opIntFromAny(row.WeaponLevel, 0)
	for _, value := range row.EquipLevels {
		equipTotal += opIntFromAny(value, 0)
	}
	matrixLevel = opIntFromAny(row.MatrixLevel, 0)
	return
}

func opSortTrainingRows(rows []opTrainingRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ar, al, ask, aw, ae, am := opTrainingMetrics(rows[i])
		br, bl, bsk, bw, be, bm := opTrainingMetrics(rows[j])
		if ar != br {
			return ar > br
		}
		if al != bl {
			return al > bl
		}
		if ask != bsk {
			return ask > bsk
		}
		if aw != bw {
			return aw > bw
		}
		if ae != be {
			return ae > be
		}
		if am != bm {
			return am > bm
		}
		return strings.Compare(rows[i].Name, rows[j].Name) < 0
	})
}

// handleOperatorPanel 对齐上游 <干员名>面板。
func handleOperatorPanel(ctx context.Context, c sdk.Call, cfg config, operatorName string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	operatorName = strings.TrimSpace(operatorName)
	if operatorName == "" {
		return replyText(ctx, c, getMessage("operator.provide_name", map[string]any{"prefix": ":"}))
	}

	noteRes, err := session.Client.getData(ctx, "note", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !noteRes.ok() {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	var noteData map[string]any
	if err := noteRes.decode(&noteData); err != nil {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	base := pexMap(noteData, "base")
	chars := pexSlice(noteData["chars"])
	if len(chars) == 0 {
		return replyText(ctx, c, getMessage("operator.not_found_info"))
	}

	var matched map[string]any
	for _, raw := range chars {
		char := pexAsMap(raw)
		if char != nil && pexString(char, "name") == operatorName {
			matched = char
			break
		}
	}
	if matched == nil {
		for _, raw := range chars {
			char := pexAsMap(raw)
			if char != nil && strings.Contains(pexString(char, "name"), operatorName) {
				matched = char
				break
			}
		}
	}
	if matched == nil {
		return replyText(ctx, c, getMessage("operator.not_found", map[string]any{"name": operatorName}))
	}
	instID := pexString(matched, "id", "instId")
	if instID == "" {
		return replyText(ctx, c, getMessage("operator.no_operator_id"))
	}
	templateID := pexString(matched, "templateId", "template_id")
	roleID := session.Binding.roleID()
	serverID := session.Binding.serverID()
	hasAPIKey := cfg.hasAPIKey()

	panelTemplateID := ""
	var friendCharData map[string]any
	if hasAPIKey {
		if panelRes, err := session.Client.getData(ctx, "panel_chars", map[string]any{"page": 1, "page_size": 50}); err == nil && panelRes.ok() {
			var panelData map[string]any
			if panelRes.decode(&panelData) == nil {
				targetName := pexString(matched, "name")
				if targetName == "" {
					targetName = operatorName
				}
				for _, raw := range pexSlice(panelData["synced_chars"]) {
					row := pexAsMap(raw)
					if row == nil {
						continue
					}
					rowTemplate := pexString(row, "template_id", "templateId")
					rowName := pexString(row, "name_cn", "name")
					if (templateID != "" && rowTemplate == templateID) || (rowName != "" && rowName == targetName) {
						panelTemplateID = rowTemplate
						break
					}
				}
				if panelTemplateID == "" {
					panelTemplateID = templateID
				}
			}
		}
	}
	if hasAPIKey && panelTemplateID != "" {
		if panelRes, err := session.Client.getData(ctx, "panel_char_detail", map[string]any{"template_id": panelTemplateID}); err == nil {
			var payload map[string]any
			if panelRes.decode(&payload) == nil {
				friendCharData = payload
			}
		}
	}

	friendRoleID := ""
	friendTemplateID := ""
	if friendCharData == nil && hasAPIKey {
		if friendRes, err := session.Client.getData(ctx, "friend_detail", nil); err == nil {
			var payload map[string]any
			if friendRes.decode(&payload) == nil {
				if !friendRes.ok() {
					payload = pexMap(payload, "data")
				}
				profile := pexMap(payload, "role_profile")
				friendRoleID = pexString(profile, "role_id", "roleId")
				targetName := pexString(matched, "name")
				if targetName == "" {
					targetName = operatorName
				}
				for _, raw := range pexSlice(profile["char_data"]) {
					item := pexAsMap(raw)
					if item == nil {
						continue
					}
					template := pexMap(item, "template")
					if pexString(template, "name_cn") == targetName {
						friendTemplateID = pexString(item, "template_id")
						if friendTemplateID == "" {
							friendTemplateID = pexString(template, "id")
						}
						break
					}
				}
			}
		}
	}
	enableFriendPanel := friendCharData != nil || (friendRoleID != "" && friendTemplateID != "")
	finalTemplateID := panelTemplateID
	if finalTemplateID == "" {
		finalTemplateID = friendTemplateID
	}
	if finalTemplateID == "" {
		finalTemplateID = templateID
	}

	charRes, err := session.Client.getData(ctx, "endfield_card_char", map[string]any{
		"instId":   instID,
		"roleId":   roleID,
		"serverId": serverID,
	})
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !charRes.ok() {
		return replyText(ctx, c, getMessage("operator.get_detail_failed"))
	}
	var charDataRaw map[string]any
	if err := charRes.decode(&charDataRaw); err != nil {
		return replyText(ctx, c, getMessage("operator.get_detail_failed"))
	}
	if friendCharData == nil && hasAPIKey && enableFriendPanel && finalTemplateID != "" && friendRoleID != "" {
		if friendRes, err := session.Client.getData(ctx, "friend_char", map[string]any{
			"role_id":     friendRoleID,
			"template_id": finalTemplateID,
		}); err == nil {
			var payload map[string]any
			if friendRes.decode(&payload) == nil {
				if !friendRes.ok() {
					payload = pexMap(payload, "data")
				}
				friendCharData = payload
			}
		}
	}

	operator, charData, userSkills, container := opExtractDetail(charDataRaw)
	if operator == nil || charData == nil {
		return replyText(ctx, c, getMessage("operator.not_found_info"))
	}
	panel := opBuildPanelData(operator, charData, userSkills, container)
	operatorNames := map[string]string{}
	_ = loadAssetJSON("data/operatorMap.json", &operatorNames)
	if (panel.Name == "" || panel.Name == "未知") && templateID != "" {
		if name := operatorNames[templateID]; name != "" {
			panel.Name = name
		}
	}
	var friendPanel *opFriendPanel
	if enableFriendPanel {
		friendPanel = opBuildFriendPanelData(friendCharData)
	}
	if friendPanel != nil && friendPanel.NameCN != "" {
		panel.Name = friendPanel.NameCN
	}
	gearCards := opBuildGearCards(panel, friendPanel)

	body := opPanelBody(panel, friendPanel, gearCards)
	userName := pexString(base, "name")
	if userName == "" {
		userName = getMessage("common.unknown")
	}
	html := buildPage(pageOptions{
		title:    panel.Name + " 面板",
		eyebrow:  "终末地 · 干员面板",
		subtitle: htmlEscape(fmt.Sprintf("%s · Lv.%s", userName, playerFormatValue(panel.Level))),
		seal:     "面板",
		body:     body,
		width:    980,
	})
	return replyCard(ctx, c, html, 980, opPanelFallbackText(panel, friendPanel))
}

func opPanelBody(panel *opPanelData, friend *opFriendPanel, gearCards []opGear) string {
	var b strings.Builder
	b.WriteString(`<div class="section"><h2>` + htmlEscape(panel.Name) + `</h2><div class="card">`)
	b.WriteString(`<div class="v" style="font-size:26px">` + htmlEscape(panel.Name) + ` <small>Lv.` + htmlEscape(playerFormatValue(panel.Level)) + ` · ★` + strconv.Itoa(len(panel.Stars)) + ` · 潜能 ` + strconv.Itoa(panel.PotentialLevel) + `</small></div>`)
	b.WriteString(`<div class="tags" style="margin-top:10px">`)
	for _, tag := range panel.Tags {
		b.WriteString(`<span class="chip">` + htmlEscape(tag) + `</span>`)
	}
	for _, chip := range panel.IdentityChips {
		label := chip.Label + "：" + chip.Value
		if chip.IconURL != "" {
			label = `<img src="` + chip.IconURL + `" alt="" style="width:16px;height:16px;border-radius:4px;vertical-align:middle"> ` + htmlEscape(label)
		} else {
			label = htmlEscape(label)
		}
		b.WriteString(`<span class="chip">` + label + `</span>`)
	}
	b.WriteString(`</div></div></div>`)

	coreHP, coreATK, coreDEF := 0, 0, 0
	hpSub, atkSub, defSub := "", "", ""
	mini := opFriendMini{}
	resist := opFriendResist{}
	charge, arts := "", ""
	if friend != nil {
		coreHP, coreATK, coreDEF = friend.Core.HP, friend.Core.ATK, friend.Core.DEF
		hpSub, atkSub, defSub = friend.Core.HPSub, friend.Core.ATKSub, friend.Core.DEFSub
		mini = friend.Mini
		resist = friend.Resist
		charge, arts = friend.ChargeEfficiency, friend.ArtsStrength
	}
	b.WriteString(`<div class="section"><h2>核心属性</h2><div class="grid c3">`)
	b.WriteString(playerStatCard("生命值", strconv.Itoa(coreHP), hpSub))
	b.WriteString(playerStatCard("攻击力", strconv.Itoa(coreATK), atkSub))
	b.WriteString(playerStatCard("防御力", strconv.Itoa(coreDEF), defSub))
	b.WriteString(`</div></div>`)

	b.WriteString(`<div class="section"><h2>能力属性</h2><div class="grid c4">`)
	b.WriteString(playerStatCard("敏捷", strconv.Itoa(mini.AGI), ""))
	b.WriteString(playerStatCard("力量", strconv.Itoa(mini.STR), ""))
	b.WriteString(playerStatCard("智识", strconv.Itoa(mini.WISD), ""))
	b.WriteString(playerStatCard("意志", strconv.Itoa(mini.WILL), ""))
	b.WriteString(playerStatCard("暴击率", mini.CRT, ""))
	b.WriteString(playerStatCard("暴击伤害", mini.CDMG, ""))
	b.WriteString(playerStatCard("充能效率", charge, ""))
	b.WriteString(playerStatCard("源石技艺强度", arts, ""))
	b.WriteString(`</div>`)
	b.WriteString(`<div class="grid c3" style="margin-top:12px">`)
	b.WriteString(playerStatCard("物理抗性", resist.Physical, ""))
	b.WriteString(playerStatCard("法术抗性", resist.Spell, ""))
	b.WriteString(playerStatCard("受治疗加成", resist.HealTaken, ""))
	b.WriteString(`</div></div>`)

	if len(panel.Skills) > 0 {
		b.WriteString(`<div class="section"><h2>技能</h2>`)
		for _, skill := range panel.Skills {
			b.WriteString(`<div class="card" style="margin-bottom:10px"><div class="v" style="font-size:18px">` + htmlEscape(skill.Name) + ` <small>Lv.` + strconv.Itoa(skill.Level) + `</small></div>`)
			if skill.Desc != "" {
				b.WriteString(`<div class="body-text" style="font-size:14px">` + htmlEscape(skill.Desc) + `</div>`)
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	}

	if len(gearCards) > 0 {
		b.WriteString(`<div class="section"><h2>武器与装备</h2>`)
		for _, gear := range gearCards {
			b.WriteString(`<div class="card" style="margin-bottom:10px">`)
			b.WriteString(`<div class="row"><span class="label">` + htmlEscape(gear.SlotLabel) + `</span><span class="value">` + htmlEscape(gear.Name))
			if gear.Level != nil {
				b.WriteString(` Lv.` + htmlEscape(playerFormatValue(gear.Level)))
			}
			b.WriteString(`</span></div>`)
			meta := []string{}
			if gear.Meta != "" {
				meta = append(meta, gear.Meta)
			}
			if gear.Note != "" {
				meta = append(meta, gear.Note)
			}
			if len(meta) > 0 {
				b.WriteString(`<div class="note">` + htmlEscape(strings.Join(meta, " · ")) + `</div>`)
			}
			for _, affix := range gear.Affixes {
				if affix.Empty {
					continue
				}
				b.WriteString(`<div class="row"><span class="label">` + htmlEscape(affix.Name) + `</span><span class="value">` + htmlEscape(affix.Value) + `</span></div>`)
			}
			if gear.Desc != "" {
				b.WriteString(`<div class="body-text" style="font-size:13px">` + htmlEscape(gear.Desc) + `</div>`)
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	}

	talentGroups := []struct {
		label string
		items []opTalent
	}{
		{"作战天赋", panel.CombatTalents},
		{"能力扩延", panel.AbilityTalents},
		{"驻舰天赋", panel.CultivationTalents},
	}
	for _, group := range talentGroups {
		if len(group.items) == 0 {
			continue
		}
		b.WriteString(`<div class="section"><h2>` + group.label + `</h2>`)
		for _, talent := range group.items {
			b.WriteString(`<div class="row"><span class="label">` + htmlEscape(talent.Name) + `</span><span class="value">` + htmlEscape(talent.Desc) + `</span></div>`)
		}
		b.WriteString(`</div>`)
	}
	if len(panel.EquipSuits) > 0 {
		b.WriteString(`<div class="section"><h2>套装效果</h2>`)
		for _, suit := range panel.EquipSuits {
			b.WriteString(`<div class="row"><span class="label">` + htmlEscape(fmt.Sprintf("%s ×%d", suit.Name, suit.Count)) + `</span><span class="value">` + htmlEscape(suit.Effect) + `</span></div>`)
		}
		b.WriteString(`</div>`)
	}
	return b.String()
}

func opPanelFallbackText(panel *opPanelData, friend *opFriendPanel) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("【%s】Lv.%s ★%d 潜能%d\n", panel.Name, playerFormatValue(panel.Level), len(panel.Stars), panel.PotentialLevel))
	if panel.Profession != "" || panel.Property != "" {
		b.WriteString(fmt.Sprintf("职业：%s｜属性：%s｜阶段：精英化%s\n", panel.Profession, panel.Property, playerFormatValue(panel.EvolvePhase)))
	}
	if friend != nil {
		b.WriteString(fmt.Sprintf("生命：%d（%s）｜攻击：%d（%s）｜防御：%d（%s）\n", friend.Core.HP, friend.Core.HPSub, friend.Core.ATK, friend.Core.ATKSub, friend.Core.DEF, friend.Core.DEFSub))
		b.WriteString(fmt.Sprintf("敏捷 %d｜力量 %d｜智识 %d｜意志 %d｜暴击 %s｜暴伤 %s\n", friend.Mini.AGI, friend.Mini.STR, friend.Mini.WISD, friend.Mini.WILL, friend.Mini.CRT, friend.Mini.CDMG))
	}
	if len(panel.Skills) > 0 {
		b.WriteString("技能：")
		parts := make([]string, 0, len(panel.Skills))
		for _, skill := range panel.Skills {
			parts = append(parts, fmt.Sprintf("%s Lv.%d", skill.Name, skill.Level))
		}
		b.WriteString(strings.Join(parts, "｜") + "\n")
	}
	if panel.Weapon != nil {
		b.WriteString(fmt.Sprintf("武器：%s Lv.%s", panel.Weapon.Name, playerFormatValue(panel.Weapon.Level)))
		if panel.Weapon.GemName != "" {
			b.WriteString("｜基质：" + panel.Weapon.GemName)
		}
		b.WriteString("\n")
	}
	equips := []*opGear{panel.BodyEquip, panel.ArmEquip, panel.FirstAccessory, panel.SecondAccessory}
	parts := make([]string, 0, len(equips))
	for _, equip := range equips {
		if equip != nil {
			parts = append(parts, fmt.Sprintf("%s Lv.%s", equip.Name, playerFormatValue(equip.Level)))
		}
	}
	if len(parts) > 0 {
		b.WriteString("装备：" + strings.Join(parts, "｜") + "\n")
	}
	return strings.TrimSpace(b.String())
}

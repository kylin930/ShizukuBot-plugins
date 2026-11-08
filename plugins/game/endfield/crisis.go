package endfield

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

var (
	crisisDescRe       = regexp.MustCompile(`\{(?:@([^@{}]+)@)?([^{}:]+):([^{}]+)\}`)
	crisisNumberRe     = regexp.MustCompile(`[+-]?\d+(?:\.\d+)?%?`)
	crisisIdentRe      = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	crisisSafeExprRe   = regexp.MustCompile(`^[\d+\-*/().\s]+$`)
	crisisNormalizeRe  = regexp.MustCompile(`[“”"'\s]`)
	crisisIndexRe      = regexp.MustCompile(`^第?(\d+)(?:个|期)?$`)
	crisisReductionRe  = regexp.MustCompile(`^-([A-Za-z_][A-Za-z0-9_]*)$`)
	crisisTagRe        = regexp.MustCompile(`<[^>]+>`)
	crisisCleanSpaceRe = regexp.MustCompile(`\s+`)
	crisisPlusMinusRe  = regexp.MustCompile(`\+\s*-`)
	crisisTrailingOpRe = regexp.MustCompile(`(?:/[+-]?)+$`)
)

func init() {
	registerRoute(78, prefixMatch("危机合约"), handleCrisisContract)
	registerRoute(76, prefixMatch("危机", "合约"), handleCrisisContract)
}

type crisisCard struct {
	Label string
	Value string
}

type crisisRoleInfo struct {
	Name      string
	RoleID    string
	Level     any
	AvatarURL string
}

type crisisContractInfo struct {
	ID                string
	Name              string
	ActivityName      string
	Cover             string
	HeaderImage       string
	TimeRange         string
	GameplayTimeRange string
}

type crisisMedalInfo struct {
	Name     string
	Icon     string
	Status   string
	IsPlated bool
}

type crisisIndicator struct {
	ID          string
	Name        string
	GroupName   string
	Desc        string
	Score       float64
	Type        int
	Icon        string
	HasAward    bool
	Selected    bool
	Unlocked    bool
	Depends     []string
	DependsText string
}

type crisisIndicatorGroup struct {
	Name          string
	Type          int
	Icon          string
	Count         int
	Scores        []float64
	ScoreText     string
	ScoreTags     []string
	DescPreview   string
	UnlockedCount int
	UnlockedText  string
	AwardCount    int
	SelectedCount int
	DependsText   string
	HasAward      bool
	Selected      bool
}

type crisisIndicatorDisplay struct {
	Stats         map[string]int
	StatCards     []crisisCard
	Basic         []crisisIndicatorGroup
	Advanced      []crisisIndicatorGroup
	BasicTotal    int
	AdvancedTotal int
	Hint          string
}

type crisisRecordChar struct {
	Name           string
	AvatarURL      string
	Level          any
	Rarity         any
	Property       string
	PotentialLevel any
}

type crisisRecord struct {
	Score          any
	IndicatorCount float64
	Time           string
	Date           string
	Duration       string
	IsPass         bool
	IsBest         bool
	Status         string
	PassWave       int
	WaveText       string
	Chars          []crisisRecordChar
}

type crisisHistoryInfo struct {
	Best    *crisisRecord
	Records []crisisRecord
}

type crisisData struct {
	Title              string
	IsIndicatorView    bool
	Role               crisisRoleInfo
	Contract           crisisContractInfo
	SummaryCards       []crisisCard
	Highest            any
	Medal              crisisMedalInfo
	Indicators         []crisisIndicator
	IndicatorDisplay   crisisIndicatorDisplay
	SelectedIndicators []crisisIndicator
	History            crisisHistoryInfo
}

func handleCrisisContract(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	indicatorOnly, keyword := crisisParseKeyword(args)
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	cardDetailRes, cardErr := session.Client.getData(ctx, "endfield_card_detail", nil)
	if cardErr != nil {
		cardDetailRes = nil
	}
	contracts := crisisExtractContracts(cardDetailRes)
	contract := crisisPickContract(contracts, keyword)
	if contract == nil && keyword != "" {
		contract = map[string]any{"id": keyword, "name": keyword}
	}
	contractID := ""
	if contract != nil {
		contractID = pexString(contract, "id")
	}
	if contractID == "" {
		if keyword != "" {
			return replyText(ctx, c, getMessage("crisis_contract.no_match", map[string]any{"keyword": keyword}))
		}
		return replyText(ctx, c, getMessage("crisis_contract.not_found_info"))
	}

	res, err := session.Client.getData(ctx, "crisis_contract", map[string]any{
		"contractId": contractID,
		"roleId":     session.Binding.roleID(),
		"serverId":   session.Binding.serverID(),
	})
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !res.ok() {
		return replyText(ctx, c, getMessage("crisis_contract.get_failed"))
	}
	crisis := crisisExtractPayload(res)
	if crisis == nil {
		return replyText(ctx, c, getMessage("crisis_contract.not_found_info"))
	}
	renderData := crisisBuildRenderData(crisis, contract, cardDetailRes, indicatorOnly)
	html := crisisCardHTML(renderData)
	return replyCard(ctx, c, html, 760, crisisFormatText(renderData))
}

func crisisParseKeyword(raw string) (bool, string) {
	indicatorOnly := false
	fields := make([]string, 0)
	for _, field := range strings.Fields(raw) {
		lower := strings.ToLower(field)
		switch {
		case field == "原始" || field == "响应" || field == "调试" || lower == "debug":
			continue
		case field == "指标" || field == "词条":
			indicatorOnly = true
		default:
			fields = append(fields, field)
		}
	}
	return indicatorOnly, strings.Join(fields, " ")
}

func crisisCleanText(text string) string {
	cleaned := crisisTagRe.ReplaceAllString(text, "")
	cleaned = crisisCleanSpaceRe.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

func crisisNormalizeText(text string) string {
	return strings.ToLower(crisisNormalizeRe.ReplaceAllString(text, ""))
}

func crisisToArray(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	m := pexAsMap(value)
	if m == nil {
		return nil
	}
	for _, key := range []string{"list", "items", "tasks", "missions", "records", "data"} {
		if list, ok := m[key].([]any); ok {
			return list
		}
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		if pexAsMap(m[key]) != nil {
			out = append(out, m[key])
		}
	}
	return out
}

func crisisFormatDate(value any) string {
	ts, ok := pexFloatValue(value)
	if !ok || ts == 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02")
}

func crisisFormatDateTime(value any) string {
	ts, ok := pexFloatValue(value)
	if !ok || ts == 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("01-02 15:04")
}

func crisisFormatRange(startRaw, endRaw any) string {
	start, _ := pexFloatValue(startRaw)
	end, _ := pexFloatValue(endRaw)
	if start == 0 && end == 0 {
		return "长期"
	}
	if start != 0 && end == 0 {
		return crisisFormatDate(start) + " 起"
	}
	if start == 0 && end != 0 {
		return "至 " + crisisFormatDate(end)
	}
	return crisisFormatDate(start) + " ~ " + crisisFormatDate(end)
}

func crisisFormatDuration(seconds any) string {
	value, _ := pexFloatValue(seconds)
	if value < 0 {
		value = 0
	}
	total := int(value)
	minutes := total / 60
	rest := total % 60
	return fmt.Sprintf("%02d:%02d", minutes, rest)
}

func crisisExtractContracts(cardDetailRes *apiResponse) []map[string]any {
	if cardDetailRes == nil {
		return nil
	}
	var data map[string]any
	if cardDetailRes.decode(&data) != nil {
		return nil
	}
	detail := pexMap(data, "detail")
	if detail == nil {
		detail = data
	}
	rawList := pexField(detail, "crisisContract", "crisis_contract", "crisisContracts")
	out := make([]map[string]any, 0)
	for _, item := range crisisToArray(rawList) {
		raw := pexAsMap(item)
		if raw == nil {
			raw = map[string]any{"id": item}
		}
		status := pexMap(raw, "status")
		if status == nil {
			status = raw
		}
		id := pexString(raw, "id", "contractId", "contract_id", "activityId", "activity_id")
		if id == "" {
			continue
		}
		name := pexString(status, "name", "title", "contractName", "activityName", "activity_name")
		if name == "" {
			name = id
		}
		start, _ := pexNumber(status, "startTs", "start_ts", "activityStartTs", "activity_start_ts", "startAtTs")
		end, _ := pexNumber(status, "endTs", "end_ts", "activityEndTs", "activity_end_ts", "endAtTs")
		raw["id"] = id
		raw["name"] = name
		raw["startTs"] = start
		raw["endTs"] = end
		out = append(out, raw)
	}
	return out
}

func crisisPickContract(contracts []map[string]any, keyword string) map[string]any {
	key := crisisNormalizeText(keyword)
	if key != "" {
		for _, item := range contracts {
			if crisisNormalizeText(pexString(item, "id")) == key {
				return item
			}
		}
		if match := crisisIndexRe.FindStringSubmatch(strings.TrimSpace(keyword)); len(match) > 1 {
			index, _ := strconv.Atoi(match[1])
			if index > 0 && index <= len(contracts) {
				return contracts[index-1]
			}
		}
		for _, item := range contracts {
			for _, field := range []string{"name", "activityName", "title", "id"} {
				if strings.Contains(crisisNormalizeText(pexString(item, field)), key) {
					return item
				}
			}
		}
		return nil
	}
	now := time.Now().Unix()
	for _, item := range contracts {
		if pexBool(item, "isInActivity", "isActive", "active") {
			return item
		}
	}
	for _, item := range contracts {
		start, _ := pexNumber(item, "startTs")
		end, _ := pexNumber(item, "endTs")
		if start > 0 && end > 0 && float64(now) >= start && float64(now) <= end {
			return item
		}
	}
	if len(contracts) > 0 {
		return contracts[0]
	}
	return nil
}

func crisisExtractPayload(res *apiResponse) map[string]any {
	if res == nil {
		return nil
	}
	var data map[string]any
	if res.decode(&data) != nil {
		return nil
	}
	value := pexField(data, "crisisContract", "crisis_contract")
	if value == nil {
		detail := pexMap(data, "detail")
		value = pexField(detail, "crisisContract", "crisis_contract")
	}
	if value == nil {
		value = data
	}
	return pexAsMap(value)
}

func crisisBuildRenderData(crisis, contract map[string]any, cardDetailRes *apiResponse, indicatorOnly bool) crisisData {
	status := pexMap(crisis, "status")
	indicators := crisisBuildIndicators(crisis["indicators"])
	charNameMap := crisisBuildCharNameMap(cardDetailRes)
	role := crisisBuildRoleInfo(cardDetailRes)

	startRaw := pexField(status, "startAtTs", "startTs", "start_ts", "activityStartTs", "activity_start_ts")
	if startRaw == nil {
		startRaw = pexField(contract, "startTs")
	}
	endRaw := pexField(status, "endAtTs", "endTs", "end_ts", "activityEndTs", "activity_end_ts")
	if endRaw == nil {
		endRaw = pexField(contract, "endTs")
	}
	gameplayEnd := pexField(status, "gameplayEndAtTs", "gameplayEndTs")
	score := pexField(status, "highest", "maxScore", "highestScore", "bestScore", "score")
	unlocked := 0
	for _, item := range indicators {
		if item.Unlocked {
			unlocked++
		}
	}
	indicatorDisplay := crisisBuildIndicatorDisplay(indicators, indicatorOnly)
	selected := make([]crisisIndicator, 0)
	for _, item := range indicators {
		if item.Selected {
			selected = append(selected, item)
			if len(selected) >= 16 {
				break
			}
		}
	}

	contractInfo := crisisContractInfo{
		ID:           pexString(contract, "id"),
		Name:         crisisCleanText(pexString(status, "name", "title", "contractName", "activityName")),
		ActivityName: crisisCleanText(pexString(status, "activityName", "activity_name")),
		Cover:        pexString(status, "kvImage", "headerImage", "kv", "kvUrl", "kvImg", "pic", "banner", "bannerUrl"),
		HeaderImage:  pexString(status, "headerImage"),
		TimeRange:    crisisFormatRange(startRaw, endRaw),
	}
	if contractInfo.Name == "" {
		contractInfo.Name = crisisCleanText(pexString(contract, "name"))
	}
	if contractInfo.Name == "" {
		contractInfo.Name = getMessage("common.unknown")
	}
	if contractInfo.Cover == "" {
		contractInfo.Cover = pexString(contract, "pic", "kv", "bannerUrl")
	}
	if gameplayEnd != nil {
		contractInfo.GameplayTimeRange = crisisFormatRange(startRaw, gameplayEnd)
	}

	summary := crisisBuildMissionCards(status)
	summary = append(summary, crisisCard{Label: "解锁指标", Value: fmt.Sprintf("%d/%d", unlocked, len(indicators))})

	return crisisData{
		Title:              "危机合约",
		IsIndicatorView:    indicatorOnly,
		Role:               role,
		Contract:           contractInfo,
		SummaryCards:       summary,
		Highest:            score,
		Medal:              crisisBuildMedal(status),
		Indicators:         indicators,
		IndicatorDisplay:   indicatorDisplay,
		SelectedIndicators: selected,
		History:            crisisBuildHistory(pexMap(crisis, "history"), charNameMap),
	}
}

func crisisBuildRoleInfo(cardDetailRes *apiResponse) crisisRoleInfo {
	role := crisisRoleInfo{Name: getMessage("common.unknown"), RoleID: getMessage("common.unknown")}
	if cardDetailRes == nil {
		return role
	}
	var data map[string]any
	if cardDetailRes.decode(&data) != nil {
		return role
	}
	detail := pexMap(data, "detail")
	if detail == nil {
		detail = data
	}
	base := pexMap(detail, "base")
	role.Name = pexString(base, "name")
	role.RoleID = pexString(base, "roleId", "role_id")
	role.Level = pexField(base, "level")
	role.AvatarURL = pexString(base, "avatarUrl", "avatar_url")
	if role.Name == "" {
		role.Name = getMessage("common.unknown")
	}
	if role.RoleID == "" {
		role.RoleID = getMessage("common.unknown")
	}
	return role
}

func crisisBuildCharNameMap(cardDetailRes *apiResponse) map[string]string {
	out := map[string]string{}
	if cardDetailRes == nil {
		return out
	}
	var data map[string]any
	if cardDetailRes.decode(&data) != nil {
		return out
	}
	detail := pexMap(data, "detail")
	if detail == nil {
		detail = data
	}
	for _, raw := range pexSlice(detail["chars"]) {
		char := pexAsMap(raw)
		if char == nil {
			continue
		}
		data := pexMap(char, "charData")
		if data == nil {
			data = char
		}
		id := pexString(char, "id", "instId")
		if id == "" {
			id = pexString(data, "id", "charId")
		}
		name := pexString(data, "name")
		if name == "" {
			name = pexString(pexMap(data, "template"), "name_cn")
		}
		if id != "" && name != "" {
			out[id] = name
		}
	}
	return out
}

func crisisBuildMedal(status map[string]any) crisisMedalInfo {
	achieve := pexMap(status, "achieve")
	if achieve == nil {
		achieve = pexMap(status, "medal")
	}
	if achieve == nil {
		achieve = pexMap(status, "achievement")
	}
	raw := pexMap(achieve, "achievementData")
	if raw == nil {
		raw = achieve
	}
	level := pexInt(achieve, 0, "level")
	if level == 0 {
		level = pexInt(raw, 0, "initLevel")
	}
	isPlated := pexBool(achieve, "isPlated")
	name := pexString(raw, "name", "title")
	if name == "" {
		name = pexString(status, "medalName", "achievementName")
	}
	if name == "" {
		name = "奖章"
	}
	icon := ""
	if isPlated {
		icon = pexString(raw, "platedIcon")
		if icon == "" {
			icon = pexString(raw, "initIcon")
		}
	} else {
		icon = pexString(raw, "initIcon", "icon", "iconUrl")
	}
	statusText := ""
	if level > 0 {
		statusText = fmt.Sprintf("Lv.%d", level)
		if isPlated {
			statusText += " · 镀层"
		}
	}
	return crisisMedalInfo{Name: name, Icon: icon, Status: statusText, IsPlated: isPlated}
}

func crisisBuildMissionCards(status map[string]any) []crisisCard {
	type pair struct {
		label string
		raw   any
	}
	pairs := []pair{
		{"挑战次数", pexField(status, "challengeCount")},
		{"周期任务", pexField(status, "weeklyMission")},
		{"指标任务", pexField(status, "indicatorMission")},
		{"阶段任务", pexField(status, "stageMission")},
	}
	out := make([]crisisCard, 0, len(pairs))
	for _, item := range pairs {
		m := pexAsMap(item.raw)
		count := pexField(m, "count", "current", "progress")
		if count == nil {
			count = item.raw
		}
		total := pexField(m, "total", "max")
		value := "0"
		if count != nil {
			value = pexStringValue(count)
		}
		if total != nil {
			countText := "0"
			if count != nil {
				countText = pexStringValue(count)
			}
			value = countText + "/" + pexStringValue(total)
		}
		out = append(out, crisisCard{Label: item.label, Value: value})
	}
	return out
}

func crisisBuildRecord(raw map[string]any, charNameMap map[string]string) *crisisRecord {
	if raw == nil {
		return nil
	}
	scoreRaw := pexField(raw, "indicatorCount", "score", "bestScore", "totalScore", "maxScore")
	score := any("—")
	indicatorCount := 0.0
	if value, ok := pexFloatValue(scoreRaw); ok {
		indicatorCount = value
		score = opFormatPanelNumber(value, 0)
	}
	ts := pexField(raw, "ts", "time", "createTs", "challengeTs", "finishTs")
	duration := pexField(raw, "passTs", "duration", "costTime", "clearTime")
	chars := make([]crisisRecordChar, 0)
	for _, item := range crisisToArray(pexField(raw, "chars", "team", "operators")) {
		if len(chars) >= 4 {
			break
		}
		char := pexAsMap(item)
		if char == nil {
			continue
		}
		data := pexMap(char, "charData")
		if data == nil {
			data = char
		}
		id := pexString(data, "charId", "char_id", "id", "instId")
		name := pexString(data, "name")
		if name == "" {
			name = charNameMap[id]
		}
		if name == "" {
			name = id
		}
		if name == "" {
			name = getMessage("common.unknown")
		}
		chars = append(chars, crisisRecordChar{
			Name:           name,
			AvatarURL:      pexString(data, "avatarUrl", "avatarSqUrl", "avatarRtUrl", "icon"),
			Level:          pexField(data, "level"),
			Rarity:         pexField(pexMap(data, "rarity"), "value"),
			Property:       pexString(pexMap(data, "property"), "value"),
			PotentialLevel: pexField(data, "potentialLevel", "potential_level"),
		})
	}
	passWave := 0
	if value, ok := pexNumber(raw, "passWave", "pass_wave", "wave"); ok {
		passWave = int(value)
	}
	isPass := pexBool(raw, "isPass", "is_pass")
	tsValue, _ := pexFloatValue(ts)
	record := &crisisRecord{
		Score:          score,
		IndicatorCount: indicatorCount,
		IsPass:         isPass,
		IsBest:         pexBool(raw, "isBest"),
		Status:         "行动中断",
		PassWave:       passWave,
		Chars:          chars,
	}
	if isPass {
		record.Status = "挑战成功"
	}
	if tsValue > 0 {
		record.Time = crisisFormatDateTime(tsValue)
		record.Date = crisisFormatDate(tsValue)
		if len(record.Date) > 5 {
			record.Date = record.Date[5:]
		}
	}
	if duration != nil {
		record.Duration = crisisFormatDuration(duration)
	}
	if passWave > 0 {
		record.WaveText = fmt.Sprintf("第 %d 波", passWave)
	}
	return record
}

func crisisBuildHistory(raw map[string]any, charNameMap map[string]string) crisisHistoryInfo {
	var info crisisHistoryInfo
	recordsRaw := crisisToArray(pexField(raw, "records", "challengeRecords", "histories", "list"))
	bestRaw := pexMap(raw, "bestRecord")
	if bestRaw == nil {
		bestRaw = pexAsMap(pexField(raw, "best"))
	}
	if bestRaw == nil {
		bestList := crisisToArray(raw["bestRecords"])
		if len(bestList) > 0 {
			bestRaw = pexAsMap(bestList[0])
		}
	}
	if bestRaw == nil && len(recordsRaw) > 0 {
		bestRaw = pexAsMap(recordsRaw[0])
	}
	info.Best = crisisBuildRecord(bestRaw, charNameMap)
	for _, item := range recordsRaw {
		if len(info.Records) >= 12 {
			break
		}
		if record := crisisBuildRecord(pexAsMap(item), charNameMap); record != nil {
			info.Records = append(info.Records, *record)
		}
	}
	return info
}

// ---------- 指标 ----------

func crisisGetIndicatorID(item map[string]any) string {
	return pexString(item, "id", "indicatorId", "indicator_id", "riskId", "risk_id")
}

func crisisReadParams(item map[string]any) map[string]float64 {
	params := map[string]float64{}
	keys := []string{
		"descParams", "descParam", "desc_params", "descParamList", "desc_param_list",
		"descParamMap", "desc_param_map", "descriptionParamMap", "description_param_map",
		"descriptionParams", "descriptionParam", "description_params", "templateParams",
		"template_params", "paramList", "param_list", "paramMap", "param_map", "params",
		"param", "parameters", "blackboard", "attrs", "attr", "attrMap", "attributeMap",
		"values", "valueMap",
	}
	for _, key := range keys {
		crisisMergeParamSource(params, item[key])
	}
	return params
}

func crisisMergeParamSource(params map[string]float64, source any) {
	if source == nil {
		return
	}
	if m := pexAsMap(source); m != nil {
		for key, value := range m {
			crisisPutParam(params, key, value)
		}
		return
	}
	for _, item := range pexSlice(source) {
		entry := pexAsMap(item)
		if entry == nil {
			continue
		}
		key := pexString(entry, "key", "name", "id", "param", "paramKey", "param_key", "attr", "attribute")
		crisisPutParam(params, key, entry)
	}
}

func crisisPutParam(params map[string]float64, key string, value any) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if m := pexAsMap(value); m != nil {
		value = pexField(m, "value", "val", "v", "num", "count", "raw")
	}
	if value == nil {
		return
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if strings.HasSuffix(text, "%") {
			if number, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64); err == nil {
				params[key] = number / 100
				return
			}
		}
	}
	if number, ok := pexFloatValue(value); ok {
		params[key] = number
	}
}

func crisisEvalExpr(expr string, params map[string]float64) (float64, bool) {
	expr = strings.TrimSpace(expr)
	if value, ok := params[expr]; ok {
		return value, true
	}
	if match := crisisReductionRe.FindStringSubmatch(expr); len(match) > 1 {
		if base, ok := params[match[1]]; ok {
			if base < 0 {
				return -base, true
			}
			if base >= 0 && base <= 1 {
				if base > 0.5 {
					return 1 - base, true
				}
				return base, true
			}
		}
	}
	missed := false
	replaced := crisisIdentRe.ReplaceAllStringFunc(expr, func(name string) string {
		value, ok := params[name]
		if !ok {
			missed = true
			return "0"
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	})
	if missed || !crisisSafeExprRe.MatchString(replaced) {
		return 0, false
	}
	node, err := parser.ParseExpr(replaced)
	if err != nil {
		return 0, false
	}
	value, ok := crisisEvalAST(node)
	return value, ok
}

func crisisEvalAST(node ast.Expr) (float64, bool) {
	switch expr := node.(type) {
	case *ast.BasicLit:
		value, err := strconv.ParseFloat(expr.Value, 64)
		return value, err == nil
	case *ast.ParenExpr:
		return crisisEvalAST(expr.X)
	case *ast.UnaryExpr:
		value, ok := crisisEvalAST(expr.X)
		if !ok {
			return 0, false
		}
		switch expr.Op {
		case token.SUB:
			return -value, true
		case token.ADD:
			return value, true
		}
	case *ast.BinaryExpr:
		left, ok := crisisEvalAST(expr.X)
		if !ok {
			return 0, false
		}
		right, ok := crisisEvalAST(expr.Y)
		if !ok {
			return 0, false
		}
		switch expr.Op {
		case token.ADD:
			return left + right, true
		case token.SUB:
			return left - right, true
		case token.MUL:
			return left * right, true
		case token.QUO:
			if right == 0 {
				return 0, false
			}
			return left / right, true
		case token.REM:
			if right == 0 {
				return 0, false
			}
			return math.Mod(left, right), true
		}
	}
	return 0, false
}

func crisisFormatValue(value float64, format string) string {
	percent := strings.Contains(format, "%")
	digits := 0
	if match := regexp.MustCompile(`0\.(0+)`).FindStringSubmatch(format); len(match) > 1 {
		digits = len(match[1])
	}
	scaled := value
	if percent && math.Abs(value) <= 1 {
		scaled = value * 100
	}
	text := crisisFormatNumber(scaled, digits)
	if percent {
		text += "%"
	}
	return text
}

func crisisFormatNumber(value float64, digits int) string {
	if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value-math.Round(value)) < 1e-9 {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	if digits <= 0 {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	text := strconv.FormatFloat(value, 'f', digits, 64)
	text = strings.TrimRight(text, "0")
	return strings.TrimRight(text, ".")
}

func crisisRenderIndicatorDescription(item map[string]any, itemMap map[string]map[string]any) string {
	rawText := pexString(item, "desc", "description", "content", "effect")
	if rawText == "" {
		return ""
	}
	rendered := crisisDescRe.ReplaceAllStringFunc(rawText, func(match string) string {
		sub := crisisDescRe.FindStringSubmatch(match)
		if len(sub) < 4 {
			return ""
		}
		source := item
		if sub[1] != "" {
			source = itemMap[sub[1]]
			if source == nil {
				source = map[string]any{}
			}
		}
		value, ok := crisisEvalExpr(sub[2], crisisReadParams(source))
		if !ok {
			return ""
		}
		return crisisFormatValue(value, sub[3])
	})
	rendered = crisisCleanText(rendered)
	rendered = crisisPlusMinusRe.ReplaceAllString(rendered, "-")
	rendered = strings.ReplaceAll(rendered, "--", "-")
	rendered = crisisTrailingOpRe.ReplaceAllString(rendered, "")
	return strings.TrimSpace(rendered)
}

func crisisFlattenIndicators(raw any) []map[string]any {
	out := make([]map[string]any, 0)
	for _, item := range crisisToArray(raw) {
		group := pexAsMap(item)
		if group == nil {
			continue
		}
		children := crisisToArray(pexField(group, "indicators", "indicatorList", "risks"))
		if len(children) > 0 {
			groupName := pexString(group, "name", "title")
			for _, child := range children {
				entry := pexAsMap(child)
				if entry == nil {
					continue
				}
				entry["groupName"] = groupName
				out = append(out, entry)
			}
			continue
		}
		out = append(out, group)
	}
	return out
}

func crisisBuildIndicators(raw any) []crisisIndicator {
	source := crisisFlattenIndicators(raw)
	itemMap := map[string]map[string]any{}
	for _, item := range source {
		if id := crisisGetIndicatorID(item); id != "" {
			itemMap[id] = item
		}
	}
	out := make([]crisisIndicator, 0, len(source))
	for _, item := range source {
		selected := pexBool(item, "isSelected", "selected", "checked", "isChosen")
		unlocked := true
		if pexField(item, "isUnlock", "unlock", "locked") != nil {
			if pexField(item, "isUnlock") != nil {
				unlocked = pexBool(item, "isUnlock")
			} else if pexField(item, "unlock") != nil {
				unlocked = pexBool(item, "unlock")
			} else {
				unlocked = !pexBool(item, "locked")
			}
		}
		depends := make([]string, 0)
		for _, dep := range crisisToArray(pexField(item, "depends", "dependencies", "dependIds", "depend_ids")) {
			if depMap := pexAsMap(dep); depMap != nil {
				if text := crisisCleanText(pexString(depMap, "name", "id")); text != "" {
					depends = append(depends, text)
				}
				continue
			}
			if text := crisisCleanText(pexStringValue(dep)); text != "" {
				depends = append(depends, text)
			}
		}
		score, _ := pexNumber(item, "score", "point", "riskScore", "value")
		itemType := 1
		if value, ok := pexNumber(item, "type"); ok {
			itemType = int(value)
		}
		name := crisisCleanText(pexString(item, "name", "title"))
		if name == "" {
			name = "未知"
		}
		out = append(out, crisisIndicator{
			ID:          crisisGetIndicatorID(item),
			Name:        name,
			GroupName:   pexString(item, "groupName"),
			Desc:        crisisRenderIndicatorDescription(item, itemMap),
			Score:       score,
			Type:        itemType,
			Icon:        pexString(item, "icon", "iconUrl", "imageUrl"),
			HasAward:    pexBool(item, "hasAward"),
			Selected:    selected,
			Unlocked:    unlocked,
			Depends:     depends,
			DependsText: strings.Join(depends, " / "),
		})
	}
	return out
}

func crisisUniqueSortedScores(values []float64) []float64 {
	seen := map[float64]bool{}
	out := make([]float64, 0)
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Float64s(out)
	return out
}

func crisisDescSkeleton(text string) string {
	return crisisNumberRe.ReplaceAllString(crisisCleanText(text), "{}")
}

func crisisDescTokens(text string) []string {
	return crisisNumberRe.FindAllString(crisisCleanText(text), -1)
}

func crisisCompactSameSkeleton(descs []string) string {
	if len(descs) == 0 {
		return ""
	}
	skeleton := crisisDescSkeleton(descs[0])
	if skeleton == "" {
		return ""
	}
	for _, desc := range descs {
		if crisisDescSkeleton(desc) != skeleton {
			return ""
		}
	}
	tokenRows := make([][]string, 0, len(descs))
	tokenCount := 0
	for index, desc := range descs {
		tokens := crisisDescTokens(desc)
		if index == 0 {
			tokenCount = len(tokens)
		}
		if len(tokens) != tokenCount || tokenCount == 0 {
			return ""
		}
		tokenRows = append(tokenRows, tokens)
	}
	merged := make([]string, tokenCount)
	for index := 0; index < tokenCount; index++ {
		seen := map[string]bool{}
		values := make([]string, 0)
		for _, row := range tokenRows {
			if row[index] == "" || seen[row[index]] {
				continue
			}
			seen[row[index]] = true
			values = append(values, row[index])
		}
		merged[index] = strings.Join(values, "/")
	}
	offset := 0
	return regexp.MustCompile(`\{\}`).ReplaceAllStringFunc(skeleton, func(string) string {
		if offset >= len(merged) {
			return ""
		}
		value := merged[offset]
		offset++
		return value
	})
}

func crisisCompactDescriptions(items []crisisIndicator) string {
	descs := make([]string, 0)
	seen := map[string]bool{}
	for _, item := range items {
		text := crisisCleanText(item.Desc)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		descs = append(descs, text)
	}
	if len(descs) == 0 {
		return ""
	}
	if len(descs) == 1 {
		return descs[0]
	}
	if compact := crisisCompactSameSkeleton(descs); compact != "" {
		return compact
	}
	return fmt.Sprintf("%s 等 %d 条效果", descs[0], len(descs))
}

func crisisBuildDependsText(items []crisisIndicator) string {
	depends := make([]string, 0)
	seen := map[string]bool{}
	for _, item := range items {
		for _, value := range item.Depends {
			text := crisisCleanText(value)
			if text == "" || seen[text] {
				continue
			}
			seen[text] = true
			depends = append(depends, text)
			if len(depends) >= 3 {
				return strings.Join(depends, " / ")
			}
		}
	}
	return strings.Join(depends, " / ")
}

func crisisGroupIndicators(items []crisisIndicator) []crisisIndicatorGroup {
	order := make([]string, 0)
	byKey := map[string][]crisisIndicator{}
	groupMeta := map[string]crisisIndicatorGroup{}
	for _, item := range items {
		key := fmt.Sprintf("%d:%s", item.Type, crisisNormalizeText(item.Name))
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
			groupMeta[key] = crisisIndicatorGroup{
				Name: item.Name,
				Type: item.Type,
				Icon: item.Icon,
			}
		}
		if groupMeta[key].Icon == "" && item.Icon != "" {
			meta := groupMeta[key]
			meta.Icon = item.Icon
			groupMeta[key] = meta
		}
		byKey[key] = append(byKey[key], item)
	}
	out := make([]crisisIndicatorGroup, 0, len(order))
	for _, key := range order {
		meta := groupMeta[key]
		group := byKey[key]
		meta.Count = len(group)
		scoreValues := make([]float64, 0, len(group))
		for _, item := range group {
			scoreValues = append(scoreValues, item.Score)
		}
		meta.Scores = crisisUniqueSortedScores(scoreValues)
		tags := make([]string, 0, len(meta.Scores))
		for _, score := range meta.Scores {
			tags = append(tags, fmt.Sprintf("+%s", crisisFormatNumber(score, 0)))
		}
		meta.ScoreTags = tags
		meta.ScoreText = strings.Join(tags, "/")
		meta.DescPreview = crisisCompactDescriptions(group)
		for _, item := range group {
			if item.Unlocked {
				meta.UnlockedCount++
			}
			if item.HasAward {
				meta.AwardCount++
			}
			if item.Selected {
				meta.SelectedCount++
			}
		}
		meta.UnlockedText = fmt.Sprintf("%d/%d", meta.UnlockedCount, meta.Count)
		meta.HasAward = meta.AwardCount > 0
		meta.Selected = meta.SelectedCount > 0
		meta.DependsText = crisisBuildDependsText(group)
		out = append(out, meta)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		left, right := 0.0, 0.0
		if len(out[i].Scores) > 0 {
			left = out[i].Scores[0]
		}
		if len(out[j].Scores) > 0 {
			right = out[j].Scores[0]
		}
		if left != right {
			return left < right
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func crisisBuildIndicatorDisplay(indicators []crisisIndicator, full bool) crisisIndicatorDisplay {
	basicLimit, advancedLimit := 8, 6
	if full {
		basicLimit, advancedLimit = 0, 0
	}
	groups := crisisGroupIndicators(indicators)
	basicGroups := make([]crisisIndicatorGroup, 0)
	advancedGroups := make([]crisisIndicatorGroup, 0)
	for _, group := range groups {
		if group.Type == 1 {
			basicGroups = append(basicGroups, group)
		} else if group.Type == 2 {
			advancedGroups = append(advancedGroups, group)
		}
	}
	stats := map[string]int{
		"total":          len(indicators),
		"unlocked":       0,
		"award":          0,
		"selected":       0,
		"basic":          0,
		"advanced":       0,
		"basicGroups":    len(basicGroups),
		"advancedGroups": len(advancedGroups),
	}
	for _, item := range indicators {
		if item.Unlocked {
			stats["unlocked"]++
		}
		if item.HasAward {
			stats["award"]++
		}
		if item.Selected {
			stats["selected"]++
		}
		if item.Type == 1 {
			stats["basic"]++
		} else if item.Type == 2 {
			stats["advanced"]++
		}
	}
	basic := basicGroups
	if basicLimit > 0 && len(basic) > basicLimit {
		basic = basic[:basicLimit]
	}
	advanced := advancedGroups
	if advancedLimit > 0 && len(advanced) > advancedLimit {
		advanced = advanced[:advancedLimit]
	}
	hint := ""
	if !full {
		hint = "发送「:危机 指标」查看完整指标列表"
	}
	return crisisIndicatorDisplay{
		Stats: stats,
		StatCards: []crisisCard{
			{Label: "指标总数", Value: strconv.Itoa(stats["total"])},
			{Label: "已解锁", Value: fmt.Sprintf("%d/%d", stats["unlocked"], stats["total"])},
			{Label: "基础/综合", Value: fmt.Sprintf("%d/%d", stats["basic"], stats["advanced"])},
			{Label: "带奖励", Value: strconv.Itoa(stats["award"])},
		},
		Basic:         basic,
		Advanced:      advanced,
		BasicTotal:    len(basicGroups),
		AdvancedTotal: len(advancedGroups),
		Hint:          hint,
	}
}

func crisisCardHTML(data crisisData) string {
	var body strings.Builder
	body.WriteString(`<div class="section"><h2>` + htmlEscape(data.Contract.Name) + `</h2>`)
	body.WriteString(`<div class="grid c2">`)
	body.WriteString(playerStatCard("账号", data.Role.Name, "UID "+data.Role.RoleID+" · Lv."+playerFormatValue(data.Role.Level)))
	body.WriteString(playerStatCard("时间", data.Contract.TimeRange, data.Contract.GameplayTimeRange))
	body.WriteString(`</div></div>`)

	body.WriteString(`<div class="section"><h2>合约进度</h2><div class="grid c3">`)
	for _, card := range data.SummaryCards {
		body.WriteString(playerStatCard(card.Label, card.Value, ""))
	}
	body.WriteString(playerStatCard("最高分", crisisValueText(data.Highest), ""))
	body.WriteString(`</div>`)
	if data.Medal.Name != "" {
		medal := data.Medal.Name
		if data.Medal.Status != "" {
			medal += "｜" + data.Medal.Status
		}
		body.WriteString(`<div class="tags" style="margin-top:12px"><span class="chip gold">` + htmlEscape(medal) + `</span></div>`)
	}
	body.WriteString(`</div>`)

	if data.IsIndicatorView {
		display := data.IndicatorDisplay
		body.WriteString(`<div class="section"><h2>指标信息</h2><div class="grid c4">`)
		for _, card := range display.StatCards {
			body.WriteString(playerStatCard(card.Label, card.Value, ""))
		}
		body.WriteString(`</div>`)
		body.WriteString(crisisIndicatorGroupsHTML(display.Basic, "基础指标", display.BasicTotal))
		body.WriteString(crisisIndicatorGroupsHTML(display.Advanced, "综合指标", display.AdvancedTotal))
		if display.Hint != "" {
			body.WriteString(`<p class="muted">` + htmlEscape(display.Hint) + `</p>`)
		}
		body.WriteString(`</div>`)
		return buildPage(pageOptions{
			title:    "危机合约 · 指标",
			eyebrow:  "终末地 · 危机合约",
			subtitle: htmlEscape(data.Role.Name + " · " + data.Contract.Name),
			seal:     "指标",
			body:     body.String(),
			width:    760,
		})
	}

	if data.History.Best != nil {
		best := data.History.Best
		value := crisisValueText(best.Score)
		if best.Duration != "" {
			value += "｜" + best.Duration
		}
		if best.Time != "" {
			value += "｜" + best.Time
		}
		body.WriteString(`<div class="section"><h2>最佳记录</h2>` + playerStatCard("评分", value, best.Status) + `</div>`)
	}
	if len(data.SelectedIndicators) > 0 {
		body.WriteString(`<div class="section"><h2>已选词条</h2><ul class="list">`)
		for index, item := range data.SelectedIndicators {
			if index >= 10 {
				break
			}
			value := fmt.Sprintf("+%s", crisisFormatNumber(item.Score, 0))
			body.WriteString(`<li><span>` + htmlEscape(item.Name) + `</span><span>` + htmlEscape(value) + `</span></li>`)
		}
		body.WriteString(`</ul></div>`)
	}
	if len(data.History.Records) > 0 {
		body.WriteString(`<div class="section"><h2>挑战记录</h2><table><thead><tr><th>时间</th><th>评分</th><th>耗时</th><th>状态</th><th>队伍</th></tr></thead><tbody>`)
		for _, record := range data.History.Records {
			team := make([]string, 0, len(record.Chars))
			for _, char := range record.Chars {
				team = append(team, char.Name)
			}
			body.WriteString(`<tr><td>` + htmlEscape(record.Time) + `</td><td>` + htmlEscape(crisisValueText(record.Score)) + `</td><td>` + htmlEscape(record.Duration) + `</td><td>` + htmlEscape(record.Status) + `</td><td>` + htmlEscape(strings.Join(team, "、")) + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div>`)
	}
	return buildPage(pageOptions{
		title:    "危机合约",
		eyebrow:  "终末地 · 危机合约",
		subtitle: htmlEscape(data.Role.Name + " · " + data.Contract.Name),
		seal:     "合约",
		body:     body.String(),
		width:    760,
	})
}

func crisisIndicatorGroupsHTML(groups []crisisIndicatorGroup, title string, total int) string {
	if len(groups) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="section"><h2>` + htmlEscape(fmt.Sprintf("%s（%d 组）", title, total)) + `</h2><ul class="list">`)
	for index, group := range groups {
		value := group.ScoreText
		if group.UnlockedText != "" {
			value += "｜" + group.UnlockedText
		}
		label := group.Name
		if group.DescPreview != "" {
			label += "｜" + group.DescPreview
		}
		b.WriteString(`<li><span>` + htmlEscape(fmt.Sprintf("%d. %s", index+1, label)) + `</span><span>` + htmlEscape(value) + `</span></li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

func crisisValueText(value any) string {
	if value == nil {
		return "—"
	}
	if number, ok := pexFloatValue(value); ok {
		return crisisFormatNumber(number, 0)
	}
	text := strings.TrimSpace(pexStringValue(value))
	if text == "" {
		return "—"
	}
	return text
}

func crisisFormatText(data crisisData) string {
	lines := []string{
		fmt.Sprintf("【%s】%s", data.Title, data.Contract.Name),
		fmt.Sprintf("账号：%s Lv.%s UID %s", data.Role.Name, playerFormatValue(data.Role.Level), data.Role.RoleID),
		fmt.Sprintf("时间：%s", data.Contract.TimeRange),
	}
	for _, card := range data.SummaryCards {
		lines = append(lines, fmt.Sprintf("%s：%s", card.Label, card.Value))
	}
	if data.Medal.Name != "" {
		medal := data.Medal.Name
		if data.Medal.Status != "" {
			medal += "｜" + data.Medal.Status
		}
		lines = append(lines, "奖章："+medal)
	}
	if data.IsIndicatorView {
		display := data.IndicatorDisplay
		lines = append(lines, "", fmt.Sprintf("【指标信息】总数 %d｜解锁 %d/%d", display.Stats["total"], display.Stats["unlocked"], display.Stats["total"]))
		if len(display.Basic) > 0 {
			lines = append(lines, fmt.Sprintf("基础指标（%d 组）", display.BasicTotal))
			for index, item := range display.Basic {
				line := fmt.Sprintf("%d. %s %s", index+1, item.Name, item.ScoreText)
				if item.DescPreview != "" {
					line += "｜" + item.DescPreview
				}
				lines = append(lines, line)
			}
		}
		if len(display.Advanced) > 0 {
			lines = append(lines, fmt.Sprintf("综合指标（%d 组）", display.AdvancedTotal))
			for index, item := range display.Advanced {
				line := fmt.Sprintf("%d. %s %s", index+1, item.Name, item.ScoreText)
				if item.DescPreview != "" {
					line += "｜" + item.DescPreview
				}
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	if data.History.Best != nil {
		best := data.History.Best
		line := "最佳记录：评分 " + crisisValueText(best.Score)
		if best.Duration != "" {
			line += "｜" + best.Duration
		}
		if best.Time != "" {
			line += "｜" + best.Time
		}
		lines = append(lines, line)
	}
	if len(data.SelectedIndicators) > 0 {
		lines = append(lines, "", "【已选词条】")
		for index, item := range data.SelectedIndicators {
			if index >= 10 {
				break
			}
			line := fmt.Sprintf("%d. %s +%s", index+1, item.Name, crisisFormatNumber(item.Score, 0))
			if item.Desc != "" {
				line += "｜" + item.Desc
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

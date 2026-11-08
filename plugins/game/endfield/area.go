package endfield

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

func init() {
	registerRoute(80, exactMatch("地区建设", "地区收集", "地区探索"), handleArea)
	registerRoute(80, exactMatch("帝江号建设"), handleSpaceship)
}

type areaCharInfo struct {
	Name   string
	Avatar string
}

type areaSettlement struct {
	Name               string
	Level              any
	OfficerName        string
	OfficerAvatar      string
	RemainMoneyText    string
	RemainMoneyPercent float64
}

type areaLevelStat struct {
	Name         string
	ChestText    string
	PuzzleText   string
	BlackboxText string
	EquipText    string
	PieceText    string
}

type areaZoneView struct {
	ZoneName          string
	Level             any
	MoneyText         string
	Settlements       []areaSettlement
	LevelStats        []areaLevelStat
	TotalChest        int
	TotalPuzzle       int
	TotalBlackbox     int
	TotalEquip        int
	TotalPiece        int
	TotalChestText    string
	TotalPuzzleText   string
	TotalBlackboxText string
	TotalEquipText    string
	TotalPieceText    string
}

type spaceshipChar struct {
	Name             string
	Avatar           string
	PhysicalStrength int
	Favorability     int
	MoodPercent      int
	TrustPercent     int
	TrustLevelName   string
}

type spaceshipRoom struct {
	Name           string
	Level          any
	Type           any
	LastReportTime string
	Chars          []spaceshipChar
}

// handleArea 对齐上游 apps/area.js 的地区建设/收集/探索。
func handleArea(ctx context.Context, c sdk.Call, cfg config, _ string, text string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	zoneRes, err := session.Client.getData(ctx, "cultivate_zone", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !zoneRes.ok() {
		return replyText(ctx, c, getMessage("area.get_zone_failed"))
	}
	var data map[string]any
	if err := zoneRes.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("area.get_zone_failed"))
	}

	noteBase := map[string]any{}
	if noteRes, err := session.Client.getData(ctx, "note", nil); err == nil && noteRes.ok() {
		var noteData map[string]any
		if noteRes.decode(&noteData) == nil {
			noteBase = pexMap(noteData, "base")
		}
	}
	cardBase := map[string]any{}
	charInfoMap := map[string]areaCharInfo{}
	if cardRes, err := session.Client.getData(ctx, "endfield_card_detail", nil); err == nil && cardRes.ok() {
		var cardData map[string]any
		if cardRes.decode(&cardData) == nil {
			detail := pexMap(cardData, "detail")
			if detail == nil {
				detail = cardData
			}
			cardBase = pexMap(detail, "base")
			charInfoMap = areaCharMap(detail)
		}
	}

	rawZones := pexSlice(data["domain"])
	if len(rawZones) == 0 {
		rawZones = pexSlice(data["zones"])
	}
	if len(rawZones) == 0 {
		return replyText(ctx, c, getMessage("area.not_found_info"))
	}
	charNameMap := pexStringMap(data["charNameMap"])
	officerAvatarMap := pexStringMap(data["officerAvatarMap"])

	zones := make([]areaZoneView, 0, len(rawZones))
	for _, raw := range rawZones {
		zone := pexAsMap(raw)
		if zone == nil {
			continue
		}
		zones = append(zones, buildAreaZone(zone, charNameMap, officerAvatarMap, charInfoMap))
	}

	isExplore := strings.Contains(text, "地区探索")
	title := "地区建设"
	if isExplore {
		title = "地区探索"
	}
	userName := pexString(noteBase, "name")
	if userName == "" {
		userName = pexString(cardBase, "name")
	}
	if userName == "" {
		userName = getMessage("common.unknown")
	}
	userLevel := pexField(noteBase, "level")
	if userLevel == nil {
		userLevel = pexField(cardBase, "level")
	}
	userUID := pexString(noteBase, "roleId")
	if userUID == "" {
		userUID = pexString(cardBase, "roleId")
	}
	if userUID == "" {
		userUID = session.Binding.roleID()
	}
	userAvatar := pexString(noteBase, "avatarUrl")
	if userAvatar == "" {
		userAvatar = pexString(cardBase, "avatarUrl")
	}

	var body strings.Builder
	body.WriteString(`<div class="section"><h2>账号</h2><div class="card"><div style="display:flex;gap:12px;align-items:center">`)
	if userAvatar != "" {
		body.WriteString(`<img src="` + htmlEscape(userAvatar) + `" alt="" style="width:52px;height:52px;border-radius:50%;object-fit:cover">`)
	}
	body.WriteString(`<div><div class="v" style="font-size:20px">` + htmlEscape(userName) + `</div><div class="note">UID ` + htmlEscape(userUID) + ` · Lv.` + htmlEscape(playerFormatValue(userLevel)) + `</div></div></div></div></div>`)

	for _, zone := range zones {
		body.WriteString(`<div class="section"><h2>` + htmlEscape(zone.ZoneName) + `</h2>`)
		body.WriteString(`<div class="grid c3">`)
		body.WriteString(playerStatCard("地区等级", playerFormatValue(zone.Level), ""))
		if zone.MoneyText != "" {
			body.WriteString(playerStatCard("总调度券", zone.MoneyText, ""))
		}
		body.WriteString(playerStatCard("聚落", strconv.Itoa(len(zone.Settlements)), ""))
		body.WriteString(`</div>`)
		if len(zone.Settlements) > 0 {
			body.WriteString(`<ul class="list" style="margin-top:12px">`)
			for _, settlement := range zone.Settlements {
				label := settlement.Name + " Lv." + playerFormatValue(settlement.Level)
				if settlement.OfficerName != "" {
					label += "（派驻：" + settlement.OfficerName + "）"
				}
				value := settlement.RemainMoneyText
				if value == "" {
					value = "—"
				}
				body.WriteString(`<li><span>` + htmlEscape(label) + `</span><span>` + htmlEscape(value) + `</span></li>`)
			}
			body.WriteString(`</ul>`)
		}
		body.WriteString(`<div class="grid c3" style="margin-top:12px">`)
		body.WriteString(playerStatCard("宝箱", zone.TotalChestText, ""))
		body.WriteString(playerStatCard("醚质", zone.TotalPuzzleText, ""))
		body.WriteString(playerStatCard("协议采录桩", zone.TotalBlackboxText, ""))
		body.WriteString(playerStatCard("装备制造模板", zone.TotalEquipText, ""))
		body.WriteString(playerStatCard("维修灵感点", zone.TotalPieceText, ""))
		body.WriteString(`</div>`)
		if len(zone.LevelStats) > 0 {
			body.WriteString(`<table style="margin-top:14px"><thead><tr><th>关卡</th><th>宝箱</th><th>醚质</th><th>采录桩</th><th>制造模板</th><th>灵感点</th></tr></thead><tbody>`)
			for _, level := range zone.LevelStats {
				body.WriteString(`<tr><td>` + htmlEscape(level.Name) + `</td><td>` + htmlEscape(level.ChestText) + `</td><td>` + htmlEscape(level.PuzzleText) + `</td><td>` + htmlEscape(level.BlackboxText) + `</td><td>` + htmlEscape(level.EquipText) + `</td><td>` + htmlEscape(level.PieceText) + `</td></tr>`)
			}
			body.WriteString(`</tbody></table>`)
		}
		body.WriteString(`</div>`)
	}

	html := buildPage(pageOptions{
		title:    title,
		eyebrow:  "终末地 · " + title,
		subtitle: htmlEscape(userName + " · " + userUID),
		seal:     title,
		body:     body.String(),
		width:    980,
	})

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("【%s】(%d个地区)\n", title, len(zones)))
	for _, zone := range zones {
		msg.WriteString(fmt.Sprintf("\n- 地区：%s\n", zone.ZoneName))
		msg.WriteString("  等级：" + playerFormatValue(zone.Level) + "\n")
		if zone.MoneyText != "" {
			msg.WriteString("  资金：" + zone.MoneyText + "\n")
		}
		if len(zone.Settlements) > 0 {
			msg.WriteString(fmt.Sprintf("  聚落：%d个\n", len(zone.Settlements)))
			for _, settlement := range zone.Settlements {
				text := fmt.Sprintf("  • %s Lv.%s", settlement.Name, playerFormatValue(settlement.Level))
				if settlement.OfficerName != "" {
					text += "（派驻：" + settlement.OfficerName + "）"
				}
				msg.WriteString(text + "\n")
			}
		}
		msg.WriteString(fmt.Sprintf("  收集：宝箱 %d、醚质 %d、协议采录桩 %d、装备制造模板 %d、维修灵感点 %d\n",
			zone.TotalChest, zone.TotalPuzzle, zone.TotalBlackbox, zone.TotalEquip, zone.TotalPiece))
	}
	return replyCard(ctx, c, html, 980, strings.TrimSpace(msg.String()))
}

func areaCharMap(detail map[string]any) map[string]areaCharInfo {
	out := map[string]areaCharInfo{}
	for _, raw := range pexSlice(detail["chars"]) {
		char := pexAsMap(raw)
		if char == nil {
			continue
		}
		data := pexMap(char, "charData")
		if data == nil {
			data = char
		}
		id := pexString(char, "id")
		if id == "" {
			id = pexString(char, "instId")
		}
		if id == "" {
			continue
		}
		out[id] = areaCharInfo{
			Name:   pexString(data, "name"),
			Avatar: pexString(data, "avatarRtUrl", "avatar_rt_url"),
		}
	}
	return out
}

func buildAreaZone(zone map[string]any, charNameMap, officerAvatarMap map[string]string, charInfoMap map[string]areaCharInfo) areaZoneView {
	zoneName := pexString(zone, "zoneName")
	if zoneName == "" {
		zoneName = pexString(zone, "zoneId")
	}
	if zoneName == "" {
		zoneName = "未知"
	}
	settlements := make([]areaSettlement, 0)
	for _, raw := range pexSlice(zone["settlements"]) {
		settlement := pexAsMap(raw)
		if settlement == nil {
			continue
		}
		officerIDs := areaOfficerIDs(pexField(settlement, "officerCharIds"))
		charID := ""
		if len(officerIDs) > 0 {
			charID = officerIDs[0]
		}
		officerName := charNameMap[charID]
		if officerName == "" {
			officerName = charInfoMap[charID].Name
		}
		officerAvatar := pexString(settlement, "officerCharAvatar")
		if officerAvatar == "" {
			officerAvatar = officerAvatarMap[charID]
		}
		if officerAvatar == "" {
			officerAvatar = charInfoMap[charID].Avatar
		}
		remainMoney := pexField(settlement, "remainMoney")
		moneyMax := pexField(settlement, "moneyMax")
		remainText := areaMoneyToWan(remainMoney)
		maxText := areaMoneyToWan(moneyMax)
		remainMoneyText := maxText
		if remainText != "" && maxText != "" {
			remainMoneyText = remainText + "/" + maxText
		} else if remainText != "" {
			remainMoneyText = remainText
		}
		remainPercent := 0.0
		if remain, ok := pexFloatValue(remainMoney); ok {
			if maxValue, ok := pexFloatValue(moneyMax); ok && maxValue > 0 {
				remainPercent = math.Max(0, math.Min(100, remain/maxValue*100))
			}
		}
		settlementName := pexString(settlement, "name", "id")
		if settlementName == "" {
			settlementName = "未知"
		}
		settlements = append(settlements, areaSettlement{
			Name:               settlementName,
			Level:              pexField(settlement, "level"),
			OfficerName:        officerName,
			OfficerAvatar:      officerAvatar,
			RemainMoneyText:    remainMoneyText,
			RemainMoneyPercent: remainPercent,
		})
	}

	collectionSource := pexSlice(zone["collections"])
	if len(collectionSource) == 0 {
		collectionSource = pexSlice(zone["levels"])
	}
	totalChest, totalChestAll := 0, 0
	totalPuzzle, totalPuzzleAll := 0, 0
	totalBlackbox, totalBlackboxAll := 0, 0
	totalEquip, totalEquipAll := 0, 0
	totalPiece, totalPieceAll := 0, 0
	for _, raw := range collectionSource {
		item := pexAsMap(raw)
		if item == nil {
			continue
		}
		chest, chestTotal := areaCountPair(item["trchestCount"])
		puzzle, puzzleTotal := areaCountPair(item["puzzleCount"])
		blackbox, blackboxTotal := areaCountPair(item["blackboxCount"])
		equip, equipTotal := areaCountPair(item["equipTrchestCount"])
		piece, pieceTotal := areaCountPair(item["pieceCount"])
		totalChest += chest
		totalChestAll += chestTotal
		totalPuzzle += puzzle
		totalPuzzleAll += puzzleTotal
		totalBlackbox += blackbox
		totalBlackboxAll += blackboxTotal
		totalEquip += equip
		totalEquipAll += equipTotal
		totalPiece += piece
		totalPieceAll += pieceTotal
	}

	levelsSource := pexSlice(zone["levels"])
	if len(levelsSource) == 0 {
		levelsSource = collectionSource
	}
	levelStats := make([]areaLevelStat, 0, len(levelsSource))
	for _, raw := range levelsSource {
		level := pexAsMap(raw)
		if level == nil {
			continue
		}
		name := pexString(level, "name")
		if name == "" {
			name = pexString(level, "levelId")
		}
		if name == "" {
			name = "未知"
		}
		chest, chestTotal := areaCountPair(level["trchestCount"])
		puzzle, puzzleTotal := areaCountPair(level["puzzleCount"])
		blackbox, blackboxTotal := areaCountPair(level["blackboxCount"])
		equip, equipTotal := areaCountPair(level["equipTrchestCount"])
		piece, pieceTotal := areaCountPair(level["pieceCount"])
		levelStats = append(levelStats, areaLevelStat{
			Name:         name,
			ChestText:    areaCountText(chest, chestTotal),
			PuzzleText:   areaCountText(puzzle, puzzleTotal),
			BlackboxText: areaCountText(blackbox, blackboxTotal),
			EquipText:    areaCountText(equip, equipTotal),
			PieceText:    areaCountText(piece, pieceTotal),
		})
	}

	return areaZoneView{
		ZoneName:          zoneName,
		Level:             pexField(zone, "level"),
		MoneyText:         areaFormatMoney(pexField(zone, "moneyMgr")),
		Settlements:       settlements,
		LevelStats:        levelStats,
		TotalChest:        totalChest,
		TotalPuzzle:       totalPuzzle,
		TotalBlackbox:     totalBlackbox,
		TotalEquip:        totalEquip,
		TotalPiece:        totalPiece,
		TotalChestText:    areaCountText(totalChest, totalChestAll),
		TotalPuzzleText:   areaCountText(totalPuzzle, totalPuzzleAll),
		TotalBlackboxText: areaCountText(totalBlackbox, totalBlackboxAll),
		TotalEquipText:    areaCountText(totalEquip, totalEquipAll),
		TotalPieceText:    areaCountText(totalPiece, totalPieceAll),
	}
}

func areaOfficerIDs(value any) []string {
	if list, ok := value.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text := strings.TrimSpace(pexStringValue(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	text := pexStringValue(value)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func areaCountPair(value any) (int, int) {
	if m := pexAsMap(value); m != nil {
		return pexInt(m, 0, "count"), pexInt(m, 0, "total")
	}
	if number, ok := pexFloatValue(value); ok {
		return int(number), 0
	}
	return 0, 0
}

func areaCountText(count, total int) string {
	if total > 0 {
		return fmt.Sprintf("%d/%d", count, total)
	}
	return strconv.Itoa(count)
}

func areaFormatMoney(value any) string {
	if m := pexAsMap(value); m != nil {
		countRaw := pexField(m, "count")
		totalRaw := pexField(m, "total")
		if countRaw == nil && totalRaw == nil {
			return ""
		}
		countText := areaMoneyToWan(countRaw)
		totalText := areaMoneyToWan(totalRaw)
		if countText != "" && totalText != "" {
			return countText + "/" + totalText
		}
		if countText != "" {
			return countText
		}
		return totalText
	}
	return areaMoneyToWan(value)
}

func areaMoneyToWan(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return ""
	}
	if number, ok := pexFloatValue(value); ok {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", number/10000), "0"), ".") + "万"
	}
	return pexStringValue(value)
}

// handleSpaceship 对齐上游 apps/area.js 的帝江号建设。
func handleSpaceship(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	shipRes, err := session.Client.getData(ctx, "spaceship", nil)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	if !shipRes.ok() {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	var data map[string]any
	if err := shipRes.decode(&data); err != nil {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}
	roomsRaw := pexSlice(data["rooms"])
	if len(roomsRaw) == 0 {
		roomsRaw = pexSlice(pexMap(data, "spaceShip")["rooms"])
	}
	if len(roomsRaw) == 0 {
		return replyText(ctx, c, getMessage("spaceship.not_found_info"))
	}

	noteBase := map[string]any{}
	if noteRes, err := session.Client.getData(ctx, "note", nil); err == nil && noteRes.ok() {
		var noteData map[string]any
		if noteRes.decode(&noteData) == nil {
			noteBase = pexMap(noteData, "base")
		}
	}
	charAvatarMap := map[string]string{}
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
				id := pexString(char, "id")
				if id == "" {
					id = pexString(char, "instId")
				}
				c := pexMap(char, "charData")
				if c == nil {
					c = char
				}
				if id != "" {
					charAvatarMap[id] = pexString(c, "avatarRtUrl", "avatar_rt_url")
				}
			}
		}
	}

	operatorMap := map[string]string{}
	_ = loadAssetJSON("data/operatorMap.json", &operatorMap)
	charNameMap := pexStringMap(data["charNameMap"])
	relationLevels := pexMapList(pexField(data, "relationLevels"))
	role := pexMap(data, "role")

	rooms := make([]spaceshipRoom, 0, len(roomsRaw))
	for _, raw := range roomsRaw {
		room := pexAsMap(raw)
		if room == nil {
			continue
		}
		roomName := pexString(room, "roomName")
		if roomName == "" {
			roomName = pexString(room, "id")
		}
		if roomName == "" {
			roomName = "未知"
		}
		lastReport := ""
		if ts, ok := pexNumber(room, "lastReportTs"); ok && ts > 0 {
			lastReport = time.Unix(int64(ts), 0).Format("2006-01-02 15:04:05")
		}
		chars := make([]spaceshipChar, 0)
		for _, rawChar := range pexSlice(room["chars"]) {
			item := pexAsMap(rawChar)
			if item == nil {
				continue
			}
			charID := pexString(item, "charId", "id")
			name := operatorMap[charID]
			if name == "" {
				name = pexString(item, "name")
			}
			if name == "" {
				name = charNameMap[charID]
			}
			if name == "" {
				name = charID
			}
			if name == "" {
				name = "未知"
			}
			avatar := pexString(item, "avatarUrl")
			if avatar == "" {
				avatar = charAvatarMap[charID]
			}
			physical := int(math.Round(pexFloatOrZero(pexField(item, "physicalStrength"))))
			favorability := int(math.Round(pexFloatOrZero(pexField(item, "favorability"))))
			chars = append(chars, spaceshipChar{
				Name:             name,
				Avatar:           avatar,
				PhysicalStrength: physical,
				Favorability:     favorability,
				MoodPercent:      areaCalcMoodPercent(pexField(item, "physicalStrength"), pexField(item, "moodPercent")),
				TrustPercent:     areaCalcTrustPercent(favorability, pexField(item, "trustPercent")),
				TrustLevelName:   areaResolveTrustLevelName(favorability, pexString(item, "trustLevelName"), relationLevels),
			})
		}
		rooms = append(rooms, spaceshipRoom{
			Name:           roomName,
			Level:          pexField(room, "level"),
			Type:           pexField(room, "type"),
			LastReportTime: lastReport,
			Chars:          chars,
		})
	}

	userName := pexString(noteBase, "name")
	if userName == "" {
		userName = pexString(role, "name")
	}
	if userName == "" {
		userName = getMessage("common.unknown")
	}
	userLevel := pexField(noteBase, "level")
	if userLevel == nil {
		userLevel = pexField(role, "level")
	}
	userUID := pexString(noteBase, "roleId")
	if userUID == "" {
		userUID = pexString(role, "roleId")
	}
	if userUID == "" {
		userUID = session.Binding.roleID()
	}
	userAvatar := pexString(noteBase, "avatarUrl")

	var body strings.Builder
	body.WriteString(`<div class="section"><h2>账号</h2><div class="card"><div style="display:flex;gap:12px;align-items:center">`)
	if userAvatar != "" {
		body.WriteString(`<img src="` + htmlEscape(userAvatar) + `" alt="" style="width:52px;height:52px;border-radius:50%;object-fit:cover">`)
	}
	body.WriteString(`<div><div class="v" style="font-size:20px">` + htmlEscape(userName) + `</div><div class="note">UID ` + htmlEscape(userUID) + ` · Lv.` + htmlEscape(playerFormatValue(userLevel)) + `</div></div></div></div></div>`)

	for _, room := range rooms {
		body.WriteString(`<div class="section"><h2>` + htmlEscape(room.Name) + `</h2><div class="grid c3">`)
		body.WriteString(playerStatCard("房间等级", playerFormatValue(room.Level), ""))
		body.WriteString(playerStatCard("干员", strconv.Itoa(len(room.Chars)), ""))
		if room.LastReportTime != "" {
			body.WriteString(playerStatCard("最近报告", room.LastReportTime, ""))
		}
		body.WriteString(`</div>`)
		if len(room.Chars) > 0 {
			body.WriteString(`<ul class="list" style="margin-top:12px">`)
			for _, char := range room.Chars {
				label := char.Name + "（" + char.TrustLevelName + "）"
				value := fmt.Sprintf("心情 %d%% / 体力 %d，信赖 %d%% / 好感 %d", char.MoodPercent, char.PhysicalStrength, char.TrustPercent, char.Favorability)
				body.WriteString(`<li><span>` + htmlEscape(label) + `</span><span>` + htmlEscape(value) + `</span></li>`)
			}
			body.WriteString(`</ul>`)
		}
		body.WriteString(`</div>`)
	}

	html := buildPage(pageOptions{
		title:    "帝江号建设",
		eyebrow:  "终末地 · 帝江号",
		subtitle: htmlEscape(userName + " · " + userUID),
		seal:     "帝江号",
		body:     body.String(),
		width:    980,
	})

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("【帝江号建设】(%d个房间)\n", len(rooms)))
	for _, room := range rooms {
		msg.WriteString(fmt.Sprintf("\n- 房间：%s\n", room.Name))
		msg.WriteString("  等级：" + playerFormatValue(room.Level) + "\n")
		if len(room.Chars) == 0 {
			msg.WriteString("  干员：无\n")
			continue
		}
		msg.WriteString(fmt.Sprintf("  干员：%d人\n", len(room.Chars)))
		for _, char := range room.Chars {
			msg.WriteString(fmt.Sprintf("  • %s（%s，心情%d%% / 体力%d，信赖%d%% / 好感%d）\n",
				char.Name, char.TrustLevelName, char.MoodPercent, char.PhysicalStrength, char.TrustPercent, char.Favorability))
		}
	}
	return replyCard(ctx, c, html, 980, strings.TrimSpace(msg.String()))
}

func pexFloatOrZero(value any) float64 {
	if number, ok := pexFloatValue(value); ok {
		return number
	}
	return 0
}

func areaCalcMoodPercent(physicalStrength, moodPercent any) int {
	if number, ok := pexFloatValue(moodPercent); ok {
		return clampInt(int(math.Round(number)), 0, 100)
	}
	raw := pexFloatOrZero(physicalStrength)
	return clampInt(int(math.Floor(raw/10000*100)), 0, 100)
}

func areaCalcTrustPercent(favorability int, trustPercent any) int {
	if number, ok := pexFloatValue(trustPercent); ok {
		return clampInt(int(math.Round(number)), 0, 200)
	}
	fav := float64(favorability)
	if fav >= 1500 {
		return 200
	}
	if fav >= 300 {
		return 100 + int(math.Floor((fav-300)/1200*100))
	}
	return int(math.Floor(fav / 300 * 100))
}

func areaResolveTrustLevelName(favorability int, trustLevelName string, relationLevels []map[string]any) string {
	if trustLevelName != "" {
		return trustLevelName
	}
	if len(relationLevels) > 0 {
		hit := relationLevels[0]
		for _, level := range relationLevels {
			if float64(favorability) >= pexFloatOrZero(pexField(level, "threshold")) {
				hit = level
			}
		}
		if name := pexString(hit, "name"); name != "" {
			return name
		}
	}
	if favorability >= 1500 {
		return "信任"
	}
	if favorability >= 300 {
		return "亲近"
	}
	return "友好"
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

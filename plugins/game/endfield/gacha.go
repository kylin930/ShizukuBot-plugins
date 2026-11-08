package endfield

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	gachaCachePrefix = "endfield:gacha:"

	gachaSyncPollInterval = 1500 * time.Millisecond
	gachaSyncTimeout      = 3 * time.Minute

	gachaPityCharSoft       = 80
	gachaPityCharProbBoost  = 65
	gachaPityCharHard       = 120
	gachaPityWeaponHard     = 4
	gachaPityWeaponUpHard   = 8
	gachaPitySpecial60      = 60
	gachaPitySpecial120     = 120
	gachaPitySpecial240     = 240
	gachaRecordPreviewLimit = 10
)

var gachaPoolKeys = []string{"limited", "joint_char", "standard", "beginner", "weapon"}

var (
	gachaTimeWithYear = regexp.MustCompile(`^(\d{4})[.\-/](\d{1,2})[.\-/](\d{1,2})(?:\s+(\d{1,2}):(\d{1,2}))?$`)
	gachaTimeNoYear   = regexp.MustCompile(`^(\d{1,2})[.\-/](\d{1,2})(?:\s+(\d{1,2}):(\d{1,2}))?$`)
)

func init() {
	registerRoute(120, prefixMatch("同步抽卡记录", "更新抽卡记录"), handleGachaSync)
	registerRoute(115, prefixMatch("抽卡分析"), handleGachaAnalysis)
	registerRoute(115, prefixMatch("抽卡记录"), handleGachaRecords)
	registerRoute(110, prefixMatch("全服抽卡统计"), handleGachaGlobal)
}

// ---------- 数据结构 ----------

type gachaRecord struct {
	CharID     any    `json:"char_id"`
	CharName   string `json:"char_name"`
	Rarity     int    `json:"rarity"`
	GachaTS    any    `json:"gacha_ts"`
	PoolID     string `json:"pool_id"`
	PoolName   string `json:"pool_name"`
	IsNew      bool   `json:"is_new"`
	IsFree     bool   `json:"is_free"`
	SeqID      any    `json:"seq_id"`
	WeaponType string `json:"weapon_type"`
}

type gachaUserInfo struct {
	Nickname    string `json:"nickname"`
	GameUID     string `json:"game_uid"`
	RoleID      string `json:"role_id"`
	ServerID    any    `json:"server_id"`
	ServerName  string `json:"server_name"`
	ChannelName string `json:"channel_name"`
	Level       int    `json:"level"`
}

type gachaStats struct {
	TotalCount        int `json:"total_count"`
	LimitedCharCount  int `json:"limited_char_count"`
	JointCharCount    int `json:"joint_char_count"`
	StandardCharCount int `json:"standard_char_count"`
	BeginnerCharCount int `json:"beginner_char_count"`
	WeaponCount       int `json:"weapon_count"`
	Star6Count        int `json:"star6_count"`
	Star5Count        int `json:"star5_count"`
	Star4Count        int `json:"star4_count"`
}

type gachaPoolStat struct {
	Total int `json:"total"`
	Star6 int `json:"star6"`
	Star5 int `json:"star5"`
}

type gachaStatsData struct {
	Stats      gachaStats               `json:"stats"`
	PoolStats  map[string]gachaPoolStat `json:"pool_stats"`
	LastFetch  string                   `json:"last_fetch"`
	HasRecords bool                     `json:"has_records"`
	UserInfo   gachaUserInfo            `json:"user_info"`
}

func (s *gachaStatsData) hasAnyRecord() bool {
	if s == nil {
		return false
	}
	return s.HasRecords || strings.TrimSpace(s.LastFetch) != "" || s.Stats.TotalCount > 0
}

type gachaCacheSummary struct {
	RoleID    string `json:"role_id"`
	ServerID  string `json:"server_id"`
	Total     int    `json:"total"`
	Star6     int    `json:"star6"`
	UpdatedAt int64  `json:"updated_at"`
}

type gachaSyncStatusData struct {
	Status         string  `json:"status"`
	Stage          string  `json:"stage"`
	Progress       int     `json:"progress"`
	Message        string  `json:"message"`
	CurrentPool    string  `json:"current_pool"`
	TotalPools     int     `json:"total_pools"`
	CompletedPools int     `json:"completed_pools"`
	RecordsFound   int     `json:"records_found"`
	NewRecords     int     `json:"new_records"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Error          string  `json:"error"`
}

type gachaGroupDef struct {
	Key      string
	Label    string
	PoolKeys []string
}

var gachaGroupDefs = []gachaGroupDef{
	{Key: "limited", Label: "特许寻访", PoolKeys: []string{"limited"}},
	{Key: "joint", Label: "辉光庆典", PoolKeys: []string{"joint_char"}},
	{Key: "weapon", Label: "武库交易所", PoolKeys: []string{"weapon"}},
	{Key: "base", Label: "基础寻访", PoolKeys: []string{"standard", "beginner"}},
}

// ---------- 缓存与账号 ----------

func gachaCacheKey(userID, roleID string) string {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		uid = "0"
	}
	rid := strings.TrimSpace(roleID)
	if rid == "" {
		rid = "0"
	}
	return gachaCachePrefix + uid + ":" + rid
}

func gachaSaveSummary(ctx context.Context, st sdk.State, userID, roleID string, serverID int, stats *gachaStatsData) {
	if stats == nil {
		return
	}
	summary := gachaCacheSummary{
		RoleID:    roleID,
		ServerID:  strconv.Itoa(serverID),
		Total:     stats.Stats.TotalCount,
		Star6:     stats.Stats.Star6Count,
		UpdatedAt: time.Now().Unix(),
	}
	_ = sdk.SaveJSON(ctx, st, gachaCacheKey(userID, roleID), summary)
}

func gachaLoadSummary(ctx context.Context, st sdk.State, userID, roleID string) gachaCacheSummary {
	var summary gachaCacheSummary
	_ = sdk.LoadJSON(ctx, st, gachaCacheKey(userID, roleID), &summary)
	return summary
}

func gachaSessionFor(ctx context.Context, c sdk.Call, cfg config, userID string) (*userSession, bool, error) {
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil {
		return nil, false, replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
	}
	account, ok := primaryBinding(accounts)
	if !ok || account.FrameworkToken == "" {
		return nil, false, replyText(ctx, c, getMessage("unbind_message"))
	}
	return &userSession{UserID: userID, Binding: account, Client: newAPIClient(cfg, account)}, true, nil
}

// gachaResolveTarget 对齐上游 resolveSyncTarget：@ 优先，其次平台 ID；查询他人需管理员。
func gachaResolveTarget(ctx context.Context, c sdk.Call, cfg config, args string) (string, bool, error) {
	self := c.Event.UserID
	target := targetUserID(c)
	arg := firstField(args)
	if target == self && arg != "" {
		target = arg
	}
	if target == "" {
		target = self
	}
	if target != self && !cfg.isAdmin(self) {
		return "", false, replyText(ctx, c, getMessage("gacha.sync_master_only"))
	}
	if target != self && arg != "" && target == arg {
		accounts, err := loadBindings(ctx, c.State, target)
		if err != nil {
			return "", false, replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
		}
		if len(accounts) == 0 {
			return "", false, replyText(ctx, c, getMessage("gacha.no_accounts"))
		}
	}
	return target, true, nil
}

func gachaQuery(roleID string, serverID int) url.Values {
	query := url.Values{}
	if roleID != "" {
		query.Set("role_id", roleID)
	}
	if serverID <= 0 {
		serverID = 1
	}
	query.Set("server_id", strconv.Itoa(serverID))
	return query
}

// ---------- 后端请求 ----------

func gachaFetchStats(ctx context.Context, client *apiClient, roleID string, serverID int) (*gachaStatsData, error) {
	res, err := client.request(ctx, http.MethodGet, "/api/endfield/gacha/stats", gachaQuery(roleID, serverID), nil)
	if err != nil {
		return nil, err
	}
	if !res.ok() {
		return nil, fmt.Errorf("gacha stats: code %d %s", res.Code, res.Message)
	}
	var data gachaStatsData
	if err := res.decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

func gachaFetchSyncStatus(ctx context.Context, client *apiClient, query url.Values) (*gachaSyncStatusData, error) {
	res, err := client.request(ctx, http.MethodGet, "/api/endfield/gacha/sync/status", query, nil)
	if err != nil {
		return nil, err
	}
	if !res.ok() {
		return nil, fmt.Errorf("gacha status: code %d %s", res.Code, res.Message)
	}
	var data gachaSyncStatusData
	if err := res.decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

func gachaFetchPoolRecords(ctx context.Context, client *apiClient, pool, roleID string, serverID int) []gachaRecord {
	var out []gachaRecord
	page := 1
	for page <= 100 {
		query := gachaQuery(roleID, serverID)
		query.Set("pools", pool)
		query.Set("page", strconv.Itoa(page))
		query.Set("limit", "500")
		res, err := client.request(ctx, http.MethodGet, "/api/endfield/gacha/records", query, nil)
		if err != nil || !res.ok() {
			break
		}
		var data struct {
			Records []gachaRecord `json:"records"`
			Pages   int           `json:"pages"`
			Page    int           `json:"page"`
		}
		if err := res.decode(&data); err != nil {
			break
		}
		out = append(out, data.Records...)
		if data.Pages <= 0 || page >= data.Pages || len(data.Records) == 0 {
			break
		}
		page++
	}
	return gachaDedupRecords(out)
}

func gachaFetchRecords(ctx context.Context, client *apiClient, roleID string, serverID int) map[string][]gachaRecord {
	out := make(map[string][]gachaRecord, len(gachaPoolKeys))
	for _, pool := range gachaPoolKeys {
		out[pool] = gachaFetchPoolRecords(ctx, client, pool, roleID, serverID)
	}
	return out
}

func gachaDedupRecords(rows []gachaRecord) []gachaRecord {
	if len(rows) <= 1 {
		return rows
	}
	order := make([]string, 0, len(rows))
	index := map[string]int{}
	out := make([]gachaRecord, 0, len(rows))
	for _, row := range rows {
		seq := strings.TrimSpace(anyString(row.SeqID))
		if seq == "" {
			out = append(out, row)
			continue
		}
		if position, ok := index[seq]; ok {
			out[position] = gachaMergeRecord(out[position], row)
			continue
		}
		index[seq] = len(out)
		order = append(order, seq)
		out = append(out, row)
	}
	return out
}

func gachaMergeRecord(base, incoming gachaRecord) gachaRecord {
	if strings.TrimSpace(anyString(base.CharID)) == "" {
		base.CharID = incoming.CharID
	}
	if base.CharName == "" {
		base.CharName = incoming.CharName
	}
	if base.Rarity == 0 {
		base.Rarity = incoming.Rarity
	}
	if base.GachaTS == nil {
		base.GachaTS = incoming.GachaTS
	}
	if base.PoolID == "" {
		base.PoolID = incoming.PoolID
	}
	if base.PoolName == "" {
		base.PoolName = incoming.PoolName
	}
	if base.SeqID == nil {
		base.SeqID = incoming.SeqID
	}
	if base.WeaponType == "" {
		base.WeaponType = incoming.WeaponType
	}
	if incoming.IsNew {
		base.IsNew = true
	}
	if incoming.IsFree {
		base.IsFree = true
	}
	return base
}

// ---------- 卡池 UP 信息 ----------

type gachaBannerInfo struct {
	UpCharNames          []string
	UpCharName           string
	UpWeaponName         string
	ActiveCharPoolName   string
	ActiveWeaponPoolName string
	PoolUpMap            map[string][]string
	CharPoolOrderByTime  []string
}

func gachaNormalizePoolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if index := strings.Index(name, "·"); index >= 0 {
		return strings.TrimSpace(name[index+len("·"):])
	}
	return name
}

func gachaStringList(value any) []string {
	var out []string
	switch list := value.(type) {
	case []string:
		for _, item := range list {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
	case []any:
		for _, item := range list {
			if text := pexStringValue(item); text != "" {
				out = append(out, text)
			}
		}
	case string:
		if text := strings.TrimSpace(list); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func gachaParseBannerTime(value any, now time.Time) int64 {
	if value == nil {
		return 0
	}
	if number, ok := pexFloatValue(value); ok {
		if number <= 0 {
			return 0
		}
		if number < 1e10 {
			return int64(number * 1000)
		}
		return int64(number)
	}
	raw := strings.TrimSpace(pexStringValue(value))
	if raw == "" {
		return 0
	}
	if match := gachaTimeWithYear.FindStringSubmatch(raw); match != nil {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		hour, minute := 0, 0
		if match[4] != "" {
			hour, _ = strconv.Atoi(match[4])
		}
		if match[5] != "" {
			minute, _ = strconv.Atoi(match[5])
		}
		if month < 1 || month > 12 || day < 1 || day > 31 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return 0
		}
		return time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.Local).UnixMilli()
	}
	if match := gachaTimeNoYear.FindStringSubmatch(raw); match != nil {
		month, _ := strconv.Atoi(match[1])
		day, _ := strconv.Atoi(match[2])
		hour, minute := 0, 0
		if match[3] != "" {
			hour, _ = strconv.Atoi(match[3])
		}
		if match[4] != "" {
			minute, _ = strconv.Atoi(match[4])
		}
		if month < 1 || month > 12 || day < 1 || day > 31 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return 0
		}
		var best int64
		bestDiff := int64(math.MaxInt64)
		for _, year := range []int{now.Year() - 1, now.Year(), now.Year() + 1} {
			candidate := time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.Local).UnixMilli()
			diff := candidate - now.UnixMilli()
			if diff < 0 {
				diff = -diff
			}
			if diff < bestDiff {
				best, bestDiff = candidate, diff
			}
		}
		return best
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

func gachaParseBannerRows(rows []map[string]any, now time.Time) *gachaBannerInfo {
	info := &gachaBannerInfo{PoolUpMap: map[string][]string{}}
	type bannerRow struct {
		kind    string
		name    string
		upNames []string
		start   int64
		active  bool
	}
	parsed := make([]bannerRow, 0, len(rows))
	nowMS := now.UnixMilli()
	for _, row := range rows {
		kind := pexString(row, "type")
		rawName := pexString(row, "name")
		if rawName == "" {
			continue
		}
		name := gachaNormalizePoolName(rawName)
		upNames := gachaStringList(row["up_char_names"])
		if len(upNames) == 0 {
			if single := pexString(row, "up_char_name"); single != "" {
				upNames = []string{single}
			} else if single := pexString(row, "up"); single != "" {
				upNames = []string{single}
			}
		}
		start := gachaParseBannerTime(pexField(row, "start_ts", "start_time"), now)
		end := gachaParseBannerTime(pexField(row, "end_ts", "end_time"), now)
		active := pexBool(row, "is_active")
		if start > 0 && end > 0 {
			active = nowMS >= start && nowMS <= end
		}
		if name != "" && len(upNames) > 0 {
			info.PoolUpMap[name] = upNames
		}
		parsed = append(parsed, bannerRow{kind: kind, name: name, upNames: upNames, start: start, active: active})
	}

	var charRows []bannerRow
	for _, row := range parsed {
		switch {
		case row.kind == "特许寻访":
			charRows = append(charRows, row)
		case row.kind == "武库申领":
			if row.active && len(row.upNames) > 0 {
				if info.UpWeaponName == "" {
					info.UpWeaponName = row.upNames[0]
					info.ActiveWeaponPoolName = row.upNames[0]
				}
			} else if row.active && info.ActiveWeaponPoolName == "" {
				info.ActiveWeaponPoolName = row.name
			}
		}
	}
	for _, row := range charRows {
		if row.active && len(row.upNames) > 0 && len(info.UpCharNames) == 0 {
			info.UpCharNames = row.upNames
			info.ActiveCharPoolName = row.name
		}
	}
	if len(info.UpCharNames) > 0 {
		info.UpCharName = strings.Join(info.UpCharNames, "、")
	}
	sort.SliceStable(charRows, func(i, j int) bool { return charRows[i].start < charRows[j].start })
	for _, row := range charRows {
		if row.name != "" {
			info.CharPoolOrderByTime = append(info.CharPoolOrderByTime, row.name)
		}
	}
	return info
}

func gachaFetchBanner(ctx context.Context, client *apiClient, cfg config) *gachaBannerInfo {
	now := time.Now()
	if cfg.GachaSource == "local_file" {
		if info := gachaLocalBanner(now); info != nil {
			return info
		}
	}
	if res, err := client.getWikiData(ctx, "bili_wiki_activities", nil); err == nil && res.ok() {
		var data struct {
			Items      []map[string]any `json:"items"`
			Activities []map[string]any `json:"activities"`
		}
		if res.decode(&data) == nil {
			rows := data.Items
			if len(rows) == 0 {
				rows = data.Activities
			}
			if len(rows) > 0 {
				return gachaParseBannerRows(rows, now)
			}
		}
	}
	if cfg.GachaSource != "local_file" {
		if info := gachaLocalBanner(now); info != nil {
			return info
		}
	}
	return nil
}

func gachaLocalBanner(now time.Time) *gachaBannerInfo {
	var data struct {
		UpCharacters     []map[string]any `json:"up_characters"`
		UpWeapons        []map[string]any `json:"up_weapons"`
		PermanentWeapons []map[string]any `json:"permanent_weapons"`
	}
	if err := loadAssetJSON("data/game_banners.json", &data); err != nil {
		return nil
	}
	nowMS := now.UnixMilli()
	rows := make([]map[string]any, 0, len(data.UpCharacters)+len(data.UpWeapons)+len(data.PermanentWeapons))
	for _, item := range data.UpCharacters {
		poolName := pexString(item, "pool_name")
		charName := pexString(item, "character_name")
		if poolName == "" || charName == "" {
			continue
		}
		start := gachaParseBannerTime(pexField(item, "start_time"), now)
		end := gachaParseBannerTime(pexField(item, "end_time"), now)
		active := start > 0 && end > 0 && nowMS >= start && nowMS <= end
		rows = append(rows, map[string]any{
			"type":          "特许寻访",
			"name":          "特许寻访·" + poolName,
			"up":            charName,
			"up_char_names": []string{charName},
			"start_ts":      start,
			"end_ts":        end,
			"is_active":     active,
		})
	}
	for _, item := range data.UpWeapons {
		poolName := pexString(item, "pool_name")
		weaponName := pexString(item, "weapon_name")
		if poolName == "" || weaponName == "" {
			continue
		}
		start := gachaParseBannerTime(pexField(item, "start_time"), now)
		end := gachaParseBannerTime(pexField(item, "end_time"), now)
		active := start > 0 && end > 0 && nowMS >= start && nowMS <= end
		rows = append(rows, map[string]any{
			"type":          "武库申领",
			"name":          "武库申领·" + poolName,
			"up":            weaponName,
			"up_char_names": []string{weaponName},
			"start_ts":      start,
			"end_ts":        end,
			"is_active":     active,
		})
	}
	for _, item := range data.PermanentWeapons {
		poolName := pexString(item, "pool_name")
		weaponName := pexString(item, "weapon_name")
		if poolName == "" || weaponName == "" {
			continue
		}
		rows = append(rows, map[string]any{
			"type":          "武库申领",
			"name":          "武库申领·" + poolName,
			"up":            weaponName,
			"up_char_names": []string{weaponName},
			"is_active":     true,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return gachaParseBannerRows(rows, now)
}

func gachaUPNamesForPool(info *gachaBannerInfo, poolName, group string) []string {
	if info == nil {
		return nil
	}
	if names, ok := info.PoolUpMap[strings.TrimSpace(poolName)]; ok && len(names) > 0 {
		return names
	}
	if names, ok := info.PoolUpMap[gachaNormalizePoolName(poolName)]; ok && len(names) > 0 {
		return names
	}
	switch group {
	case "limited", "joint":
		if len(info.UpCharNames) > 0 {
			return info.UpCharNames
		}
	case "weapon":
		if info.UpWeaponName != "" {
			return []string{info.UpWeaponName}
		}
	}
	return nil
}

// ---------- 抽卡记录 ----------

func handleGachaRecords(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	target, ok, err := gachaResolveTarget(ctx, c, cfg, args)
	if !ok {
		return err
	}
	session, ok, err := gachaSessionFor(ctx, c, cfg, target)
	if !ok {
		return err
	}
	roleID := session.Binding.roleID()
	serverID := session.Binding.serverID()
	stats, statsErr := gachaFetchStats(ctx, session.Client, roleID, serverID)
	if statsErr != nil || !stats.hasAnyRecord() {
		if replyErr := replyText(ctx, c, getMessage("gacha.no_records")); replyErr != nil {
			return replyErr
		}
		return gachaSyncAndReply(ctx, c, cfg, target, false)
	}
	records := gachaFetchRecords(ctx, session.Client, roleID, serverID)
	banner := gachaFetchBanner(ctx, session.Client, cfg)
	return gachaReplyRecords(ctx, c, stats, records, banner)
}

func gachaReplyRecords(ctx context.Context, c sdk.Call, stats *gachaStatsData, records map[string][]gachaRecord, banner *gachaBannerInfo) error {
	overall := gachaOverallFromRecords(records)
	user := gachaStatsDataUser(stats)
	name := gachaUserDisplayName(user)
	channel := ""
	if user.ChannelName != "" {
		channel = " | " + user.ChannelName
	}

	var body strings.Builder
	body.WriteString(`<div class="grid c4">`)
	body.WriteString(gachaMetric("总抽数", strconv.Itoa(overall.Total), ""))
	body.WriteString(gachaMetric("六星", strconv.Itoa(overall.Star6), ""))
	body.WriteString(gachaMetric("五星", strconv.Itoa(overall.Star5), ""))
	body.WriteString(gachaMetric("四星", strconv.Itoa(overall.Star4), ""))
	body.WriteString(`</div>`)

	fallback := getMessage("gacha.record_fallback_title") + "\n"
	fallback += getMessage("gacha.record_fallback_user", map[string]any{"name": name, "channel": channel}) + "\n"
	fallback += getMessage("gacha.record_fallback_stats", map[string]any{
		"total": overall.Total, "star6": overall.Star6, "star5": overall.Star5, "star4": overall.Star4,
	}) + "\n"

	for _, def := range gachaGroupDefs {
		rows := gachaGroupRecords(records, def)
		preview := gachaPreviewRecords(rows, gachaRecordPreviewLimit)
		body.WriteString(`<div class="section"><h2>` + htmlEscape(def.Label) + ` · 共 ` + strconv.Itoa(len(rows)) + ` 抽</h2>`)
		if len(preview) == 0 {
			body.WriteString(`<p class="muted">` + htmlEscape(getMessage("gacha.record_empty")) + `</p>`)
		} else {
			body.WriteString(`<table><thead><tr><th>#</th><th>星级</th><th>名称</th><th>UP</th></tr></thead><tbody>`)
			for index, row := range preview {
				body.WriteString(gachaRecordRowHTML(index+1, row, def.Key, banner))
			}
			body.WriteString(`</tbody></table>`)
		}
		body.WriteString(`</div>`)

		fallback += "\n" + getMessage("gacha.record_section_header", map[string]any{"label": def.Label, "total": len(rows)}) + "\n"
		if len(preview) == 0 {
			fallback += getMessage("gacha.record_empty") + "\n"
		} else {
			for index, row := range preview {
				line := getMessage("gacha.record_item_line", map[string]any{
					"index": index + 1, "rarity": row.Rarity, "name": gachaRecordName(row),
				})
				if gachaRecordIsUp(row, def.Key, banner) {
					line += " UP"
				}
				fallback += line + "\n"
			}
		}
	}

	html := buildPage(pageOptions{
		title:    "抽卡记录",
		eyebrow:  "终末地 · 寻访记录",
		subtitle: htmlEscape(strings.TrimSpace(name + channel)),
		seal:     "记录",
		tags:     []string{fmt.Sprintf("共 %d 抽", overall.Total), fmt.Sprintf("六星 %d", overall.Star6)},
		body:     body.String(),
		width:    900,
	})
	return replyCard(ctx, c, html, 900, strings.TrimSpace(fallback))
}

func gachaRecordRowHTML(index int, row gachaRecord, group string, banner *gachaBannerInfo) string {
	rarityClass := "chip"
	if row.Rarity == 6 {
		rarityClass = "chip gold"
	} else if row.Rarity == 5 {
		rarityClass = "chip purple"
	}
	up := ""
	if gachaRecordIsUp(row, group, banner) {
		up = `<span class="chip red">UP</span>`
	}
	return `<tr><td>` + strconv.Itoa(index) + `</td><td><span class="` + rarityClass + `">★` +
		strconv.Itoa(row.Rarity) + `</span></td><td>` + htmlEscape(gachaRecordName(row)) + `</td><td>` + up + `</td></tr>`
}

func gachaRecordIsUp(row gachaRecord, group string, banner *gachaBannerInfo) bool {
	if row.Rarity < 5 {
		return false
	}
	var upNames []string
	switch group {
	case "limited", "joint":
		upNames = gachaUPNamesForPool(banner, row.PoolName, group)
	case "weapon":
		upNames = gachaUPNamesForPool(banner, row.PoolName, group)
	default:
		return false
	}
	return gachaIsUpName(gachaRecordName(row), upNames)
}

func gachaGroupRecords(records map[string][]gachaRecord, def gachaGroupDef) []gachaRecord {
	var out []gachaRecord
	for _, key := range def.PoolKeys {
		out = append(out, records[key]...)
	}
	return out
}

func gachaPreviewRecords(rows []gachaRecord, limit int) []gachaRecord {
	out := append([]gachaRecord(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return gachaRecordNewer(out[i], out[j]) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func gachaRecordNewer(left, right gachaRecord) bool {
	ls, lok := gachaRecordSeqNumber(left)
	rs, rok := gachaRecordSeqNumber(right)
	if lok && rok && ls != rs {
		return ls > rs
	}
	return gachaRecordTime(left) > gachaRecordTime(right)
}

func gachaRecordSeqNumber(row gachaRecord) (float64, bool) {
	return pexFloatValue(row.SeqID)
}

func gachaRecordTime(row gachaRecord) int64 {
	if value, ok := pexFloatValue(row.GachaTS); ok {
		if value < 1e10 {
			value *= 1000
		}
		return int64(value)
	}
	return 0
}

func gachaRecordName(row gachaRecord) string {
	name := strings.TrimSpace(row.CharName)
	if name == "" {
		name = strings.TrimSpace(anyString(row.CharID))
	}
	if name == "" {
		name = getMessage("common.unknown")
	}
	return name
}

func gachaIsUpName(name string, upNames []string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, up := range upNames {
		up = strings.TrimSpace(up)
		if up == "" {
			continue
		}
		if name == up || strings.Contains(name, up) || strings.Contains(up, name) {
			return true
		}
	}
	return false
}

// ---------- 抽卡同步 ----------

func handleGachaSync(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	target, ok, err := gachaResolveTarget(ctx, c, cfg, args)
	if !ok {
		return err
	}
	return gachaSyncAndReply(ctx, c, cfg, target, false)
}

func gachaSyncAndReply(ctx context.Context, c sdk.Call, cfg config, target string, fromAnalysis bool) error {
	session, ok, err := gachaSessionFor(ctx, c, cfg, target)
	if !ok {
		return err
	}
	roleID := session.Binding.roleID()
	serverID := session.Binding.serverID()
	query := gachaQuery(roleID, serverID)

	if status, err := gachaFetchSyncStatus(ctx, session.Client, query); err == nil && status != nil && status.Status == "syncing" {
		return replyText(ctx, c, gachaProgressText(status, c))
	}

	before, _ := gachaFetchStats(ctx, session.Client, roleID, serverID)
	firstSync := !before.hasAnyRecord()

	startMsg := getMessage("gacha.auth_full_sync")
	if fromAnalysis {
		startMsg = getMessage("gacha.analysis_sync_start")
	} else if !firstSync {
		startMsg = getMessage("gacha.sync_start")
	}
	if err := replyText(ctx, c, startMsg); err != nil {
		return err
	}

	body := map[string]any{"server_id": strconv.Itoa(serverID)}
	if roleID != "" {
		body["role_id"] = roleID
	}
	res, fetchErr := session.Client.request(ctx, http.MethodPost, "/api/endfield/gacha/fetch", nil, body)
	if fetchErr != nil {
		if strings.Contains(fetchErr.Error(), "HTTP 409") {
			return replyText(ctx, c, getMessage("gacha.sync_busy"))
		}
		return replyText(ctx, c, getMessage("gacha.sync_start_failed"))
	}
	if !res.ok() {
		return replyText(ctx, c, getMessage("gacha.sync_start_failed"))
	}
	var fetchData struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = res.decode(&fetchData)
	if fetchData.Status == "" {
		return replyText(ctx, c, getMessage("gacha.sync_start_failed"))
	}

	deadline := time.Now().Add(gachaSyncTimeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gachaSyncPollInterval):
		}
		if time.Now().After(deadline) {
			return replyText(ctx, c, getMessage("gacha.sync_timeout"))
		}
		status, statusErr := gachaFetchSyncStatus(ctx, session.Client, query)
		if statusErr != nil || status == nil {
			continue
		}
		switch status.Status {
		case "completed":
			after, statsErr := gachaFetchStats(ctx, session.Client, roleID, serverID)
			if statsErr == nil && after != nil {
				gachaSaveSummary(ctx, c.State, target, roleID, serverID, after)
			}
			added := status.NewRecords
			if added < 0 {
				added = 0
			}
			poolLine := gachaSyncPoolLine(after)
			doneMsg := getMessage("gacha.sync_done", map[string]any{
				"records_found": status.RecordsFound,
				"new_records":   added,
				"pool_detail":   poolLine,
			})
			if err := replyText(ctx, c, strings.TrimSpace(doneMsg)); err != nil {
				return err
			}
			if after == nil {
				return replyText(ctx, c, getMessage("gacha.no_records"))
			}
			records := gachaFetchRecords(ctx, session.Client, roleID, serverID)
			banner := gachaFetchBanner(ctx, session.Client, cfg)
			return gachaReplyAnalysis(ctx, c, after, records, banner)
		case "failed":
			errorText := status.Error
			if errorText == "" {
				errorText = status.Message
			}
			if errorText == "" {
				errorText = getMessage("common.unknown")
			}
			return replyText(ctx, c, getMessage("gacha.sync_failed", map[string]any{"error": errorText}))
		}
	}
}

func gachaSyncPoolLine(stats *gachaStatsData) string {
	if stats == nil {
		return ""
	}
	s := stats.Stats
	parts := []string{
		fmt.Sprintf("特许寻访 %d 条", s.LimitedCharCount),
		fmt.Sprintf("辉光庆典 %d 条", s.JointCharCount),
		fmt.Sprintf("武库交易所 %d 条", s.WeaponCount),
		fmt.Sprintf("基础寻访 %d 条", s.StandardCharCount+s.BeginnerCharCount),
	}
	return "\n" + getMessage("gacha.sync_done_pools", map[string]any{"pools": strings.Join(parts, " | ")})
}

func gachaProgressText(status *gachaSyncStatusData, c sdk.Call) string {
	message := status.Message
	if message == "" && status.CurrentPool != "" {
		message = getMessage("gacha.sync_query_pool", map[string]any{"pool": status.CurrentPool})
	}
	message = strings.ReplaceAll(message, "{qq号}", c.Event.UserID)
	message = strings.ReplaceAll(message, "{qqname}", c.Event.Nickname)
	stage := ""
	switch status.Stage {
	case "grant":
		stage = getMessage("gacha.sync_stage_grant")
	case "bindings":
		stage = getMessage("gacha.sync_stage_bindings")
	case "u8token":
		stage = getMessage("gacha.sync_stage_u8token")
	case "records":
		stage = getMessage("gacha.sync_stage_records")
	case "saving":
		stage = getMessage("gacha.sync_stage_saving")
	default:
		stage = status.Stage
	}
	pools := ""
	if status.TotalPools > 0 {
		pools = getMessage("gacha.sync_progress_pools", map[string]any{
			"completed": status.CompletedPools, "total": status.TotalPools,
		})
	}
	records := ""
	if status.RecordsFound > 0 {
		records = getMessage("gacha.sync_progress_records", map[string]any{"records": status.RecordsFound})
	}
	elapsed := ""
	if status.ElapsedSeconds > 0 {
		elapsed = getMessage("gacha.sync_progress_elapsed", map[string]any{"seconds": int(math.Round(status.ElapsedSeconds))})
	}
	line := getMessage("gacha.sync_progress_line", map[string]any{
		"progress": status.Progress, "pools": pools, "records": records, "elapsed": elapsed,
	})
	text := getMessage("gacha.sync_in_progress") + "\n" + line
	if stage != "" {
		text += "\n" + getMessage("gacha.sync_progress_stage", map[string]any{"stage": stage})
	}
	if message != "" {
		text += "\n" + message
	}
	return text
}

// ---------- 抽卡分析 ----------

func handleGachaAnalysis(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	target, ok, err := gachaResolveTarget(ctx, c, cfg, args)
	if !ok {
		return err
	}
	session, ok, err := gachaSessionFor(ctx, c, cfg, target)
	if !ok {
		return err
	}
	roleID := session.Binding.roleID()
	serverID := session.Binding.serverID()
	stats, statsErr := gachaFetchStats(ctx, session.Client, roleID, serverID)
	if statsErr != nil || !stats.hasAnyRecord() {
		if replyErr := replyText(ctx, c, getMessage("gacha.no_records")); replyErr != nil {
			return replyErr
		}
		return gachaSyncAndReply(ctx, c, cfg, target, true)
	}
	records := gachaFetchRecords(ctx, session.Client, roleID, serverID)
	banner := gachaFetchBanner(ctx, session.Client, cfg)
	return gachaReplyAnalysis(ctx, c, stats, records, banner)
}

func gachaReplyAnalysis(ctx context.Context, c sdk.Call, stats *gachaStatsData, records map[string][]gachaRecord, banner *gachaBannerInfo) error {
	overall, groups := gachaBuildAnalysis(stats, records, banner)
	user := gachaStatsDataUser(stats)
	name := gachaUserDisplayName(user)
	channel := ""
	if user.ChannelName != "" {
		channel = " · " + user.ChannelName
	}

	var body strings.Builder
	body.WriteString(`<div class="grid c4">`)
	body.WriteString(gachaMetric("总抽数", strconv.Itoa(overall.Total), ""))
	body.WriteString(gachaMetric("六星", strconv.Itoa(overall.Star6), ""))
	body.WriteString(gachaMetric("五星", strconv.Itoa(overall.Star5), ""))
	body.WriteString(gachaMetric("四星", strconv.Itoa(overall.Star4), ""))
	body.WriteString(gachaMetric("六星率", overall.SixRate, ""))
	body.WriteString(gachaMetric("平均出货", overall.AvgCost, ""))
	body.WriteString(gachaMetric("UP 命中", strconv.Itoa(overall.Up6), ""))
	body.WriteString(gachaMetric("不歪率", overall.WinRate, ""))
	body.WriteString(`</div>`)

	fallback := getMessage("gacha.analysis_fallback_title") + "\n"
	fallback += getMessage("gacha.analysis_fallback_user", map[string]any{"name": name, "channel": channel}) + "\n"

	for _, group := range groups {
		body.WriteString(`<div class="section"><h2>` + htmlEscape(group.Label) + ` · 共 ` +
			strconv.Itoa(group.Total) + ` 抽</h2>`)
		body.WriteString(`<div class="tags">`)
		body.WriteString(`<span class="chip">六星 ` + strconv.Itoa(group.Star6) + `</span>`)
		body.WriteString(`<span class="chip">五星 ` + strconv.Itoa(group.Star5) + `</span>`)
		body.WriteString(`<span class="chip">四星 ` + strconv.Itoa(group.Star4) + `</span>`)
		if group.Up6 > 0 || group.Star6 > 0 {
			body.WriteString(`<span class="chip green">UP ` + strconv.Itoa(group.Up6) + `</span>`)
			body.WriteString(`<span class="chip gold">不歪率 ` + htmlEscape(group.WinRate) + `</span>`)
		}
		if group.Free > 0 {
			body.WriteString(`<span class="chip purple">免费 ` + strconv.Itoa(group.Free) + `</span>`)
		}
		body.WriteString(`</div>`)
		if len(group.Pools) == 0 {
			body.WriteString(`<p class="muted">` + htmlEscape(getMessage("gacha.record_empty")) + `</p>`)
		} else {
			body.WriteString(`<table><thead><tr><th>卡池</th><th>抽数</th><th>六星</th><th>五星</th><th>四星</th><th>UP</th><th>不歪率</th><th>平均出货</th><th>平均UP</th><th>垫抽</th></tr></thead><tbody>`)
			for _, pool := range group.Pools {
				pity := "—"
				if !pool.HidePity {
					pity = strconv.Itoa(pool.Pity)
				}
				body.WriteString(`<tr><td>` + htmlEscape(pool.PoolName) + `</td><td>` + strconv.Itoa(pool.Paid) +
					`</td><td>` + strconv.Itoa(pool.Star6) + `</td><td>` + strconv.Itoa(pool.Star5) +
					`</td><td>` + strconv.Itoa(pool.Star4) + `</td><td>` + strconv.Itoa(pool.Up6) +
					`</td><td>` + htmlEscape(pool.WinRate) + `</td><td>` + htmlEscape(pool.AvgCost) +
					`</td><td>` + htmlEscape(pool.AvgUpCost) + `</td><td>` + pity + `</td></tr>`)
			}
			body.WriteString(`</tbody></table>`)
		}
		body.WriteString(gachaGroupStatusHTML(group))
		body.WriteString(gachaSixStarHTML(group))
		body.WriteString(`</div>`)

		for _, pool := range group.Pools {
			metric1, metric1Label, metric2, metric2Label := gachaPoolMetrics(group.Key, pool)
			fallback += getMessage("gacha.analysis_fallback_line", map[string]any{
				"group": group.Label, "pool": pool.PoolName, "total": pool.Paid,
				"metric1Label": metric1Label, "metric1": metric1,
				"metric2Label": metric2Label, "metric2": metric2,
			}) + "\n"
		}
	}
	fallback += getMessage("gacha.analysis_fallback_recent_hint", map[string]any{"prefix": ":"})

	html := buildPage(pageOptions{
		title:    "抽卡分析",
		eyebrow:  "终末地 · 寻访分析",
		subtitle: htmlEscape(strings.TrimSpace(name + channel)),
		seal:     "分析",
		tags:     []string{fmt.Sprintf("总抽数 %d", overall.Total), fmt.Sprintf("六星 %d", overall.Star6)},
		body:     body.String(),
		width:    1000,
	})
	return replyCard(ctx, c, html, 1000, strings.TrimSpace(fallback))
}

func gachaGroupStatusHTML(group gachaAnalysisGroup) string {
	var b strings.Builder
	if group.Key == "limited" {
		for _, pool := range group.Pools {
			if pool.HidePity || pool.Milestones == nil {
				continue
			}
			m := pool.Milestones
			b.WriteString(`<div class="tags" style="margin-top:10px">`)
			b.WriteString(`<span class="chip ` + gachaPityChipClass(group.SharedPity, gachaPityCharSoft) + `">共享垫抽 ` + strconv.Itoa(group.SharedPity) + `</span>`)
			b.WriteString(`<span class="chip ` + gachaPityChipClass(group.SharedPityUp, gachaPityCharHard) + `">大保底进度 ` + strconv.Itoa(group.SharedPityUp) + `</span>`)
			b.WriteString(`<span class="chip">120 抽 UP 还差 ` + strconv.Itoa(m.PullsToUp120) + `</span>`)
			b.WriteString(`<span class="chip">60 抽情报书 ` + gachaBoolText(m.HasInfoBook60, "已获得", "还差 "+strconv.Itoa(m.PullsToInfoBook60)+" 抽") + `</span>`)
			b.WriteString(`<span class="chip">240 抽信物 × ` + strconv.Itoa(m.Token240Times) + ` · 下次还差 ` + strconv.Itoa(m.PullsToNextToken240) + `</span>`)
			b.WriteString(`</div>`)
			break
		}
	}
	if group.Key == "weapon" {
		for _, pool := range group.Pools {
			if pool.Weapon == nil {
				continue
			}
			w := pool.Weapon
			b.WriteString(`<div class="tags" style="margin-top:10px">`)
			b.WriteString(`<span class="chip">申领次数 ` + strconv.Itoa(w.TotalSessions) + `</span>`)
			b.WriteString(`<span class="chip">4 次十连硬保底还差 ` + strconv.Itoa(w.SessionsToHardPity) + `</span>`)
			if w.HasUp6 {
				b.WriteString(`<span class="chip green">已获得 UP</span>`)
			} else {
				b.WriteString(`<span class="chip">8 次十连 UP 还差 ` + strconv.Itoa(w.SessionsToUp6) + `</span>`)
			}
			b.WriteString(`<span class="chip gold">下次 ` + htmlEscape(w.NextRewardType) + ` 还差 ` + strconv.Itoa(w.RemainingSessions) + ` 次</span>`)
			b.WriteString(`</div>`)
		}
	}
	return b.String()
}

func gachaSixStarHTML(group gachaAnalysisGroup) string {
	rows := append([]gachaSixStar(nil), group.SixStars...)
	for _, pool := range group.Pools {
		rows = append(rows, pool.FreeSixStars...)
	}
	if len(rows) == 0 {
		return ""
	}
	if len(rows) > gachaRecordPreviewLimit {
		rows = rows[:gachaRecordPreviewLimit]
	}
	var b strings.Builder
	b.WriteString(`<div class="section"><h2>六星记录</h2><ul class="list">`)
	for _, row := range rows {
		chip := "chip gold"
		if row.Tag == "UP" {
			chip = "chip green"
		} else if row.Tag == "歪" {
			chip = "chip red"
		}
		tag := ""
		if row.Tag != "" {
			tag = ` <span class="` + chip + `">` + htmlEscape(row.Tag) + `</span>`
		}
		b.WriteString(`<li><span>★6 ` + htmlEscape(row.Name) + tag + `</span><span>第 ` + strconv.Itoa(row.Pull) + ` 抽</span></li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

func gachaPoolMetrics(groupKey string, pool gachaAnalysisPool) (string, string, string, string) {
	if groupKey == "limited" || groupKey == "joint" {
		return pool.AvgUpCost, "平均UP花费", pool.WinRate, "不歪率"
	}
	if groupKey == "weapon" {
		return pool.AvgUpCost, "平均UP花费", pool.WinRate, "不歪率"
	}
	return pool.AvgCost, "每红花费", strconv.Itoa(pool.Star6), "出红数"
}

func gachaPityChipClass(current, max int) string {
	if max <= 0 {
		return "chip"
	}
	percent := float64(current) / float64(max)
	if percent >= 0.8 {
		return "chip red"
	}
	if percent >= 0.5 {
		return "chip gold"
	}
	return "chip green"
}

func gachaBoolText(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

// ---------- 分析计算 ----------

type gachaOverall struct {
	Total   int
	Paid    int
	Free    int
	Star6   int
	Star5   int
	Star4   int
	Up6     int
	SixRate string
	AvgCost string
	WinRate string
}

type gachaSixStar struct {
	Name string
	Pull int
	Tag  string
}

type gachaMilestones struct {
	PaidCount           int
	HasUp6              bool
	PullsToUp120        int
	HasInfoBook60       bool
	PullsToInfoBook60   int
	Token240Times       int
	PullsToNextToken240 int
	SharedPity6         int
	SharedPityUp        int
}

type gachaWeaponStatus struct {
	TotalSessions      int
	SessionsSinceLast6 int
	SessionsToHardPity int
	HasUp6             bool
	SessionsToUp6      int
	NextRewardAt       int
	NextRewardType     string
	RemainingSessions  int
}

type gachaAnalysisPool struct {
	PoolName     string
	Total        int
	Paid         int
	Free         int
	Star6        int
	Star5        int
	Star4        int
	Up6          int
	WinRate      string
	AvgCost      string
	AvgUpCost    string
	Pity         int
	HidePity     bool
	PityTo5      int
	Milestones   *gachaMilestones
	Weapon       *gachaWeaponStatus
	SixStars     []gachaSixStar
	FreeSixStars []gachaSixStar

	avgCostValue   int
	avgUpCostValue int
}

type gachaAnalysisGroup struct {
	Key          string
	Label        string
	Total        int
	Paid         int
	Free         int
	Star6        int
	Star5        int
	Star4        int
	Up6          int
	WinRate      string
	AvgCost      string
	AvgUpCost    string
	Pools        []gachaAnalysisPool
	SixStars     []gachaSixStar
	SharedPity   int
	SharedPityUp int
}

func gachaOverallFromRecords(records map[string][]gachaRecord) gachaOverall {
	var overall gachaOverall
	for _, rows := range records {
		for _, row := range rows {
			overall.Total++
			if row.IsFree {
				overall.Free++
			}
			switch row.Rarity {
			case 6:
				overall.Star6++
			case 5:
				overall.Star5++
			default:
				overall.Star4++
			}
		}
	}
	overall.Paid = overall.Total - overall.Free
	if overall.Total > 0 {
		overall.SixRate = gachaPercent(overall.Star6, overall.Total)
	}
	if overall.Star6 > 0 {
		overall.AvgCost = strconv.Itoa(int(math.Round(float64(overall.Paid)/float64(overall.Star6)))) + "抽"
	} else {
		overall.AvgCost = "-"
	}
	overall.WinRate = "-"
	return overall
}

func gachaBuildAnalysis(stats *gachaStatsData, records map[string][]gachaRecord, banner *gachaBannerInfo) (gachaOverall, []gachaAnalysisGroup) {
	overall := gachaOverallFromRecords(records)
	groups := make([]gachaAnalysisGroup, 0, len(gachaGroupDefs))
	for _, def := range gachaGroupDefs {
		rows := gachaGroupRecords(records, def)
		group := gachaAnalysisGroup{Key: def.Key, Label: def.Label}
		byPool := map[string][]gachaRecord{}
		for _, row := range rows {
			name := strings.TrimSpace(row.PoolName)
			if name == "" {
				name = getMessage("common.unknown")
			}
			byPool[name] = append(byPool[name], row)
		}
		names := make([]string, 0, len(byPool))
		for name := range byPool {
			names = append(names, name)
		}
		gachaSortPoolNames(names, byPool, def.Key)
		for _, name := range names {
			poolRows := byPool[name]
			upNames := gachaUPNamesForPool(banner, name, def.Key)
			pool := gachaCalcPool(def.Key, name, poolRows, upNames)
			group.Pools = append(group.Pools, pool)
			group.Total += pool.Paid
			group.Paid += pool.Paid
			group.Free += pool.Free
			group.Star6 += pool.Star6
			group.Star5 += pool.Star5
			group.Star4 += pool.Star4
			group.Up6 += pool.Up6
			group.SixStars = append(group.SixStars, pool.SixStars...)
		}
		if len(group.SixStars) > 0 {
			reverseSixStars(group.SixStars)
		}
		if group.Star6 > 0 {
			group.WinRate = gachaPercent(group.Up6, group.Star6)
			group.AvgCost = strconv.Itoa(int(math.Round(float64(group.Paid)/float64(group.Star6)))) + "抽"
			upCostSum := 0
			upCount := 0
			for _, pool := range group.Pools {
				if pool.avgUpCostValue > 0 {
					upCostSum += pool.avgUpCostValue * pool.Up6
					upCount += pool.Up6
				}
			}
			if upCount > 0 {
				group.AvgUpCost = strconv.Itoa(int(math.Round(float64(upCostSum)/float64(upCount)))) + "抽"
			} else {
				group.AvgUpCost = "-"
			}
		} else {
			group.WinRate = "-"
			group.AvgCost = "-"
			group.AvgUpCost = "-"
		}
		groups = append(groups, group)
	}

	overall.Up6 = 0
	for _, group := range groups {
		overall.Up6 += group.Up6
	}
	if overall.Star6 > 0 {
		overall.WinRate = gachaPercent(overall.Up6, overall.Star6)
	}
	if overall.Total == 0 && stats != nil {
		overall.Total = stats.Stats.TotalCount
		overall.Star6 = stats.Stats.Star6Count
		overall.Star5 = stats.Stats.Star5Count
		overall.Star4 = stats.Stats.Star4Count
	}
	gachaApplySharedLimited(groups, records, banner)
	return overall, groups
}

func gachaCalcPool(group, poolName string, rows []gachaRecord, upNames []string) gachaAnalysisPool {
	pool := gachaAnalysisPool{PoolName: poolName, Total: len(rows)}
	paid := make([]gachaRecord, 0, len(rows))
	free := make([]gachaRecord, 0, len(rows))
	for _, row := range rows {
		if row.IsFree {
			free = append(free, row)
		} else {
			paid = append(paid, row)
		}
	}
	pool.Paid = len(paid)
	pool.Free = len(free)
	sort.SliceStable(paid, func(i, j int) bool { return gachaRecordOlder(paid[i], paid[j]) })

	pity := 0
	pityTo5 := 0
	upCostSum := 0
	upCostCount := 0
	for _, row := range paid {
		pity++
		pityTo5++
		switch row.Rarity {
		case 6:
			pull := pity
			isUp := gachaIsUpName(gachaRecordName(row), upNames)
			if isUp {
				pool.Up6++
				upCostSum += pull
				upCostCount++
			}
			pool.SixStars = append(pool.SixStars, gachaSixStar{
				Name: gachaRecordName(row),
				Pull: pull,
				Tag:  gachaSixStarTag(group, pull, isUp, upNames),
			})
			pity = 0
		case 5:
			pool.Star5++
		default:
			pool.Star4++
		}
		if row.Rarity >= 5 {
			pityTo5 = 0
		}
	}
	pool.Star6 = len(pool.SixStars)
	pool.Pity = pity
	pool.PityTo5 = pityTo5
	if pool.Star6 > 0 {
		pool.avgCostValue = int(math.Round(float64(pool.Paid) / float64(pool.Star6)))
		pool.AvgCost = strconv.Itoa(pool.avgCostValue) + "抽"
		pool.WinRate = gachaPercent(pool.Up6, pool.Star6)
	} else {
		pool.AvgCost = "-"
		pool.WinRate = "-"
	}
	if upCostCount > 0 {
		pool.avgUpCostValue = int(math.Round(float64(upCostSum) / float64(upCostCount)))
		pool.AvgUpCost = strconv.Itoa(pool.avgUpCostValue) + "抽"
	} else {
		pool.AvgUpCost = "-"
	}

	if group == "limited" || group == "joint" {
		milestones := &gachaMilestones{PaidCount: pool.Paid, HasUp6: pool.Up6 > 0}
		if milestones.HasUp6 {
			milestones.PullsToUp120 = 0
		} else {
			milestones.PullsToUp120 = maxInt(0, gachaPitySpecial120-pool.Paid)
		}
		milestones.HasInfoBook60 = pool.Paid >= gachaPitySpecial60
		if milestones.HasInfoBook60 {
			milestones.PullsToInfoBook60 = 0
		} else {
			milestones.PullsToInfoBook60 = gachaPitySpecial60 - pool.Paid
		}
		milestones.Token240Times = pool.Paid / gachaPitySpecial240
		milestones.PullsToNextToken240 = (milestones.Token240Times+1)*gachaPitySpecial240 - pool.Paid
		pool.Milestones = milestones
	}
	if group == "weapon" {
		sessions := pool.Paid / 10
		pullsSinceLast6 := 0
		for index := len(paid) - 1; index >= 0; index-- {
			if paid[index].Rarity == 6 {
				break
			}
			pullsSinceLast6++
		}
		sessionsSince := pullsSinceLast6 / 10
		status := &gachaWeaponStatus{
			TotalSessions:      sessions,
			SessionsSinceLast6: sessionsSince,
			SessionsToHardPity: maxInt(0, gachaPityWeaponHard-sessionsSince),
			HasUp6:             pool.Up6 > 0,
		}
		if status.HasUp6 {
			status.SessionsToUp6 = 0
		} else {
			status.SessionsToUp6 = maxInt(0, gachaPityWeaponUpHard-sessions)
		}
		nextBox := 10
		if sessions >= 10 {
			nextBox = 10 + int(math.Ceil(float64(sessions+1-10)/16))*16
		}
		nextUp := 18
		if sessions >= 18 {
			nextUp = 18 + int(math.Ceil(float64(sessions+1-18)/16))*16
		}
		status.NextRewardAt = minInt(nextBox, nextUp)
		if status.NextRewardAt == nextBox {
			status.NextRewardType = "武库箱"
		} else {
			status.NextRewardType = "UP武器"
		}
		status.RemainingSessions = maxInt(0, status.NextRewardAt-sessions)
		pool.Weapon = status
	}

	sort.SliceStable(free, func(i, j int) bool { return gachaRecordOlder(free[i], free[j]) })
	freePity := 0
	for _, row := range free {
		freePity++
		if row.Rarity != 6 {
			continue
		}
		pool.FreeSixStars = append(pool.FreeSixStars, gachaSixStar{
			Name: gachaRecordName(row),
			Pull: freePity,
			Tag:  "免费",
		})
		freePity = 0
	}
	if len(pool.FreeSixStars) > 0 {
		reverseSixStars(pool.FreeSixStars)
	}
	reverseSixStars(pool.SixStars)
	return pool
}

func gachaSixStarTag(group string, pull int, isUp bool, upNames []string) string {
	switch group {
	case "weapon":
		sessions := int(math.Ceil(float64(pull) / 10))
		if sessions >= gachaPityWeaponHard {
			return "保底"
		}
		if len(upNames) > 0 && !isUp {
			return "歪"
		}
		if isUp {
			return "UP"
		}
	case "limited", "joint":
		if pull >= gachaPityCharSoft+1 {
			return "保底"
		}
		if isUp {
			return "UP"
		}
		if len(upNames) > 0 {
			return "歪"
		}
	}
	return ""
}

func gachaApplySharedLimited(groups []gachaAnalysisGroup, records map[string][]gachaRecord, banner *gachaBannerInfo) {
	rows := records["limited"]
	if len(rows) == 0 {
		return
	}
	type limitedRow struct {
		row      gachaRecord
		poolName string
	}
	paid := make([]limitedRow, 0, len(rows))
	for _, row := range rows {
		if row.IsFree {
			continue
		}
		poolName := strings.TrimSpace(row.PoolName)
		if poolName == "" {
			poolName = getMessage("common.unknown")
		}
		paid = append(paid, limitedRow{row: row, poolName: poolName})
	}
	if len(paid) == 0 {
		return
	}
	sort.SliceStable(paid, func(i, j int) bool {
		left, right := paid[i].row, paid[j].row
		lt, rt := gachaRecordTime(left), gachaRecordTime(right)
		if lt != rt {
			return lt < rt
		}
		ls, lok := gachaRecordSeqNumber(left)
		rs, rok := gachaRecordSeqNumber(right)
		if lok && rok && ls != rs {
			return ls < rs
		}
		return anyString(left.SeqID) < anyString(right.SeqID)
	})
	sharedPity := 0
	sharedUpPity := 0
	lastWasUp := false
	currentPool := ""
	for _, item := range paid {
		sharedPity++
		sharedUpPity++
		currentPool = item.poolName
		if item.row.Rarity != 6 {
			continue
		}
		upNames := gachaUPNamesForPool(banner, item.poolName, "limited")
		lastWasUp = gachaIsUpName(gachaRecordName(item.row), upNames)
		sharedPity = 0
		if lastWasUp {
			sharedUpPity = 0
		}
	}
	if currentPool == "" {
		return
	}
	for groupIndex := range groups {
		group := &groups[groupIndex]
		if group.Key != "limited" {
			continue
		}
		group.SharedPity = sharedPity
		group.SharedPityUp = sharedUpPity
		for poolIndex := range group.Pools {
			pool := &group.Pools[poolIndex]
			if pool.PoolName == currentPool {
				pool.Pity = sharedPity
				pool.HidePity = false
				if pool.Milestones != nil {
					pool.Milestones.SharedPity6 = sharedPity
					pool.Milestones.SharedPityUp = sharedUpPity
					if lastWasUp {
						pool.Milestones.PullsToUp120 = 0
					} else {
						pool.Milestones.PullsToUp120 = maxInt(0, gachaPitySpecial120-sharedUpPity)
					}
				}
			} else {
				pool.HidePity = true
			}
		}
		return
	}
}

func gachaSortPoolNames(names []string, byPool map[string][]gachaRecord, group string) {
	sort.SliceStable(names, func(i, j int) bool {
		left, right := names[i], names[j]
		if group == "base" {
			order := map[string]int{"基础寻访": 0, "启程寻访": 1}
			li, lok := order[left]
			ri, rok := order[right]
			if lok && rok {
				return li < ri
			}
			if lok {
				return true
			}
			if rok {
				return false
			}
		}
		if group == "weapon" {
			ls := strings.Contains(left, "星声申领")
			rs := strings.Contains(right, "星声申领")
			if ls != rs {
				return !ls
			}
		}
		return gachaMaxSeq(byPool[left]) > gachaMaxSeq(byPool[right])
	})
}

func gachaMaxSeq(rows []gachaRecord) float64 {
	max := float64(-1)
	for _, row := range rows {
		if value, ok := gachaRecordSeqNumber(row); ok && value > max {
			max = value
		}
	}
	return max
}

func gachaRecordOlder(left, right gachaRecord) bool {
	lt, rt := gachaRecordTime(left), gachaRecordTime(right)
	if lt != rt {
		return lt < rt
	}
	ls, lok := gachaRecordSeqNumber(left)
	rs, rok := gachaRecordSeqNumber(right)
	if lok && rok && ls != rs {
		return ls < rs
	}
	return anyString(left.SeqID) < anyString(right.SeqID)
}

func reverseSixStars(rows []gachaSixStar) {
	for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
		rows[left], rows[right] = rows[right], rows[left]
	}
}

// ---------- 全服统计 ----------

func handleGachaGlobal(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	params := gachaParseGlobalArgs(args)
	client := newAPIClient(cfg, binding{})
	query := url.Values{}
	if params.provider != "" && params.provider != "all" {
		query.Set("provider", params.provider)
	}
	if params.poolPeriod != "" {
		query.Set("pool_period", params.poolPeriod)
	}
	if params.weaponPoolPeriod != "" {
		query.Set("weapon_pool_period", params.weaponPoolPeriod)
	}
	if params.refresh {
		query.Set("refresh", "true")
	}
	res, err := client.request(ctx, http.MethodGet, "/api/endfield/gacha/global-stats", query, nil)
	if err != nil || !res.ok() {
		return replyText(ctx, c, getMessage("gacha.global_stats_failed"))
	}
	var data gachaGlobalResponse
	if err := res.decode(&data); err != nil || data.Stats == nil {
		return replyText(ctx, c, getMessage("gacha.global_stats_failed"))
	}
	banner := gachaFetchBanner(ctx, client, cfg)
	return gachaReplyGlobal(ctx, c, &data, banner, params)
}

type gachaGlobalArgs struct {
	provider         string
	poolPeriod       string
	weaponPoolPeriod string
	refresh          bool
}

func gachaParseGlobalArgs(args string) gachaGlobalArgs {
	result := gachaGlobalArgs{}
	tokens := strings.Fields(args)
	rest := make([]string, 0, len(tokens))
	mapProvider := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "国服", "skland", "cn", "official":
			return "skland"
		case "国际服", "skport", "int", "global":
			return "skport"
		case "全量", "全部", "all":
			return "all"
		}
		return ""
	}
	for _, token := range tokens {
		lower := strings.ToLower(token)
		if lower == "刷新" || lower == "强制" || lower == "refresh" || lower == "force" {
			result.refresh = true
			continue
		}
		if provider := mapProvider(token); provider != "" {
			result.provider = provider
			continue
		}
		if key, value, ok := strings.Cut(token, ":"); ok {
			gachaApplyGlobalKeyValue(&result, key, value, mapProvider)
			continue
		}
		if key, value, ok := strings.Cut(token, "="); ok {
			gachaApplyGlobalKeyValue(&result, key, value, mapProvider)
			continue
		}
		rest = append(rest, token)
	}
	if result.poolPeriod == "" && len(rest) > 0 {
		result.poolPeriod = strings.Join(rest, " ")
	}
	return result
}

func gachaApplyGlobalKeyValue(result *gachaGlobalArgs, key, value string, mapProvider func(string) string) {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch key {
	case "provider", "平台":
		if provider := mapProvider(value); provider != "" {
			result.provider = provider
		}
	case "pool", "pool_period", "限定", "限定池", "限定期", "limited":
		result.poolPeriod = value
	case "weapon", "weapon_pool_period", "武器", "武器池", "武器期":
		result.weaponPoolPeriod = value
	}
}

type gachaGlobalResponse struct {
	Cached     bool              `json:"cached"`
	LastUpdate string            `json:"last_update"`
	Provider   string            `json:"provider"`
	Stats      *gachaGlobalStats `json:"stats"`
}

type gachaGlobalStats struct {
	TotalPulls        float64                        `json:"total_pulls"`
	TotalUsers        float64                        `json:"total_users"`
	Star6Total        float64                        `json:"star6_total"`
	Star6Limited      float64                        `json:"star6_limited"`
	Star6Standard     float64                        `json:"star6_standard"`
	Star5Total        float64                        `json:"star5_total"`
	Star4Total        float64                        `json:"star4_total"`
	AvgPity           float64                        `json:"avg_pity"`
	LimitedUpAvgPity  float64                        `json:"limited_up_avg_pity"`
	OperatorTotal     float64                        `json:"operator_total_pulls"`
	OperatorStar6     float64                        `json:"operator_star6_total"`
	OperatorAvgPity   float64                        `json:"operator_avg_pity"`
	WeaponTotal       float64                        `json:"weapon_total_pulls"`
	WeaponStar6       float64                        `json:"weapon_star6_total"`
	WeaponAvgPity     float64                        `json:"weapon_avg_pity"`
	ByType            map[string]gachaGlobalPool     `json:"by_type"`
	ByProvider        map[string]gachaGlobalProvider `json:"by_provider"`
	ByChannel         map[string]gachaGlobalProvider `json:"by_channel"`
	ByRegion          map[string]gachaGlobalProvider `json:"by_region"`
	PoolPeriods       []gachaGlobalPeriod            `json:"pool_periods"`
	WeaponPoolPeriods []gachaGlobalPeriod            `json:"weapon_pool_periods"`
	CurrentPool       gachaGlobalPeriod              `json:"current_pool"`
	Ranking           map[string]gachaGlobalRanking  `json:"ranking"`
}

type gachaGlobalPool struct {
	Total         float64                   `json:"total"`
	Star6         float64                   `json:"star6"`
	Star6Limited  float64                   `json:"star6_limited"`
	Star6Standard float64                   `json:"star6_standard"`
	Star5         float64                   `json:"star5"`
	Star4         float64                   `json:"star4"`
	AvgPity       float64                   `json:"avg_pity"`
	UpAvgPity     float64                   `json:"up_avg_pity"`
	Distribution  []gachaGlobalDistribution `json:"distribution"`
}

type gachaGlobalDistribution struct {
	Range string  `json:"range"`
	Count float64 `json:"count"`
}

type gachaGlobalProvider struct {
	TotalUsers         float64 `json:"total_users"`
	TotalPulls         float64 `json:"total_pulls"`
	Star6Total         float64 `json:"star6_total"`
	Star5Total         float64 `json:"star5_total"`
	Star4Total         float64 `json:"star4_total"`
	AvgPity            float64 `json:"avg_pity"`
	OperatorTotalPulls float64 `json:"operator_total_pulls"`
	OperatorStar6Total float64 `json:"operator_star6_total"`
	OperatorAvgPity    float64 `json:"operator_avg_pity"`
	WeaponTotalPulls   float64 `json:"weapon_total_pulls"`
	WeaponStar6Total   float64 `json:"weapon_star6_total"`
	WeaponAvgPity      float64 `json:"weapon_avg_pity"`
}

type gachaGlobalPeriod struct {
	PoolName      string   `json:"pool_name"`
	UpCharNames   []string `json:"up_char_names"`
	UpWeaponNames []string `json:"up_weapon_names"`
	TotalPulls    float64  `json:"total_pulls"`
	Star6Count    float64  `json:"star6_count"`
	UpCount       float64  `json:"up_count"`
	UpAvgPity     float64  `json:"up_avg_pity"`
	Star6AvgPity  float64  `json:"star6_avg_pity"`
}

type gachaGlobalRanking struct {
	SixStar  []gachaGlobalRankRow `json:"six_star"`
	FiveStar []gachaGlobalRankRow `json:"five_star"`
}

type gachaGlobalRankRow struct {
	CharName   string  `json:"char_name"`
	WeaponName string  `json:"weapon_name"`
	CharID     string  `json:"char_id"`
	WeaponID   string  `json:"weapon_id"`
	Count      float64 `json:"count"`
	Percent    float64 `json:"percent"`
}

func gachaReplyGlobal(ctx context.Context, c sdk.Call, data *gachaGlobalResponse, banner *gachaBannerInfo, params gachaGlobalArgs) error {
	stats := data.Stats
	provider := data.Provider
	if provider == "" {
		provider = params.provider
	}
	if provider == "" {
		provider = "all"
	}
	providerLabel := map[string]string{"all": "全量", "skland": "国服", "skport": "国际服"}[provider]
	if providerLabel == "" {
		providerLabel = provider
	}
	syncTime := gachaFormatSyncTime(data.Cached, data.LastUpdate)

	currentPoolName := ""
	currentUpNames := []string{}
	if banner != nil {
		currentPoolName = banner.ActiveCharPoolName
		currentUpNames = banner.UpCharNames
	}
	if currentPoolName == "" {
		currentPoolName = stats.CurrentPool.PoolName
	}
	if len(currentUpNames) == 0 {
		currentUpNames = stats.CurrentPool.UpCharNames
	}
	weaponPoolName := ""
	weaponUpNames := []string{}
	if banner != nil {
		weaponPoolName = banner.ActiveWeaponPoolName
		if banner.UpWeaponName != "" {
			weaponUpNames = []string{banner.UpWeaponName}
		}
	}
	if weaponPoolName == "" {
		weaponPoolName = params.weaponPoolPeriod
	}

	var body strings.Builder
	body.WriteString(`<div class="tags">`)
	body.WriteString(`<span class="chip">口径 ` + htmlEscape(providerLabel) + `</span>`)
	if params.poolPeriod != "" {
		body.WriteString(`<span class="chip">限定 ` + htmlEscape(params.poolPeriod) + `</span>`)
	}
	if params.weaponPoolPeriod != "" {
		body.WriteString(`<span class="chip">武器 ` + htmlEscape(params.weaponPoolPeriod) + `</span>`)
	}
	body.WriteString(`<span class="chip">` + htmlEscape(syncTime) + `</span>`)
	if currentPoolName != "" {
		body.WriteString(`<span class="chip gold">当期UP ` + htmlEscape(currentPoolName) + `</span>`)
	}
	body.WriteString(`</div>`)

	body.WriteString(`<div class="grid c4" style="margin-top:14px">`)
	body.WriteString(gachaMetric("总抽数", gachaFormatNum(stats.TotalPulls), ""))
	body.WriteString(gachaMetric("统计用户", gachaFormatNum(stats.TotalUsers), ""))
	body.WriteString(gachaMetric("六星", gachaFormatNum(stats.Star6Total), ""))
	body.WriteString(gachaMetric("平均出货", gachaFormatPity(stats.AvgPity), ""))
	body.WriteString(gachaMetric("干员池抽数", gachaFormatNum(stats.OperatorTotal), ""))
	body.WriteString(gachaMetric("干员六星", gachaFormatNum(stats.OperatorStar6), ""))
	body.WriteString(gachaMetric("武器池抽数", gachaFormatNum(stats.WeaponTotal), ""))
	body.WriteString(gachaMetric("武器六星", gachaFormatNum(stats.WeaponStar6), ""))
	body.WriteString(`</div>`)

	typeLabels := []struct {
		key   string
		label string
	}{
		{"limited", "限定池"}, {"weapon", "武器池"}, {"standard", "常驻池"}, {"beginner", "新手池"},
	}
	body.WriteString(`<div class="section"><h2>卡池统计</h2><table><thead><tr><th>卡池</th><th>抽数</th><th>六星</th><th>六星(限定/常驻)</th><th>五星</th><th>四星</th><th>六星率</th><th>平均出货</th><th>UP平均</th></tr></thead><tbody>`)
	for _, item := range typeLabels {
		pool, ok := stats.ByType[item.key]
		if !ok {
			continue
		}
		rate := "0%"
		if pool.Total > 0 {
			rate = gachaPercentFloat(pool.Star6, pool.Total)
		}
		body.WriteString(`<tr><td>` + item.label + `</td><td>` + gachaFormatNum(pool.Total) + `</td><td>` +
			gachaFormatNum(pool.Star6) + `</td><td>` + gachaFormatNum(pool.Star6Limited) + ` / ` +
			gachaFormatNum(pool.Star6Standard) + `</td><td>` + gachaFormatNum(pool.Star5) + `</td><td>` +
			gachaFormatNum(pool.Star4) + `</td><td>` + rate + `</td><td>` + gachaFormatPity(pool.AvgPity) +
			`</td><td>` + gachaFormatPity(pool.UpAvgPity) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div>`)

	body.WriteString(gachaGlobalProviderHTML("平台分布", stats.ByProvider))
	body.WriteString(gachaGlobalProviderHTML("渠道分布", stats.ByChannel))
	body.WriteString(gachaGlobalProviderHTML("地区分布", stats.ByRegion))

	body.WriteString(gachaGlobalPeriodHTML("限定卡池", stats.PoolPeriods, banner, "limited"))
	body.WriteString(gachaGlobalPeriodHTML("武器卡池", stats.WeaponPoolPeriods, banner, "weapon"))
	if currentPoolName != "" {
		upText := "—"
		if len(currentUpNames) > 0 {
			upText = strings.Join(currentUpNames, "、")
		}
		body.WriteString(`<div class="section"><h2>当期 UP</h2><div class="row"><span class="label">` +
			htmlEscape(currentPoolName) + `</span><span class="value">` + htmlEscape(upText) + `</span></div></div>`)
	}
	if weaponPoolName != "" {
		upText := "—"
		if len(weaponUpNames) > 0 {
			upText = strings.Join(weaponUpNames, "、")
		}
		body.WriteString(`<div class="section"><h2>当期武器 UP</h2><div class="row"><span class="label">` +
			htmlEscape(weaponPoolName) + `</span><span class="value">` + htmlEscape(upText) + `</span></div></div>`)
	}

	fallback := getMessage("gacha.global_stats_fallback_title") + "\n"
	fallback += "口径: " + providerLabel
	if params.poolPeriod != "" {
		fallback += " | 限定: " + params.poolPeriod
	}
	if params.weaponPoolPeriod != "" {
		fallback += " | 武器: " + params.weaponPoolPeriod
	}
	fallback += "\n" + syncTime + "\n"
	fallback += fmt.Sprintf("总抽数 %s / 统计用户 %s / 6星 %s / 平均出货 %s\n",
		gachaFormatNum(stats.TotalPulls), gachaFormatNum(stats.TotalUsers), gachaFormatNum(stats.Star6Total), gachaFormatPity(stats.AvgPity))
	fallback += fmt.Sprintf("干员池 %s 抽 / 6星 %s / 均出 %s\n",
		gachaFormatNum(stats.OperatorTotal), gachaFormatNum(stats.OperatorStar6), gachaFormatPity(stats.OperatorAvgPity))
	fallback += fmt.Sprintf("武器池 %s 抽 / 6星 %s / 均出 %s",
		gachaFormatNum(stats.WeaponTotal), gachaFormatNum(stats.WeaponStar6), gachaFormatPity(stats.WeaponAvgPity))

	html := buildPage(pageOptions{
		title:    "全服寻访统计",
		eyebrow:  "终末地 · 全服统计",
		subtitle: htmlEscape(syncTime),
		seal:     "全服",
		body:     body.String(),
		width:    1000,
	})
	return replyCard(ctx, c, html, 1000, strings.TrimSpace(fallback))
}

func gachaGlobalProviderHTML(title string, rows map[string]gachaGlobalProvider) string {
	if len(rows) == 0 {
		return ""
	}
	order := []string{"all", "skland", "skport", "official", "bilibili"}
	seen := map[string]bool{}
	keys := make([]string, 0, len(rows))
	for _, key := range order {
		if _, ok := rows[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for key := range rows {
		if !seen[key] && !strings.EqualFold(key, "unknown") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<div class="section"><h2>` + htmlEscape(title) + `</h2><table><thead><tr><th>分组</th><th>用户</th><th>抽数</th><th>六星</th><th>平均出货</th><th>干员均出</th><th>武器均出</th></tr></thead><tbody>`)
	for _, key := range keys {
		row := rows[key]
		label := map[string]string{"all": "全量", "skland": "国服", "skport": "国际服", "official": "官服 CN-01", "bilibili": "B服 CN-02"}[key]
		if label == "" {
			label = strings.ToUpper(key)
		}
		b.WriteString(`<tr><td>` + htmlEscape(label) + `</td><td>` + gachaFormatNum(row.TotalUsers) + `</td><td>` +
			gachaFormatNum(row.TotalPulls) + `</td><td>` + gachaFormatNum(row.Star6Total) + `</td><td>` +
			gachaFormatPity(row.AvgPity) + `</td><td>` + gachaFormatPity(row.OperatorAvgPity) +
			`</td><td>` + gachaFormatPity(row.WeaponAvgPity) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func gachaGlobalPeriodHTML(title string, rows []gachaGlobalPeriod, banner *gachaBannerInfo, group string) string {
	if len(rows) == 0 {
		return ""
	}
	limit := len(rows)
	if limit > 4 {
		limit = 4
	}
	var b strings.Builder
	b.WriteString(`<div class="section"><h2>` + htmlEscape(title) + `</h2><table><thead><tr><th>卡池</th><th>UP</th><th>抽数</th><th>六星</th><th>UP</th><th>平均出货</th><th>UP平均</th></tr></thead><tbody>`)
	for index := 0; index < limit; index++ {
		row := rows[index]
		upNames := row.UpCharNames
		if len(upNames) == 0 {
			upNames = row.UpWeaponNames
		}
		if len(upNames) == 0 && banner != nil {
			upNames = gachaUPNamesForPool(banner, row.PoolName, group)
		}
		upText := "—"
		if len(upNames) > 0 {
			upText = strings.Join(upNames, "、")
		}
		b.WriteString(`<tr><td>` + htmlEscape(row.PoolName) + `</td><td>` + htmlEscape(upText) + `</td><td>` +
			gachaFormatNum(row.TotalPulls) + `</td><td>` + gachaFormatNum(row.Star6Count) + `</td><td>` +
			gachaFormatNum(row.UpCount) + `</td><td>` + gachaFormatPity(row.Star6AvgPity) +
			`</td><td>` + gachaFormatPity(row.UpAvgPity) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func gachaFormatSyncTime(cached bool, raw string) string {
	timeText := ""
	if raw != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				timeText = parsed.Format("2006-01-02 15:04")
				break
			}
		}
		if timeText == "" {
			timeText = raw
		}
	}
	if cached && timeText != "" {
		return "缓存 · " + timeText
	}
	if cached {
		return "缓存数据"
	}
	if timeText != "" {
		return "最新 · " + timeText
	}
	return "最新数据"
}

// ---------- 展示辅助 ----------

func gachaStatsDataUser(stats *gachaStatsData) gachaUserInfo {
	if stats == nil {
		return gachaUserInfo{}
	}
	return stats.UserInfo
}

func gachaUserDisplayName(user gachaUserInfo) string {
	if strings.TrimSpace(user.Nickname) != "" {
		return strings.TrimSpace(user.Nickname)
	}
	if strings.TrimSpace(user.GameUID) != "" {
		return strings.TrimSpace(user.GameUID)
	}
	return getMessage("common.unknown")
}

func gachaMetric(label, value, note string) string {
	html := `<div class="card"><div class="k">` + htmlEscape(label) + `</div><div class="v">` + htmlEscape(value) + `</div>`
	if note != "" {
		html += `<div class="note">` + htmlEscape(note) + `</div>`
	}
	return html + `</div>`
}

func gachaPercent(part, total int) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(total)*100)
}

func gachaPercentFloat(part, total float64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.2f%%", part/total*100)
}

func gachaFormatPity(value float64) string {
	if value <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", value)
}

func gachaFormatNum(value float64) string {
	number := int64(math.Round(value))
	text := strconv.FormatInt(number, 10)
	if number < 0 {
		return text
	}
	parts := make([]string, 0, len(text)/3+1)
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return strings.Join(parts, ",")
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

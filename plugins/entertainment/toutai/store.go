package toutai

import (
	"context"
	"sort"
	"time"

	"shizukubot/sdk"
)

const (
	recordKeyPrefix = "toutai:record:"
	usersIndexKey   = "toutai:users"
)

func recordKey(userID string) string { return recordKeyPrefix + userID }

// loadRecord 读取一个用户的命簿；不存在时返回零值。
func loadRecord(ctx context.Context, st sdk.State, userID string) (record, bool, error) {
	var rec record
	raw, err := st.Get(ctx, recordKey(userID))
	if err != nil {
		return rec, false, err
	}
	if len(raw) == 0 {
		return rec, false, nil
	}
	if err := sdk.LoadJSON(ctx, st, recordKey(userID), &rec); err != nil {
		return rec, false, err
	}
	return rec, true, nil
}

func saveRecord(ctx context.Context, st sdk.State, rec record) error {
	return sdk.SaveJSON(ctx, st, recordKey(rec.UserID), rec)
}

// listRecords 读取全部玩家的命簿。State 无法枚举，因此维护一个用户索引。
func listRecords(ctx context.Context, st sdk.State) ([]record, error) {
	var ids []string
	if err := sdk.LoadJSON(ctx, st, usersIndexKey, &ids); err != nil {
		return nil, err
	}
	out := make([]record, 0, len(ids))
	for _, id := range ids {
		rec, ok, err := loadRecord(ctx, st, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func rememberUser(ctx context.Context, st sdk.State, userID string) error {
	if userID == "" {
		return nil
	}
	var ids []string
	if err := sdk.LoadJSON(ctx, st, usersIndexKey, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		if id == userID {
			return nil
		}
	}
	ids = append(ids, userID)
	return sdk.SaveJSON(ctx, st, usersIndexKey, ids)
}

// rankAmong 在全体玩家中求名次：比自己高的人数 + 1，同分并列；0 分不上榜。
func rankAmong(records []record, userID string, pick func(record) int) int {
	var self *record
	for i := range records {
		if records[i].UserID == userID {
			self = &records[i]
			break
		}
	}
	if self == nil {
		return -1
	}
	own := pick(*self)
	if own <= 0 {
		return -1
	}
	better := 0
	for _, rec := range records {
		if pick(rec) > own {
			better++
		}
	}
	return better + 1
}

// rankRow 是排行榜的一行；rank 为并列名次。
type rankRow struct {
	UserID   string
	Username string
	Value    int
	Rank     int
}

// rankRows 计分、过滤 0 分并按降序排列，并列同名次（1、1、3）。
func rankRows(records []record, pick func(record) int) []rankRow {
	rows := make([]rankRow, 0, len(records))
	for _, rec := range records {
		value := pick(rec)
		if value <= 0 {
			continue
		}
		rows = append(rows, rankRow{UserID: rec.UserID, Username: rec.Username, Value: value})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Value > rows[j].Value })
	rank := 0
	for i := range rows {
		if i == 0 || rows[i].Value != rows[i-1].Value {
			rank = i + 1
		}
		rows[i].Rank = rank
	}
	return rows
}

// displayLimit 计算这一张榜列多少名：0 表示全部（受上限约束）。
func displayLimit(requested, fallback, max int) int {
	n := requested
	if n <= 0 {
		n = fallback
	}
	if n <= 0 {
		if max > 0 {
			return max
		}
		return 1 << 30
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

// favorite 返回出现次数最多的一项。
type favorite struct {
	Name  string
	Count int
}

func mostFrequent(counts map[string]int) favorite {
	best := favorite{}
	for name, count := range counts {
		if count > best.Count {
			best = favorite{Name: name, Count: count}
		}
	}
	return best
}

// nameCount 是「名称 → 次数」的一行。
type nameCount struct {
	name  string
	count int
}

func sortNameCounts(items []nameCount) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].count > items[j].count })
}

type chinaAnalysis struct {
	totalCount      int
	orderCounts     map[string]int
	genderCounts    map[string]int
	categoryCounts  map[string]int
	provinceCounts  map[string]int
	uniqueProvinces int
	favourite       favorite
}

func analyzeChina(results []birthResultInChina) chinaAnalysis {
	analysis := chinaAnalysis{
		totalCount:     len(results),
		orderCounts:    map[string]int{"一": 0, "二": 0, "三": 0, "四": 0, "五及以上": 0},
		genderCounts:   map[string]int{"male": 0, "female": 0},
		categoryCounts: map[string]int{"城镇": 0, "城市": 0, "乡村": 0},
		provinceCounts: map[string]int{},
	}
	for _, result := range results {
		analysis.provinceCounts[result.Province]++
		switch result.Order {
		case "一", "二", "三", "四":
			analysis.orderCounts[result.Order]++
		default:
			analysis.orderCounts["五及以上"]++
		}
		if result.Gender == "male" {
			analysis.genderCounts["male"]++
		} else {
			analysis.genderCounts["female"]++
		}
		analysis.categoryCounts[result.Category]++
	}
	analysis.uniqueProvinces = len(analysis.provinceCounts)
	analysis.favourite = mostFrequent(analysis.provinceCounts)
	return analysis
}

type worldAnalysis struct {
	totalCount      int
	continentCounts map[string]int
	countryCounts   map[string]int
	uniqueCountries int
	favourite       favorite
}

func analyzeWorld(results []birthResultInWorld) worldAnalysis {
	analysis := worldAnalysis{
		totalCount:      len(results),
		continentCounts: map[string]int{},
		countryCounts:   map[string]int{},
	}
	for _, name := range continentOrder {
		analysis.continentCounts[name] = 0
	}
	for _, result := range results {
		if _, ok := analysis.continentCounts[result.DictContinent]; ok {
			analysis.continentCounts[result.DictContinent]++
		}
		analysis.countryCounts[result.DictName]++
	}
	analysis.uniqueCountries = len(analysis.countryCounts)
	analysis.favourite = mostFrequent(analysis.countryCounts)
	return analysis
}

// provinceCounts 返回从多到少的省份次数。
func provinceCounts(results []birthResultInChina) [][2]any {
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Province]++
	}
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for name, count := range counts {
		pairs = append(pairs, pair{name, count})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].count > pairs[j].count })
	out := make([][2]any, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, [2]any{p.name, p.count})
	}
	return out
}

// firstAppearance 是某省男女各自的初见次序。
type firstAppearance struct {
	Male   *int
	Female *int
}

func earliestAppearance(entry firstAppearance) int {
	best := 1 << 30
	if entry.Male != nil && *entry.Male < best {
		best = *entry.Male
	}
	if entry.Female != nil && *entry.Female < best {
		best = *entry.Female
	}
	return best
}

// provinceFirstAppearances 按初见先后排列省份。
func provinceFirstAppearances(results []birthResultInChina) []struct {
	Province string
	Entry    firstAppearance
} {
	first := map[string]*firstAppearance{}
	order := make([]string, 0, len(results))
	for _, result := range results {
		entry, ok := first[result.Province]
		if !ok {
			entry = &firstAppearance{}
			first[result.Province] = entry
			order = append(order, result.Province)
		}
		index := result.Index
		if result.Gender == "male" && entry.Male == nil {
			entry.Male = &index
		} else if result.Gender == "female" && entry.Female == nil {
			entry.Female = &index
		}
	}
	out := make([]struct {
		Province string
		Entry    firstAppearance
	}, 0, len(order))
	for _, province := range order {
		out = append(out, struct {
			Province string
			Entry    firstAppearance
		}{province, *first[province]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return earliestAppearance(out[i].Entry) < earliestAppearance(out[j].Entry)
	})
	return out
}

// cooldownRemaining 返回距离下一次投胎还需等待的秒数；未在冷却时返回 0。
func cooldownRemaining(rec record, now time.Time, cooldown int) int {
	if rec.Timestamp <= 0 || cooldown <= 0 {
		return 0
	}
	elapsed := now.Sub(time.UnixMilli(rec.Timestamp)).Seconds()
	if elapsed >= float64(cooldown) {
		return 0
	}
	return int(float64(cooldown) - elapsed)
}

package gloryofkings

// 排行榜：移植自上游 apps/rankList.js 与 utils/rankStore.js。
//
// 本仓库没有跨机器人共享库，也没有群成员列表：
//   - #排位bot排名 / #巅峰bot排名：统计本机已绑定的营地号；
//   - #排位总排名 / #巅峰总排名：统计本机快照里所有用户的历史数据（含已失效登录态）；
//   - #排位排名 / #巅峰排名：入口保留，但宿主取不到群成员，只回不可用提示。
//
// State 无法枚举，因此绑定时把「营地号 → 绑定人」写进索引，排名按索引逐个刷新。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	rankTypeRank = "rank"
	rankTypePeak = "peak"

	rankScopeBot   = "bot"
	rankScopeTotal = "total"
	rankScopeGroup = "group"

	rankSnapshotTTL = 12 * time.Hour
	rankMaxRows     = 30

	rankIndexKey    = "gok:rank:index"
	rankSnapshotKey = "gok:rank:snapshot"
)

var (
	rankMu      sync.Mutex
	rankNowFunc = time.Now

	// fetchRankProfile 单独抽成包级变量，便于测试替换成离线数据。
	fetchRankProfile = func(ctx context.Context, campID string, a *campAuth) (*profileData, error) {
		return newCampClient().getProfile(ctx, campID, a)
	}

	rankGlobalRe = rankCommandRe("总排名")
	rankBotRe    = rankCommandRe("bot排名")
	rankGroupRe  = rankCommandRe("排名")
)

// rankCommandRe 生成 `(排位|巅峰)xxx\s*(刷新)?` 形式的整句正则。
func rankCommandRe(suffix string) *regexp.Regexp {
	return regexp.MustCompile(`^(排位|巅峰)` + suffix + `\s*(刷新)?$`)
}

// rankCommand 是「营地号 → 绑定人」的索引，用于遍历本机全部绑定。
type rankIndex map[string]string

// rankEntry 是一个营地号的排名快照。
type rankEntry struct {
	CampID    string `json:"campId"`
	Owner     string `json:"owner,omitempty"`
	RoleName  string `json:"roleName"`
	RankName  string `json:"rankName,omitempty"`
	RankStar  int    `json:"rankStar,omitempty"`
	RankSort  int    `json:"rankSort,omitempty"`
	PeakScore int    `json:"peakScore,omitempty"`
	PeakDesc  string `json:"peakDesc,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

type rankSnapshot struct {
	UpdatedAt int64                `json:"updatedAt"`
	Entries   map[string]rankEntry `json:"entries"`
}

/* ------------------------------------------------------------------ *
 * 存储
 * ------------------------------------------------------------------ */

func loadRankIndex(ctx context.Context, st sdk.State) rankIndex {
	index := rankIndex{}
	_ = sdk.LoadJSON(ctx, st, rankIndexKey, &index)
	if index == nil {
		index = rankIndex{}
	}
	return index
}

func saveRankIndex(ctx context.Context, st sdk.State, index rankIndex) error {
	return sdk.SaveJSON(ctx, st, rankIndexKey, index)
}

func loadRankSnapshot(ctx context.Context, st sdk.State) *rankSnapshot {
	snapshot := &rankSnapshot{}
	_ = sdk.LoadJSON(ctx, st, rankSnapshotKey, snapshot)
	if snapshot.Entries == nil {
		snapshot.Entries = map[string]rankEntry{}
	}
	return snapshot
}

func saveRankSnapshot(ctx context.Context, st sdk.State, snapshot *rankSnapshot) error {
	return sdk.SaveJSON(ctx, st, rankSnapshotKey, snapshot)
}

// registerRankAccounts 把用户的营地号补进本机索引；已有记录不覆盖绑定人。
func registerRankAccounts(ctx context.Context, st sdk.State, owner string, campIDs []string) {
	if st == nil || owner == "" || len(campIDs) == 0 {
		return
	}
	index := loadRankIndex(ctx, st)
	changed := false
	for _, campID := range campIDs {
		if campID == "" {
			continue
		}
		if _, ok := index[campID]; !ok {
			index[campID] = owner
			changed = true
		}
	}
	if changed {
		_ = saveRankIndex(ctx, st, index)
	}
}

/* ------------------------------------------------------------------ *
 * 段位与快照
 * ------------------------------------------------------------------ */

// rankTierWeight 对应上游 TIER_WEIGHT；王者段统一用 KING_WEIGHT。
var rankTierWeight = []struct {
	Key    string
	Weight int
}{
	{"倔强青铜", 1},
	{"秩序白银", 2},
	{"荣耀黄金", 3},
	{"尊贵铂金", 4},
	{"永恒钻石", 5},
	{"至尊星耀", 6},
}

var rankRoman = map[string]int{"I": 1, "II": 2, "III": 3, "IV": 4, "V": 5}

// calcRankSort 把段位名 + 段内星数换算成跨段单调的排序键，
// 公式与上游一致：大段权重 × 100000 + 小段偏移 × 1000 + 星数。
func calcRankSort(rankName string, rankStar int) int {
	name := strings.TrimSpace(rankName)
	if name == "" {
		return 0
	}
	if strings.Contains(name, "王者") {
		return 7*100000 + rankStar
	}
	for _, tier := range rankTierWeight {
		index := strings.Index(name, tier.Key)
		if index < 0 {
			continue
		}
		sub := 0
		if roman, ok := rankRoman[strings.TrimSpace(name[index+len(tier.Key):])]; ok {
			sub = 6 - roman
		}
		return tier.Weight*100000 + sub*1000 + rankStar
	}
	return 0
}

// rankEntryFromProfile 从营地主页的 head.mods 里抽 701（排位）与 702（巅峰）。
func rankEntryFromProfile(campID, owner string, data *profileData, now int64) rankEntry {
	entry := rankEntry{CampID: campID, Owner: owner, UpdatedAt: now}
	if data == nil {
		return entry
	}
	if role := profileMainRole(data); role != nil {
		entry.RoleName = cleanRoleName(role.RoleName)
	}
	for _, mod := range data.Head.Mods {
		switch mod.ModID.Int() {
		case 701:
			entry.RankName = strings.TrimSpace(mod.Name)
			var param struct {
				RankingStar scalarValue `json:"rankingStar"`
			}
			_ = json.Unmarshal([]byte(mod.Param1), &param)
			entry.RankStar = param.RankingStar.Int()
			entry.RankSort = calcRankSort(entry.RankName, entry.RankStar)
		case 702:
			entry.PeakScore = intFromText(mod.Content)
			if entry.PeakScore <= 0 {
				var param struct {
					Desc string `json:"desc"`
				}
				_ = json.Unmarshal([]byte(mod.Param1), &param)
				entry.PeakDesc = strings.TrimSpace(param.Desc)
			}
		}
	}
	return entry
}

func intFromText(text string) int {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0
	}
	return value
}

// rankNeedsRefresh 判断是否要请求营地：缺快照或超过 12 小时；刷新指令强制。
func rankNeedsRefresh(snapshot *rankSnapshot, index rankIndex, force bool, now time.Time) bool {
	if force {
		return true
	}
	for campID := range index {
		entry, ok := snapshot.Entries[campID]
		if !ok || now.Unix()-entry.UpdatedAt >= int64(rankSnapshotTTL/time.Second) {
			return true
		}
	}
	return false
}

// collectRankSnapshot 按索引刷新快照：请求失败或没有登录态时沿用旧数据。
func collectRankSnapshot(ctx context.Context, st sdk.State, index rankIndex, force bool, now time.Time) *rankSnapshot {
	snapshot := loadRankSnapshot(ctx, st)
	for campID, owner := range index {
		old, hasOld := snapshot.Entries[campID]
		if !force && hasOld && now.Unix()-old.UpdatedAt < int64(rankSnapshotTTL/time.Second) {
			continue
		}

		ownerState := loadUserState(ctx, st, owner)
		cred := ownerState.bestCredential(campID)
		if cred == nil {
			continue
		}
		profile, err := fetchRankProfile(ctx, campID, cred.auth())
		if err != nil {
			continue
		}
		entry := rankEntryFromProfile(campID, owner, profile, now.Unix())
		if entry.RoleName == "" {
			entry.RoleName = cleanRoleName(ownerState.Names[campID])
		}
		snapshot.Entries[campID] = entry
	}
	snapshot.UpdatedAt = now.Unix()
	_ = saveRankSnapshot(ctx, st, snapshot)
	return snapshot
}

/* ------------------------------------------------------------------ *
 * 指令
 * ------------------------------------------------------------------ */

// parseRankCommand 识别五个排名指令；刷新后缀可省略。
func parseRankCommand(body string) (typ string, scope string, force bool, ok bool) {
	patterns := []struct {
		re    *regexp.Regexp
		scope string
	}{
		{rankGlobalRe, rankScopeTotal},
		{rankBotRe, rankScopeBot},
		{rankGroupRe, rankScopeGroup},
	}
	for _, pattern := range patterns {
		matched := pattern.re.FindStringSubmatch(body)
		if matched == nil {
			continue
		}
		typ = rankTypeRank
		if matched[1] == "巅峰" {
			typ = rankTypePeak
		}
		return typ, pattern.scope, len(matched) > 2 && matched[2] == "刷新", true
	}
	return "", "", false, false
}

func rankCommand(ctx context.Context, c sdk.Call, typ, scope string, force bool) error {
	if scope == rankScopeGroup {
		if c.Event.GroupID == "" {
			return c.Reply(ctx, "本群排名只能在群里使用，私聊请用 #排位bot排名 / #排位总排名")
		}
		return c.Reply(ctx, "当前平台无法获取群成员列表，群内排名暂不可用\n可以改用 #排位bot排名 / #排位总排名")
	}

	state := loadUserState(ctx, c.State, c.Event.UserID)

	rankMu.Lock()
	defer rankMu.Unlock()

	registerRankAccounts(ctx, c.State, c.Event.UserID, state.IDs)
	index := loadRankIndex(ctx, c.State)
	snapshot := loadRankSnapshot(ctx, c.State)
	if len(index) == 0 && len(snapshot.Entries) == 0 {
		return c.Reply(ctx, "还没有人绑定营地ID，先发送 #绑定营地 [营地ID] 加入排名吧")
	}

	now := rankNowFunc()
	if rankNeedsRefresh(snapshot, index, force, now) {
		_ = c.Reply(ctx, "正在更新数据，请稍候...")
	}
	snapshot = collectRankSnapshot(ctx, c.State, index, force, now)

	entries := make([]rankEntry, 0, len(snapshot.Entries))
	if scope == rankScopeBot {
		for campID := range index {
			if entry, ok := snapshot.Entries[campID]; ok {
				entries = append(entries, entry)
			}
		}
	} else {
		for _, entry := range snapshot.Entries {
			entries = append(entries, entry)
		}
	}

	rows := buildRankRows(entries, typ)
	label := rankScopeLabel(scope)
	if len(rows) == 0 {
		if typ == rankTypePeak {
			return c.Reply(ctx, label+"暂无巅峰分数据，可能大家都还没打巅峰赛")
		}
		return c.Reply(ctx, label+"暂无排位数据，请稍后再试")
	}
	return c.Reply(ctx, formatRankText(typ, scope, rows, snapshot.UpdatedAt))
}

// buildRankRows 过滤无数据账号并按维度降序排列。
func buildRankRows(entries []rankEntry, typ string) []rankEntry {
	rows := make([]rankEntry, 0, len(entries))
	for _, entry := range entries {
		if typ == rankTypePeak {
			if entry.PeakScore > 0 {
				rows = append(rows, entry)
			}
			continue
		}
		if entry.RankSort > 0 {
			rows = append(rows, entry)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if typ == rankTypePeak {
			return rows[i].PeakScore > rows[j].PeakScore
		}
		return rows[i].RankSort > rows[j].RankSort
	})
	return rows
}

func rankScopeLabel(scope string) string {
	if scope == rankScopeTotal {
		return "全库"
	}
	return "本机"
}

func rankTitle(typ, scope string) string {
	metric := "排位"
	if typ == rankTypePeak {
		metric = "巅峰分"
	}
	return rankScopeLabel(scope) + metric + "排行榜"
}

func rankValue(entry rankEntry, typ string) string {
	if typ == rankTypePeak {
		return fmt.Sprintf("%d 分", entry.PeakScore)
	}
	return fmt.Sprintf("%s %d星", entry.RankName, entry.RankStar)
}

func rankRefreshCommand(typ, scope string) string {
	metric := "排位"
	if typ == rankTypePeak {
		metric = "巅峰"
	}
	if scope == rankScopeTotal {
		return "#" + metric + "总排名 刷新"
	}
	return "#" + metric + "bot排名 刷新"
}

func formatRankText(typ, scope string, rows []rankEntry, updatedAt int64) string {
	shown := rows
	if len(shown) > rankMaxRows {
		shown = shown[:rankMaxRows]
	}

	lines := []string{
		rankTitle(typ, scope),
		fmt.Sprintf("上榜 %d 人 · 展示前 %d 名", len(rows), len(shown)),
	}
	for index, entry := range shown {
		name := entry.RoleName
		if name == "" {
			name = "无名召唤师"
		}
		lines = append(lines, fmt.Sprintf("%d. %s - %s", index+1, name, rankValue(entry, typ)))
	}
	if len(rows) > len(shown) {
		lines = append(lines, fmt.Sprintf("……共 %d 人", len(rows)))
	}
	lines = append(lines,
		"",
		"数据来源：王者营地 · 仅统计已绑定营地ID的用户 · 发送 "+rankRefreshCommand(typ, scope)+" 可强制更新",
	)
	if updatedAt > 0 {
		lines = append(lines, "更新于 "+time.Unix(updatedAt, 0).Format("2006-01-02 15:04"))
	}
	return strings.Join(lines, "\n")
}

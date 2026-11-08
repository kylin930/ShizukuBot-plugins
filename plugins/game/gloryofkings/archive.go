package gloryofkings

// 战绩归档：把每次拉到的战绩按营地号落盘，供 #巅峰趋势 / #段位趋势 取数。
// 口径与上游 utils/battleArchive.js 一致：按 gameSeq 去重、按时间倒序、
// 只保留最近 35 天。受框架 State 单值 64KiB 限制，每个营地号最多留 300 场
// （重度玩家约 10 天，休闲玩家一个半月以上），对趋势图足够。

import (
	"context"
	"sort"
	"time"

	"shizukubot/sdk"
)

const (
	archiveKeepDays  = 35
	archiveMaxBattle = 300
)

// archiveBattle 是落盘的最小字段集（对应上游 KEEP_FIELDS）。
type archiveBattle struct {
	GameSeq             string `json:"gameSeq"`
	Time                int64  `json:"t"`
	Result              int    `json:"r"`
	HeroID              string `json:"h"`
	GradeGame           string `json:"g,omitempty"`
	MvpCnt              int    `json:"mvp,omitempty"`
	LoseMvp             int    `json:"lmvp,omitempty"`
	MapName             string `json:"m"`
	UsedTime            int    `json:"u,omitempty"`
	Kill                int    `json:"k"`
	Dead                int    `json:"d"`
	Assist              int    `json:"a"`
	RoleJobName         string `json:"rjn,omitempty"`
	RoleJob             int    `json:"rj,omitempty"`
	Stars               int    `json:"st,omitempty"`
	OldMasterMatchScore int    `json:"oms,omitempty"`
	NewMasterMatchScore int    `json:"nms,omitempty"`
	Desc                string `json:"desc,omitempty"`
}

type campArchive struct {
	UpdatedAt     int64           `json:"updatedAt"`
	OldestFetched int64           `json:"oldestFetched,omitempty"`
	Battles       []archiveBattle `json:"battles"`
}

func archiveKey(campID string) string { return "gok:archive:" + campID }

func loadCampArchive(ctx context.Context, st sdk.State, campID string) *campArchive {
	archive := &campArchive{}
	_ = sdk.LoadJSON(ctx, st, archiveKey(campID), archive)
	return archive
}

func saveCampArchive(ctx context.Context, st sdk.State, campID string, archive *campArchive) error {
	return sdk.SaveJSON(ctx, st, archiveKey(campID), archive)
}

func slimBattle(item battleItem) archiveBattle {
	return archiveBattle{
		GameSeq:             item.GameSeq.String(),
		Time:                item.eventTime(),
		Result:              item.GameResult,
		HeroID:              item.HeroID.String(),
		GradeGame:           item.GradeGame.String(),
		MvpCnt:              item.MvpCnt.Int(),
		LoseMvp:             item.LoseMvp.Int(),
		MapName:             item.MapName,
		UsedTime:            item.UsedTime,
		Kill:                item.KillCnt,
		Dead:                item.DeadCnt,
		Assist:              item.AssistCnt,
		RoleJobName:         item.RoleJobName,
		RoleJob:             item.RoleJob.Int(),
		Stars:               item.Stars.Int(),
		OldMasterMatchScore: item.OldMasterMatchScore.Int(),
		NewMasterMatchScore: item.NewMasterMatchScore.Int(),
		Desc:                item.Desc,
	}
}

// merge 把一页战绩并入驻地号的归档：按 gameSeq 去重、保持倒序、裁剪窗口。
func (a *campArchive) merge(items []battleItem, now time.Time) {
	seen := make(map[string]struct{}, len(a.Battles)+len(items))
	for _, battle := range a.Battles {
		seen[battle.GameSeq] = struct{}{}
	}
	cutoff := now.Unix() - int64(archiveKeepDays)*86400
	for _, item := range items {
		slim := slimBattle(item)
		if slim.GameSeq == "" || slim.Time <= 0 {
			continue
		}
		if _, dup := seen[slim.GameSeq]; dup {
			continue
		}
		if slim.Time < cutoff {
			continue
		}
		seen[slim.GameSeq] = struct{}{}
		a.Battles = append(a.Battles, slim)
	}
	kept := a.Battles[:0]
	for _, battle := range a.Battles {
		if battle.Time >= cutoff {
			kept = append(kept, battle)
		}
	}
	a.Battles = kept
	sort.Slice(a.Battles, func(i, j int) bool { return a.Battles[i].Time > a.Battles[j].Time })
	if len(a.Battles) > archiveMaxBattle {
		a.Battles = a.Battles[:archiveMaxBattle]
	}
	a.UpdatedAt = now.Unix()
}

// markOldest 推进「确认翻页到过的最早时刻」水位，只往更早走。
func (a *campArchive) markOldest(oldest int64) {
	if oldest <= 0 {
		return
	}
	if a.OldestFetched == 0 || oldest < a.OldestFetched {
		a.OldestFetched = oldest
	}
}

// archiveBattles 是「查战绩顺手归档」的便捷入口（对应上游挂在 fetchLatest 的做法）。
func archiveBattles(ctx context.Context, st sdk.State, campID string, items []battleItem) {
	if campID == "" || len(items) == 0 {
		return
	}
	archive := loadCampArchive(ctx, st, campID)
	archive.merge(items, time.Now())
	_ = saveCampArchive(ctx, st, campID, archive)
}

// collectResult 是一次翻页补拉的结果。
type collectResult struct {
	Battles   []archiveBattle // 倒序、已按 from 过滤
	Fetched   int
	Truncated bool
	Failed    bool
}

// collectBattles 从营地翻页拉取 fromSec（秒）以来的战绩并归档。
// 对应上游 collectBattles(campId, qq, fromSec, {maxPages:8})。
func collectBattles(ctx context.Context, st sdk.State, campID string, a *campAuth, fromSec int64, maxPages int) (*collectResult, error) {
	if maxPages <= 0 {
		maxPages = 8
	}
	now := time.Now()
	result := &collectResult{}

	archive := loadCampArchive(ctx, st, campID)
	lastTime := "0"
	var lastSeen int64
	for page := 0; page < maxPages; page++ {
		data, err := newCampClient().getMoreBattleListPage(ctx, campID, a, 0, lastTime)
		if err != nil {
			result.Failed = true
			break
		}
		if len(data.List) == 0 {
			break
		}
		result.Fetched += len(data.List)
		archive.merge(data.List, now)
		lastSeen = data.List[len(data.List)-1].eventTime()
		if lastSeen > 0 && lastSeen <= fromSec {
			break // 本页末场已越过目标起点，够用了
		}
		if !data.HasMore || data.LastTime.String() == "" || data.LastTime.String() == lastTime {
			break
		}
		lastTime = data.LastTime.String()
		if page == maxPages-1 {
			result.Truncated = true
		}
	}
	if lastSeen > 0 {
		archive.markOldest(lastSeen)
	}
	if err := saveCampArchive(ctx, st, campID, archive); err != nil {
		return result, err
	}

	for _, battle := range archive.Battles {
		if battle.Time < fromSec {
			continue
		}
		result.Battles = append(result.Battles, battle)
	}
	return result, nil
}

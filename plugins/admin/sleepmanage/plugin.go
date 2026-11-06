// Package sleepmanage 睡眠小助手：记录群友的晚安/早安时间。
package sleepmanage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"shizukubot/sdk"
)

// nowFunc 便于测试替换时间源。
var nowFunc = time.Now

// mu 保护同一实例内的睡眠记录读改写。
var mu sync.Mutex

type records struct {
	Users map[string]int64 `json:"users"`
}

// Definition subscribes to the 早安/晚安 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "sleepmanage",
		Name:        "睡眠小助手",
		Version:     "1.0.0",
		Description: "发送「晚安」记录入睡时间，发送「早安」记录起床时间与睡眠时长",
	}, sdk.MessageEvents, Handle, sdk.WithPermissions("state.read", "state.write"))
}

// Handle only replies inside the original time windows: 早安 6:00-12:00,
// 晚安 21:00-次日 3:00。私聊消息不参与统计。
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := sdk.Text(c.Event)
	now := nowFunc()
	switch {
	case sdk.MatchFull(text, "早安"):
		if now.Hour() < 6 || now.Hour() > 12 {
			return nil
		}
		return act(ctx, c, now, false)
	case sdk.MatchFull(text, "晚安"):
		if now.Hour() > 3 && now.Hour() < 21 {
			return nil
		}
		return act(ctx, c, now, true)
	}
	return nil
}

func act(ctx context.Context, c sdk.Call, now time.Time, sleep bool) error {
	key := "sleep:" + c.Event.GroupID
	start := morningStart(now)
	if sleep {
		start = eveningStart(now)
	}
	mu.Lock()
	rec := loadRecords(ctx, c, key)
	if rec.Users == nil {
		rec.Users = map[string]int64{}
	}
	prev, existed := rec.Users[c.Event.UserID]
	rec.Users[c.Event.UserID] = now.Unix()
	_ = sdk.SaveJSON(ctx, c.State, key, rec)
	position := countPosition(rec, start, now)
	mu.Unlock()

	var elapsed time.Duration
	if existed {
		elapsed = now.Sub(time.Unix(prev, 0))
	}
	return c.Reply(ctx, message(sleep, position, elapsed))
}

func loadRecords(ctx context.Context, c sdk.Call, key string) records {
	rec := records{}
	_ = sdk.LoadJSON(ctx, c.State, key, &rec)
	return rec
}

func countPosition(rec records, start, now time.Time) int {
	position := 0
	for _, t := range rec.Users {
		if t >= start.Unix() && t <= now.Unix() {
			position++
		}
	}
	return position
}

// morningStart 早安统计当天 6:00 之后的数据。
func morningStart(now time.Time) time.Time {
	loc := now.Location()
	return time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, loc)
}

// eveningStart 晚安统计当天 21:00（凌晨则从前一天算起）之后的数据。
func eveningStart(now time.Time) time.Time {
	loc := now.Location()
	if now.Hour() >= 21 {
		return time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, loc)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, loc).AddDate(0, 0, -1)
}

func message(sleep bool, position int, elapsed time.Duration) string {
	h, m, s := hms(elapsed)
	if !sleep {
		if elapsed <= 0 || elapsed >= 24*time.Hour {
			return fmt.Sprintf("早安成功！你是今天第%d个起床的", position)
		}
		return fmt.Sprintf("早安成功！你的睡眠时长为%d时%d分%d秒,你是今天第%d个起床的", h, m, s, position)
	}
	if elapsed <= 0 || elapsed >= 24*time.Hour {
		return fmt.Sprintf("晚安成功！你是今天第%d个睡觉的", position)
	}
	return fmt.Sprintf("晚安成功！你的清醒时长为%d时%d分%d秒,你是今天第%d个睡觉的", h, m, s, position)
}

func hms(d time.Duration) (hour, minute, second int64) {
	total := int64(d / time.Second)
	hour = total / 3600
	minute = (total % 3600) / 60
	second = total % 60
	return hour, minute, second
}

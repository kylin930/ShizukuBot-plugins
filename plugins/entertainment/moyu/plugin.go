// Package moyu 摸鱼提醒：返回当天的摸鱼文案、周末与节日倒计时。
package moyu

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// nowFunc 便于测试替换时间源。
var nowFunc = time.Now

// holiday 是一个节日的起始日期与假期天数。
type holiday struct {
	Name string
	Date time.Time
	Days int
}

// holidayTable 按节日名保存 2025–2027 年的日期与假期天数，避免运行时网络依赖。
// 日期为节日当天，Days 为假期长度，用于「好好享受假期」判断。
var holidayTable = map[string][]holiday{
	"元旦": {
		{"元旦", time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), 3},
		{"元旦", time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local), 3},
		{"元旦", time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local), 3},
	},
	"春节": {
		{"春节", time.Date(2025, 1, 29, 0, 0, 0, 0, time.Local), 7},
		{"春节", time.Date(2026, 2, 17, 0, 0, 0, 0, time.Local), 7},
		{"春节", time.Date(2027, 2, 6, 0, 0, 0, 0, time.Local), 7},
	},
	"清明节": {
		{"清明节", time.Date(2025, 4, 4, 0, 0, 0, 0, time.Local), 3},
		{"清明节", time.Date(2026, 4, 5, 0, 0, 0, 0, time.Local), 3},
		{"清明节", time.Date(2027, 4, 5, 0, 0, 0, 0, time.Local), 3},
	},
	"劳动节": {
		{"劳动节", time.Date(2025, 5, 1, 0, 0, 0, 0, time.Local), 5},
		{"劳动节", time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local), 5},
		{"劳动节", time.Date(2027, 5, 1, 0, 0, 0, 0, time.Local), 5},
	},
	"端午节": {
		{"端午节", time.Date(2025, 5, 31, 0, 0, 0, 0, time.Local), 3},
		{"端午节", time.Date(2026, 6, 19, 0, 0, 0, 0, time.Local), 3},
		{"端午节", time.Date(2027, 6, 9, 0, 0, 0, 0, time.Local), 3},
	},
	"中秋节": {
		{"中秋节", time.Date(2025, 10, 6, 0, 0, 0, 0, time.Local), 3},
		{"中秋节", time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local), 3},
		{"中秋节", time.Date(2027, 9, 15, 0, 0, 0, 0, time.Local), 3},
	},
	"国庆节": {
		{"国庆节", time.Date(2025, 10, 1, 0, 0, 0, 0, time.Local), 7},
		{"国庆节", time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local), 7},
		{"国庆节", time.Date(2027, 10, 1, 0, 0, 0, 0, time.Local), 7},
	},
}

// holidayOrder 与上游消息顺序保持一致。
var holidayOrder = []string{"元旦", "春节", "清明节", "劳动节", "端午节", "中秋节", "国庆节"}

// Definition subscribes to the 「摸鱼提醒」 trigger.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "moyu",
		Name:        "摸鱼提醒",
		Version:     "1.0.0",
		Description: "发送「摸鱼提醒」，返回当天的摸鱼提醒、周末与七个节日的倒计时",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "moyu", Trigger: "摸鱼提醒"},
	})
}

// Handle replies with the daily reminder and holiday countdowns.
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.MatchFull(sdk.Text(c.Event), "摸鱼提醒") {
		return nil
	}
	now := nowFunc()
	holidays := make([]string, 0, len(holidayOrder))
	for _, name := range holidayOrder {
		holidays = append(holidays, holidayText(name, now))
	}
	return c.Reply(ctx, fmt.Sprintf("%s\n上午好，摸鱼人！\n工作再累，一定不要忘记摸鱼哦！有事没事起身去茶水间，去厕所，去廊道走走别老在工位上坐着，钱是老板的,但命是自己的。\n\n%s\n\n%s\n\n上班是帮老板赚钱，摸鱼是赚老板的钱！最后，祝愿天下所有摸鱼人，都能愉快的渡过每一天…",
		now.Format("2006-01-02"), weekend(now), strings.Join(holidays, "\n")))
}

// holidayText 选取离当前时间最近的节日并输出倒计时。
func holidayText(name string, now time.Time) string {
	items := holidayTable[name]
	if len(items) == 0 {
		return "今年 " + name + " 假期已过"
	}
	chosen := items[len(items)-1]
	for _, h := range items {
		if !h.Date.Add(time.Duration(h.Days) * 24 * time.Hour).Before(now) {
			chosen = h
			break
		}
	}
	return chosen.text(now)
}

// text 对齐上游 Holiday.String 的三种状态文案。
func (h holiday) text(now time.Time) string {
	d := h.Date.Sub(now)
	switch {
	case d >= 0:
		return "距离" + h.Name + "还有: " + strconv.FormatFloat(d.Hours()/24.0, 'f', 2, 64) + "天！"
	case d+time.Duration(h.Days)*24*time.Hour >= 0:
		return "好好享受 " + h.Name + " 假期吧!"
	default:
		return "今年 " + h.Name + " 假期已过"
	}
}

func weekend(now time.Time) string {
	switch now.Weekday() {
	case time.Saturday, time.Sunday:
		return "好好享受周末吧！"
	default:
		return fmt.Sprintf("距离周末还有:%d天！", 5-int(now.Weekday()))
	}
}

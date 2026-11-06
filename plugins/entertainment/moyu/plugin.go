// Package moyu 摸鱼提醒：返回当天的摸鱼文案与周末倒计时。
package moyu

import (
	"context"
	"fmt"
	"time"

	"shizukubot/sdk"
)

// nowFunc 便于测试替换时间源。
var nowFunc = time.Now

// Definition subscribes to the 「摸鱼提醒」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "moyu",
		Name:        "摸鱼提醒",
		Version:     "1.0.0",
		Description: "发送「摸鱼提醒」，返回当天的摸鱼提醒与周末倒计时",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the daily reminder. 上游的节日倒计时依赖私有 registry
// TCP 服务，本移植不接入该服务，只保留日期与周末倒计时。
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.MatchFull(sdk.Text(c.Event), "摸鱼提醒") {
		return nil
	}
	now := nowFunc()
	return c.Reply(ctx, fmt.Sprintf("%s\n上午好，摸鱼人！\n工作再累，一定不要忘记摸鱼哦！有事没事起身去茶水间，去厕所，去廊道走走别老在工位上坐着，钱是老板的,但命是自己的。\n\n%s\n\n上班是帮老板赚钱，摸鱼是赚老板的钱！最后，祝愿天下所有摸鱼人，都能愉快的渡过每一天…",
		now.Format("2006-01-02"), weekend(now)))
}

func weekend(now time.Time) string {
	switch now.Weekday() {
	case time.Saturday, time.Sunday:
		return "好好享受周末吧！"
	default:
		return fmt.Sprintf("距离周末还有:%d天！", 5-int(now.Weekday()))
	}
}

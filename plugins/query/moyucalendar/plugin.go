// Package moyucalendar 摸鱼人日历：返回当天的摸鱼日历图片。
package moyucalendar

import (
	"context"

	"shizukubot/sdk"
)

// imageURL 与上游 vvhan 一致，测试可替换。
var imageURL = "https://api.vvhan.com/api/moyu"

// Definition subscribes to the 「摸鱼人日历」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "moyucalendar",
		Name:        "摸鱼人日历",
		Version:     "1.0.0",
		Description: "发送「摸鱼人日历」，返回当天的摸鱼日历图片",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the calendar image provided by the upstream API.
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.MatchFull(sdk.Text(c.Event), "摸鱼人日历") {
		return nil
	}
	return c.ReplyImage(ctx, imageURL)
}

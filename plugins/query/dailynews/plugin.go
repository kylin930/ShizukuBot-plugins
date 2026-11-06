// Package dailynews 今日早报：返回当天的新闻早报图片。
package dailynews

import (
	"context"

	"shizukubot/sdk"
)

// imageURL 与上游 uapis.cn 一致，测试可替换。
var imageURL = "https://uapis.cn/api/v1/daily/news-image"

// Definition subscribes to the 「今日早报」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "dailynews",
		Name:        "今日早报",
		Version:     "1.0.0",
		Description: "发送「今日早报」，返回当天的新闻早报图片",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the early-news image provided by the upstream API.
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.MatchFull(sdk.Text(c.Event), "今日早报") {
		return nil
	}
	return c.ReplyImage(ctx, imageURL)
}

// Package poker 抽扑克牌（文本版）。上游用图片牌面，这里改为文本输出，
// 避免引入 50MB+ 图片资源。
package poker

import (
	"context"
	"math/rand"
	"strconv"

	"shizukubot/sdk"
)

var suits = []string{"♠", "♥", "♣", "♦"}

// Definition subscribes to 「抽扑克」/「poker」.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "poker",
		Name:        "抽扑克牌",
		Version:     "1.0.0",
		Description: "发送「抽扑克」或「poker」，随机抽一张扑克牌",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with one random card.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if !sdk.MatchFull(text, "抽扑克") && !sdk.MatchFull(text, "poker") {
		return nil
	}
	rank := randomRank()
	suit := suits[rand.Intn(len(suits))]
	return c.Reply(ctx, "你抽到了 "+suit+rank)
}

func randomRank() string {
	switch n := rand.Intn(13); n {
	case 0:
		return "A"
	case 10:
		return "J"
	case 11:
		return "Q"
	case 12:
		return "K"
	default:
		return strconv.Itoa(n + 1)
	}
}

// Package poker 抽扑克牌（图片版）。牌面图片来自 FloatTech/zbpdata 的 Poker 目录，
// 已压缩到 600 像素宽并内嵌，总体积约 1.3 MB。
package poker

import (
	"context"
	"embed"
	"math/rand"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed cards/*.jpg
var cards embed.FS

// pick 可在测试中替换随机源。
var pick = rand.Intn

// playingCard 是一张可抽取的牌；上游卡池同时包含 52 张常规牌与
// Lynette/Lyney 两张特殊牌，因此特殊牌同样按等概率被选中。
type playingCard struct {
	label string
	file  string
}

var cardDeck = func() []playingCard {
	suits := []struct {
		symbol string
		file   string
	}{
		{"♠", "spade"},
		{"♥", "heart"},
		{"♣", "club"},
		{"♦", "diamond"},
	}
	ranks := []string{"A", "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K"}
	deck := make([]playingCard, 0, len(suits)*len(ranks)+2)
	for _, suit := range suits {
		for _, rank := range ranks {
			deck = append(deck, playingCard{label: suit.symbol + rank, file: rank + suit.file})
		}
	}
	return append(deck,
		playingCard{label: "Lynette", file: "Lynette"},
		playingCard{label: "Lyney", file: "Lyney"},
	)
}()

// Definition subscribes to 「抽扑克」/「poker」.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "poker",
		Name:        "抽扑克牌",
		Version:     "1.0.0",
		Description: "发送「抽扑克」或「poker」，随机抽一张扑克牌并返回牌面图片",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "poker", Trigger: "poker"}})
}

// Handle replies with one random card image.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if !sdk.MatchFull(text, "抽扑克") && !sdk.MatchFull(text, "poker") {
		return nil
	}
	card := cardDeck[pick(len(cardDeck))]
	text = "你抽到了 " + card.label
	data, err := cards.ReadFile("cards/" + card.file + ".jpg")
	if err != nil {
		return c.ReplyKeyboardOrText(ctx, text, drawKeyboard())
	}
	// 图片消息不能带键盘，先发一条带「再抽一张」的 Markdown，再发牌面图。
	if err := c.ReplyKeyboardOrText(ctx, text, drawKeyboard()); err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, data)
}

// drawKeyboard 抽牌结果下方的快捷按钮。
func drawKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("poker-draw", "再抽一张", "抽扑克"),
	))
}

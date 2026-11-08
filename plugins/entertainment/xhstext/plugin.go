// Package xhstext 小红书式捧场与有梗回复。
package xhstext

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed pengchang.json
var pengchangJSON []byte

//go:embed yougeng.json
var yougengJSON []byte

var (
	pengchang []string
	yougeng   []string
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(pengchangJSON, &pengchang); err != nil {
		panic("xhstext: invalid pengchang data: " + err.Error())
	}
	if err := json.Unmarshal(yougengJSON, &yougeng); err != nil {
		panic("xhstext: invalid yougeng data: " + err.Error())
	}
}

// Definition subscribes to the 「捧场」「有梗」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "xhstext",
		Name:        "捧场与接梗",
		Version:     "1.0.0",
		Description: "发送「捧场」得到一句捧场话术，发送「有梗」得到一句接梗点评",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "xhstext", Trigger: "捧场"},
	})
}

// Handle 处理两种话术。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "捧场"):
		return c.ReplyKeyboardOrText(ctx, pengchang[pickIndex(len(pengchang))], moreKeyboard("捧场", "再捧一次"))
	case sdk.MatchFull(text, "有梗"):
		return c.ReplyKeyboardOrText(ctx, yougeng[pickIndex(len(yougeng))], moreKeyboard("有梗", "再接一个"))
	}
	return nil
}

// moreKeyboard 「再来一句」按钮。
func moreKeyboard(command, label string) sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("xhstext-more", label, command)))
}

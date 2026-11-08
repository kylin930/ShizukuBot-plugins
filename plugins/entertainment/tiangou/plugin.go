// Package tiangou 舔狗日记。
package tiangou

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed diary.json
var diaryJSON []byte

//go:embed short.json
var shortJSON []byte

var (
	diaries []string
	shorts  []string
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(diaryJSON, &diaries); err != nil {
		panic("tiangou: invalid diary data: " + err.Error())
	}
	if err := json.Unmarshal(shortJSON, &shorts); err != nil {
		panic("tiangou: invalid short data: " + err.Error())
	}
}

// Definition subscribes to the 「舔狗日记」「舔狗语录」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "tiangou",
		Name:        "舔狗日记",
		Version:     "1.0.0",
		Description: "发送「舔狗日记」看一篇日记，发送「舔狗语录」看一句短句",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "tiangou", Trigger: "舔狗日记"},
	})
}

// Handle 处理两种舔狗文本。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "舔狗日记"):
		return c.ReplyKeyboardOrText(ctx, diaries[pickIndex(len(diaries))], moreKeyboard("舔狗日记", "再来一篇"))
	case sdk.MatchFull(text, "舔狗语录"):
		return c.ReplyKeyboardOrText(ctx, shorts[pickIndex(len(shorts))], moreKeyboard("舔狗语录", "再来一句"))
	}
	return nil
}

// moreKeyboard 「再来」按钮，点击后重新触发同一指令。
func moreKeyboard(command, label string) sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("tiangou-more", label, command)))
}

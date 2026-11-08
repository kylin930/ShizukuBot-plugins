// Package funny 讲个笑话与夸夸。
package funny

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed jokes.json
var jokesJSON []byte

var (
	// jokes 是笑话与夸夸共用的数据表，对应上游同一个 jokes 表。
	jokes []string
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(jokesJSON, &jokes); err != nil {
		panic("funny: invalid jokes data: " + err.Error())
	}
	if len(jokes) == 0 {
		panic("funny: jokes table is empty")
	}
}

// Definition subscribes to the 「讲个笑话」「夸夸」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "funny",
		Name:        "笑话与夸夸",
		Version:     "1.0.0",
		Description: "发送「讲个笑话」听冷笑话，发送「夸夸」听一句夸奖",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "funny", Trigger: "讲个笑话"}})
}

// Handle 处理两个前缀触发词，后续的 @、QQ 号或人名等参数一律忽略。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if _, ok := sdk.MatchPrefix(text, "讲个笑话"); ok {
		return c.ReplyKeyboardOrText(ctx, render(c, jokes[pickIndex(len(jokes))]), moreKeyboard("讲个笑话", "再来一个"))
	}
	if _, ok := sdk.MatchPrefix(text, "夸夸"); ok {
		return c.ReplyKeyboardOrText(ctx, render(c, jokes[pickIndex(len(jokes))]), moreKeyboard("夸夸", "再夸一次"))
	}
	return nil
}

// render 用发送者昵称替换模板中的 %name。
func render(c sdk.Call, tpl string) string {
	name := strings.TrimSpace(c.Event.Nickname)
	if name == "" {
		name = "你"
	}
	return strings.ReplaceAll(tpl, "%name", name)
}

// moreKeyboard 「再来一个」按钮，点击后重新触发同一条指令。
func moreKeyboard(command, label string) sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("funny-more", label, command)))
}

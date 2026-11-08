// Package dish 程序员做饭指南：按菜名查做法或随机推荐一道菜。
package dish

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed recipes.json
var recipesJSON []byte

// recipe 一道菜的名称、原材料与步骤。
type recipe struct {
	Name      string `json:"name"`
	Materials string `json:"materials"`
	Steps     string `json:"steps"`
}

var (
	recipes []recipe
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(recipesJSON, &recipes); err != nil {
		panic("dish: invalid recipes data: " + err.Error())
	}
}

// Definition subscribes to the 「怎么做」「烹饪」「随机菜谱」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "dish",
		Name:        "程序员做饭指南",
		Version:     "1.0.0",
		Description: "发送「怎么做 菜名」「烹饪 菜名」查询做法，发送「随机菜谱」随机推荐一道菜",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "dish", Trigger: "怎么做"}})
}

// Handle 处理菜名查询与随机菜谱。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "随机菜谱") || sdk.MatchFull(text, "随便做点菜") {
		return c.ReplyKeyboardOrText(ctx, randomRecipe(c), moreKeyboard())
	}
	for _, prefix := range []string{"怎么做", "烹饪"} {
		if name, ok := sdk.MatchPrefix(text, prefix); ok {
			if name = strings.TrimSpace(name); name == "" {
				return c.Reply(ctx, "用法：怎么做 可乐鸡翅")
			}
			return c.ReplyKeyboardOrText(ctx, findRecipe(c, name), moreKeyboard())
		}
	}
	return nil
}

// findRecipe 按名称模糊匹配，找不到时给出提示。
func findRecipe(c sdk.Call, name string) string {
	for _, r := range recipes {
		if strings.Contains(r.Name, name) {
			return formatRecipe(c, r, "找到", "辣！")
		}
	}
	return "客官，本店没有" + name + "，试试「随机菜谱」？"
}

// randomRecipe 随机挑一道菜。
func randomRecipe(c sdk.Call) string {
	return formatRecipe(c, recipes[pickIndex(len(recipes))], "送上", "：")
}

// formatRecipe 拼出与上游一致的「客官」称呼与结尾标志。
func formatRecipe(c sdk.Call, r recipe, action, tail string) string {
	name := strings.TrimSpace(c.Event.Nickname)
	return "已为客官" + name + action + r.Name + "的做法" + tail + "\n原材料：" + r.Materials + "\n步骤：" + r.Steps
}

// moreKeyboard 「随机菜谱」按钮。
func moreKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("dish-random", "随机菜谱", "随机菜谱")))
}

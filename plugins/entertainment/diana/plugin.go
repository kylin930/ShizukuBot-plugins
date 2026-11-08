// Package diana 小作文与发大病。
package diana

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	essaysKey = "essays"
	addPrefix = "教你一篇小作文"
	minRunes  = 8
	maxRunes  = 500
)

//go:embed essays.json
var essaysJSON []byte

//go:embed sick.json
var sickText string

var (
	essays []string
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(essaysJSON, &essays); err != nil {
		panic("diana: invalid essays data: " + err.Error())
	}
}

// Definition subscribes to 小作文 / 发大病 / 教你一篇小作文.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "diana",
		Name:        "小作文",
		Version:     "1.0.0",
		Description: "发送「小作文」看一段小作文，发送「发大病」看一段发病文学；管理员可用「教你一篇小作文 <内容>」扩充词库",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "diana", Trigger: "小作文"}},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许教小作文的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle 处理小作文相关指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "小作文"):
		return c.ReplyKeyboardOrText(ctx, pick(ctx, c), moreKeyboard())
	case sdk.MatchFull(text, "发大病"):
		return c.ReplyKeyboardOrText(ctx, strings.TrimSpace(sickText), moreKeyboard())
	}
	if rest, ok := sdk.MatchPrefix(text, addPrefix); ok {
		return add(ctx, c, rest)
	}
	return nil
}

// pick 从内置与自定义词库中随机取一篇。
func pick(ctx context.Context, c sdk.Call) string {
	pool := append([]string(nil), essays...)
	var custom []string
	_ = sdk.LoadJSON(ctx, c.State, essaysKey, &custom)
	pool = append(pool, custom...)
	return pool[pickIndex(len(pool))]
}

// add 校验管理员身份并保存自定义小作文。
func add(ctx context.Context, c sdk.Call, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	content := strings.TrimSpace(rest)
	if n := utf8.RuneCountInString(content); n < minRunes || n > maxRunes {
		return c.Reply(ctx, "小作文长度需要在 8 到 500 字之间。")
	}
	var custom []string
	_ = sdk.LoadJSON(ctx, c.State, essaysKey, &custom)
	custom = append(custom, content)
	if err := sdk.SaveJSON(ctx, c.State, essaysKey, custom); err != nil {
		return c.Reply(ctx, "保存失败："+err.Error())
	}
	return c.Reply(ctx, "记住啦!")
}

// isAdmin 判断发送者是否在 admin_users 中。
func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" || c.Config == nil {
		return false
	}
	switch v := c.Config["admin_users"].(type) {
	case []string:
		for _, id := range v {
			if id == c.Event.UserID {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if id, ok := item.(string); ok && id == c.Event.UserID {
				return true
			}
		}
	}
	return false
}

// moreKeyboard 「再来一篇」按钮。
func moreKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("diana-more", "再来一篇", "小作文")))
}

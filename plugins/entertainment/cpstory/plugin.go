// Package cpstory 生成两人的 CP 小故事。
package cpstory

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed stories.json
var storiesJSON []byte

var (
	stories []string
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

func init() {
	if err := json.Unmarshal(storiesJSON, &stories); err != nil {
		panic("cpstory: invalid stories data: " + err.Error())
	}
}

// Definition subscribes to the 「组cp」「磕cp」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "cpstory",
		Name:        "CP 故事",
		Version:     "1.0.0",
		Description: "发送「组cp 攻名 受名」或「磕cp 攻名 受名」，生成一段 CP 小故事",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "cpstory", Trigger: "组cp"}})
}

// mentionMarkup 匹配宿主可能保留的 @ 片段，避免文本回落时把它们当成名字。
var mentionMarkup = regexp.MustCompile(`<@!?[^>]+>|<qqbot-at-user[^>]*/>`)

// Handle 解析两个名字并生成故事；组cp 优先从 @ 消息中提取两个 QQ 号。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	var rest string
	prefix := ""
	groupCmd := false
	for _, p := range []string{"组cp", "磕cp", "组CP", "磕CP"} {
		if r, ok := sdk.MatchPrefix(text, p); ok {
			rest, prefix = r, p
			groupCmd = p == "组cp" || p == "组CP"
			break
		}
	}
	if prefix == "" {
		return nil
	}
	names := splitNames(mentionMarkup.ReplaceAllString(rest, " "))
	if groupCmd {
		if ids := sdk.MentionUserIDs(c.Event); len(ids) >= 2 {
			names = []string{clipName(ids[0]), clipName(ids[1])}
		}
	}
	if len(names) != 2 {
		return c.Reply(ctx, "请用空格分开两个人名")
	}
	attack, receive := names[0], names[1]
	story := render(story(), attack, receive)
	return c.ReplyKeyboardOrText(ctx, story, againKeyboard(prefix, attack, receive))
}

// story 取一条模板。
func story() string {
	return stories[pickIndex(len(stories))]
}

// splitNames 支持空格、逗号、顿号或「和」分隔。
func splitNames(s string) []string {
	s = strings.NewReplacer("，", " ", ",", " ", "、", " ", "　", " ").Replace(s)
	fields := strings.Fields(s)
	if len(fields) != 2 {
		fields = strings.Fields(strings.Replace(s, "和", " ", 1))
	}
	if len(fields) != 2 {
		return fields
	}
	return []string{clipName(fields[0]), clipName(fields[1])}
}

// clipName 限制名字长度，避免超长回复。
func clipName(name string) string {
	if r := []rune(name); len(r) > 16 {
		return string(r[:16])
	}
	return name
}

// render 把模板中的 {攻} {受} 替换为真实名字。
func render(tpl, attack, receive string) string {
	return strings.NewReplacer("{攻}", attack, "{受}", receive).Replace(tpl)
}

// againKeyboard 「再磕一次」按钮，携带同样的两个名字。
func againKeyboard(prefix, attack, receive string) sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("cpstory-more", "再磕一次", prefix+" "+attack+" "+receive)))
}

// Package antiabuse 群违禁词检测与词表维护。
package antiabuse

import (
	"context"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	addPrefix  = "添加违禁词"
	delPrefix  = "删除违禁词"
	listPrefix = "查看违禁词"
)

const banDuration = 2 * time.Minute

// defaultWords 是内嵌的小规模示例词表，检测时始终生效，且不能通过指令删除。
var defaultWords = []string{"傻逼", "操你妈", "狗日的"}

// mu 保护同一实例内的词表读改写。
var mu sync.Mutex

// Definition subscribes to group messages.
// 框架不提供群管理员身份，词表增删无法像原插件那样校验管理员权限。
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "antiabuse",
		Name:        "违禁词检测",
		Version:     "1.0.0",
		Description: "检测群消息中的违禁词，撤回并禁言 2 分钟；支持添加/删除/查看本群词表",
	}, []sdk.EventType{sdk.GroupMessage, sdk.GroupAtMessage}, Handle,
		sdk.WithPermissions("state.read", "state.write", "message.recall", "group.manage"))
}

// Handle detects abuse words before handling word-list commands.
// 命中后与上游一致：先撤回消息，再禁言 2 分钟；机器人缺少群管理员权限时降级为提醒。
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := normalize(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	if word, ok := sdk.MatchPrefix(text, addPrefix); ok {
		return addWord(ctx, c, word)
	}
	if word, ok := sdk.MatchPrefix(text, delPrefix); ok {
		return delWord(ctx, c, word)
	}
	if _, ok := sdk.MatchPrefix(text, listPrefix); ok {
		return listWords(ctx, c)
	}
	for _, word := range allWords(ctx, c) {
		if word != "" && strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
			return punish(ctx, c, word)
		}
	}
	return nil
}

func punish(ctx context.Context, c sdk.Call, word string) error {
	recallErr := c.Recall(ctx)
	muteErr := c.Mute(ctx, banDuration)
	switch {
	case recallErr == nil && muteErr == nil:
		return c.Reply(ctx, "检测到违禁词「"+word+"」，已撤回并禁言 2 分钟。")
	case recallErr == nil:
		return c.Reply(ctx, "检测到违禁词「"+word+"」，已撤回；禁言失败，请确认机器人是群管理员。")
	case muteErr == nil:
		return c.Reply(ctx, "检测到违禁词「"+word+"」，已禁言 2 分钟；撤回失败。")
	default:
		return c.Reply(ctx, "检测到违禁词「"+word+"」，请注意言行。")
	}
}

func addWord(ctx context.Context, c sdk.Call, word string) error {
	if word == "" {
		return c.Reply(ctx, "用法：添加违禁词<违禁词>")
	}
	mu.Lock()
	words := loadWords(ctx, c)
	for _, w := range words {
		if w == word {
			mu.Unlock()
			return c.Reply(ctx, "成功")
		}
	}
	words = append(words, word)
	_ = sdk.SaveJSON(ctx, c.State, wordsKey(c.Event.GroupID), words)
	mu.Unlock()
	return c.Reply(ctx, "成功")
}

func delWord(ctx context.Context, c sdk.Call, word string) error {
	if word == "" {
		return c.Reply(ctx, "用法：删除违禁词<违禁词>")
	}
	mu.Lock()
	words := loadWords(ctx, c)
	if len(words) == 0 {
		mu.Unlock()
		return c.Reply(ctx, "本群还没有违禁词~")
	}
	kept := words[:0]
	for _, w := range words {
		if w != word {
			kept = append(kept, w)
		}
	}
	_ = sdk.SaveJSON(ctx, c.State, wordsKey(c.Event.GroupID), kept)
	mu.Unlock()
	return c.Reply(ctx, "成功")
}

func listWords(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	words := loadWords(ctx, c)
	mu.Unlock()
	if len(words) == 0 {
		return c.Reply(ctx, "本群违禁词有\n[]")
	}
	return c.Reply(ctx, "本群违禁词有\n["+strings.Join(words, " | ")+"]")
}

func allWords(ctx context.Context, c sdk.Call) []string {
	words := append([]string{}, defaultWords...)
	mu.Lock()
	words = append(words, loadWords(ctx, c)...)
	mu.Unlock()
	return words
}

func loadWords(ctx context.Context, c sdk.Call) []string {
	var words []string
	_ = sdk.LoadJSON(ctx, c.State, wordsKey(c.Event.GroupID), &words)
	return words
}

func wordsKey(gid string) string { return "words:" + gid }

func normalize(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"\n", "\r", "\t", ";"} {
		s = strings.ReplaceAll(s, sep, "")
	}
	return strings.TrimSpace(s)
}

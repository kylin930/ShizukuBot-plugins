// Package antiabuse 群违禁词检测与词表维护。
package antiabuse

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	addPrefix  = "添加违禁词"
	delPrefix  = "删除违禁词"
	listPrefix = "查看违禁词"
)

const banDuration = 2 * time.Minute

// noPermission 是缺少管理员身份时的统一提示。
const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

// defaultWords 是内嵌的小规模示例词表，首次检测时生效；
// 与上游一致，管理员可以通过「删除违禁词」将其移除。
var defaultWords = []string{"傻逼", "操你妈", "狗日的"}

// mu 保护同一实例内的词表读改写。
var mu sync.Mutex

// Definition subscribes to group messages.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "antiabuse",
		Name:        "违禁词检测",
		Version:     "1.0.0",
		Description: "检测群消息中的违禁词，撤回并禁言 2 分钟；管理员可添加/删除/查看本群词表",
	}, []sdk.EventType{sdk.GroupMessage, sdk.GroupAtMessage}, Handle,
		[]triggercmd.Command{{Name: "antiabuse", Usage: "违禁词检测自动运行；管理员可发送「添加违禁词」「删除违禁词」「查看违禁词」维护本群词表。"}},
		sdk.WithPermissions("state.read", "state.write", "message.recall", "group.manage", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许添加/删除违禁词的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
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
		if !isAdmin(c) {
			return c.Reply(ctx, noPermission)
		}
		return addWord(ctx, c, word)
	}
	if word, ok := sdk.MatchPrefix(text, delPrefix); ok {
		if !isAdmin(c) {
			return c.Reply(ctx, noPermission)
		}
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
	if isDefaultWord(word) {
		removed := removeWord(loadRemoved(ctx, c), word)
		_ = sdk.SaveJSON(ctx, c.State, removedKey(c.Event.GroupID), removed)
		mu.Unlock()
		return c.Reply(ctx, "成功")
	}
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
	removed := loadRemoved(ctx, c)
	isDefault := isDefaultWord(word)
	if len(words) == 0 && (!isDefault || containsWord(removed, word)) {
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
	if isDefault && !containsWord(removed, word) {
		removed = append(removed, word)
		_ = sdk.SaveJSON(ctx, c.State, removedKey(c.Event.GroupID), removed)
	}
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
	mu.Lock()
	removed := loadRemoved(ctx, c)
	words := make([]string, 0, len(defaultWords)+len(loadWords(ctx, c)))
	for _, w := range defaultWords {
		if !containsWord(removed, w) {
			words = append(words, w)
		}
	}
	words = append(words, loadWords(ctx, c)...)
	mu.Unlock()
	return words
}

func loadWords(ctx context.Context, c sdk.Call) []string {
	var words []string
	_ = sdk.LoadJSON(ctx, c.State, wordsKey(c.Event.GroupID), &words)
	return words
}

func loadRemoved(ctx context.Context, c sdk.Call) []string {
	var words []string
	_ = sdk.LoadJSON(ctx, c.State, removedKey(c.Event.GroupID), &words)
	return words
}

func wordsKey(gid string) string   { return "words:" + gid }
func removedKey(gid string) string { return "words_removed:" + gid }

func isDefaultWord(word string) bool { return containsWord(defaultWords, word) }

func containsWord(list []string, word string) bool {
	for _, w := range list {
		if w == word {
			return true
		}
	}
	return false
}

func removeWord(list []string, word string) []string {
	kept := list[:0]
	for _, w := range list {
		if w != word {
			kept = append(kept, w)
		}
	}
	return kept
}

func normalize(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"\n", "\r", "\t", ";"} {
		s = strings.ReplaceAll(s, sep, "")
	}
	return strings.TrimSpace(s)
}

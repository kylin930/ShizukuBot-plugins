// Package thesaurus 词典式聊天：仅在被 @ 时按关键词匹配回复。
package thesaurus

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand/v2"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed pairs.json
var pairsJSON []byte

// entry 一组关键词与可选回复。
type entry struct {
	Keys    []string `json:"keys"`
	Replies []string `json:"replies"`
}

var (
	entries []entry
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

var fallbacks = []string{
	"嗯嗯，我在听。",
	"这个话题我不太懂，但我觉得你说得挺有道理。",
	"然后呢？继续说。",
	"原来是这样。",
}

func init() {
	if err := json.Unmarshal(pairsJSON, &entries); err != nil {
		panic("thesaurus: invalid pairs data: " + err.Error())
	}
}

// Definition subscribes to @-mentions.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "thesaurus",
		Name:        "词典聊天",
		Version:     "1.0.0",
		Description: "@机器人并说一句话，按内置词典匹配回复",
	}, sdk.MessageEvents, Handle,
		[]triggercmd.Command{{Name: "thesaurus", Usage: "词典聊天自动运行：@机器人并发送一句话即可。"}})
}

// Handle 只处理被 @ 的消息。
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.Mentioned(c.Event) {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	// 与上游 canmatch 一致：空文本不触发词典回复。
	if text == "" {
		return nil
	}
	for _, e := range entries {
		for _, key := range e.Keys {
			if strings.Contains(text, key) {
				return c.Reply(ctx, e.Replies[pickIndex(len(e.Replies))])
			}
		}
	}
	return c.Reply(ctx, fallbacks[pickIndex(len(fallbacks))])
}

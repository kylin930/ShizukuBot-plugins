// Package breakrepeat 打断群内复读：连续复读超过阈值后打乱原文发回。
package breakrepeat

import (
	"context"
	"math/rand"
	"sync"

	"shizukubot/sdk"
)

const throttle = 3

var (
	mu      sync.Mutex
	shuffle = rand.Shuffle
)

type repeat struct {
	Count int    `json:"count"`
	Text  string `json:"text"`
}

// Definition subscribes to group text messages.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "breakrepeat",
		Name:        "打断复读",
		Version:     "1.0.0",
		Description: "连续复读超过3条时打乱原文后回复",
	}, []sdk.EventType{sdk.GroupMessage, sdk.GroupAtMessage}, Handle,
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle tracks the last text per group and breaks the repeat. 图片消息没有
// 纯文本，不参与复读判定。
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" || c.Event.Message == "" {
		return nil
	}
	key := "repeat:" + c.Event.GroupID
	mu.Lock()
	var last repeat
	_ = sdk.LoadJSON(ctx, c.State, key, &last)
	if last.Text != c.Event.Message {
		_ = sdk.SaveJSON(ctx, c.State, key, repeat{Text: c.Event.Message})
		mu.Unlock()
		return nil
	}
	if last.Count < throttle {
		_ = sdk.SaveJSON(ctx, c.State, key, repeat{Count: last.Count + 1, Text: last.Text})
		mu.Unlock()
		return nil
	}
	_ = sdk.SaveJSON(ctx, c.State, key, repeat{})
	mu.Unlock()

	out := last.Text
	if len(out) > 2 {
		runes := []rune(out)
		shuffle(len(runes), func(i, j int) { runes[i], runes[j] = runes[j], runes[i] })
		out = string(runes)
	}
	return c.Reply(ctx, out)
}

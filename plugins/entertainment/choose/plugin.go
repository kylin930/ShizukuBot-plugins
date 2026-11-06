// Package choose 是选择困难症帮手：把「选择 A 还是 B」交给随机数决定。
package choose

import (
	"context"
	"math/rand"
	"strconv"
	"strings"

	"shizukubot/sdk"
)

// Definition subscribes to group and C2C messages for the 选择 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "choose",
		Name:        "选择困难症帮手",
		Version:     "1.0.0",
		Description: "发送「选择 A 还是 B 还是 C」，随机帮你选一个",
	}, sdk.MessageEvents, Handle)
}

// Handle picks one option from the 「还是」-separated list.
func Handle(ctx context.Context, c sdk.Call) error {
	rest, ok := sdk.MatchPrefix(sdk.Text(c.Event), "选择")
	if !ok || rest == "" {
		return nil
	}
	options := splitOptions(rest)
	if len(options) < 2 {
		return c.Reply(ctx, "选项太少啦，试试「选择 可乐 还是 雪碧」")
	}
	var lines []string
	for i, option := range options {
		lines = append(lines, strconv.Itoa(i+1)+", "+option)
	}
	return c.Reply(ctx, "你的选项有:\n"+strings.Join(lines, "\n")+"\n你最终会选: "+options[rand.Intn(len(options))])
}

func splitOptions(text string) []string {
	raw := strings.Split(text, "还是")
	options := make([]string, 0, len(raw))
	for _, option := range raw {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			options = append(options, trimmed)
		}
	}
	return options
}

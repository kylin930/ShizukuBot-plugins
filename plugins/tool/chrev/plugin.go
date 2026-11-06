// Package chrev 把英文文本倒过来并替换成上下翻转字符。
package chrev

import (
	"context"
	"regexp"
	"strings"

	"shizukubot/sdk"
)

var trigger = regexp.MustCompile(`^翻转\s*([A-Za-z\s]*)$`)

// Definition subscribes to the 「翻转 xxx」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "chrev",
		Name:        "英文字符翻转",
		Version:     "1.0.0",
		Description: "发送「翻转 I love you」，返回倒序且上下翻转的英文",
	}, sdk.MessageEvents, Handle)
}

// Handle reverses the matched English text and maps each character to its
// upside-down form.
func Handle(ctx context.Context, c sdk.Call) error {
	match := sdk.MatchRegexp(sdk.Text(c.Event), trigger)
	if len(match) < 2 {
		return nil
	}
	input := match[1]
	if strings.TrimSpace(input) == "" {
		return c.Reply(ctx, "用法：翻转 I love you")
	}
	var b strings.Builder
	for i := len(input) - 1; i >= 0; i-- {
		b.WriteRune(charMap[input[i]])
	}
	return c.Reply(ctx, b.String())
}

// Package emojimix 合成 emoji：发送两个 emoji 返回 Emoji Kitchen 合成图。
// 行为对齐上游 zerobot-plugin/plugin/emojimix。
package emojimix

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// bed 是 Google Emoji Kitchen 的图片地址模板，与上游一致。
const bed = "https://www.gstatic.com/android/keyboard/emojikitchen/%d/u%x/u%x_u%x.png"

var client = &http.Client{Timeout: 10 * time.Second}

// headOK 探测图片是否存在；测试中可替换。
var headOK = func(rawURL string) bool {
	resp, err := client.Head(rawURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Definition 订阅聊天消息，匹配由两个已知 emoji 组成的消息。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "emojimix",
		Name:        "合成 Emoji",
		Version:     "1.0.0",
		Description: "发送两个 emoji 合成一张 Emoji Kitchen 图片",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "emojimix", Usage: "发送两个 emoji 即可合成一张 Emoji Kitchen 图片"}})
}

// Handle 去除消息中的空白后要求恰好两个 emoji，按上游顺序探测并发送合成图。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, sdk.Text(c.Event))
	r := []rune(text)
	if len(r) != 2 {
		return nil
	}
	r1, r2 := r[0], r[1]
	if _, ok := emojis[r1]; !ok {
		return nil
	}
	if _, ok := emojis[r2]; !ok {
		return nil
	}

	u1 := fmt.Sprintf(bed, emojis[r1], r1, r1, r2)
	u2 := fmt.Sprintf(bed, emojis[r2], r2, r2, r1)
	if headOK(u1) {
		return c.ReplyImage(ctx, u1)
	}
	if headOK(u2) {
		return c.ReplyImage(ctx, u2)
	}
	return nil
}

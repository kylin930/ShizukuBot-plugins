// Package alipayvoice 支付宝到账语音：把指定金额播报成语音。
package alipayvoice

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// voiceURL 语音地址模板，测试中可替换。
var voiceURL = "https://mm.cqu.cc/share/zhifubaodaozhang/mp3/%v.mp3"

// amountRe 只接受数字和至多两位小数，避免把任意内容拼进 URL。
var amountRe = regexp.MustCompile(`^\d{1,9}(\.\d{1,2})?$`)

// Definition subscribes to the 「支付宝到账」prefix.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "alipayvoice",
		Name:        "支付宝到账语音",
		Version:     "1.0.0",
		Description: "发送「支付宝到账 100」播放对应金额的到账语音",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "alipayvoice", Trigger: "支付宝到账"}})
}

// Handle 校验金额后发送语音。
func Handle(ctx context.Context, c sdk.Call) error {
	amount, ok := sdk.MatchPrefix(sdk.Text(c.Event), "支付宝到账")
	if !ok {
		return nil
	}
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return c.Reply(ctx, "用法：支付宝到账 100（支持整数或两位小数）")
	}
	if !amountRe.MatchString(amount) {
		return c.Reply(ctx, "金额格式不对，示例：支付宝到账 100 或 支付宝到账 88.88")
	}
	return c.ReplyVoice(ctx, fmt.Sprintf(voiceURL, amount))
}

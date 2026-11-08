// Package chouxianghua 抽象话：按汉字的拼音把中文替换成对应表情。
//
// 数据来自上游 cxh.db：pinyin 表为单字到拼音的映射，emoji 表为拼音到表情的
// 映射。翻译时优先尝试相邻两字的拼音拼接（双字拼音），其次单字拼音，
// 最后原样输出，与上游 chouxianghua.go 的算法一致。
package chouxianghua

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed pinyin.json
var pinyinJSON []byte

//go:embed emoji.json
var emojiJSON []byte

var (
	// pinyinMap 是汉字到拼音的映射（上游 pinyin 表）。
	pinyinMap map[string]string
	// emojiMap 是拼音到表情的映射（上游 emoji 表）。
	emojiMap map[string]string
)

func init() {
	if err := json.Unmarshal(pinyinJSON, &pinyinMap); err != nil {
		panic("chouxianghua: invalid pinyin data: " + err.Error())
	}
	if err := json.Unmarshal(emojiJSON, &emojiMap); err != nil {
		panic("chouxianghua: invalid emoji data: " + err.Error())
	}
}

// Definition subscribes to 「抽象翻译」.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "chouxianghua",
		Name:        "抽象话",
		Version:     "1.1.0",
		Description: "发送「抽象翻译 <中文>」按拼音把中文换成对应表情",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "chouxianghua", Trigger: "抽象翻译"},
	})
}

// Handle 处理抽象翻译。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if rest, ok := sdk.MatchPrefix(text, "抽象翻译"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return c.Reply(ctx, "用法：抽象翻译 今天上班好累")
		}
		return c.Reply(ctx, cx(rest))
	}
	return nil
}

// cx 优先匹配双字拼音组合，其次单字拼音，未命中时保留原字符。
func cx(s string) string {
	h := []rune(s)
	out := make([]rune, 0, len(h))
	for i := 0; i < len(h); i++ {
		if i < len(h)-1 {
			if e := emojiMap[pinyinMap[string(h[i])]+pinyinMap[string(h[i+1])]]; e != "" {
				out = append(out, []rune(e)...)
				i++
				continue
			}
		}
		if e := emojiMap[pinyinMap[string(h[i])]]; e != "" {
			out = append(out, []rune(e)...)
			continue
		}
		out = append(out, h[i])
	}
	return string(out)
}

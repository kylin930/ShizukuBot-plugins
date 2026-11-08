// Package hyaku 百人一首：随机或按编号发送和歌与配图。
package hyaku

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed poems.json
var poemsJSON []byte

// imageBase 图片目录，可在测试中替换。
var imageBase = "https://gitea.seku.su/fumiama/OguraHyakuninIsshu/raw/branch/master/"

// pickIndex 可在测试中替换。
var pickIndex = rand.IntN

// poem 一首和歌，字段顺序与上游 CSV 一致。
type poem struct {
	No        int    `json:"no"`
	Poet      string `json:"poet"`
	Kami      string `json:"kami"`
	Shimo     string `json:"shimo"`
	KamiKana  string `json:"kami_kana"`
	ShimoKana string `json:"shimo_kana"`
}

var poems []poem

func init() {
	if err := json.Unmarshal(poemsJSON, &poems); err != nil {
		panic("hyaku: invalid poems data: " + err.Error())
	}
	if len(poems) != 100 {
		panic("hyaku: expected 100 poems")
	}
}

// Definition subscribes to 「百人一首」and「百人一首之N」.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "hyaku",
		Name:        "百人一首",
		Version:     "1.0.0",
		Description: "发送「百人一首」随机看一首和歌，发送「百人一首之N」查看第 N 首",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "hyaku", Trigger: "百人一首"},
	})
}

// Handle 处理随机与按编号查询。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "百人一首") {
		return send(ctx, c, poems[pickIndex(len(poems))])
	}
	if rest, ok := sdk.MatchPrefix(text, "百人一首之"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil || n < 1 || n > 100 {
			return c.Reply(ctx, "编号需要在 1 到 100 之间。")
		}
		return send(ctx, c, poems[n-1])
	}
	return nil
}

// send 先发文本与按钮，再尽力发送上下两句的配图。
func send(ctx context.Context, c sdk.Call, p poem) error {
	if err := c.ReplyKeyboardOrText(ctx, poemText(p), moreKeyboard()); err != nil {
		return err
	}
	for _, ext := range []string{"jpg", "png"} {
		_ = c.ReplyImage(ctx, fmt.Sprintf("%simg/%03d.%s", imageBase, p.No, ext))
	}
	return nil
}

// poemText 拼出和歌文本。
func poemText(p poem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "●番号：%d\n", p.No)
	fmt.Fprintf(&b, "◉歌人：%s\n", p.Poet)
	fmt.Fprintf(&b, "○上の句：%s\n", p.Kami)
	fmt.Fprintf(&b, "○下の句：%s\n", p.Shimo)
	fmt.Fprintf(&b, "◎上の句ひらがな：%s\n", p.KamiKana)
	fmt.Fprintf(&b, "◎下の句ひらがな：%s", p.ShimoKana)
	return b.String()
}

// moreKeyboard 「再来一首」按钮。
func moreKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("hyaku-more", "再来一首", "百人一首")))
}

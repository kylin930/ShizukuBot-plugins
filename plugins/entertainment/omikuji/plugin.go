// Package omikuji 浅草寺求签：按用户与日期抽取签号并返回签图或签文。
package omikuji

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed kuji.json
var kujiJSON []byte

// kujiTexts 是从上游 kuji.db 导出的签文，键为签号。
var kujiTexts map[string]string

func init() {
	if err := json.Unmarshal(kujiJSON, &kujiTexts); err != nil {
		panic("omikuji: invalid kuji data: " + err.Error())
	}
}

var (
	// imageBase 上游签图仓库，测试可替换。
	imageBase = "https://gitea.seku.su/fumiama/senso-ji-omikuji/raw/branch/main/"

	// nowFunc 便于测试固定日期。
	nowFunc = time.Now
)

const maxNumber = 100

// Definition subscribes to the 求签/占卜 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "omikuji",
		Name:        "浅草寺求签",
		Version:     "1.0.0",
		Description: "发送「求签」或「占卜」抽取浅草寺签并返回签图；发送「解签」返回当天的签文",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "omikuji", Trigger: "求签"},
	})
}

// Handle replies with the two images of one sign, or the sign text for 解签.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	n := signNumber(c.Event.UserID, nowFunc())
	if sdk.MatchFull(text, "解签") {
		if kuji := kujiTexts[strconv.Itoa(n)]; kuji != "" {
			return c.ReplyAt(ctx, kuji, c.Event.UserID)
		}
		return c.Reply(ctx, "签文数据缺失，请稍后再试。")
	}
	if !sdk.MatchFull(text, "求签") && !sdk.MatchFull(text, "占卜") {
		return nil
	}
	uids := []string{
		fmt.Sprintf("%s%d_0.jpg", imageBase, n),
		fmt.Sprintf("%s%d_1.jpg", imageBase, n),
	}
	// 宿主单条 Reply 只能携带一张图片，因此用 Markdown 一次发送 @ 与两张签图，
	// 对齐上游 message.At + 两张 ImageBytes 的合并消息。
	keyboard, err := jieqianKeyboard().JSON()
	if err == nil {
		markdown := fmt.Sprintf("![img#300px #300px](%s)\n![img#300px #300px](%s)\n回复「解签」查看签文", uids[0], uids[1])
		err = c.Send(ctx, sdk.Reply{Kind: "keyboard", Text: markdown, Keyboard: keyboard, Mention: c.Event.UserID})
		if err == nil {
			return nil
		}
	}
	// Markdown 不受支持时退回：先发带 @ 的提示，再逐张发送签图。
	if err := c.ReplyAt(ctx, "回复「解签」查看签文", c.Event.UserID); err != nil {
		return err
	}
	for _, u := range uids {
		if err := c.ReplyImage(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// jieqianKeyboard 签图后的「解签」按钮。
func jieqianKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("omikuji-explain", "解签", "解签")))
}

// signNumber 同一用户在同一天得到同一个签号（1-100）。
func signNumber(userID string, now time.Time) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(userID + "|" + now.Format("2006-01-02")))
	return int(h.Sum64()%maxNumber) + 1
}

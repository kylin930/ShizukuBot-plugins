// Package omikuji 浅草寺求签：按用户与日期抽取签号并返回签图。
package omikuji

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"shizukubot/sdk"
)

var (
	// imageBase 上游签图仓库，测试可替换。
	imageBase = "https://gitea.seku.su/fumiama/senso-ji-omikuji/raw/branch/main/"

	// nowFunc 便于测试固定日期。
	nowFunc = time.Now
)

const maxNumber = 100

// Definition subscribes to the 求签/占卜 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "omikuji",
		Name:        "浅草寺求签",
		Version:     "1.0.0",
		Description: "发送「求签」或「占卜」，按用户与日期抽取浅草寺签并返回签图",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the two images of one sign. 解签需要本地签文数据库与
// 字体渲染，框架侧不引入大资源，因此未移植。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if !sdk.MatchFull(text, "求签") && !sdk.MatchFull(text, "占卜") {
		return nil
	}
	n := signNumber(c.Event.UserID, nowFunc())
	for _, i := range []int{0, 1} {
		if err := c.ReplyImage(ctx, fmt.Sprintf("%s%d_%d.jpg", imageBase, n, i)); err != nil {
			return err
		}
	}
	return nil
}

// signNumber 同一用户在同一天得到同一个签号（1-100）。
func signNumber(userID string, now time.Time) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(userID + "|" + now.Format("2006-01-02")))
	return int(h.Sum64()%maxNumber) + 1
}

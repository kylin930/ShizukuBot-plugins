// Package fortune 今日运势：按用户与日期从 text.json 签文中固定抽取一条。
package fortune

import (
	"context"
	_ "embed"
	"encoding/json"
	"hash/crc64"
	"math/rand"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed text.json
var textJSON []byte

// nowFunc 便于测试固定日期。
var nowFunc = time.Now

// oracle 对应上游 text.json 中的一条签文。
type oracle struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

var oracles []oracle

func init() {
	if err := json.Unmarshal(textJSON, &oracles); err != nil {
		panic("fortune: invalid text.json: " + err.Error())
	}
	if len(oracles) == 0 {
		panic("fortune: text.json is empty")
	}
}

// Definition 订阅「运势」「今日运势」「抽签」触发词。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "fortune",
		Name:        "今日运势",
		Version:     "1.0.0",
		Description: "发送「运势」「今日运势」或「抽签」查看自己的今日运势",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "fortune", Trigger: "今日运势"}})
}

// Handle 生成并回复当天运势。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if !triggered(text) {
		return nil
	}
	o := draw(c.Event.UserID, nowFunc())
	return c.Reply(ctx, "【"+o.Title+"】\n"+o.Content)
}

// triggered 判断消息是否命中运势指令，允许「今日运势」后附带一句说明。
func triggered(text string) bool {
	switch {
	case sdk.MatchFull(text, "运势"),
		sdk.MatchFull(text, "今日运势"),
		sdk.MatchFull(text, "抽签"):
		return true
	}
	_, ok := sdk.MatchPrefix(text, "今日运势")
	return ok
}

// draw 用「日期 + 用户ID」作为种子，保证同一用户同一天抽到的签文固定。
func draw(userID string, now time.Time) oracle {
	sum := crc64.New(crc64.MakeTable(crc64.ISO))
	_, _ = sum.Write([]byte(now.Format("20060102")))
	_, _ = sum.Write([]byte(userID))
	r := rand.New(rand.NewSource(int64(sum.Sum64())))
	return oracles[r.Intn(len(oracles))]
}

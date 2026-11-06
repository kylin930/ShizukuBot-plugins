// Package chat 基础反应与群空调。
package chat

import (
	"context"
	"math/rand"
	"regexp"
	"strconv"
	"sync"

	"shizukubot/sdk"
)

var tempRe = regexp.MustCompile(`^设置温度(\d+)$`)

// replies 是被 @ 但没带内容时的候选应答。框架没有昵称字段，故不使用机器人名字。
var replies = []string{
	"在此，有何贵干~",
	"(っ●ω●)っ在~",
	"这里是机器人(っ●ω●)っ",
	"我不在呢~",
}

// pick 便于测试替换随机源。
var pick = rand.Intn

// mu 保护同一实例内的空调状态读改写。
var mu sync.Mutex

type air struct {
	On   bool `json:"on"`
	Temp int  `json:"temp"`
}

// Definition subscribes to the 基础反应 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "chat",
		Name:        "基础反应",
		Version:     "1.0.0",
		Description: "被@时应答；空调开/空调关/群温度/设置温度",
	}, sdk.MessageEvents, Handle, sdk.WithPermissions("state.read", "state.write"))
}

// Handle implements 被喊名字与群空调。戳一戳事件不在框架可订阅的规范事件内，未移植。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if text == "" && sdk.Mentioned(c.Event) {
		return c.Reply(ctx, replies[pick(len(replies))])
	}
	key := stateKey(c.Event.GroupID)
	switch {
	case sdk.MatchFull(text, "空调开"):
		mu.Lock()
		_ = sdk.SaveJSON(ctx, c.State, key, air{On: true, Temp: 26})
		mu.Unlock()
		return c.Reply(ctx, "❄️哔~")
	case sdk.MatchFull(text, "空调关"):
		mu.Lock()
		_ = sdk.SaveJSON(ctx, c.State, key, air{On: false, Temp: 26})
		mu.Unlock()
		return c.Reply(ctx, "💤哔~")
	case sdk.MatchFull(text, "群温度"):
		mu.Lock()
		a := loadAir(ctx, c.State, key)
		mu.Unlock()
		return c.Reply(ctx, status(a))
	}
	match := tempRe.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	temp, err := strconv.Atoi(match[1])
	if err != nil {
		return nil
	}
	mu.Lock()
	a := loadAir(ctx, c.State, key)
	if a.On {
		a.Temp = temp
		_ = sdk.SaveJSON(ctx, c.State, key, a)
	}
	mu.Unlock()
	return c.Reply(ctx, status(a))
}

func stateKey(gid string) string {
	if gid == "" {
		return "air:private"
	}
	return "air:" + gid
}

func loadAir(ctx context.Context, st sdk.State, key string) air {
	a := air{Temp: 26}
	_ = sdk.LoadJSON(ctx, st, key, &a)
	return a
}

func status(a air) string {
	icon := "💤"
	if a.On {
		icon = "❄️风速中"
	}
	return icon + "\n群温度 " + strconv.Itoa(a.Temp) + "℃"
}

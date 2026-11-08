// Package mcfish Minecraft 风格钓鱼小游戏。
// 玩法对齐上游 zerobot-plugin/plugin/mcfish 5.6.2，输出为纯文本，
// 状态通过 sdk.State 按用户持久化，不依赖图片、钱包或 SQLite。
package mcfish

import (
	"context"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// nowFunc 与 randIntn 可在测试中替换，保证结果确定。
var (
	nowFunc  = time.Now
	randIntn = rand.Intn
	mu       sync.Mutex
)

var (
	timesFishRe = regexp.MustCompile(`^进行\s*([1-5]\d|[1-9])\s*次钓鱼$`)
	curseRe     = regexp.MustCompile(`^消除(绑定|宝藏)诅咒\s*(\d*)$`)
)

// Definition 注册钓鱼插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "mcfish",
		Name:        "Minecraft 钓鱼",
		Version:     "2.0.0",
		Description: "钓鱼、背包、鱼竿、商店、附魔与合成，纯文本移植自 McFish 5.6.2",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "mcfish", Trigger: "钓鱼"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle 分发所有钓鱼相关指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()

	switch text {
	case "钓鱼背包", "背包", "我的背包":
		return handleBackpack(ctx, c)
	case "钓鱼", "进行钓鱼":
		return handleFish(ctx, c, 1)
	case "修复鱼竿", "维修鱼竿":
		return handleRepair(ctx, c)
	case "钓鱼商店", "钓鱼看板":
		return handleShop(ctx, c)
	case "出售所有垃圾":
		return handleSellAllWaste(ctx, c)
	case "当前装备概率明细":
		return handleProbabilities(ctx, c)
	case "查看钓鱼规则":
		return handleRules(ctx, c)
	}

	if m := timesFishRe.FindStringSubmatch(text); m != nil {
		n := atoiDefault(m[1], 1)
		return handleFish(ctx, c, n)
	}
	if m := curseRe.FindStringSubmatch(text); m != nil {
		n := 1
		if m[2] != "" {
			n = atoiDefault(m[2], 1)
		}
		return handleRemoveCurse(ctx, c, n)
	}
	if rest, ok := sdk.MatchPrefix(text, "购买"); ok {
		return handleBuy(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, "出售"); ok {
		return handleSell(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, "装备"); ok {
		return handleEquip(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, "附魔"); ok {
		return handleEnchant(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, "合成"); ok {
		return handleCraft(ctx, c, rest)
	}
	return nil
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}

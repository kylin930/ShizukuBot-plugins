package endfield

import (
	"context"
	"fmt"
	"strings"

	"shizukubot/sdk"
)

func init() {
	registerRoute(135, exactMatch("更新", "强制更新", "插件更新", "插件强制更新", "update"), handleEndfieldUpdate)
	registerRoute(135, exactMatch("清理缓存", "redis清理"), handleCleanCache)
}

// handleEndfieldUpdate 对齐上游 apps/update.js；编译期插件无法自更新。
func handleEndfieldUpdate(ctx context.Context, c sdk.Call, cfg config, _ string, text string) error {
	if !cfg.isAdmin(c.Event.UserID) {
		return replyText(ctx, c, getMessage("gacha.simulate_reset_all_no_auth"))
	}
	var message string
	if strings.Contains(text, "强制") {
		message = getMessage("update.starting") + "\n" + getMessage("update.already_latest")
	} else {
		message = getMessage("update.already_latest")
	}
	message += "\n当前为编译期插件，需重新构建并部署后才会更新。"
	return replyText(ctx, c, message)
}

// handleCleanCache 等价于上游 redisClean：清理当前用户的抽卡同步缓存与模拟抽卡状态。
func handleCleanCache(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	userID := c.Event.UserID
	count := 0
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil {
		return replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
	}
	for _, account := range accounts {
		roleID := account.roleID()
		_ = sdk.SaveJSON(ctx, c.State, gachaCacheKey(userID, roleID), map[string]any{})
		count++
	}
	_ = sdk.SaveJSON(ctx, c.State, simulateKey(userID), simulateStore{
		Pools: map[string]*simulateState{},
		Daily: map[string]int{},
	})
	count++
	return replyText(ctx, c, fmt.Sprintf("已清理当前用户的抽卡同步缓存与模拟抽卡状态（共 %d 项）。", count))
}

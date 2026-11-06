// Package manager 群管理：自我禁言、撤回与本群管理员操作。
package manager

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	selfMutePrefix = "我要自闭"
	zenPrefix      = "禅定"
	mutePrefix     = "禁言"
	unmutePrefix   = "解除禁言"
	kickPrefix     = "踢出"
	maxMute        = 30 * 24 * time.Hour
)

var durationPattern = regexp.MustCompile(`(\d+)\s*(秒|分钟|分|小时|时|天)`)

// Definition subscribes to group messages. Admin targets are identified by
// member_openid because QQ group events do not expose QQ numbers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "manager",
		Name:        "群管理",
		Version:     "1.0.0",
		Description: "自我禁言、撤回本条消息；管理员可按 openid 禁言/解禁/踢出成员",
	}, []sdk.EventType{sdk.GroupMessage, sdk.GroupAtMessage}, Handle,
		sdk.WithPermissions("message.recall", "group.manage", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许执行群管命令的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle routes only explicit commands and never blocks ordinary chat.
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	switch {
	case sdk.MatchFull(text, "我的ID"), sdk.MatchFull(text, "我的id"):
		if c.Event.UserID == "" {
			return c.Reply(ctx, "无法获取你的 openid。")
		}
		return c.Reply(ctx, "你的 ID："+c.Event.UserID)
	case sdk.MatchFull(text, "撤回"), sdk.MatchFull(text, "撤回本条"), sdk.MatchFull(text, "撤回这条"):
		if err := c.Recall(ctx); err != nil {
			return c.Reply(ctx, "撤回失败："+err.Error())
		}
		return c.Reply(ctx, "已撤回本条消息。")
	case sdk.MatchFull(text, "解除禁言我"), sdk.MatchFull(text, "解除自闭"):
		if err := c.Unmute(ctx); err != nil {
			return c.Reply(ctx, "解除禁言失败："+err.Error())
		}
		return c.Reply(ctx, "禁言已解除。")
	}
	if rest, ok := sdk.MatchPrefix(text, selfMutePrefix); ok {
		return selfMute(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, zenPrefix); ok {
		return selfMute(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, unmutePrefix); ok {
		return adminUnmute(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, mutePrefix); ok {
		return adminMute(ctx, c, rest)
	}
	if rest, ok := sdk.MatchPrefix(text, kickPrefix); ok {
		return adminKick(ctx, c, rest)
	}
	return nil
}

func selfMute(ctx context.Context, c sdk.Call, rest string) error {
	d, ok := parseDuration(rest)
	if !ok {
		return c.Reply(ctx, "用法：我要自闭 10分钟（最长 30 天）")
	}
	if err := c.Mute(ctx, d); err != nil {
		return c.Reply(ctx, "禁言失败："+err.Error())
	}
	return c.Reply(ctx, "已自我禁言 "+formatDuration(d)+"。")
}

func adminMute(ctx context.Context, c sdk.Call, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	target := targetUser(c.Event)
	if target == "" {
		return c.Reply(ctx, "用法：禁言 @成员 10分钟")
	}
	d, ok := parseDuration(rest)
	if !ok {
		return c.Reply(ctx, "用法：禁言 @成员 10分钟（最长 30 天）")
	}
	if err := c.MuteUser(ctx, target, d); err != nil {
		return c.Reply(ctx, "禁言失败："+err.Error())
	}
	return c.Reply(ctx, "已禁言该成员 "+formatDuration(d)+"。")
}

func adminUnmute(ctx context.Context, c sdk.Call, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	target := targetUser(c.Event)
	if target == "" {
		return c.Reply(ctx, "用法：解除禁言 @成员")
	}
	if err := c.UnmuteUser(ctx, target); err != nil {
		return c.Reply(ctx, "解除禁言失败："+err.Error())
	}
	return c.Reply(ctx, "已解除该成员的禁言。")
}

func adminKick(ctx context.Context, c sdk.Call, rest string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, "没有权限。请先在插件配置的 admin_users 中加入你的 openid。")
	}
	target := targetUser(c.Event)
	if target == "" {
		return c.Reply(ctx, "用法：踢出 @成员（可选：踢出 @成员 并拉黑）")
	}
	if err := c.KickUser(ctx, target, strings.Contains(rest, "拉黑")); err != nil {
		return c.Reply(ctx, "踢出失败："+err.Error())
	}
	return c.Reply(ctx, "已将该成员移出本群。")
}

// targetUser accepts the current documented mention tag and the legacy form.
func targetUser(e sdk.Event) string {
	ids := sdk.MentionUserIDs(e)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" {
		return false
	}
	for _, id := range adminUsers(c.Config) {
		if id == c.Event.UserID {
			return true
		}
	}
	return false
}

func adminUsers(config map[string]any) []string {
	if config == nil {
		return nil
	}
	switch v := config["admin_users"].(type) {
	case []string:
		return v
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if id, ok := item.(string); ok {
				ids = append(ids, id)
			}
		}
		return ids
	default:
		return nil
	}
}

func parseDuration(s string) (time.Duration, bool) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	var unit time.Duration
	switch m[2] {
	case "秒":
		unit = time.Second
	case "分钟", "分":
		unit = time.Minute
	case "小时", "时":
		unit = time.Hour
	case "天":
		unit = 24 * time.Hour
	}
	d := time.Duration(n) * unit
	if d > maxMute {
		d = maxMute
	}
	return d, true
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "天"
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "小时"
	case d >= time.Minute && d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "分钟"
	default:
		return strconv.Itoa(int(d/time.Second)) + "秒"
	}
}

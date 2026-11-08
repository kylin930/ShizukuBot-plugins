package minecraftobserver

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

// 订阅指令，与上游保持一致。
var (
	subAddRe  = regexp.MustCompile(`(?i)^mc服务器添加订阅\s*(.*)$`)
	subDelRe  = regexp.MustCompile(`(?i)^mc服务器取消订阅\s*(.*)$`)
	subListRe = regexp.MustCompile(`(?i)^mc服务器订阅列表\s*$`)
	subAllRe  = regexp.MustCompile(`(?i)^mc服务器全局订阅列表\s*$`)
	subPullRe = regexp.MustCompile(`(?i)^mc服务器订阅拉取\s*$`)
)

const (
	// subsKey 保存全部订阅记录。
	subsKey = "minecraftobserver:subscriptions"
	// noPermission 是无权限时的统一提示。
	noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"
)

// subMu 保护订阅记录的读改写，避免并发指令互相覆盖。
var subMu sync.Mutex

// snapshot 是用于比较变化的关键状态快照。
type snapshot struct {
	Online   bool   `json:"online"`
	Version  string `json:"version,omitempty"`
	Motd     string `json:"motd,omitempty"`
	Players  int    `json:"players"`
	Max      int    `json:"max"`
	IconHash string `json:"icon_hash,omitempty"`
}

// subscription 是一条订阅记录，Target 为 group 或 user。
type subscription struct {
	Addr   string   `json:"addr"`
	Target string   `json:"target"`
	ID     string   `json:"id"`
	Last   snapshot `json:"last"`
}

// targetOf 返回当前会话的推送目标：群聊为 group，私聊为 user。
func targetOf(c sdk.Call) (string, string) {
	if c.Event.GroupID != "" {
		return "group", c.Event.GroupID
	}
	return "user", c.Event.UserID
}

// pendingKey 返回某个目标的待推送通知存储键。
func pendingKey(target, id string) string {
	return "minecraftobserver:pending:" + target + ":" + id
}

// snapshotOf 从接口响应中提取状态快照。
func snapshotOf(status *statusResponse) snapshot {
	if status == nil || !status.Online {
		return snapshot{}
	}
	version := strings.TrimSpace(status.Version)
	if version == "" {
		version = strings.TrimSpace(status.Protocol.Name)
	}
	return snapshot{
		Online:   true,
		Version:  version,
		Motd:     motdText(status),
		Players:  status.Players.Online,
		Max:      status.Players.Max,
		IconHash: iconHash(status),
	}
}

// iconHash 计算服务器图标的 MD5，用于检测图标变更。
func iconHash(status *statusResponse) string {
	if status == nil {
		return ""
	}
	icon := strings.TrimSpace(status.Icon)
	if icon == "" {
		return ""
	}
	sum := md5.Sum([]byte(icon))
	return hex.EncodeToString(sum[:])
}

// loadSubs 读取全部订阅记录。
func loadSubs(ctx context.Context, c sdk.Call) []subscription {
	var subs []subscription
	_ = sdk.LoadJSON(ctx, c.State, subsKey, &subs)
	return subs
}

// saveSubs 写入全部订阅记录。
func saveSubs(ctx context.Context, c sdk.Call, subs []subscription) error {
	return sdk.SaveJSON(ctx, c.State, subsKey, subs)
}

// indexSub 返回同目标同地址的订阅下标，不存在时返回 -1。
func indexSub(subs []subscription, target, id, addr string) int {
	for i, sub := range subs {
		if sub.Addr == addr && sub.Target == target && sub.ID == id {
			return i
		}
	}
	return -1
}

// addSubscription 校验服务器可用后写入订阅，并立即回复一次状态。
func addSubscription(ctx context.Context, c sdk.Call, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return c.Reply(ctx, helpText)
	}
	addr, err := normalizeAddr(raw)
	if err != nil {
		return c.Reply(ctx, "地址格式不正确："+err.Error())
	}
	status, delay, err := fetchStatus(ctx, addr)
	if err != nil || status == nil || !status.Online {
		reason := "服务器未响应或地址不存在"
		if err != nil {
			reason = err.Error()
		}
		return c.Reply(ctx, "服务器信息初始化失败，请检查服务器是否可用！\n错误信息："+reason)
	}

	target, id := targetOf(c)
	subMu.Lock()
	subs := loadSubs(ctx, c)
	if idx := indexSub(subs, target, id, addr); idx >= 0 {
		subs[idx].Last = snapshotOf(status)
		err = saveSubs(ctx, c, subs)
		subMu.Unlock()
		if err != nil {
			return err
		}
		return c.Reply(ctx, fmt.Sprintf("服务器 %s 已在订阅列表中，状态已更新。", addr))
	}
	subs = append(subs, subscription{Addr: addr, Target: target, ID: id, Last: snapshotOf(status)})
	err = saveSubs(ctx, c, subs)
	subMu.Unlock()
	if err != nil {
		return err
	}

	if err := c.Reply(ctx, fmt.Sprintf("服务器 %s 订阅添加成功", addr)); err != nil {
		return err
	}
	return sendStatus(ctx, c, addr, status, delay)
}

// removeSubscription 删除当前目标的订阅。
func removeSubscription(ctx context.Context, c sdk.Call, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return c.Reply(ctx, helpText)
	}
	addr, err := normalizeAddr(raw)
	if err != nil {
		return c.Reply(ctx, "地址格式不正确："+err.Error())
	}
	target, id := targetOf(c)
	subMu.Lock()
	subs := loadSubs(ctx, c)
	idx := indexSub(subs, target, id, addr)
	if idx < 0 {
		subMu.Unlock()
		return c.Reply(ctx, "未找到该订阅，发送「mc服务器订阅列表」查看当前订阅。")
	}
	subs = append(subs[:idx], subs[idx+1:]...)
	err = saveSubs(ctx, c, subs)
	subMu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, "取消订阅成功")
}

// listSubscriptions 列出当前会话的订阅。
func listSubscriptions(ctx context.Context, c sdk.Call) error {
	target, id := targetOf(c)
	subMu.Lock()
	subs := loadSubs(ctx, c)
	subMu.Unlock()

	var b strings.Builder
	count := 0
	for _, sub := range subs {
		if sub.Target == target && sub.ID == id {
			count++
			b.WriteString("服务器地址：" + sub.Addr + "\n")
		}
	}
	if count == 0 {
		return c.Reply(ctx, "当前没有订阅哦")
	}
	return c.Reply(ctx, "[订阅列表]\n"+strings.TrimRight(b.String(), "\n"))
}

// globalSubscriptions 输出现有订阅的全局分布，仅管理员可用。
func globalSubscriptions(ctx context.Context, c sdk.Call) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	subMu.Lock()
	subs := loadSubs(ctx, c)
	subMu.Unlock()
	if len(subs) == 0 {
		return c.Reply(ctx, "当前一个订阅都没有哦")
	}

	groups := map[string][]string{}
	users := map[string][]string{}
	var groupOrder, userOrder []string
	for _, sub := range subs {
		switch sub.Target {
		case "group":
			if _, ok := groups[sub.ID]; !ok {
				groupOrder = append(groupOrder, sub.ID)
			}
			groups[sub.ID] = append(groups[sub.ID], sub.Addr)
		case "user":
			if _, ok := users[sub.ID]; !ok {
				userOrder = append(userOrder, sub.ID)
			}
			users[sub.ID] = append(users[sub.ID], sub.Addr)
		}
	}

	var b strings.Builder
	for _, id := range groupOrder {
		fmt.Fprintf(&b, "[群 %s]存在以下订阅：\n", id)
		for _, addr := range groups[id] {
			b.WriteString("服务器地址：" + addr + "\n")
		}
	}
	for _, id := range userOrder {
		fmt.Fprintf(&b, "[用户 %s]存在以下订阅：\n", id)
		for _, addr := range users[id] {
			b.WriteString("服务器地址：" + addr + "\n")
		}
	}
	return c.Reply(ctx, strings.TrimRight(b.String(), "\n"))
}

// pullSubscriptions 扫描全部订阅服务器，把变化推送到对应会话。
// 当前会话的变化立即回复；其他会话的变化写入待推送队列，等该会话下次发消息时补发。
func pullSubscriptions(ctx context.Context, c sdk.Call) error {
	curTarget, curID := targetOf(c)

	subMu.Lock()
	subs := loadSubs(ctx, c)
	if len(subs) == 0 {
		subMu.Unlock()
		return c.Reply(ctx, "当前没有订阅哦")
	}

	order := make([]string, 0, len(subs))
	index := map[string][]int{}
	for i := range subs {
		addr := subs[i].Addr
		if _, ok := index[addr]; !ok {
			order = append(order, addr)
		}
		index[addr] = append(index[addr], i)
	}

	var current []string
	pending := map[string][]string{}
	for _, addr := range order {
		status, delay, err := fetchStatus(ctx, addr)
		if err != nil || status == nil {
			// 暂时不可达时保持原快照，等待下次扫描。
			continue
		}
		latest := snapshotOf(status)
		for _, i := range index[addr] {
			if subs[i].Last == latest {
				continue
			}
			msg := changeNotify(addr, subs[i].Last, latest, status, delay)
			subs[i].Last = latest
			if subs[i].Target == curTarget && subs[i].ID == curID {
				current = append(current, msg)
				continue
			}
			key := pendingKey(subs[i].Target, subs[i].ID)
			pending[key] = append(pending[key], msg)
		}
	}
	err := saveSubs(ctx, c, subs)
	subMu.Unlock()
	if err != nil {
		return err
	}

	for key, msgs := range pending {
		var old []string
		_ = sdk.LoadJSON(ctx, c.State, key, &old)
		_ = sdk.SaveJSON(ctx, c.State, key, append(old, msgs...))
	}

	if len(current) == 0 {
		return c.Reply(ctx, "没有检测到订阅服务器的状态变化。")
	}
	return c.Reply(ctx, strings.Join(current, "\n\n"))
}

// flushPending 补发当前会话积压的订阅通知。
func flushPending(ctx context.Context, c sdk.Call) error {
	target, id := targetOf(c)
	key := pendingKey(target, id)
	subMu.Lock()
	var msgs []string
	_ = sdk.LoadJSON(ctx, c.State, key, &msgs)
	if len(msgs) == 0 {
		subMu.Unlock()
		return nil
	}
	err := sdk.SaveJSON(ctx, c.State, key, []string{})
	subMu.Unlock()
	if err != nil {
		return err
	}
	return c.Reply(ctx, strings.Join(msgs, "\n\n"))
}

// changeNotify 生成一条状态变更通知。
func changeNotify(addr string, old, latest snapshot, status *statusResponse, delay time.Duration) string {
	var b strings.Builder
	b.WriteString("[Minecraft服务器状态变更通知]\n")
	b.WriteString("服务器地址：" + addr + "\n")
	if old.Motd != latest.Motd {
		b.WriteString("\n-----[描述变更]-----\n")
		b.WriteString("[旧]\n" + orEmpty(old.Motd) + "\n")
		b.WriteString("[新]\n" + orEmpty(latest.Motd) + "\n")
	}
	if old.Version != latest.Version {
		b.WriteString("\n-----[版本变更]-----\n")
		b.WriteString("[旧]\n" + orEmpty(old.Version) + "\n")
		b.WriteString("[新]\n" + orEmpty(latest.Version) + "\n")
	}
	if old.IconHash != latest.IconHash {
		b.WriteString("\n-----[图标变更]-----\n")
		b.WriteString("[旧]\n" + orEmpty(old.IconHash) + "\n")
		b.WriteString("[新]\n" + orEmpty(latest.IconHash) + "\n")
	}
	if old.Online != latest.Online {
		b.WriteString("\n-----[在线状态]-----\n")
		b.WriteString("[旧]\n" + onlineText(old.Online) + "\n")
		b.WriteString("[新]\n" + onlineText(latest.Online) + "\n")
	} else if old.Players != latest.Players || old.Max != latest.Max {
		b.WriteString("\n-----[在线人数]-----\n")
		fmt.Fprintf(&b, "[旧]\n%d/%d\n", old.Players, old.Max)
		fmt.Fprintf(&b, "[新]\n%d/%d\n", latest.Players, latest.Max)
	}
	b.WriteString("\n-------最新状态-------\n")
	b.WriteString(formatStatus(addr, status, delay))
	return b.String()
}

// orEmpty 在文本为空时返回占位符。
func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(空)"
	}
	return s
}

// onlineText 输出在线状态。
func onlineText(online bool) string {
	if online {
		return "在线"
	}
	return "离线"
}

// isAdmin 使用插件配置中的 admin_users 做授权。
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

// adminUsers 兼容 []string 与 []any 两种配置形态。
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

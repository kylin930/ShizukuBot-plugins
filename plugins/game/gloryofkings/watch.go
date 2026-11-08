package gloryofkings

// 观战：上游 watchBattle 依赖独立 Node 服务（控制面 8898 + 播放面 8899）推流，
// 「谁在打游戏」又依赖群成员索引与常驻快照，QQ 官方机器人都用不了。
// 这里只保留「用户触发一次、查一次」的在线/对局状态查询，
// 数据口径与上游 utils/pushStore.js 的 fetchOnlineState / fetchLatest 一致。

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"shizukubot/sdk"
)

// maxOnlineQuery 一次最多查几个营地号，避免多号用户一条指令打出一串请求。
const maxOnlineQuery = 5

const (
	onlineKeyUnknown = "unknown"
	onlineKeyOffline = "offline"
	onlineKeyOnline  = "online"
	onlineKeyIdle    = "idle"
	onlineKeyPlaying = "playing"
)

// gamingInfo 是战绩列表响应里的「当前这一局」快照。
// 实测字段：isGaming / dtEventTime / heroId / heroIcon / mapName / duration。
type gamingInfo struct {
	IsGaming    bool        `json:"isGaming"`
	HeroID      scalarValue `json:"heroId"`
	HeroIcon    string      `json:"heroIcon"`
	MapName     string      `json:"mapName"`
	Duration    scalarValue `json:"duration"`
	DtEventTime scalarValue `json:"dtEventTime"`
}

type onlineStatus struct {
	Key   string
	Label string
}

// onlineStatusOf 判定展示状态。
//
// 关键口径（上游实测）：gameOnline=2 只代表「游戏客户端开着」，
// 大厅、匹配中、翻战绩都算 2，**不等于在对局**；真正在打的判据只有 isGaming。
func onlineStatusOf(gameOnline int, onlineTime, offlineTime int64, gaming *gamingInfo) onlineStatus {
	if gaming != nil && gaming.IsGaming {
		return onlineStatus{Key: onlineKeyPlaying, Label: "对局中"}
	}
	switch gameOnline {
	case 2:
		return onlineStatus{Key: onlineKeyIdle, Label: "游戏中"}
	case 1:
		return onlineStatus{Key: onlineKeyOnline, Label: "在线"}
	case 0:
		if onlineTime > 0 || offlineTime > 0 {
			return onlineStatus{Key: onlineKeyOffline, Label: "离线"}
		}
		return onlineStatus{Key: onlineKeyUnknown, Label: "未公开在线状态"}
	default:
		return onlineStatus{Key: onlineKeyUnknown, Label: "未公开在线状态"}
	}
}

// profileMainRole 取主角色的在线状态。主角色认 targetRoleId，取不到退回第一个。
func profileMainRole(data *profileData) *profileRole {
	if data == nil || len(data.RoleList) == 0 {
		return nil
	}
	target := strings.TrimSpace(data.TargetRoleID.String())
	if target != "" && target != "0" {
		for index := range data.RoleList {
			if strings.TrimSpace(data.RoleList[index].RoleID.String()) == target {
				return &data.RoleList[index]
			}
		}
	}
	return &data.RoleList[0]
}

// isOnlineCommand 只认明确指向「我自己的在线状态」的指令。
// 「谁在玩」这类群名单不做：本插件没有群成员索引。
func isOnlineCommand(body string) bool {
	switch body {
	case "王者在线", "营地在线", "在线状态", "王者在线状态", "营地在线状态":
		return true
	}
	return false
}

// ---------- 查一个号 ----------

type onlineEntry struct {
	CampID   string
	Name     string
	Status   onlineStatus
	HeroIcon string
	Mode     string
	Minutes  int
	Note     string
}

func fetchOnlineEntry(ctx context.Context, client *campClient, state *userState, campID string) onlineEntry {
	entry := onlineEntry{CampID: campID, Name: accountLabel(state, campID)}

	cred := state.bestCredential(campID)
	if cred == nil {
		entry.Status = onlineStatus{Key: onlineKeyUnknown, Label: "查询失败"}
		entry.Note = "没有可用的营地登录态"
		return entry
	}
	auth := cred.auth()

	profile, err := client.getProfile(ctx, campID, auth)
	if err != nil {
		entry.Status = onlineStatus{Key: onlineKeyUnknown, Label: "查询失败"}
		entry.Note = signErrorText(err)
		return entry
	}
	role := profileMainRole(profile)
	if role == nil {
		entry.Status = onlineStatus{Key: onlineKeyUnknown, Label: "查询失败"}
		entry.Note = "营地没有返回角色"
		return entry
	}
	if name := cleanRoleName(role.RoleName); name != "" {
		entry.Name = name
	}

	gameOnline := role.GameOnline.Int()
	onlineTime, _ := role.OnlineTime.Int64()
	offlineTime, _ := role.OfflineTime.Int64()

	// 客户端开着才可能在对局，这时才多打一次战绩列表；hidden 的号不显示对局信息。
	var gaming *gamingInfo
	if gameOnline == 2 {
		data, err := client.getMoreBattleList(ctx, campID, auth, 0)
		if err == nil && data != nil && !data.Invisible {
			gaming = data.Gaming
		}
	}

	entry.Status = onlineStatusOf(gameOnline, onlineTime, offlineTime, gaming)
	if gaming != nil && gaming.IsGaming {
		entry.HeroIcon = strings.TrimSpace(gaming.HeroIcon)
		entry.Mode = strings.TrimSpace(gaming.MapName)
		entry.Minutes = gaming.Duration.Int()
	}
	return entry
}

// ---------- 视图 ----------

type onlineRow struct {
	No       int
	CampID   string
	Name     string
	Key      string
	Label    string
	HeroIcon string
	Mode     string
	Minutes  string
	Note     string
}

type onlinePageView struct {
	Avatar      string
	Who         string
	Rows        []onlineRow
	GeneratedAt string
}

func buildOnlinePage(state *userState, entries []onlineEntry, avatar string) onlinePageView {
	page := onlinePageView{
		Avatar:      avatar,
		Who:         html.EscapeString(accountLabel(state, state.currentID())),
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	for index, entry := range entries {
		row := onlineRow{
			No:       index + 1,
			CampID:   html.EscapeString(entry.CampID),
			Name:     html.EscapeString(entry.Name),
			Key:      entry.Status.Key,
			Label:    html.EscapeString(entry.Status.Label),
			HeroIcon: html.EscapeString(entry.HeroIcon),
			Mode:     html.EscapeString(entry.Mode),
			Note:     html.EscapeString(entry.Note),
		}
		if entry.Minutes > 0 {
			row.Minutes = fmt.Sprintf("已进行 %d 分钟", entry.Minutes)
		}
		page.Rows = append(page.Rows, row)
	}
	return page
}

func onlineTextFallback(entries []onlineEntry) string {
	if len(entries) == 0 {
		return "没有查到在线状态"
	}
	lines := []string{"王者营地在线状态："}
	for _, entry := range entries {
		line := fmt.Sprintf("%s（%s）：%s", entry.Name, entry.CampID, entry.Status.Label)
		if entry.Status.Key == onlineKeyPlaying {
			detail := entry.Mode
			if detail == "" {
				detail = "对局"
			}
			if entry.Minutes > 0 {
				detail += fmt.Sprintf(" · 已进行 %d 分钟", entry.Minutes)
			}
			line += " · " + detail
		}
		if entry.Note != "" {
			line += " —— " + entry.Note
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ---------- 指令 ----------

// watchCommand 处理 #王者在线：逐个查本人绑定的营地号，一次一条指令、不落盘、不轮询。
func watchCommand(ctx context.Context, c sdk.Call) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return sendBindGuide(ctx, c, "还没有绑定营地号")
	}

	ids := state.IDs
	truncated := false
	if len(ids) > maxOnlineQuery {
		ids = ids[:maxOnlineQuery]
		truncated = true
	}

	client := newCampClient()
	entries := make([]onlineEntry, 0, len(ids))
	for _, campID := range ids {
		entries = append(entries, fetchOnlineEntry(ctx, client, state, campID))
	}

	view := buildOnlinePage(state, entries, avatarURL(c.Event.UserID))
	if image, err := renderOnline(ctx, view); err == nil && len(image) > 0 {
		if err := c.ReplyImageBytes(ctx, image); err != nil {
			return err
		}
	} else if err := c.Reply(ctx, onlineTextFallback(entries)); err != nil {
		return err
	}

	if truncated {
		return c.Reply(ctx, fmt.Sprintf("绑定超过 %d 个营地号，这次只查了前 %d 个", maxOnlineQuery, maxOnlineQuery))
	}
	return nil
}

// Package steam Steam 查询：查看玩家状态与正在玩的游戏，群内可订阅并手动拉取变化。
package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	stateKey = "steam"
	// maxSubs 每个群最多订阅的玩家数量。
	maxSubs = 5
	// pullCooldown 手动拉取的最小间隔。
	pullCooldown = time.Minute
)

var (
	// 下列地址均为包级变量，测试中替换为 httptest 服务。
	summaryURL = "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v2/?key=%s&steamids=%s"
	resolveURL = "https://api.steampowered.com/ISteamUser/ResolveVanityURL/v1/?key=%s&vanityurl=%s"

	httpClient = &http.Client{Timeout: 10 * time.Second}
)

// Definition subscribes to the Steam query and subscription commands.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "steam",
		Name:        "Steam查询",
		Version:     "1.0.0",
		Description: "发送「steam <SteamID/自定义ID>」查询玩家状态；管理员配置 API Key，群成员可管理订阅",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "steam", Trigger: "steam"},
	},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许绑定 API Key 与管理 Steam 订阅的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// steamSub 是某个群对一位玩家的订阅。
type steamSub struct {
	SteamID string `json:"steam_id"`
	Name    string `json:"name"`
	State   int    `json:"state"`
	Game    string `json:"game"`
}

// steamStore 保存全局 API Key 与按群订阅。
type steamStore struct {
	APIKey string                `json:"api_key"`
	Groups map[string][]steamSub `json:"groups"`
	Pulled map[string]int64      `json:"pulled"`
}

// Handle 路由各条 Steam 指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	switch {
	case sdk.MatchFull(text, "steam"), sdk.MatchFull(text, "steam帮助"), sdk.MatchFull(text, "Steam帮助"),
		sdk.MatchFull(text, "Steam"):
		return c.Reply(ctx, helpText)
	case sdk.MatchFull(text, "查看apikey"):
		return viewKey(ctx, c)
	case sdk.MatchFull(text, "steam订阅列表"), sdk.MatchFull(text, "Steam订阅列表"),
		sdk.MatchFull(text, "steam查询订阅"), sdk.MatchFull(text, "Steam查询订阅"):
		return listSubs(ctx, c)
	case sdk.MatchFull(text, "steam推送"), sdk.MatchFull(text, "Steam推送"),
		sdk.MatchFull(text, "拉取steam订阅"), sdk.MatchFull(text, "拉取Steam订阅"):
		return pull(ctx, c)
	}
	if rest, ok := matchBindKey(text); ok {
		return bindKey(ctx, c, rest)
	}
	for _, prefix := range []string{"steam添加订阅", "Steam添加订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return addSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"steam删除订阅", "Steam删除订阅"} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return removeSub(ctx, c, rest)
		}
	}
	for _, prefix := range []string{"steam ", "Steam "} {
		if rest, ok := sdk.MatchPrefix(text, prefix); ok {
			return queryPlayer(ctx, c, rest)
		}
	}
	return nil
}

const helpText = "Steam 指令：\n" +
	"steam <SteamID 或自定义 ID> — 查询玩家状态\n" +
	"steam订阅列表 / steam查询订阅 — 查看本群订阅\n" +
	"添加/删除：steam添加订阅 <ID>、steam删除订阅 <ID>（群成员可用）\n" +
	"steam推送 / 拉取steam订阅 — 管理员手动拉取一次变化（每分钟最多一次）\n" +
	"私聊管理员发送「steam绑定 api key <Key>」配置 Web API Key\n" +
	"私聊管理员发送「查看apikey」查看已配置的 Key（脱敏）"

// matchBindKey 兼容上游「steam绑定 api key」的空格写法。
func matchBindKey(text string) (string, bool) {
	lower := strings.ToLower(text)
	if !strings.HasPrefix(lower, "steam绑定") {
		return "", false
	}
	rest := strings.TrimSpace(text[len("steam绑定"):])
	lowerRest := strings.ToLower(rest)
	for _, prefix := range []string{"api key", "apikey"} {
		if strings.HasPrefix(lowerRest, prefix) {
			return strings.TrimSpace(rest[len(prefix):]), true
		}
	}
	return "", false
}

// bindKey 在私聊中保存全局 Steam Web API Key。
func bindKey(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID != "" || c.Event.GuildID != "" {
		return c.Reply(ctx, "该指令仅在私聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	key := strings.TrimSpace(arg)
	if key == "" {
		return c.Reply(ctx, "用法：steam绑定 api key <Key>，可在 https://steamcommunity.com/dev/apikey 获取")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	st.APIKey = key
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存配置失败："+err.Error())
	}
	return c.Reply(ctx, "Steam Web API Key 已保存")
}

// viewKey 在私聊中向管理员展示脱敏后的 API Key。
func viewKey(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID != "" || c.Event.GuildID != "" {
		return c.Reply(ctx, "该指令仅在私聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	if st.APIKey == "" {
		return c.Reply(ctx, "尚未配置 Steam Web API Key，请在私聊由管理员发送「steam绑定apikey <Key>」")
	}
	return c.Reply(ctx, "apikey为: "+maskKey(st.APIKey))
}

// maskKey 只保留前 4 后 4 位，中间用 **** 代替。
func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// queryPlayer 查询玩家资料与状态。
func queryPlayer(ctx context.Context, c sdk.Call, arg string) error {
	input := strings.TrimSpace(arg)
	if input == "" {
		return c.Reply(ctx, "用法：steam 76561197960287930")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	key, err := requireKey(st)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	id, err := resolveID(ctx, key, input)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	player, err := fetchPlayer(ctx, key, id)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	return c.Reply(ctx, formatPlayer(player))
}

// addSub 新增订阅并记录当前状态作为基线；与上游一致，群成员即可操作。
func addSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	key, err := requireKey(st)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	id, err := resolveID(ctx, key, arg)
	if err != nil {
		return c.Reply(ctx, "订阅失败："+err.Error())
	}
	player, err := fetchPlayer(ctx, key, id)
	if err != nil {
		return c.Reply(ctx, "订阅失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	for _, sub := range subs {
		if sub.SteamID == player.SteamID {
			return c.Reply(ctx, "已经订阅了 "+player.PersonaName)
		}
	}
	if len(subs) >= maxSubs {
		return c.Reply(ctx, fmt.Sprintf("本群订阅已达上限 %d 个，请先删除部分订阅", maxSubs))
	}
	st.Groups[c.Event.GroupID] = append(subs, steamSub{
		SteamID: player.SteamID,
		Name:    player.PersonaName,
		State:   player.PersonaState,
		Game:    player.GameExtrainfo,
	})
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, "已订阅 "+player.PersonaName+"，发送「steam推送」拉取变化")
}

// removeSub 删除订阅；与上游一致，群成员即可操作。
func removeSub(ctx context.Context, c sdk.Call, arg string) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	key, err := requireKey(st)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	id, err := resolveID(ctx, key, arg)
	if err != nil {
		return c.Reply(ctx, "删除失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	kept := subs[:0]
	for _, sub := range subs {
		if sub.SteamID != id {
			kept = append(kept, sub)
		}
	}
	if len(kept) == len(subs) {
		return c.Reply(ctx, "本群没有订阅该玩家")
	}
	st.Groups[c.Event.GroupID] = kept
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存订阅失败："+err.Error())
	}
	return c.Reply(ctx, "已取消订阅 "+id)
}

// listSubs 列出本群订阅。
func listSubs(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	subs := st.Groups[c.Event.GroupID]
	if len(subs) == 0 {
		return c.Reply(ctx, "本群还没有 Steam 订阅，管理员可发送「steam添加订阅 <ID>」")
	}
	lines := []string{fmt.Sprintf("本群 Steam 订阅（%d/%d）：", len(subs), maxSubs)}
	for _, sub := range subs {
		line := "· " + sub.Name + "（" + sub.SteamID + "，状态：" + stateText(sub.State)
		if sub.Game != "" {
			line += "，正在玩：" + sub.Game
		}
		lines = append(lines, line+"）")
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

// pull 手动拉取全部订阅的状态变化。
func pull(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return c.Reply(ctx, "该指令仅在群聊可用")
	}
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	st, err := loadStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取配置失败："+err.Error())
	}
	key, err := requireKey(st)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}
	if wait := pullCooldown - time.Duration(time.Now().Unix()-st.Pulled[c.Event.GroupID])*time.Second; wait > 0 {
		return c.Reply(ctx, fmt.Sprintf("拉取太频繁，请 %d 秒后再试", int(wait.Seconds())+1))
	}
	subs := st.Groups[c.Event.GroupID]
	if len(subs) == 0 {
		return c.Reply(ctx, "本群还没有 Steam 订阅，管理员可发送「steam添加订阅 <ID>」")
	}

	var updates []string
	var failed []string
	for i := range subs {
		sub := &subs[i]
		player, err := fetchPlayer(ctx, key, sub.SteamID)
		if err != nil {
			failed = append(failed, sub.Name)
			continue
		}
		if msg := changeText(*sub, steamSub{State: player.PersonaState, Game: player.GameExtrainfo}); msg != "" {
			updates = append(updates, "· "+player.PersonaName+" "+msg)
		}
		sub.Name = player.PersonaName
		sub.State = player.PersonaState
		sub.Game = player.GameExtrainfo
	}
	st.Groups[c.Event.GroupID] = subs
	st.Pulled[c.Event.GroupID] = time.Now().Unix()
	if err := sdk.SaveJSON(ctx, c.State, stateKey, st); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	if len(updates) == 0 {
		msg := "本次没有状态变化"
		if len(failed) > 0 {
			msg += "；获取失败：" + strings.Join(failed, "、")
		}
		return c.Reply(ctx, msg)
	}
	msg := "【Steam 更新】\n" + strings.Join(updates, "\n")
	if len(failed) > 0 {
		msg += "\n获取失败：" + strings.Join(failed, "、")
	}
	return c.Reply(ctx, msg)
}

// changeText 比较订阅基线与最新状态，返回需要播报的变化。
func changeText(old, now steamSub) string {
	var parts []string
	switch {
	case old.State == 0 && now.State != 0:
		parts = append(parts, "上线了")
	case old.State != 0 && now.State == 0:
		parts = append(parts, "下线了")
	case old.State != now.State:
		parts = append(parts, "状态变为「"+stateText(now.State)+"」")
	}
	if now.Game != "" && now.Game != old.Game {
		parts = append(parts, "开始玩："+now.Game)
	} else if now.Game == "" && old.Game != "" {
		parts = append(parts, "停止玩："+old.Game)
	}
	return strings.Join(parts, "；")
}

// player 是 GetPlayerSummaries 返回的玩家信息。
type player struct {
	SteamID                  string `json:"steamid"`
	PersonaName              string `json:"personaname"`
	ProfileURL               string `json:"profileurl"`
	PersonaState             int    `json:"personastate"`
	GameExtrainfo            string `json:"gameextrainfo"`
	GameID                   string `json:"gameid"`
	CommunityVisibilityState int    `json:"communityvisibilitystate"`
}

// summaryResp 玩家信息接口响应。
type summaryResp struct {
	Response struct {
		Players []player `json:"players"`
	} `json:"response"`
}

// resolveResp 自定义 ID 解析接口响应。
type resolveResp struct {
	Response struct {
		Success int    `json:"success"`
		SteamID string `json:"steamid"`
		Message string `json:"message"`
	} `json:"response"`
}

// resolveID 把自定义 ID 解析为 SteamID64，纯数字则直接返回。
func resolveID(ctx context.Context, key, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("缺少 SteamID")
	}
	if _, err := strconv.ParseUint(input, 10, 64); err == nil {
		return input, nil
	}
	var resp resolveResp
	rawURL := fmt.Sprintf(resolveURL, url.QueryEscape(key), url.QueryEscape(input))
	if err := getJSON(ctx, rawURL, &resp); err != nil {
		return "", err
	}
	if resp.Response.Success != 1 || resp.Response.SteamID == "" {
		return "", errors.New("找不到该自定义 ID")
	}
	return resp.Response.SteamID, nil
}

// fetchPlayer 查询单个玩家的公开资料。
func fetchPlayer(ctx context.Context, key, steamID string) (*player, error) {
	var resp summaryResp
	rawURL := fmt.Sprintf(summaryURL, url.QueryEscape(key), url.QueryEscape(steamID))
	if err := getJSON(ctx, rawURL, &resp); err != nil {
		return nil, err
	}
	if len(resp.Response.Players) == 0 {
		return nil, errors.New("未找到该玩家，或资料不可见")
	}
	return &resp.Response.Players[0], nil
}

// getJSON 发起 GET 请求并解析 JSON。
func getJSON(ctx context.Context, rawURL string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("接口返回 " + resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("响应解析失败")
	}
	return nil
}

// requireKey 返回已配置的 API Key。
func requireKey(st *steamStore) (string, error) {
	if st.APIKey == "" {
		return "", errors.New("尚未配置 Steam Web API Key，请在私聊由管理员发送「steam绑定apikey <Key>」")
	}
	return st.APIKey, nil
}

// formatPlayer 拼出玩家信息文本。
func formatPlayer(p *player) string {
	lines := []string{"【Steam】" + p.PersonaName}
	if p.CommunityVisibilityState == 1 {
		lines = append(lines, "状态：私密资料")
	} else {
		status := stateText(p.PersonaState)
		if p.GameExtrainfo != "" {
			status += "｜正在玩：" + p.GameExtrainfo
		}
		lines = append(lines, "状态："+status)
	}
	if p.ProfileURL != "" {
		lines = append(lines, p.ProfileURL)
	}
	return strings.Join(lines, "\n")
}

// stateText 把 personastate 转成中文。
func stateText(state int) string {
	switch state {
	case 0:
		return "离线"
	case 1:
		return "在线"
	case 2:
		return "忙碌"
	case 3:
		return "离开"
	case 4:
		return "打盹"
	case 5:
		return "想交易"
	case 6:
		return "想玩游戏"
	default:
		return "未知"
	}
}

// loadStore 读取状态，保证 map 已初始化。
func loadStore(ctx context.Context, c sdk.Call) (*steamStore, error) {
	st := &steamStore{}
	if err := sdk.LoadJSON(ctx, c.State, stateKey, st); err != nil {
		return nil, err
	}
	if st.Groups == nil {
		st.Groups = map[string][]steamSub{}
	}
	if st.Pulled == nil {
		st.Pulled = map[string]int64{}
	}
	return st, nil
}

const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

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

// Package minecraftobserver 查询 Minecraft Java 版服务器状态，并维护服务器订阅。
// 上游依赖 job 插件定时触发「mc服务器订阅拉取」；本插件保留同样的指令，
// 拉取到的其他会话变更会在该会话下次发消息时补发。
package minecraftobserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// defaultPort 是 Minecraft Java 版的默认端口。
	defaultPort = "25565"
	// maxBody 限制上游响应体大小，避免异常响应占用内存。
	maxBody = 1 << 20
)

var (
	// apiURL 为包级变量，测试中替换为 httptest 服务地址。
	apiURL     = "https://api.mcsrvstat.us/3/%s"
	httpClient = &http.Client{Timeout: 10 * time.Second}
	// queryRe 保留上游的触发词「mc服务器状态 <地址>」，大小写不敏感。
	queryRe = regexp.MustCompile(`(?i)^mc服务器状态\s*(.*)$`)
)

// Definition 注册 Minecraft 服务器状态查询与订阅插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:      "minecraftobserver",
		Name:    "Minecraft服务器状态",
		Version: "1.1.0",
		Description: "发送「mc服务器状态 <地址>」查询 Java 版服务器状态（在线状态、版本、MOTD、玩家数、延迟、模组）；" +
			"「mc服务器添加订阅 / 取消订阅 / 订阅列表 / 订阅拉取」管理订阅并推送状态变更",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "minecraftobserver", Trigger: "mc服务器状态"},
	},
		sdk.WithPermissions("state.read", "state.write"),
		sdk.WithConfig(sdk.Schema{
			Type:        "object",
			Description: "Minecraft 服务器订阅配置",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许执行「mc服务器全局订阅列表」的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

const helpText = "Minecraft 服务器状态查询与订阅：\n" +
	"mc服务器状态 <地址> — 查询 Java 版服务器状态\n" +
	"mc服务器添加订阅 <地址> — 添加状态变更订阅\n" +
	"mc服务器取消订阅 <地址> — 取消订阅\n" +
	"mc服务器订阅列表 — 查看当前会话的订阅\n" +
	"mc服务器订阅拉取 — 立即检查一次状态变更\n" +
	"mc服务器全局订阅列表 — 管理员查看全部订阅\n" +
	"地址支持域名或域名:端口，缺省端口为 25565\n" +
	"例：mc服务器状态 mc.hypixel.net"

// Handle 路由 Minecraft 服务器状态查询与订阅指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	// 先补发本会话积压的订阅通知。
	if err := flushPending(ctx, c); err != nil {
		return err
	}
	if matched := sdk.MatchRegexp(text, subAddRe); matched != nil {
		return addSubscription(ctx, c, matched[1])
	}
	if matched := sdk.MatchRegexp(text, subDelRe); matched != nil {
		return removeSubscription(ctx, c, matched[1])
	}
	if sdk.MatchRegexp(text, subListRe) != nil {
		return listSubscriptions(ctx, c)
	}
	if sdk.MatchRegexp(text, subAllRe) != nil {
		return globalSubscriptions(ctx, c)
	}
	if sdk.MatchRegexp(text, subPullRe) != nil {
		return pullSubscriptions(ctx, c)
	}
	matched := sdk.MatchRegexp(text, queryRe)
	if matched == nil {
		return nil
	}
	addr := strings.TrimSpace(matched[1])
	if addr == "" {
		return c.Reply(ctx, helpText)
	}
	return query(ctx, c, addr)
}

// query 校验地址并查询服务器状态。
func query(ctx context.Context, c sdk.Call, raw string) error {
	addr, err := normalizeAddr(raw)
	if err != nil {
		return c.Reply(ctx, "地址格式不正确："+err.Error())
	}
	status, delay, err := fetchStatus(ctx, addr)
	if err != nil {
		return c.Reply(ctx, "服务器状态获取失败... 错误信息: "+err.Error())
	}
	return sendStatus(ctx, c, addr, status, delay)
}

// sendStatus 先发送服务器图标（接口提供时），再发送状态文本。
func sendStatus(ctx context.Context, c sdk.Call, addr string, status *statusResponse, delay time.Duration) error {
	if icon := statusIcon(status); len(icon) > 0 {
		// 图标发送失败不影响文字状态。
		_ = c.ReplyImageBytes(ctx, icon)
	}
	return c.Reply(ctx, formatStatus(addr, status, delay))
}

// statusIcon 解析接口返回的 data URI 图标。
func statusIcon(status *statusResponse) []byte {
	if status == nil || !strings.HasPrefix(status.Icon, "data:") {
		return nil
	}
	i := strings.Index(status.Icon, ",")
	if i < 0 {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(status.Icon[i+1:])
	if err != nil {
		return nil
	}
	return data
}

// normalizeAddr 解析域名/IP 与端口，缺省端口补 25565。
func normalizeAddr(raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", errors.New("地址不能为空")
	}
	// 兼容带协议前缀或末尾斜杠的写法。
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	addr = strings.TrimSuffix(addr, "/")

	host, port := addr, defaultPort
	switch {
	case strings.HasPrefix(addr, "["):
		// 带方括号的 IPv6，可能附带端口。
		if h, p, err := net.SplitHostPort(addr); err == nil {
			host, port = h, p
		} else {
			host = strings.Trim(addr, "[]")
		}
	case strings.Count(addr, ":") == 1:
		h, p, err := net.SplitHostPort(addr)
		if err != nil {
			return "", errors.New("端口格式不正确")
		}
		host, port = h, p
	case strings.Count(addr, ":") > 1:
		// 裸 IPv6 地址，使用默认端口。
		host = addr
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("主机名为空")
	}
	if strings.ContainsAny(host, " /?#") {
		return "", errors.New("主机名包含非法字符")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("端口应为 1-65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// statusResponse 是 mcsrvstat.us 返回的服务器状态。
type statusResponse struct {
	Online  bool   `json:"online"`
	Version string `json:"version"`
	// Protocol 在 Version 为空时提供版本名。
	Protocol struct {
		Name string `json:"name"`
	} `json:"protocol"`
	Motd struct {
		Raw   []string `json:"raw"`
		Clean []string `json:"clean"`
	} `json:"motd"`
	Players struct {
		Online int `json:"online"`
		Max    int `json:"max"`
	} `json:"players"`
	Mods []modInfo `json:"mods"`
	// Icon 是服务器图标，通常是 data:image/png;base64,...。
	Icon string `json:"icon"`
}

// modInfo 是服务器返回的单个模组信息。
type modInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// fetchStatus 请求上游接口并解析状态，同时返回请求耗时。
func fetchStatus(ctx context.Context, addr string) (*statusResponse, time.Duration, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf(apiURL, url.PathEscape(addr)), nil)
	if err != nil {
		return nil, 0, errors.New("请求创建失败")
	}
	req.Header.Set("User-Agent", "ShizukuBot-minecraftobserver/1.0")

	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	// 使用 HTTP 接口，往返耗时用于近似上游的 Ping 延迟。
	delay := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		return nil, 0, errors.New("接口返回 " + resp.Status)
	}

	var status statusResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&status); err != nil {
		return nil, 0, errors.New("响应解析失败")
	}
	return &status, delay, nil
}

// formatStatus 按上游的回复格式生成状态文本。
func formatStatus(addr string, status *statusResponse, delay time.Duration) string {
	if status == nil || !status.Online {
		return "【Minecraft 服务器状态】\n" +
			"服务器地址：" + addr + "\n" +
			"状态：离线（服务器未响应或地址不存在）"
	}

	lines := []string{"【Minecraft 服务器状态】"}
	if motd := motdText(status); motd != "" {
		lines = append(lines, motd)
	}
	lines = append(lines, "服务器地址："+addr, "状态：在线")

	version := strings.TrimSpace(status.Version)
	if version == "" {
		version = strings.TrimSpace(status.Protocol.Name)
	}
	if version != "" {
		lines = append(lines, "版本："+version)
	}
	lines = append(lines,
		fmt.Sprintf("Ping延迟：%d 毫秒", delay.Milliseconds()),
		fmt.Sprintf("在线人数：%d/%d", status.Players.Online, status.Players.Max),
	)
	if mods := modText(status.Mods); mods != "" {
		lines = append(lines, "模组："+mods)
	}
	return strings.Join(lines, "\n")
}

// motdText 优先使用去色 MOTD，缺失时退回原始文本。
func motdText(status *statusResponse) string {
	lines := status.Motd.Clean
	if len(lines) == 0 {
		lines = status.Motd.Raw
	}
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// modText 格式化模组信息，最多展示 10 个。
func modText(mods []modInfo) string {
	if len(mods) == 0 {
		return ""
	}
	limit := len(mods)
	if limit > 10 {
		limit = 10
	}
	names := make([]string, 0, limit+1)
	for _, mod := range mods[:limit] {
		name := strings.TrimSpace(mod.Name)
		if name == "" {
			continue
		}
		if version := strings.TrimSpace(mod.Version); version != "" {
			name += " " + version
		}
		names = append(names, name)
	}
	if len(mods) > limit {
		names = append(names, fmt.Sprintf("等 %d 个", len(mods)))
	}
	return strings.Join(names, "、")
}

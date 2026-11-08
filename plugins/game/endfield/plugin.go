package endfield

import (
	"context"
	"sort"
	"strings"
	"sync"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const pluginVersion = "2.2.0"

// Definition 注册「:命令 / #zmd命令」指令族。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "endfield",
		Name:        "终末地",
		Version:     pluginVersion,
		Description: "终末地查询：账号绑定、便签、角色、危机合约、公告、日历、抽卡、Wiki 与 MaaEnd",
	}, append(append([]sdk.EventType{}, sdk.MessageEvents...), sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage),
		Handle, []triggercmd.Command{{Name: "endfield", Trigger: ":帮助"}},
		sdk.WithPermissions("state.read", "state.write", "config.read"), sdk.WithConfig(configSchema()))
}

// handler 处理一条已经去掉前缀与指令名的消息。
type handler func(ctx context.Context, c sdk.Call, cfg config, args string, text string) error

type route struct {
	priority int
	match    func(text string) (string, bool)
	run      handler
}

var (
	routesMu     sync.Mutex
	routes       []route
	routesSorted bool
)

// registerRoute 注册一条指令；priority 越大越先匹配。
func registerRoute(priority int, match func(string) (string, bool), run handler) {
	routesMu.Lock()
	defer routesMu.Unlock()
	routes = append(routes, route{priority: priority, match: match, run: run})
	routesSorted = false
}

func sortedRoutes() []route {
	routesMu.Lock()
	defer routesMu.Unlock()
	if !routesSorted {
		sort.SliceStable(routes, func(i, j int) bool { return routes[i].priority > routes[j].priority })
		routesSorted = true
	}
	out := make([]route, len(routes))
	copy(out, routes)
	return out
}

var prefixes = []string{":", "：", "/zmd", "#zmd", "/终末地", "#终末地"}

// stripPrefix 去掉上游支持的 6 种命令前缀。
func stripPrefix(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, prefix := range prefixes {
		if len(text) < len(prefix) {
			continue
		}
		head := text[:len(prefix)]
		if strings.EqualFold(head, prefix) {
			return strings.TrimSpace(text[len(prefix):]), true
		}
	}
	return "", false
}

// Handle 解析并分发指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text, ok := stripPrefix(sdk.CommandText(c.Event))
	if !ok || text == "" {
		return nil
	}
	cfg := decodeConfig(c.Config)
	for _, r := range sortedRoutes() {
		if args, matched := r.match(text); matched {
			return r.run(ctx, c, cfg, args, text)
		}
	}
	return nil
}

// targetUserID 对齐上游 this.e.at || this.e.user_id。
func targetUserID(c sdk.Call) string {
	if ids := sdk.MentionUserIDs(c.Event); len(ids) > 0 && ids[0] != "" {
		return ids[0]
	}
	return c.Event.UserID
}

// exactMatch 精确匹配指令，返回剩余参数（通常为空）。
func exactMatch(keywords ...string) func(string) (string, bool) {
	return func(text string) (string, bool) {
		for _, keyword := range keywords {
			if text == keyword {
				return "", true
			}
		}
		return "", false
	}
}

// prefixMatch 匹配「指令 参数」或纯指令。
func prefixMatch(keywords ...string) func(string) (string, bool) {
	return func(text string) (string, bool) {
		for _, keyword := range keywords {
			if text == keyword {
				return "", true
			}
			if strings.HasPrefix(text, keyword+" ") {
				return strings.TrimSpace(text[len(keyword):]), true
			}
			if strings.HasPrefix(text, keyword) && hasSpaceAfter(text, keyword) {
				return strings.TrimSpace(text[len(keyword):]), true
			}
		}
		return "", false
	}
}

func hasSpaceAfter(text, keyword string) bool {
	if len(text) <= len(keyword) {
		return false
	}
	rest := text[len(keyword):]
	return strings.TrimSpace(rest) != rest
}

// withPrefixMatch 生成「关键词 + 参数」匹配，参数允许紧跟（如「公告1」）。
func withPrefixMatch(keyword string) func(string) (string, bool) {
	return func(text string) (string, bool) {
		if !strings.HasPrefix(text, keyword) {
			return "", false
		}
		return strings.TrimSpace(text[len(keyword):]), true
	}
}

func firstField(args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// requireBinding 读取当前账号；失败时已回复提示。
func requireBinding(ctx context.Context, c sdk.Call, cfg config) (*userSession, bool, error) {
	if !cfg.hasAPIKey() {
		return nil, false, replyText(ctx, c, getMessage("common.need_api_key"))
	}
	session, ok, err := loadUserSession(ctx, c, cfg)
	if err != nil {
		return nil, false, replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
	}
	if !ok {
		return nil, false, replyText(ctx, c, getMessage("unbind_message"))
	}
	return session, true, nil
}

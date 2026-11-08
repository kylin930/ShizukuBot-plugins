package endfield

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"shizukubot/sdk"
)

const userKeyPrefix = "endfield:user:"

func userKey(userID string) string { return userKeyPrefix + userID }

// binding 对齐上游 Redis 中的一条绑定记录。
type binding struct {
	FrameworkToken string `json:"framework_token,omitempty"`
	BindingID      any    `json:"binding_id,omitempty"`
	UserIdentifier any    `json:"user_identifier,omitempty"`
	RoleID         any    `json:"role_id,omitempty"`
	GameRoleID     any    `json:"game_role_id,omitempty"`
	Nickname       string `json:"nickname,omitempty"`
	ServerID       any    `json:"server_id,omitempty"`
	ServerName     string `json:"server_name,omitempty"`
	ChannelName    string `json:"channel_name,omitempty"`
	IsActive       *bool  `json:"is_active,omitempty"`
	IsPrimary      bool   `json:"is_primary,omitempty"`
	ClientType     string `json:"client_type,omitempty"`
	LoginType      string `json:"login_type,omitempty"`
	BindTime       int64  `json:"bind_time,omitempty"`
	LastSync       int64  `json:"last_sync,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
}

func (b binding) roleID() string { return anyString(b.RoleID) }
func (b binding) serverID() int  { return anyInt(b.ServerID, 1) }
func (b binding) active() bool   { return b.IsActive == nil || *b.IsActive }
func (b binding) timestamp() int64 {
	if b.LastSync > 0 {
		return b.LastSync
	}
	return b.BindTime
}

func anyString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprint(v)
	}
}

func anyInt(value any, fallback int) int {
	switch v := value.(type) {
	case nil:
		return fallback
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

// cleanBindings 过滤已撤销授权并按 role_id 去重。
func cleanBindings(accounts []binding) []binding {
	valid := make([]binding, 0, len(accounts))
	for _, account := range accounts {
		if account.active() {
			valid = append(valid, account)
		}
	}
	byRole := map[string]binding{}
	order := make([]string, 0, len(valid))
	for _, account := range valid {
		role := account.roleID()
		if role == "" {
			continue
		}
		existing, ok := byRole[role]
		if !ok {
			byRole[role] = account
			order = append(order, role)
			continue
		}
		if account.IsPrimary && !existing.IsPrimary {
			byRole[role] = account
		} else if account.IsPrimary == existing.IsPrimary && account.timestamp() > existing.timestamp() {
			byRole[role] = account
		}
	}
	out := make([]binding, 0, len(order))
	for _, role := range order {
		out = append(out, byRole[role])
	}
	if len(out) > 0 && !hasPrimary(out) {
		out[0].IsPrimary = true
	}
	return out
}

func hasPrimary(accounts []binding) bool {
	for _, account := range accounts {
		if account.IsPrimary {
			return true
		}
	}
	return false
}

func loadBindings(ctx context.Context, st sdk.State, userID string) ([]binding, error) {
	if st == nil {
		return nil, nil
	}
	var accounts []binding
	if err := sdk.LoadJSON(ctx, st, userKey(userID), &accounts); err != nil {
		return nil, err
	}
	cleaned := cleanBindings(accounts)
	return cleaned, nil
}

func saveBindings(ctx context.Context, st sdk.State, userID string, accounts []binding) error {
	cleaned := cleanBindings(accounts)
	if len(cleaned) == 0 {
		return sdk.SaveJSON(ctx, st, userKey(userID), []binding{})
	}
	return sdk.SaveJSON(ctx, st, userKey(userID), cleaned)
}

func primaryBinding(accounts []binding) (binding, bool) {
	for _, account := range accounts {
		if account.IsPrimary {
			return account, true
		}
	}
	if len(accounts) > 0 {
		return accounts[0], true
	}
	return binding{}, false
}

// userSession 是某个用户当前激活的账号与已初始化的 API 客户端。
type userSession struct {
	UserID  string
	Binding binding
	Client  *apiClient
}

// loadUserSession 读取当前激活账号；未绑定或缺少 framework_token 时返回 false。
func loadUserSession(ctx context.Context, c sdk.Call, cfg config) (*userSession, bool, error) {
	userID := targetUserID(c)
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil {
		return nil, false, err
	}
	account, ok := primaryBinding(accounts)
	if !ok || account.FrameworkToken == "" {
		return nil, false, nil
	}
	return &userSession{UserID: userID, Binding: account, Client: newAPIClient(cfg, account)}, true, nil
}

// allUserSessions 返回用户全部有效绑定。
func allUserSessions(cfg config, userID string, accounts []binding) []*userSession {
	out := make([]*userSession, 0, len(accounts))
	for _, account := range accounts {
		if account.FrameworkToken == "" {
			continue
		}
		out = append(out, &userSession{UserID: userID, Binding: account, Client: newAPIClient(cfg, account)})
	}
	return out
}

// sortedBindings 用于绑定列表展示：当前账号排在最前。
func sortedBindings(accounts []binding) []binding {
	out := append([]binding(nil), accounts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsPrimary != out[j].IsPrimary {
			return out[i].IsPrimary
		}
		return i < j
	})
	return out
}

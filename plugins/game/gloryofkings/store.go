package gloryofkings

import (
	"context"
	"sync"

	"shizukubot/sdk"
)

// credential 是一次扫码登录得到的营地登录态。不同用户的登录态互相隔离。
type credential struct {
	UserID    string `json:"userId"`
	Token     string `json:"token"`
	UserKey   string `json:"userKey"`
	EncodeRes string `json:"encodeRes"`
	OpenID    string `json:"openId"`
	Platform  string `json:"platform"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	SavedAt   int64  `json:"savedAt"`
	Invalid   bool   `json:"invalid"`
}

// userState 是「用户 → 绑定的营地号列表 → 当前账号 → 登录态」的持久化结构。
type userState struct {
	IDs         []string               `json:"ids"`
	Current     int                    `json:"current"`
	Names       map[string]string      `json:"names"`
	Credentials map[string]*credential `json:"credentials"`
}

var stateMu sync.Mutex

func stateKey(userID string) string { return "gok:user:" + userID }

func loadUserState(ctx context.Context, st sdk.State, userID string) *userState {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := &userState{}
	if err := sdk.LoadJSON(ctx, st, stateKey(userID), out); err != nil {
		return &userState{}
	}
	out.normalize()
	return out
}

func saveUserState(ctx context.Context, st sdk.State, userID string, state *userState) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return sdk.SaveJSON(ctx, st, stateKey(userID), state)
}

func (s *userState) normalize() {
	if s.IDs == nil {
		s.IDs = []string{}
	}
	if s.Names == nil {
		s.Names = map[string]string{}
	}
	if s.Credentials == nil {
		s.Credentials = map[string]*credential{}
	}
	if s.Current < 0 || s.Current >= len(s.IDs) {
		s.Current = 0
	}
}

// currentID 返回当前选中的营地号；没有绑定返回空串。
func (s *userState) currentID() string {
	if len(s.IDs) == 0 {
		return ""
	}
	if s.Current >= 0 && s.Current < len(s.IDs) {
		return s.IDs[s.Current]
	}
	return s.IDs[0]
}

// hasID 判断营地号是否已绑定。
func (s *userState) hasID(id string) bool {
	for _, existing := range s.IDs {
		if existing == id {
			return true
		}
	}
	return false
}

// addID 绑定营地号；已存在时返回 false。
func (s *userState) addID(id string) bool {
	if s.hasID(id) {
		return false
	}
	s.IDs = append(s.IDs, id)
	return true
}

// bindID 绑定营地号并切换到它；已绑定则只切换当前序号。
func (s *userState) bindID(id string) int {
	for index, existing := range s.IDs {
		if existing == id {
			s.Current = index
			return index
		}
	}
	s.IDs = append(s.IDs, id)
	s.Current = len(s.IDs) - 1
	return s.Current
}

// removeID 删除指定序号的营地号，同时保持当前选中项不漂移：
// 删掉的下标小于 current 时 current 左移一位，越界时再夹到末尾。
func (s *userState) removeID(index int) string {
	if index < 0 || index >= len(s.IDs) {
		return ""
	}
	removed := s.IDs[index]
	s.IDs = append(s.IDs[:index], s.IDs[index+1:]...)
	if index < s.Current {
		s.Current--
	}
	if s.Current >= len(s.IDs) {
		s.Current = len(s.IDs) - 1
	}
	if s.Current < 0 {
		s.Current = 0
	}
	delete(s.Names, removed)
	return removed
}

// bestCredential 为某个营地号挑一个可用登录态：优先与营地号同号的账号，
// 否则退到任意一个未失效的登录态（营地允许用任意登录态查公开主页）。
func (s *userState) bestCredential(campID string) *credential {
	if c, ok := s.Credentials[campID]; ok && c.Token != "" && !c.Invalid {
		return c
	}
	for _, c := range s.Credentials {
		if c.Token != "" && !c.Invalid {
			return c
		}
	}
	return nil
}

// campAvatar 取绑定时缓存的营地头像，没有则返回空串（模板里 img 会自动隐藏）。
func (s *userState) campAvatar(campID string) string {
	if cred := s.bestCredential(campID); cred != nil {
		return cred.Avatar
	}
	return ""
}

func (s *userState) putCredential(c *credential) {
	if c == nil || c.UserID == "" {
		return
	}
	s.Credentials[c.UserID] = c
}

func (c *credential) auth() *campAuth {
	if c == nil {
		return nil
	}
	return &campAuth{
		UserID:    c.UserID,
		Token:     c.Token,
		UserKey:   c.UserKey,
		EncodeRes: c.EncodeRes,
		OpenID:    c.OpenID,
	}
}

package endfield

// 账号绑定模块：移植上游 enduid.js 与 hypergryphApi.js 中与绑定相关的全部指令。
// 等待手机验证码/扫码结果的状态放在包级 map 中，键为 botID+source+groupID+userID。

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	authorizationFrontendURL = "https://end.shallow.ink"
	defaultAuthClientName    = "终末地机器人"
	qrPollInterval           = 2 * time.Second
	qrPollTimeout            = 120 * time.Second
	authPollInterval         = 2 * time.Second
	authPollMaxAttempts      = 90 // 约 180 秒
)

var (
	phoneNumberPattern = regexp.MustCompile(`^\d{11}$`)
	phoneCodePattern   = regexp.MustCompile(`^\d{6}$`)
)

func init() {
	// 具体指令优先于「绑定」前缀，避免被 handleCredBind 抢走。
	registerRoute(220, exactMatch("绑定帮助", "登陆帮助", "登录帮助"), handleBindHelp)
	registerRoute(220, exactMatch("绑定列表", "登陆列表", "登录列表"), handleBindList)
	registerRoute(220, exactMatch("我的cred"), handleMyCred)
	registerRoute(220, exactMatch("删除cred"), handleDeleteCred)

	registerRoute(210, exactMatch("扫码绑定", "扫码登陆", "扫码登录"), handleScanQRBind)
	registerRoute(210, exactMatch("授权绑定", "授权登陆", "授权登录"), handleAuthBind)

	for _, keyword := range []string{"手机绑定", "手机登陆", "手机登录"} {
		registerRoute(210, withPrefixMatch(keyword), handlePhoneBind)
	}
	for _, keyword := range []string{"切换绑定", "切换登陆", "切换登录"} {
		registerRoute(210, withPrefixMatch(keyword), handleSwitchBind)
	}
	for _, keyword := range []string{"删除绑定", "删除登陆", "删除登录"} {
		registerRoute(210, withPrefixMatch(keyword), handleDeleteBind)
	}

	registerRoute(100, prefixMatch("绑定", "登陆", "登录"), handleCredBind)

	// 最后兜底：消费等待中的手机验证码，其余情况静默返回。
	registerRoute(1, matchAnyText, handlePendingInput)
}

func matchAnyText(string) (string, bool) { return "", true }

// ---------- 上下文辅助 ----------

func botID(c sdk.Call) string { return strings.TrimSpace(c.Event.BotID) }

func isPrivateChat(c sdk.Call) bool {
	source := string(c.Event.Source)
	return source == "c2c" || source == "direct"
}

func resolvedClientID(c sdk.Call) string {
	if id := botID(c); id != "" {
		return id
	}
	if c.Event.UserID != "" {
		return "bot-" + c.Event.UserID
	}
	return "bot"
}

// ---------- 等待态 ----------

type waitKind string

const (
	waitPhone waitKind = "phone"
	waitQR    waitKind = "qr"
)

type waitState struct {
	kind           waitKind
	phone          string
	frameworkToken string
	expiresAt      time.Time
}

var (
	waitMu     sync.Mutex
	waitStates = map[string]*waitState{}
)

func waitKey(c sdk.Call) string {
	return strings.Join([]string{
		c.Event.BotID,
		string(c.Event.Source),
		c.Event.GroupID,
		c.Event.UserID,
	}, "|")
}

func setWait(c sdk.Call, state *waitState) {
	waitMu.Lock()
	defer waitMu.Unlock()
	waitStates[waitKey(c)] = state
}

func peekWait(c sdk.Call) (*waitState, bool) {
	waitMu.Lock()
	defer waitMu.Unlock()
	state, ok := waitStates[waitKey(c)]
	return state, ok
}

func removeWait(c sdk.Call) {
	waitMu.Lock()
	defer waitMu.Unlock()
	delete(waitStates, waitKey(c))
}

// handlePendingInput 是优先级最低的兜底路由：只消费等待中的手机验证码。
func handlePendingInput(ctx context.Context, c sdk.Call, cfg config, _ string, text string) error {
	state, ok := peekWait(c)
	if !ok || state == nil {
		return nil
	}
	if !state.expiresAt.IsZero() && time.Now().After(state.expiresAt) {
		removeWait(c)
		if state.kind == waitPhone {
			return replyText(ctx, c, getMessage("enduid.phone_code_expired"))
		}
		return nil
	}
	if state.kind != waitPhone {
		// 扫码流程在指令内联轮询，这里无需额外处理。
		return nil
	}
	code := strings.TrimSpace(text)
	if !phoneCodePattern.MatchString(code) {
		return nil
	}
	removeWait(c)
	return completePhoneLogin(ctx, c, cfg, state, code)
}

// ---------- 绑定帮助 / 列表 / Cred ----------

func handleBindHelp(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	return replyText(ctx, c, getMessage("enduid.bind_help", map[string]any{"prefix": ":"}))
}

func handleBindList(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	accounts, err := loadBindings(ctx, c.State, c.Event.UserID)
	if err != nil {
		return replyText(ctx, c, getMessage("enduid.read_bind_failed"))
	}
	var b strings.Builder
	b.WriteString(getMessage("enduid.bind_list_text_title"))
	b.WriteString("\n\n")
	if len(accounts) == 0 {
		b.WriteString(getMessage("enduid.bind_list_empty"))
		b.WriteString("\n\n")
		b.WriteString(getMessage("enduid.bind_list_help_hint", map[string]any{"prefix": ":"}))
		return replyText(ctx, c, b.String())
	}
	for index, account := range accounts {
		activeMark := ""
		if account.IsPrimary {
			activeMark = getMessage("enduid.bind_list_active_mark")
		}
		item := getMessage("enduid.bind_list_item", map[string]any{
			"index":       index + 1,
			"nickname":    valueOr(account.Nickname, getMessage("common.unknown")),
			"active_mark": activeMark,
			"level_text":  "",
			"role_id":     valueOr(account.roleID(), getMessage("common.unknown")),
			"server_label": valueOr(strings.TrimSpace(account.ChannelName),
				getMessage("common.unknown")),
			"type_label": bindingTypeLabel(account.LoginType),
			"created_at": formatBindTime(account.BindTime),
		})
		b.WriteString(item)
		if index < len(accounts)-1 {
			b.WriteString("\n\n")
		}
	}
	return replyText(ctx, c, b.String())
}

func bindingTypeLabel(loginType string) string {
	switch loginType {
	case "qr":
		return getMessage("enduid.login_type_qr")
	case "phone":
		return getMessage("enduid.login_type_phone")
	case "auth":
		return getMessage("enduid.login_type_auth")
	case "cred":
		return getMessage("enduid.login_type_cred")
	default:
		return getMessage("common.unknown")
	}
}

func formatBindTime(milliseconds int64) string {
	if milliseconds <= 0 {
		return getMessage("common.unknown")
	}
	return time.UnixMilli(milliseconds).Format("2006-01-02 15:04:05")
}

func handleSwitchBind(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	userID := c.Event.UserID
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil || len(accounts) == 0 {
		return replyText(ctx, c, getMessage("common.not_found_login_info"))
	}
	index, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil || index < 1 {
		return replyText(ctx, c, getMessage("enduid.switch_index_hint", map[string]any{"prefix": ":"}))
	}
	if index > len(accounts) {
		return replyText(ctx, c, getMessage("enduid.index_out_of_range", map[string]any{"count": len(accounts)}))
	}

	// 切换前先用远程接口刷新 binding_id。
	accounts = refreshBindingIDsQuietly(ctx, c, cfg, accounts)
	target := accounts[index-1]

	// 远程切换失败时仅更新本地状态，与上游一致。
	if bindingID := anyString(target.BindingID); bindingID != "" {
		_ = setUnifiedBackendPrimaryBinding(ctx, cfg, bindingID, userID)
	}

	for i := range accounts {
		accounts[i].IsPrimary = i == index-1
	}
	if err := saveBindings(ctx, c.State, userID, accounts); err != nil {
		return replyText(ctx, c, getMessage("enduid.switch_failed"))
	}
	return handleBindList(ctx, c, cfg, "", "")
}

func handleDeleteBind(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	userID := c.Event.UserID
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil || len(accounts) == 0 {
		return replyText(ctx, c, getMessage("common.not_found_login_info"))
	}
	index, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil || index < 1 {
		return replyText(ctx, c, getMessage("enduid.delete_index_hint", map[string]any{"prefix": ":"}))
	}
	if index > len(accounts) {
		return replyText(ctx, c, getMessage("enduid.index_out_of_range", map[string]any{"count": len(accounts)}))
	}

	deleted := accounts[index-1]
	// 网页授权/Cred 账号由远程授权状态决定，不允许手动删除。
	if deleted.LoginType == "auth" || deleted.LoginType == "cred" {
		return replyText(ctx, c, getMessage("enduid.unbind_auth_auto"))
	}

	success := false
	if bindingID := anyString(deleted.BindingID); bindingID != "" {
		success = deleteUnifiedBackendBinding(ctx, cfg, bindingID, userID)
	}

	updated := make([]binding, 0, len(accounts)-1)
	updated = append(updated, accounts[:index-1]...)
	updated = append(updated, accounts[index:]...)
	if err := saveBindings(ctx, c.State, userID, updated); err != nil {
		return replyText(ctx, c, getMessage("enduid.delete_failed"))
	}
	if !success {
		if err := replyText(ctx, c, getMessage("enduid.delete_remote_failed")); err != nil {
			return err
		}
	}
	return handleBindList(ctx, c, cfg, "", "")
}

func handleMyCred(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	accounts, err := loadBindings(ctx, c.State, c.Event.UserID)
	if err != nil || len(accounts) == 0 {
		return replyText(ctx, c, getMessage("enduid.not_logged_in"))
	}
	account, ok := primaryBinding(accounts)
	if !ok || account.FrameworkToken == "" {
		return replyText(ctx, c, getMessage("enduid.token_not_found"))
	}
	return replyText(ctx, c, getMessage("enduid.token_show", map[string]any{"token": account.FrameworkToken}))
}

func handleDeleteCred(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	userID := c.Event.UserID
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil || len(accounts) == 0 {
		return replyText(ctx, c, getMessage("enduid.not_logged_in"))
	}
	for _, account := range accounts {
		if account.LoginType == "auth" || account.LoginType == "cred" {
			continue
		}
		if bindingID := anyString(account.BindingID); bindingID != "" {
			_ = deleteUnifiedBackendBinding(ctx, cfg, bindingID, userID)
		}
	}
	if err := saveBindings(ctx, c.State, userID, nil); err != nil {
		return replyText(ctx, c, getMessage("enduid.delete_failed"))
	}
	return replyText(ctx, c, getMessage("enduid.delete_ok"))
}

// ---------- Cred 绑定 ----------

func handleCredBind(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !isPrivateChat(c) {
		return replyText(ctx, c, getMessage("enduid.please_private"))
	}
	cred := strings.TrimSpace(args)
	if cred == "" {
		return replyText(ctx, c, getMessage("enduid.cred_bind_usage", map[string]any{"prefix": ":"}))
	}

	if verified := credVerify(ctx, cfg, cred); verified != nil && !*verified {
		return replyText(ctx, c, getMessage("enduid.cred_invalid"))
	}
	data, ok := credLogin(ctx, cfg, cred)
	if !ok {
		return replyText(ctx, c, getMessage("enduid.get_token_failed"))
	}
	session := parseLoginSession(data)
	if session.FrameworkToken == "" {
		return replyText(ctx, c, getMessage("enduid.get_token_failed"))
	}
	return applyLoginBindings(ctx, c, cfg, session, "cred")
}

// ---------- 授权绑定 ----------

func handleAuthBind(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.hasAPIKey() {
		return replyText(ctx, c, getMessage("common.need_api_key"))
	}
	clientID := botID(c)
	if clientID == "" {
		clientID = "qqbot"
	}
	authReq, ok := createAuthorizationRequest(ctx, cfg, clientID, defaultAuthClientName, "bot",
		c.Event.UserID, []string{"user_info", "binding_info", "game_data", "attendance"})
	if !ok || authReq.RequestID == "" || authReq.AuthURL == "" {
		return replyText(ctx, c, getMessage("enduid.auth_create_failed"))
	}

	parts := []string{getMessage("enduid.auth_link_intro") + "\n", authReq.AuthURL}
	if formatted := formatAuthExpiry(authReq.ExpiresAt); formatted != "" {
		parts = append(parts, "\n"+getMessage("enduid.auth_link_expiry", map[string]any{"time": formatted}))
	}
	parts = append(parts, "\n"+getMessage("enduid.auth_link_wait"))
	if err := replyText(ctx, c, strings.Join(parts, "")); err != nil {
		return err
	}

	var authData map[string]any
	for attempt := 0; attempt < authPollMaxAttempts; attempt++ {
		if !sleepContext(ctx, authPollInterval) {
			return nil
		}
		status := getAuthorizationRequestStatus(ctx, cfg, authReq.RequestID)
		if status == nil {
			continue
		}
		switch anyString(status["status"]) {
		case "used", "approved":
			if anyString(status["framework_token"]) != "" {
				authData = status
			}
		case "rejected":
			return replyText(ctx, c, getMessage("enduid.auth_rejected"))
		case "expired":
			return replyText(ctx, c, getMessage("enduid.auth_expired"))
		}
		if authData != nil {
			break
		}
	}
	if authData == nil {
		return replyText(ctx, c, getMessage("enduid.auth_timeout"))
	}

	bindingInfo := asMap(authData["binding_info"])
	roleID := ""
	if bindingInfo != nil {
		roleID = anyString(bindingInfo["role_id"])
	}
	if roleID == "" {
		return replyText(ctx, c, getMessage("enduid.bind_create_failed"))
	}
	bindingData := map[string]any{
		"id":           roleID,
		"role_id":      roleID,
		"nickname":     anyString(bindingInfo["nickname"]),
		"server_id":    anyString(bindingInfo["server_id"]),
		"channel_name": anyString(bindingInfo["channel_name"]),
	}
	saveUnifiedBackendBinding(ctx, c, cfg, anyString(authData["framework_token"]), bindingData, "auth", clientID)
	return nil
}

func formatAuthExpiry(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Local().Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

// ---------- 扫码绑定 ----------

func handleScanQRBind(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	frameworkToken, qrcode, ok := getUnifiedBackendQR(ctx, cfg)
	if !ok || frameworkToken == "" || qrcode == "" {
		return replyText(ctx, c, getMessage("enduid.get_qrcode_failed"))
	}

	if err := replyText(ctx, c, getMessage("enduid.qr_scan_prompt")); err != nil {
		return err
	}
	if err := sendQRImage(ctx, c, qrcode); err != nil {
		return replyText(ctx, c, getMessage("enduid.get_qrcode_failed"))
	}

	setWait(c, &waitState{
		kind:           waitQR,
		frameworkToken: frameworkToken,
		expiresAt:      time.Now().Add(qrPollTimeout),
	})
	defer removeWait(c)

	session, ok := pollQRLogin(ctx, c, cfg, frameworkToken)
	if !ok {
		return nil
	}
	return applyLoginBindings(ctx, c, cfg, session, "qr")
}

func sendQRImage(ctx context.Context, c sdk.Call, qrcode string) error {
	value := strings.TrimSpace(qrcode)
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return c.ReplyImage(ctx, value)
	}
	if strings.HasPrefix(value, "data:image") {
		if index := strings.Index(value, "base64,"); index >= 0 {
			value = value[index+len("base64,"):]
		}
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, data)
}

func pollQRLogin(ctx context.Context, c sdk.Call, cfg config, frameworkToken string) (loginSession, bool) {
	attempts := int(qrPollTimeout / qrPollInterval)
	for attempt := 0; attempt < attempts; attempt++ {
		if !sleepContext(ctx, qrPollInterval) {
			return loginSession{}, false
		}
		data := getUnifiedBackendQRStatus(ctx, cfg, frameworkToken)
		if data == nil {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(anyString(data["status"])))
		code := 0
		hasCode := false
		if raw, exists := data["code"]; exists && raw != nil {
			code = anyInt(raw, 0)
			hasCode = true
		}
		isDone := status == "done" ||
			(hasCode && code == 0 && status == "" && anyString(data["framework_token"]) != "")
		isExpired := status == "expired" || (hasCode && code == -2)
		isFailed := status == "failed" || (hasCode && code == -3)

		if isDone {
			if session, ok := confirmUnifiedBackendLogin(ctx, cfg, frameworkToken, c.Event.UserID); ok {
				return session, true
			}
			continue
		}
		if isExpired {
			_ = replyText(ctx, c, getMessage("enduid.qr_expired"))
			return loginSession{}, false
		}
		if isFailed {
			_ = replyText(ctx, c, getMessage("enduid.qr_login_failed"))
			return loginSession{}, false
		}
	}
	_ = replyText(ctx, c, getMessage("enduid.qr_timeout"))
	return loginSession{}, false
}

// ---------- 手机绑定 ----------

func handlePhoneBind(ctx context.Context, c sdk.Call, cfg config, args string, _ string) error {
	if !isPrivateChat(c) {
		return replyText(ctx, c, getMessage("enduid.phone_please_private"))
	}
	phone := strings.TrimSpace(args)
	if !phoneNumberPattern.MatchString(phone) {
		return replyText(ctx, c, getMessage("enduid.phone_ask_example", map[string]any{"prefix": ":"}))
	}
	data, ok := sendPhoneCode(ctx, cfg, phone)
	if !ok {
		return replyText(ctx, c, getMessage("enduid.phone_send_failed"))
	}

	expireSeconds := anyInt(data["expire"], 300)
	if expireSeconds < 60 {
		expireSeconds = 60
	}
	if expireSeconds > 1800 {
		expireSeconds = 1800
	}
	validity := time.Duration(expireSeconds) * time.Second
	if validity > 5*time.Minute {
		validity = 5 * time.Minute
	}
	setWait(c, &waitState{
		kind:           waitPhone,
		phone:          phone,
		frameworkToken: strings.TrimSpace(anyString(data["framework_token"])),
		expiresAt:      time.Now().Add(validity),
	})

	mask := phone[:3] + "****" + phone[7:]
	message := getMessage("enduid.phone_code_sent", map[string]any{"mask": mask, "prefix": ":"})
	message += "\n" + getMessage("enduid.phone_code_verify_example")
	// 宿主的指令入口要求带前缀，这里额外提示用户按「:验证码」回复。
	message += "\n（回复验证码时请带上指令前缀，如：:123456）"
	return replyText(ctx, c, message)
}

func completePhoneLogin(ctx context.Context, c sdk.Call, cfg config, state *waitState, code string) error {
	data, ok := phoneLogin(ctx, cfg, state.phone, code, state.frameworkToken)
	if !ok {
		return replyText(ctx, c, getMessage("enduid.phone_code_wrong"))
	}
	session := parseLoginSession(data)
	if session.FrameworkToken == "" {
		return replyText(ctx, c, getMessage("enduid.phone_code_wrong"))
	}
	return applyLoginBindings(ctx, c, cfg, session, "phone")
}

// ---------- 登录结果落库 ----------

type availableRole struct {
	RoleID      string
	GameRoleID  string
	ServerID    string
	ServerName  string
	Nickname    string
	SklandUID   string
	ChannelName string
	Level       int
	IsDefault   bool
}

type loginSession struct {
	FrameworkToken string
	Roles          []availableRole
}

func parseLoginSession(data map[string]any) loginSession {
	if data == nil {
		return loginSession{}
	}
	return loginSession{
		FrameworkToken: strings.TrimSpace(anyString(data["framework_token"])),
		Roles:          normalizeAvailableRoles(data["available_roles"]),
	}
}

func normalizeAvailableRoles(value any) []availableRole {
	items := asMapList(value)
	seen := map[string]bool{}
	out := make([]availableRole, 0, len(items))
	for _, item := range items {
		roleID := strings.TrimSpace(anyString(item["role_id"]))
		if roleID == "" || seen[roleID] {
			continue
		}
		seen[roleID] = true
		serverID := anyString(item["server_id"])
		if serverID == "" {
			serverID = "1"
		}
		out = append(out, availableRole{
			RoleID:      roleID,
			GameRoleID:  anyString(item["game_role_id"]),
			ServerID:    serverID,
			ServerName:  anyString(item["server_name"]),
			Nickname:    anyString(item["nickname"]),
			SklandUID:   anyString(item["skland_uid"]),
			ChannelName: anyString(item["channel_name"]),
			Level:       anyInt(item["level"], 0),
			IsDefault:   boolValue(item["is_default"]),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].IsDefault && !out[j].IsDefault
	})
	return out
}

func applyLoginBindings(ctx context.Context, c sdk.Call, cfg config, session loginSession, loginType string) error {
	clientID := resolvedClientID(c)
	created := createBindingsForRoles(ctx, cfg, session.FrameworkToken, c.Event.UserID, clientID, session.Roles)
	if len(created) == 0 {
		data, ok := createUnifiedBackendBinding(ctx, cfg, session.FrameworkToken, c.Event.UserID, true, clientID, nil)
		if !ok {
			return replyText(ctx, c, getMessage("enduid.bind_create_failed"))
		}
		saveUnifiedBackendBinding(ctx, c, cfg, session.FrameworkToken, data, loginType, clientID)
		return nil
	}
	if !saveUnifiedBackendBindings(ctx, c, cfg, session.FrameworkToken, created, loginType, clientID) {
		return replyText(ctx, c, getMessage("enduid.bind_create_failed"))
	}
	return nil
}

func createBindingsForRoles(ctx context.Context, cfg config, frameworkToken, userID, clientID string, roles []availableRole) []map[string]any {
	defaultRoleID := ""
	for _, role := range roles {
		if role.IsDefault {
			defaultRoleID = role.RoleID
			break
		}
	}
	if defaultRoleID == "" && len(roles) > 0 {
		defaultRoleID = roles[0].RoleID
	}

	created := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		isPrimary := false
		if defaultRoleID != "" {
			isPrimary = role.RoleID == defaultRoleID
		} else {
			isPrimary = len(created) == 0
		}
		result, ok := createUnifiedBackendBinding(ctx, cfg, frameworkToken, userID, isPrimary, clientID, &role)
		if !ok {
			continue
		}
		gameRoleID := anyString(result["game_role_id"])
		if gameRoleID == "" {
			gameRoleID = role.GameRoleID
		}
		item := map[string]any{
			"id":           firstNonEmpty(anyString(result["id"]), anyString(result["binding_id"]), role.RoleID),
			"role_id":      firstNonEmpty(anyString(result["role_id"]), role.RoleID),
			"nickname":     firstNonEmpty(anyString(result["nickname"]), role.Nickname),
			"server_id":    firstNonEmpty(anyString(result["server_id"]), role.ServerID, "1"),
			"channel_name": firstNonEmpty(anyString(result["channel_name"]), role.ChannelName),
			"is_primary":   isPrimary,
		}
		if gameRoleID != "" {
			item["game_role_id"] = gameRoleID
		}
		created = append(created, item)
	}
	return created
}

func newBindingFromData(data map[string]any, frameworkToken, userID, loginType, clientID string) binding {
	if data == nil {
		data = map[string]any{}
	}
	bindingID := data["id"]
	if bindingID == nil {
		bindingID = data["binding_id"]
	}
	serverID := anyString(data["server_id"])
	if serverID == "" {
		serverID = "1"
	}
	now := time.Now().UnixMilli()
	account := binding{
		FrameworkToken: frameworkToken,
		BindingID:      bindingID,
		UserIdentifier: userID,
		RoleID:         anyString(data["role_id"]),
		Nickname:       strings.TrimSpace(anyString(data["nickname"])),
		ServerID:       serverID,
		ChannelName:    strings.TrimSpace(anyString(data["channel_name"])),
		IsActive:       boolPointer(true),
		IsPrimary:      boolValue(data["is_primary"]),
		ClientType:     "bot",
		LoginType:      loginType,
		BindTime:       now,
		LastSync:       now,
		ClientID:       clientID,
	}
	if gameRoleID := anyString(data["game_role_id"]); gameRoleID != "" {
		account.GameRoleID = gameRoleID
	}
	return account
}

// saveUnifiedBackendBinding 单个角色登录：同 role_id 更新，否则新增。
func saveUnifiedBackendBinding(ctx context.Context, c sdk.Call, cfg config, frameworkToken string, data map[string]any, loginType, clientID string) {
	userID := c.Event.UserID
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil {
		_ = replyText(ctx, c, getMessage("enduid.read_bind_failed"))
		return
	}

	newAccount := newBindingFromData(data, frameworkToken, userID, loginType, clientID)
	index := -1
	if roleID := newAccount.roleID(); roleID != "" {
		for i := range accounts {
			if accounts[i].roleID() == roleID {
				index = i
				break
			}
		}
	}
	if index >= 0 {
		accounts[index] = mergeBinding(accounts[index], newAccount)
	} else {
		accounts = append(accounts, newAccount)
		index = len(accounts) - 1
	}
	for i := range accounts {
		accounts[i].IsPrimary = i == index
	}

	if err := saveBindings(ctx, c.State, userID, accounts); err != nil {
		_ = replyText(ctx, c, getMessage("enduid.read_bind_failed"))
		return
	}
	accounts, _ = refreshBindingIDsAfterLogin(ctx, c, cfg, accounts)

	serverLabel := newAccount.ChannelName
	if serverLabel == "" {
		serverLabel = firstNonEmpty(anyString(newAccount.ServerID), "1")
	}
	_ = replyText(ctx, c, getMessage("enduid.login_ok", map[string]any{
		"nickname":  valueOr(newAccount.Nickname, getMessage("common.unknown")),
		"role_id":   valueOr(newAccount.roleID(), getMessage("common.unknown")),
		"server_id": serverLabel,
		"count":     len(accounts),
	}))
}

// saveUnifiedBackendBindings 多角色登录：批量创建后落库并展示本次登录结果。
func saveUnifiedBackendBindings(ctx context.Context, c sdk.Call, cfg config, frameworkToken string, items []map[string]any, loginType, clientID string) bool {
	if len(items) == 0 {
		return false
	}
	userID := c.Event.UserID
	accounts, err := loadBindings(ctx, c.State, userID)
	if err != nil {
		_ = replyText(ctx, c, getMessage("enduid.read_bind_failed"))
		return false
	}

	preferredRoleID := ""
	for _, item := range items {
		if boolValue(item["is_primary"]) {
			preferredRoleID = anyString(item["role_id"])
			break
		}
	}
	if preferredRoleID == "" {
		preferredRoleID = anyString(items[0]["role_id"])
	}

	for _, item := range items {
		roleID := strings.TrimSpace(anyString(item["role_id"]))
		if roleID == "" {
			continue
		}
		newAccount := newBindingFromData(item, frameworkToken, userID, loginType, clientID)
		if preferredRoleID != "" {
			newAccount.IsPrimary = roleID == preferredRoleID
		}
		index := -1
		for i := range accounts {
			if accounts[i].roleID() == roleID {
				index = i
				break
			}
		}
		if index >= 0 {
			accounts[index] = mergeBinding(accounts[index], newAccount)
		} else {
			accounts = append(accounts, newAccount)
		}
	}
	if preferredRoleID != "" {
		for i := range accounts {
			accounts[i].IsPrimary = accounts[i].roleID() == preferredRoleID
		}
	}

	if err := saveBindings(ctx, c.State, userID, accounts); err != nil {
		_ = replyText(ctx, c, getMessage("enduid.read_bind_failed"))
		return false
	}
	accounts, _ = refreshBindingIDsAfterLogin(ctx, c, cfg, accounts)

	current := make([]binding, 0, len(items))
	for _, item := range items {
		roleID := anyString(item["role_id"])
		matched := newBindingFromData(item, frameworkToken, userID, loginType, clientID)
		for _, account := range accounts {
			if account.roleID() == roleID {
				matched = account
				break
			}
		}
		current = append(current, matched)
	}

	lines := []string{
		getMessage("enduid.login_success_header"),
		getMessage("enduid.login_success_separator"),
		getMessage("enduid.login_success_batch", map[string]any{"count": len(current)}),
	}
	for index, account := range current {
		lines = append(lines, getMessage("enduid.login_success_item", map[string]any{
			"index":        index + 1,
			"nickname":     valueOr(account.Nickname, getMessage("common.unknown")),
			"role_id":      valueOr(account.roleID(), getMessage("common.unknown")),
			"server_label": loginServerLabel(account),
		}))
	}
	lines = append(lines, getMessage("enduid.login_success_total", map[string]any{"count": len(accounts)}))
	_ = replyText(ctx, c, strings.Join(lines, "\n"))
	return true
}

func loginServerLabel(account binding) string {
	if channel := strings.TrimSpace(account.ChannelName); channel != "" {
		return channel
	}
	if server := anyString(account.ServerID); server != "" {
		return server
	}
	return getMessage("enduid.server_unknown")
}

// mergeBinding 保留旧记录中本次未提供的信息，避免空值覆盖。
func mergeBinding(previous, next binding) binding {
	if anyString(next.GameRoleID) == "" {
		next.GameRoleID = previous.GameRoleID
	}
	if next.ChannelName == "" {
		next.ChannelName = previous.ChannelName
	}
	if next.Nickname == "" {
		next.Nickname = previous.Nickname
	}
	if next.BindingID == nil {
		next.BindingID = previous.BindingID
	}
	if next.ServerName == "" {
		next.ServerName = previous.ServerName
	}
	if previous.LoginType != "" {
		next.LoginType = previous.LoginType
	}
	return next
}

// ---------- 远程绑定同步 ----------

func refreshBindingIDsAfterLogin(ctx context.Context, c sdk.Call, cfg config, accounts []binding) ([]binding, error) {
	if len(accounts) == 0 {
		return accounts, nil
	}
	remote, ok := getRemoteBindingMap(ctx, cfg, c.Event.UserID, botID(c), accounts)
	if !ok || len(remote) == 0 {
		return accounts, nil
	}
	synced := make([]binding, len(accounts))
	changed := false
	for i, account := range accounts {
		synced[i] = applyRemoteBindingData(account, remote)
		if !reflect.DeepEqual(synced[i], account) {
			changed = true
		}
	}
	if changed {
		if err := saveBindings(ctx, c.State, c.Event.UserID, synced); err != nil {
			return synced, err
		}
	}
	return synced, nil
}

func refreshBindingIDsQuietly(ctx context.Context, c sdk.Call, cfg config, accounts []binding) []binding {
	synced, _ := refreshBindingIDsAfterLogin(ctx, c, cfg, accounts)
	return synced
}

func getRemoteBindingMap(ctx context.Context, cfg config, userID, defaultClientID string, accounts []binding) (map[string]map[string]any, bool) {
	remoteList, listOK := getUnifiedBackendBindings(ctx, cfg, userID)
	authList, authOK := getRemoteAuthorizationAccounts(ctx, cfg, userID, defaultClientID, accounts)
	if !listOK && !authOK {
		return nil, false
	}

	out := map[string]map[string]any{}
	add := func(item map[string]any) {
		token := anyString(item["framework_token"])
		info := asMap(item["binding_info"])
		roleID := anyString(item["role_id"])
		if roleID == "" && info != nil {
			roleID = anyString(info["role_id"])
		}
		bindingID := item["id"]
		if bindingID == nil {
			bindingID = item["binding_id"]
		}
		normalized := make(map[string]any, len(item)+3)
		for key, value := range item {
			normalized[key] = value
		}
		normalized["role_id"] = roleID
		normalized["framework_token"] = token
		normalized["binding_id"] = bindingID

		if token != "" && roleID != "" {
			out["tr:"+token+":"+roleID] = normalized
		}
		if roleID != "" {
			if _, exists := out["r:"+roleID]; !exists {
				out["r:"+roleID] = normalized
			}
		}
		if token != "" {
			if _, exists := out["t:"+token]; !exists {
				out["t:"+token] = normalized
			}
		}
	}

	for _, item := range remoteList {
		add(item)
	}
	for _, item := range authList {
		add(item)
	}
	return out, true
}

func getRemoteAuthorizationAccounts(ctx context.Context, cfg config, userID, defaultClientID string, accounts []binding) ([]map[string]any, bool) {
	clientIDs := map[string]bool{}
	hasAuth := false
	for _, account := range accounts {
		if account.LoginType != "auth" {
			continue
		}
		hasAuth = true
		clientID := strings.TrimSpace(account.ClientID)
		if clientID == "" {
			clientID = defaultClientID
		}
		if clientID != "" {
			clientIDs[clientID] = true
		}
	}
	if !hasAuth {
		return nil, false
	}
	if len(clientIDs) == 0 && defaultClientID != "" {
		clientIDs[defaultClientID] = true
	}

	out := []map[string]any{}
	anyOK := false
	for clientID := range clientIDs {
		list, ok := getClientPlatformAuthorizations(ctx, cfg, clientID, userID)
		if !ok {
			continue
		}
		anyOK = true
		for _, item := range list {
			info := asMap(item["binding_info"])
			roleID := anyString(item["role_id"])
			if roleID == "" && info != nil {
				roleID = anyString(info["role_id"])
			}
			if roleID == "" {
				continue
			}
			gameRoleID := anyString(item["game_role_id"])
			if gameRoleID == "" && info != nil {
				gameRoleID = anyString(info["game_role_id"])
				if gameRoleID == "" {
					gameRoleID = anyString(info["gameRoleId"])
				}
			}
			created := time.Now().UnixMilli()
			if raw := anyString(item["created_at"]); raw != "" {
				if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
					created = parsed.UnixMilli()
				}
			}
			bindingID := item["binding_id"]
			if bindingID == nil {
				bindingID = item["id"]
			}
			entry := map[string]any{
				"framework_token": anyString(item["framework_token"]),
				"binding_id":      bindingID,
				"user_identifier": userID,
				"role_id":         roleID,
				"nickname":        anyString(info["nickname"]),
				"server_id":       anyString(info["server_id"]),
				"channel_name":    anyString(info["channel_name"]),
				"is_active":       item["is_active"],
				"created_at":      created,
			}
			if info != nil && entry["nickname"] == "" {
				entry["nickname"] = anyString(info["nickname"])
			}
			if info != nil && entry["server_id"] == "" {
				entry["server_id"] = anyString(info["server_id"])
			}
			if info != nil && entry["channel_name"] == "" {
				entry["channel_name"] = anyString(info["channel_name"])
			}
			if gameRoleID != "" {
				entry["game_role_id"] = gameRoleID
			}
			out = append(out, entry)
		}
	}
	if !anyOK {
		return nil, false
	}
	return out, true
}

func applyRemoteBindingData(account binding, remoteMap map[string]map[string]any) binding {
	token := account.FrameworkToken
	roleID := account.roleID()
	var remote map[string]any
	if item := remoteMap["tr:"+token+":"+roleID]; item != nil {
		remote = item
	}
	if remote == nil {
		remote = remoteMap["r:"+roleID]
	}
	if remote == nil {
		remote = remoteMap["t:"+token]
	}
	if remote == nil {
		return account
	}

	info := asMap(remote["binding_info"])
	read := func(key string) any {
		if value, ok := remote[key]; ok && value != nil {
			return value
		}
		if info != nil {
			if value, ok := info[key]; ok && value != nil {
				return value
			}
		}
		return nil
	}

	if value := anyString(read("role_id")); value != "" {
		account.RoleID = value
	}
	gameRoleID := anyString(read("game_role_id"))
	if gameRoleID == "" {
		gameRoleID = anyString(read("gameRoleId"))
	}
	if gameRoleID != "" {
		account.GameRoleID = gameRoleID
	}
	if value := anyString(read("nickname")); value != "" {
		account.Nickname = value
	}
	if value := anyString(read("server_id")); value != "" {
		account.ServerID = value
	}
	if value := strings.TrimSpace(anyString(read("channel_name"))); value != "" {
		account.ChannelName = value
	}
	if value := read("id"); value != nil && anyString(value) != "" {
		account.BindingID = value
	} else if value := read("binding_id"); value != nil && anyString(value) != "" {
		account.BindingID = value
	}
	if value, ok := read("is_active").(bool); ok && !value {
		account.IsActive = boolPointer(false)
	}
	return account
}

// ---------- 统一后端：登录相关 ----------

func baseClient(cfg config) *apiClient {
	return newAPIClient(cfg, binding{})
}

func getUnifiedBackendQR(ctx context.Context, cfg config) (string, string, bool) {
	response, err := baseClient(cfg).request(ctx, http.MethodGet, "/login/endfield/qr", nil, nil)
	if err != nil || !response.ok() {
		return "", "", false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return "", "", false
	}
	return anyString(data["framework_token"]), anyString(data["qrcode"]), true
}

func getUnifiedBackendQRStatus(ctx context.Context, cfg config, frameworkToken string) map[string]any {
	query := url.Values{}
	query.Set("framework_token", frameworkToken)
	response, err := baseClient(cfg).request(ctx, http.MethodGet, "/login/endfield/qr/status", query, nil)
	if err != nil || !response.ok() {
		return nil
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil
	}
	return data
}

func confirmUnifiedBackendLogin(ctx context.Context, cfg config, frameworkToken, userID string) (loginSession, bool) {
	body := map[string]any{
		"framework_token": frameworkToken,
		"user_identifier": userID,
		"platform":        "bot",
	}
	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/login/endfield/qr/confirm", nil, body)
	if err != nil || !response.ok() {
		return loginSession{}, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return loginSession{}, false
	}
	session := parseLoginSession(data)
	if session.FrameworkToken == "" {
		return loginSession{}, false
	}
	return session, true
}

func sendPhoneCode(ctx context.Context, cfg config, phone string) (map[string]any, bool) {
	body := map[string]any{"phone": phone}
	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/login/endfield/phone/send", nil, body)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return map[string]any{}, true
	}
	return data, true
}

func phoneLogin(ctx context.Context, cfg config, phone, code, frameworkToken string) (map[string]any, bool) {
	body := map[string]any{"phone": phone, "code": code}
	if frameworkToken != "" {
		body["framework_token"] = frameworkToken
	}
	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/login/endfield/phone/verify", nil, body)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	return data, true
}

func credLogin(ctx context.Context, cfg config, cred string) (map[string]any, bool) {
	body := map[string]any{"cred": cred}
	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/login/endfield/cred", nil, body)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	return data, true
}

// credVerify 返回 nil 表示接口异常（不阻断后续登录），false 表示明确无效。
func credVerify(ctx context.Context, cfg config, cred string) *bool {
	query := url.Values{}
	query.Set("cred", cred)
	response, err := baseClient(cfg).request(ctx, http.MethodGet, "/login/endfield/cred/verify", query, nil)
	if err != nil {
		return nil
	}
	if !response.ok() {
		invalid := false
		return &invalid
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		valid := true
		return &valid
	}
	if value, ok := data["valid"].(bool); ok {
		return &value
	}
	if value, ok := data["is_valid"].(bool); ok {
		return &value
	}
	valid := true
	return &valid
}

// ---------- 统一后端：绑定管理 ----------

func createUnifiedBackendBinding(ctx context.Context, cfg config, frameworkToken, userID string, isPrimary bool, clientID string, role *availableRole) (map[string]any, bool) {
	if clientID == "" {
		clientID = "bot-" + userID
	}
	body := map[string]any{
		"framework_token": frameworkToken,
		"user_identifier": userID,
		"client_type":     "bot",
		"client_id":       clientID,
		"is_primary":      isPrimary,
	}
	if role != nil {
		if role.RoleID != "" {
			body["role_id"] = role.RoleID
		}
		if role.ServerID != "" {
			body["server_id"] = role.ServerID
		}
		if role.ServerName != "" {
			body["server_name"] = role.ServerName
		}
		if role.Nickname != "" {
			body["nickname"] = role.Nickname
		}
		if role.SklandUID != "" {
			body["skland_uid"] = role.SklandUID
		}
		if role.ChannelName != "" {
			body["channel_name"] = role.ChannelName
		}
		body["level"] = role.Level
	}

	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/api/v1/bindings", nil, body)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	return data, true
}

func getUnifiedBackendBindings(ctx context.Context, cfg config, userID string) ([]map[string]any, bool) {
	query := url.Values{}
	query.Set("user_identifier", userID)
	query.Set("client_type", "bot")
	response, err := baseClient(cfg).request(ctx, http.MethodGet, "/api/v1/bindings", query, nil)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	return asMapList(data["bindings"]), true
}

func deleteUnifiedBackendBinding(ctx context.Context, cfg config, bindingID, userID string) bool {
	query := url.Values{}
	if userID != "" {
		query.Set("user_identifier", userID)
		query.Set("client_type", "bot")
	}
	endpoint := "/api/v1/bindings/" + url.PathEscape(bindingID)
	response, err := baseClient(cfg).request(ctx, http.MethodDelete, endpoint, query, nil)
	return err == nil && response.ok()
}

func setUnifiedBackendPrimaryBinding(ctx context.Context, cfg config, bindingID, userID string) bool {
	query := url.Values{}
	if userID != "" {
		query.Set("user_identifier", userID)
	}
	endpoint := "/api/v1/bindings/" + url.PathEscape(bindingID) + "/primary"
	response, err := baseClient(cfg).request(ctx, http.MethodPost, endpoint, query, nil)
	return err == nil && response.ok()
}

func getUnifiedBackendHealth(ctx context.Context, cfg config) bool {
	response, err := baseClient(cfg).request(ctx, http.MethodGet, "/health", nil, nil)
	if err != nil || !response.ok() {
		return false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return false
	}
	return anyString(data["status"]) == "healthy"
}

// ---------- 统一后端：授权 ----------

type authorizationRequestInfo struct {
	RequestID string
	AuthURL   string
	ExpiresAt string
}

func createAuthorizationRequest(ctx context.Context, cfg config, clientID, clientName, clientType, platformID string, scopes []string) (*authorizationRequestInfo, bool) {
	if !cfg.hasAPIKey() {
		return nil, false
	}
	if clientID == "" {
		clientID = "qqbot"
	}
	if clientName == "" {
		clientName = defaultAuthClientName
	}
	if clientType == "" {
		clientType = "bot"
	}
	if len(scopes) == 0 {
		scopes = []string{"user_info", "binding_info", "game_data", "attendance"}
	}
	body := map[string]any{
		"client_id":   clientID,
		"client_name": clientName,
		"client_type": clientType,
		"platform_id": platformID,
		"scopes":      scopes,
	}
	response, err := baseClient(cfg).request(ctx, http.MethodPost, "/api/v1/authorization/requests", nil, body)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	authURL := anyString(data["auth_url"])
	if strings.HasPrefix(authURL, "/") {
		authURL = authorizationFrontendURL + authURL
	}
	return &authorizationRequestInfo{
		RequestID: anyString(data["request_id"]),
		AuthURL:   authURL,
		ExpiresAt: anyString(data["expires_at"]),
	}, true
}

func getAuthorizationRequestStatus(ctx context.Context, cfg config, requestID string) map[string]any {
	if !cfg.hasAPIKey() {
		return nil
	}
	endpoint := "/api/v1/authorization/requests/" + url.PathEscape(requestID) + "/status"
	response, err := baseClient(cfg).request(ctx, http.MethodGet, endpoint, nil, nil)
	if err != nil || !response.ok() {
		return nil
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil
	}
	return data
}

func getAuthorizationClientStatus(ctx context.Context, cfg config, clientID, userIdentifier string) map[string]any {
	if !cfg.hasAPIKey() {
		return nil
	}
	query := url.Values{}
	if userIdentifier != "" {
		query.Set("user_identifier", userIdentifier)
	}
	endpoint := "/api/v1/authorization/clients/" + url.PathEscape(clientID) + "/status"
	response, err := baseClient(cfg).request(ctx, http.MethodGet, endpoint, query, nil)
	if err != nil || !response.ok() {
		return nil
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil
	}
	return data
}

func getClientPlatformAuthorizations(ctx context.Context, cfg config, clientID, platformID string) ([]map[string]any, bool) {
	if !cfg.hasAPIKey() {
		return nil, false
	}
	endpoint := "/api/v1/authorization/clients/" + url.PathEscape(clientID) + "/users/" + url.PathEscape(platformID)
	response, err := baseClient(cfg).request(ctx, http.MethodGet, endpoint, nil, nil)
	if err != nil || !response.ok() {
		return nil, false
	}
	var data map[string]any
	if err := response.decode(&data); err != nil {
		return nil, false
	}
	return asMapList(data["authorizations"]), true
}

// ---------- 小工具 ----------

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	item, _ := value.(map[string]any)
	return item
}

func asMapList(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapping := asMap(item); mapping != nil {
			out = append(out, mapping)
		}
	}
	return out
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func boolPointer(value bool) *bool { return &value }

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

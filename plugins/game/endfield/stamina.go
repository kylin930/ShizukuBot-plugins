package endfield

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const staminaSubPrefix = "endfield:stamina:sub:"

func init() {
	registerRoute(90, exactMatch("全部签到"), handleAttendanceAll)
	registerRoute(85, exactMatch("签到"), handleAttendance)
	registerRoute(84, prefixMatch("订阅理智", "订阅体力", "理智订阅", "体力订阅"), handleSubscribeStamina)
	registerRoute(84, exactMatch("取消订阅理智", "取消订阅体力"), handleUnsubscribeStamina)
	registerRoute(78, prefixMatch("理智", "体力", "荔枝"), handleStamina)
}

type staminaAccount struct {
	Current       int
	Max           int
	FullTime      string
	Activation    int
	MaxActivation int
	WeeklyScore   int
	WeeklyTotal   int
	UserAvatar    string
	UserName      string
	UserLevel     any
	UserUID       string
}

type staminaSubscription struct {
	Threshold *int   `json:"threshold"`
	RoleID    string `json:"role_id"`
}

func staminaSubKey(userID string) string { return staminaSubPrefix + userID }

// handleStamina 对齐上游 apps/stamina.js 的多账号理智查询。
func handleStamina(ctx context.Context, c sdk.Call, cfg config, args string, text string) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	accounts, err := loadBindings(ctx, c.State, session.UserID)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	sessions := allUserSessions(cfg, session.UserID, accounts)
	if len(sessions) == 0 {
		return replyText(ctx, c, getMessage("unbind_message"))
	}

	targetRoleID := ""
	if fields := strings.Fields(args); len(fields) > 0 {
		if _, err := strconv.Atoi(fields[0]); err == nil {
			targetRoleID = fields[0]
		}
	}
	targets := make([]*userSession, 0, len(sessions))
	for _, item := range sessions {
		if targetRoleID != "" && item.Binding.roleID() != targetRoleID {
			continue
		}
		targets = append(targets, item)
	}
	if len(targets) == 0 {
		return replyText(ctx, c, getMessage("stamina.role_not_found", map[string]any{
			"roleId": targetRoleID,
			"prefix": ":",
		}))
	}

	results := make([]staminaAccount, 0, len(targets))
	for _, target := range targets {
		if data, ok := fetchStaminaAccount(ctx, target); ok {
			results = append(results, data)
		}
	}
	if len(results) == 0 {
		return replyText(ctx, c, getMessage("common.get_role_failed"))
	}

	var body strings.Builder
	body.WriteString(`<div class="grid c2">`)
	for _, account := range results {
		body.WriteString(staminaCard(account))
	}
	body.WriteString(`</div>`)

	html := buildPage(pageOptions{
		title:    "理智与活跃",
		eyebrow:  "终末地 · 理智",
		subtitle: htmlEscape(fmt.Sprintf("共 %d 个账号", len(results))),
		seal:     "理智",
		body:     body.String(),
		width:    900,
	})

	lines := make([]string, 0, len(results))
	for _, account := range results {
		itemText := "\n" + getMessage("stamina.text_item", map[string]any{
			"current":       account.Current,
			"max":           account.Max,
			"fullTime":      account.FullTime,
			"activation":    account.Activation,
			"maxActivation": account.MaxActivation,
			"weeklyScore":   account.WeeklyScore,
			"weeklyTotal":   account.WeeklyTotal,
		})
		lines = append(lines, getMessage("common.label_line", map[string]any{
			"label": account.UserName,
			"text":  itemText,
		}))
	}
	return replyCard(ctx, c, html, 900, strings.Join(lines, "\n\n"))
}

func fetchStaminaAccount(ctx context.Context, session *userSession) (staminaAccount, bool) {
	res, err := session.Client.getData(ctx, "stamina", nil)
	if err != nil || !res.ok() {
		return staminaAccount{}, false
	}
	var data map[string]any
	if err := res.decode(&data); err != nil {
		return staminaAccount{}, false
	}
	stamina := pexMap(data, "stamina")
	daily := pexMap(data, "dailyMission")
	weekly := pexMap(data, "weeklyMission")
	role := pexMap(data, "role")

	noteBase := map[string]any{}
	if noteRes, err := session.Client.getData(ctx, "note", nil); err == nil && noteRes.ok() {
		var noteData map[string]any
		if noteRes.decode(&noteData) == nil {
			noteBase = pexMap(noteData, "base")
		}
	}

	current := pexInt(stamina, 0, "current")
	max := pexInt(stamina, 0, "max")
	maxTs := pexInt(stamina, 0, "maxTs")
	recover := pexInt(stamina, 360, "recover")
	if recover <= 0 {
		recover = 360
	}

	fullTime := getMessage("stamina.full_time_unknown")
	if current >= max && max > 0 {
		fullTime = getMessage("stamina.full_time_full")
	} else if maxTs > 0 {
		fullTime = time.Unix(int64(maxTs), 0).Format("2006-01-02 15:04:05")
	} else if current < max {
		remaining := max - current
		minutes := int(math.Ceil(float64(remaining*recover) / 60))
		fullTime = time.Now().Add(time.Duration(minutes) * time.Minute).Format("2006-01-02 15:04:05")
	}

	name := pexString(role, "name")
	if name == "" {
		name = pexString(noteBase, "name")
	}
	if name == "" {
		name = session.Binding.Nickname
	}
	if name == "" {
		name = getMessage("common.unknown")
	}
	userLevel := pexField(role, "level")
	if userLevel == nil {
		userLevel = pexField(noteBase, "level")
	}
	userUID := pexString(role, "roleId")
	if userUID == "" {
		userUID = pexString(noteBase, "roleId")
	}
	if userUID == "" {
		userUID = session.Binding.roleID()
	}
	if userUID == "" {
		userUID = getMessage("common.unknown")
	}
	avatar := pexString(noteBase, "avatarUrl")

	return staminaAccount{
		Current:       current,
		Max:           max,
		FullTime:      fullTime,
		Activation:    pexInt(daily, 0, "activation"),
		MaxActivation: pexInt(daily, 100, "maxActivation"),
		WeeklyScore:   pexInt(weekly, 0, "score"),
		WeeklyTotal:   pexInt(weekly, 10, "total"),
		UserAvatar:    avatar,
		UserName:      name,
		UserLevel:     userLevel,
		UserUID:       userUID,
	}, true
}

func staminaCard(account staminaAccount) string {
	percent := 0.0
	if account.Max > 0 {
		percent = float64(account.Current) / float64(account.Max) * 100
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	activationPercent := 0.0
	if account.MaxActivation > 0 {
		activationPercent = float64(account.Activation) / float64(account.MaxActivation) * 100
	}
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	b.WriteString(`<div style="display:flex;gap:12px;align-items:center">`)
	if account.UserAvatar != "" {
		b.WriteString(`<img src="` + htmlEscape(account.UserAvatar) + `" alt="" style="width:52px;height:52px;border-radius:50%;object-fit:cover">`)
	}
	b.WriteString(`<div><div class="v" style="font-size:20px">` + htmlEscape(account.UserName) + `</div>`)
	b.WriteString(`<div class="note">UID ` + htmlEscape(account.UserUID) + ` · Lv.` + htmlEscape(playerFormatValue(account.UserLevel)) + `</div></div></div>`)
	b.WriteString(`<div class="row"><span class="label">理智</span><span class="value">` + strconv.Itoa(account.Current) + ` / ` + strconv.Itoa(account.Max) + `</span></div>`)
	b.WriteString(`<div class="bar"><i style="width:` + strconv.FormatFloat(percent, 'f', 1, 64) + `%"></i></div>`)
	b.WriteString(`<div class="row"><span class="label">回满时间</span><span class="value">` + htmlEscape(account.FullTime) + `</span></div>`)
	b.WriteString(`<div class="row"><span class="label">日常活跃</span><span class="value">` + strconv.Itoa(account.Activation) + ` / ` + strconv.Itoa(account.MaxActivation) + `</span></div>`)
	b.WriteString(`<div class="bar"><i style="width:` + strconv.FormatFloat(activationPercent, 'f', 1, 64) + `%"></i></div>`)
	b.WriteString(`<div class="row"><span class="label">每周任务</span><span class="value">` + strconv.Itoa(account.WeeklyScore) + ` / ` + strconv.Itoa(account.WeeklyTotal) + `</span></div>`)
	return b.String() + `</div>`
}

func handleSubscribeStamina(ctx context.Context, c sdk.Call, _ config, args string, _ string) error {
	userID := targetUserID(c)
	roleID := ""
	if accounts, err := loadBindings(ctx, c.State, userID); err == nil {
		if account, ok := primaryBinding(accounts); ok {
			roleID = account.roleID()
		}
	}
	threshold := 0
	hasThreshold := false
	if fields := strings.Fields(args); len(fields) > 0 {
		if value, err := strconv.Atoi(fields[0]); err == nil {
			if value < 0 {
				value = 0
			}
			threshold = value
			hasThreshold = true
		}
	}
	subscription := staminaSubscription{RoleID: roleID}
	if hasThreshold {
		subscription.Threshold = &threshold
	}
	if err := sdk.SaveJSON(ctx, c.State, staminaSubKey(userID), subscription); err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	text := getMessage("stamina.subscribe_ok_full")
	if hasThreshold {
		text = getMessage("stamina.subscribe_ok_threshold", map[string]any{"threshold": threshold})
	}
	return replyText(ctx, c, text+"\n（订阅已保存；需要宿主定时任务支持推送）")
}

func handleUnsubscribeStamina(ctx context.Context, c sdk.Call, _ config, _ string, _ string) error {
	userID := targetUserID(c)
	var raw json.RawMessage
	if c.State != nil {
		raw, _ = c.State.Get(ctx, staminaSubKey(userID))
	}
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return replyText(ctx, c, getMessage("stamina.not_subscribed"))
	}
	if c.State != nil {
		if err := c.State.Set(ctx, staminaSubKey(userID), json.RawMessage("{}")); err != nil {
			return playerQueryFailed(ctx, c, err)
		}
	}
	return replyText(ctx, c, getMessage("stamina.unsubscribe_ok"))
}

// ---------- 签到 ----------

func handleAttendance(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	return runAttendance(ctx, c, cfg, false)
}

func handleAttendanceAll(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.isAdmin(c.Event.UserID) {
		return replyText(ctx, c, "此命令仅限管理员使用。")
	}
	return runAttendance(ctx, c, cfg, true)
}

func runAttendance(ctx context.Context, c sdk.Call, cfg config, adminBatch bool) error {
	session, ok, err := requireBinding(ctx, c, cfg)
	if !ok {
		return err
	}
	accounts, err := loadBindings(ctx, c.State, session.UserID)
	if err != nil {
		return playerQueryFailed(ctx, c, err)
	}
	sessions := allUserSessions(cfg, session.UserID, accounts)
	if len(sessions) == 0 {
		return replyText(ctx, c, getMessage("unbind_message"))
	}

	unknown := getMessage("common.unknown")
	lines := make([]string, 0, len(sessions))
	newSigned := 0
	alreadySigned := 0
	failed := 0
	for _, account := range sessions {
		label := account.Binding.Nickname
		if label == "" {
			label = account.Binding.roleID()
		}
		if label == "" {
			label = unknown
		}
		res, err := account.Client.getData(ctx, "endfield_attendance", map[string]any{})
		if err != nil || !res.ok() {
			failed++
			lines = append(lines, getMessage("common.label_line", map[string]any{
				"label": label,
				"text":  getMessage("attendance.sign_failed"),
			}))
			continue
		}
		var data map[string]any
		if err := res.decode(&data); err != nil {
			failed++
			lines = append(lines, getMessage("common.label_line", map[string]any{
				"label": label,
				"text":  getMessage("attendance.sign_exception"),
			}))
			continue
		}
		if pexBool(data, "already_signed") {
			alreadySigned++
			lines = append(lines, getMessage("common.label_line", map[string]any{
				"label": label,
				"text":  getMessage("attendance.already_signed"),
			}))
			continue
		}
		newSigned++
		lines = append(lines, getMessage("common.label_line", map[string]any{
			"label": label,
			"text":  getMessage("attendance.sign_success", map[string]any{"items": attendanceAwards(data)}),
		}))
	}

	if adminBatch {
		header := fmt.Sprintf("【管理员批量签到】\n账号 %d｜新签 %d｜已签 %d｜失败 %d", len(sessions), newSigned, alreadySigned, failed)
		lines = append([]string{header}, lines...)
	}
	return replyText(ctx, c, strings.Join(lines, "\n"))
}

func attendanceAwards(data map[string]any) string {
	awards := pexSlice(data["awardIds"])
	if len(awards) == 0 {
		return " " + getMessage("attendance.no_award_info")
	}
	resourceMap := pexMap(data, "resourceInfoMap")
	lines := make([]string, 0, len(awards))
	for _, raw := range awards {
		item := pexAsMap(raw)
		id := pexString(item, "id")
		info := pexMap(resourceMap, id)
		name := pexString(info, "name")
		if name == "" {
			name = getMessage("common.unknown")
		}
		count := pexInt(item, 0, "count")
		if value, ok := pexNumber(info, "count"); ok {
			count = int(value)
		}
		lines = append(lines, getMessage("attendance.award_line", map[string]any{
			"name":  name,
			"count": count,
		}))
	}
	return "\n" + strings.Join(lines, "\n")
}

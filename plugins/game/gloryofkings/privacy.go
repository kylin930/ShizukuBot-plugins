package gloryofkings

// 营地隐私开关：把营地 App「设置 → 隐私」那 16 项搬到指令上。
// 语义与上游 GloryOfKings-Plugin（MIT, cchanlan）的 utils/campPrivacy.js、
// apps/campPrivacy.js 保持一致，关键结论都写在下面。
//
// 三条硬约束：
// 1. 只改「这个人当前选中的那个营地号」，不轮转 —— 隐私是账号级写操作。
// 2. 菜单读回的 status 是假的，所以不显示开关状态，也不做「已是目标状态就跳过」。
// 3. 「藏」走 form 网关，「放开」走主网关且必须带王者主角色 roleId。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	// privacyVisible / privacyHidden 是写请求里的 status。
	privacyVisible = 0
	privacyHidden  = 1

	// privacyWriteGap 是两次写之间的最小间隔；实测 1.1 秒会有一发静默不生效。
	privacyWriteGap = 3 * time.Second
	// privacyEffectDelaySec 是写完到真正生效的大概时间，只用来跟用户说一句「等多久」。
	privacyEffectDelaySec = 30
)

var (
	// battlePrivacyIDs：9=我的比赛 10=我的比赛详情 11=赛季表现 12=精彩时刻。
	battlePrivacyIDs = []int{9, 10, 11, 12}
	// unwritablePrivacyIDs：营地两个网关都回 -20011，改不动。
	unwritablePrivacyIDs = []int{17}
	// ineffectivePrivacyIDs：写得进去，但服务端不拿它拦数据。
	ineffectivePrivacyIDs = []int{4, 5, 7, 8, 15, 16}
	// verifiedPrivacyIDs：他见视角逐项验证过、确实有效。
	verifiedPrivacyIDs = []int{6, 9, 10, 11}

	// privacyWriting 正在改隐私的营地号。放模块级：一次改动要跑几十秒，
	// 同号并发会让两条指令都基于过期菜单算请求。
	privacyWriting sync.Map

	privacySetRe = regexp.MustCompile(`^隐私(公开|隐藏)\s*([0-9]*)\s*(\S*)$`)
)

/* ------------------------------------------------------------------ *
 * 纯逻辑（可脱机单测）
 * ------------------------------------------------------------------ */

type privacyDetail struct {
	FriendTypeID int
	Name         string
	Locked       bool
	Hint         string
}

type privacyItem struct {
	No          int
	PrivacyID   int
	ClassName   string
	Name        string
	Unwritable  bool
	Ineffective bool
	Verified    bool
	Note        string
	Details     []privacyDetail
}

type privacyClass struct {
	ClassName string
	Items     []privacyItem
}

type privacyMenu struct {
	Classes []privacyClass
	Items   []privacyItem
}

type privacyClassRaw struct {
	ClassName   string           `json:"className"`
	PrivacyItem []privacyItemRaw `json:"privacyItem"`
}

type privacyItemRaw struct {
	ID         scalarValue        `json:"id"`
	PrivacyID  scalarValue        `json:"privacyId"`
	Name       string             `json:"name"`
	ButtonText string             `json:"buttonText"`
	BottomText string             `json:"bottomText"`
	DetailInfo []privacyDetailRaw `json:"detailInfo"`
}

type privacyDetailRaw struct {
	FriendTypeID  scalarValue `json:"friendTypeId"`
	Name          string      `json:"name"`
	DisableModify bool        `json:"disableModify"`
	HintText      string      `json:"hintText"`
}

func containsInt(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// parsePrivacyMenu 解析整份隐私菜单（只取结构，不取 status）。
// 大类顺序、项顺序、编号全部照服务端原样，不重排。
func parsePrivacyMenu(raw json.RawMessage) privacyMenu {
	out := privacyMenu{}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return out
	}

	var classes []privacyClassRaw
	if strings.HasPrefix(text, "[") {
		_ = json.Unmarshal(raw, &classes)
	} else {
		var wrap struct {
			Data []privacyClassRaw `json:"data"`
		}
		if json.Unmarshal(raw, &wrap) == nil {
			classes = wrap.Data
		}
	}

	for _, cls := range classes {
		className := strings.TrimSpace(cls.ClassName)
		if className == "" {
			className = "其它"
		}
		group := privacyClass{ClassName: className}
		for _, entry := range cls.PrivacyItem {
			item, ok := normalizePrivacyItem(entry, className)
			if !ok {
				continue
			}
			group.Items = append(group.Items, item)
			out.Items = append(out.Items, item)
		}
		if len(group.Items) > 0 {
			out.Classes = append(out.Classes, group)
		}
	}
	return out
}

func normalizePrivacyItem(raw privacyItemRaw, className string) (privacyItem, bool) {
	no := raw.ID.Int()
	privacyID := raw.PrivacyID.Int()
	// 没编号或没 privacyId 的项既没法展示也没法改。
	if no <= 0 || privacyID < 0 {
		return privacyItem{}, false
	}

	item := privacyItem{
		No:          no,
		PrivacyID:   privacyID,
		ClassName:   className,
		Name:        strings.TrimSpace(raw.Name),
		Unwritable:  containsInt(unwritablePrivacyIDs, privacyID),
		Ineffective: containsInt(ineffectivePrivacyIDs, privacyID),
		Verified:    containsInt(verifiedPrivacyIDs, privacyID),
		Note:        strings.TrimSpace(firstNonEmpty(raw.ButtonText, raw.BottomText)),
	}
	if item.Name == "" {
		item.Name = fmt.Sprintf("第 %d 项", no)
	}

	for _, detail := range raw.DetailInfo {
		friendTypeID := detail.FriendTypeID.Int()
		if friendTypeID < 0 {
			continue
		}
		name := strings.TrimSpace(detail.Name)
		if name == "" {
			name = "未命名"
		}
		item.Details = append(item.Details, privacyDetail{
			FriendTypeID: friendTypeID,
			Name:         name,
			Locked:       detail.DisableModify,
			Hint:         strings.TrimSpace(detail.HintText),
		})
	}
	return item, true
}

// pickPrivacyItem 按营地服务端编号找项。
func pickPrivacyItem(items []privacyItem, no int) *privacyItem {
	for index := range items {
		if items[index].No == no {
			return &items[index]
		}
	}
	return nil
}

// matchPrivacyDetail 按服务端档位名匹配：先全等，再双向包含；命中多个返回 nil。
func matchPrivacyDetail(item privacyItem, text string) *privacyDetail {
	wanted := strings.TrimSpace(text)
	if wanted == "" || len(item.Details) == 0 {
		return nil
	}

	var exact []privacyDetail
	for _, detail := range item.Details {
		if detail.Name == wanted {
			exact = append(exact, detail)
		}
	}
	if len(exact) == 1 {
		return &exact[0]
	}

	var loose []privacyDetail
	for _, detail := range item.Details {
		if strings.Contains(detail.Name, wanted) || strings.Contains(wanted, detail.Name) {
			loose = append(loose, detail)
		}
	}
	if len(loose) == 1 {
		return &loose[0]
	}
	return nil
}

type privacyWrite struct {
	PrivacyID    int
	FriendTypeID int
	Status       int
	Name         string
}

// planPrivacyWrites 只跳过 locked，绝不按 status 跳过 —— 菜单读回的状态是假的。
func planPrivacyWrites(item privacyItem, status int, only *privacyDetail) (writes []privacyWrite, locked []privacyDetail) {
	pool := item.Details
	if only != nil {
		pool = []privacyDetail{*only}
	}
	for _, detail := range pool {
		if detail.Locked {
			locked = append(locked, detail)
			continue
		}
		writes = append(writes, privacyWrite{
			PrivacyID:    item.PrivacyID,
			FriendTypeID: detail.FriendTypeID,
			Status:       status,
			Name:         detail.Name,
		})
	}
	return writes, locked
}

// isVisiblePrivacyItem 图上不显示「营地不让改」和「改了不拦数据」的项。
func isVisiblePrivacyItem(item privacyItem) bool {
	return !containsInt(unwritablePrivacyIDs, item.PrivacyID) &&
		!containsInt(ineffectivePrivacyIDs, item.PrivacyID)
}

type privacyTag struct {
	Name   string
	Locked bool
}

type privacyCardRow struct {
	No       int
	Name     string
	Verified bool
	Details  []privacyTag
}

type privacyClassView struct {
	ClassName string
	Items     []privacyCardRow
}

type privacyPageView struct {
	Avatar        string
	Who           string
	CampID        string
	BattleText    string
	BattleHidden  bool
	BattleUnknown bool
	Classes       []privacyClassView
	GeneratedAt   string
}

func toPrivacyCard(item privacyItem) privacyCardRow {
	row := privacyCardRow{No: item.No, Name: item.Name, Verified: item.Verified}
	for _, detail := range item.Details {
		row.Details = append(row.Details, privacyTag{Name: detail.Name, Locked: detail.Locked})
	}
	return row
}

func describePrivacyItem(item privacyItem) string {
	names := make([]string, 0, len(item.Details))
	for _, detail := range item.Details {
		name := detail.Name
		if detail.Locked {
			name += "(锁)"
		}
		names = append(names, name)
	}

	var flags []string
	if item.Unwritable {
		flags = append(flags, "营地不让改")
	}
	if item.Ineffective {
		flags = append(flags, "营地不受这项约束、藏了别人也能查")
	}
	if item.Verified {
		flags = append(flags, "已实测有效")
	}
	suffix := ""
	if len(flags) > 0 {
		suffix = "（" + strings.Join(flags, "；") + "）"
	}
	return fmt.Sprintf("%d. %s%s\n    可设置：%s", item.No, item.Name, suffix, strings.Join(names, " / "))
}

type battleVisibilityView struct {
	Known  bool
	Hidden bool
	Count  int
}

// battleVisibility 只认他见视角的 data.invisible；拿不到就当「查不了」。
func battleVisibility(data *battleListData) battleVisibilityView {
	if data == nil {
		return battleVisibilityView{}
	}
	return battleVisibilityView{Known: true, Hidden: data.Invisible, Count: len(data.List)}
}

func battleVisibilityText(view battleVisibilityView) string {
	if !view.Known {
		return "别人能不能看到你的战绩：这次没查出来"
	}
	if view.Hidden {
		return "别人现在看不到你的战绩"
	}
	return fmt.Sprintf("别人现在能看到你的战绩（最近 %d 场）", view.Count)
}

/* ------------------------------------------------------------------ *
 * API
 * ------------------------------------------------------------------ */

// getPrivacyMenu 读隐私菜单。必须走主网关 + 本人 token：
// form 网关那份缺 4 项、也没有 disableModify。
func (c *campClient) getPrivacyMenu(ctx context.Context, a *campAuth) (privacyMenu, error) {
	code, msg, data, full, err := c.callRaw(ctx, "/user/getprivacymenu", struct{}{}, a)
	if err != nil {
		return privacyMenu{}, err
	}
	if code != 0 {
		return privacyMenu{}, businessError(code, msg)
	}
	raw := data
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = full
	}
	return parsePrivacyMenu(raw), nil
}

// setPrivacy 写一档隐私。隐藏走 form 网关；公开走主网关并带 roleId
// （主网关不带 roleId 会惰性成功、什么都不改）。
func (c *campClient) setPrivacy(ctx context.Context, privacyID, friendTypeID, status int, roleID string, a *campAuth) error {
	body := map[string]string{
		"privacyId":    strconv.Itoa(privacyID),
		"friendTypeId": strconv.Itoa(friendTypeID),
		"status":       strconv.Itoa(status),
	}
	if status == privacyHidden || roleID == "" {
		raw, err := c.gameFormCall(ctx, "/user/setprivacy", body, a)
		if err != nil {
			return err
		}
		// form 回裸 true / {data:true}；回 false 时不能报成「改好了」。
		if !formPrivacyAccepted(raw) {
			return errors.New("营地没有确认这次修改")
		}
		return nil
	}
	body["roleId"] = roleID
	_, err := c.call(ctx, "/user/setprivacy", body, a)
	return err
}

func formPrivacyAccepted(raw json.RawMessage) bool {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return false
	}
	if text == "true" {
		return true
	}
	var payload struct {
		ReturnCode scalarValue     `json:"returnCode"`
		Data       json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	if code, err := payload.ReturnCode.Int64(); err == nil && code == 0 {
		return true
	}
	dataText := strings.TrimSpace(string(payload.Data))
	if dataText == "true" {
		return true
	}
	var data struct {
		Success bool `json:"success"`
	}
	return json.Unmarshal(payload.Data, &data) == nil && data.Success
}

// mainRoleID 取 gameId=20001 下 isMain 的角色（没有就取第一个）。
func (c *campClient) mainRoleID(ctx context.Context, a *campAuth) string {
	groups, err := c.getRoleList(ctx, a)
	if err != nil {
		return ""
	}
	for _, group := range groups {
		if group.GameID.String() != "20001" {
			continue
		}
		for _, role := range group.Roles {
			if isMainRole(role.IsMain) {
				return role.RoleID.String()
			}
		}
		if len(group.Roles) > 0 {
			return group.Roles[0].RoleID.String()
		}
	}
	return ""
}

func isMainRole(value scalarValue) bool {
	text := strings.TrimSpace(value.String())
	return text == "1" || strings.EqualFold(text, "true")
}

// observerCredential 找一个「不是目标营地号」的可用登录态。
// 营地有自我豁免，自己查自己永远看得到战绩。
func observerCredential(state *userState, target string) *credential {
	if state == nil {
		return nil
	}
	for _, id := range state.IDs {
		if id == target {
			continue
		}
		if cred := state.Credentials[id]; cred != nil && cred.Token != "" && !cred.Invalid {
			return cred
		}
	}
	return nil
}

func battleSeenByOthers(ctx context.Context, client *campClient, state *userState, campID string) battleVisibilityView {
	observer := observerCredential(state, campID)
	if observer == nil {
		return battleVisibilityView{}
	}
	data, err := client.getMoreBattleList(ctx, campID, observer.auth(), 0)
	if err != nil {
		return battleVisibilityView{}
	}
	return battleVisibility(data)
}

/* ------------------------------------------------------------------ *
 * 指令
 * ------------------------------------------------------------------ */

func isPrivacyHelpCommand(body string) bool {
	for _, name := range []string{"隐私帮助", "隐私设置", "隐私状态", "隐私列表"} {
		if _, ok := cutCommand(body, name); ok {
			return true
		}
	}
	return false
}

func privacyBattleCommand(body string) (hidden bool, ok bool) {
	switch {
	case equalFold(body, "隐藏我的战绩"), equalFold(body, "王者隐藏我的战绩"):
		return true, true
	case equalFold(body, "公开我的战绩"), equalFold(body, "王者公开我的战绩"):
		return false, true
	}
	return false, false
}

func parsePrivacySetCommand(body string) (hidden bool, no int, detail string, ok bool) {
	match := privacySetRe.FindStringSubmatch(strings.TrimSpace(body))
	if match == nil {
		return false, 0, "", false
	}
	if match[2] != "" {
		no, _ = strconv.Atoi(match[2])
	}
	return match[1] == "隐藏", no, strings.TrimSpace(match[3]), true
}

func privacyLoginHint() string {
	return "还没有登录过王者营地号，先扫码登录一个：\n微信：#营地wx全局登录\nQQ：#营地QQ全局登录"
}

// privacyGate 三道闸门：私聊、有号、没在改。
func privacyGate(ctx context.Context, c sdk.Call) (*userState, string, *credential, bool, error) {
	if c.Event.GroupID != "" {
		return nil, "", nil, false, c.Reply(ctx, "隐私设置只能私聊我发")
	}

	state := loadUserState(ctx, c.State, c.Event.UserID)
	campID := state.currentID()
	if campID == "" {
		return nil, "", nil, false, c.Reply(ctx, privacyLoginHint())
	}

	// 隐私是账号级写操作，必须锁「当前选中的那个号」，不能走 bestCredential 回退。
	cred := state.Credentials[campID]
	if cred == nil || cred.Token == "" {
		return nil, "", nil, false, c.Reply(ctx, privacyLoginHint())
	}

	if _, busy := privacyWriting.Load(campID); busy {
		return nil, "", nil, false, c.Reply(ctx, "这个号上一条隐私改动还在跑，等它回话再发")
	}
	return state, campID, cred, true, nil
}

func privacyReadMenu(ctx context.Context, c sdk.Call, client *campClient, cred *credential) (*privacyMenu, error) {
	menu, err := client.getPrivacyMenu(ctx, cred.auth())
	if err != nil {
		_ = c.Reply(ctx, "读取隐私设置失败 —— "+signErrorText(err))
		return nil, err
	}
	if len(menu.Items) == 0 {
		_ = c.Reply(ctx, "没读到隐私设置，过会儿再试")
		return nil, ErrNotFound
	}
	return &menu, nil
}

// privacyHelpCommand 出图：能改什么 + 编号 + 战绩的实际效果 + 怎么改。
func privacyHelpCommand(ctx context.Context, c sdk.Call) error {
	state, campID, cred, ok, err := privacyGate(ctx, c)
	if !ok {
		return err
	}

	client := newCampClient()
	menu, err := privacyReadMenu(ctx, c, client, cred)
	if err != nil {
		return nil
	}

	seen := battleSeenByOthers(ctx, client, state, campID)
	who := accountLabel(state, campID)
	view := buildPrivacyPage(*menu, who, campID, seen, avatarURL(c.Event.UserID))

	if image, err := renderPrivacy(ctx, view); err == nil && len(image) > 0 {
		return c.ReplyImageBytes(ctx, image)
	}
	return c.Reply(ctx, privacyTextFallback(*menu, who, seen))
}

func buildPrivacyPage(menu privacyMenu, who, campID string, seen battleVisibilityView, avatar string) privacyPageView {
	view := privacyPageView{
		Avatar:        avatar,
		Who:           html.EscapeString(who),
		CampID:        html.EscapeString(campID),
		BattleText:    battleVisibilityText(seen),
		BattleHidden:  seen.Known && seen.Hidden,
		BattleUnknown: !seen.Known,
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05"),
	}
	for _, cls := range menu.Classes {
		group := privacyClassView{ClassName: html.EscapeString(cls.ClassName)}
		for _, item := range cls.Items {
			if !isVisiblePrivacyItem(item) {
				continue
			}
			card := toPrivacyCard(item)
			card.Name = html.EscapeString(card.Name)
			for index := range card.Details {
				card.Details[index].Name = html.EscapeString(card.Details[index].Name)
			}
			group.Items = append(group.Items, card)
		}
		if len(group.Items) > 0 {
			view.Classes = append(view.Classes, group)
		}
	}
	return view
}

func privacyUsageLines() []string {
	return []string{
		"#隐私隐藏 1 —— 把第 1 项对所有人藏起来",
		"#隐私公开 1 —— 把第 1 项放开给所有人",
		"#隐私隐藏 1 陌生人 —— 只改一档（档位名照图上写）",
		"#隐藏我的战绩 / #公开我的战绩 —— 一条顶战绩那 4 项",
	}
}

func privacyTextFallback(menu privacyMenu, who string, seen battleVisibilityView) string {
	lines := []string{fmt.Sprintf("营地隐私设置（%s）", who), battleVisibilityText(seen)}
	for _, cls := range menu.Classes {
		var items []privacyItem
		for _, item := range cls.Items {
			if isVisiblePrivacyItem(item) {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			continue
		}
		lines = append(lines, "", "【"+cls.ClassName+"】")
		for _, item := range items {
			lines = append(lines, describePrivacyItem(item))
		}
	}
	lines = append(lines, "", strings.Join(privacyUsageLines(), "\n"))
	return strings.Join(lines, "\n")
}

// privacyBattleShortcut 一条指令改战绩那 4 项的全部可改档位。
func privacyBattleShortcut(ctx context.Context, c sdk.Call, hidden bool) error {
	state, campID, cred, ok, err := privacyGate(ctx, c)
	if !ok {
		return err
	}
	_ = state

	client := newCampClient()
	menu, err := privacyReadMenu(ctx, c, client, cred)
	if err != nil {
		return nil
	}

	var items []privacyItem
	for _, item := range menu.Items {
		if containsInt(battlePrivacyIDs, item.PrivacyID) && !item.Ineffective && !item.Unwritable {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return c.Reply(ctx, "没读到战绩那几项隐私设置，过会儿再试")
	}

	status := privacyVisible
	if hidden {
		status = privacyHidden
	}
	jobs := make([]privacyJob, 0, len(items))
	for _, item := range items {
		jobs = append(jobs, privacyJob{Item: item, Status: status})
	}
	return privacyApplyWrites(ctx, c, client, campID, cred, jobs, privacyActionText(hidden))
}

// privacySetCommand `#隐私隐藏 1 [档位]` / `#隐私公开 1 [档位]`。
func privacySetCommand(ctx context.Context, c sdk.Call, hidden bool, no int, detailName string) error {
	_, campID, cred, ok, err := privacyGate(ctx, c)
	if !ok {
		return err
	}

	action := privacyActionText(hidden)
	if no == 0 {
		return c.Reply(ctx, fmt.Sprintf("要带编号，比如 #隐私%s 1\n发 #隐私帮助 看每一项的编号", action))
	}

	client := newCampClient()
	menu, err := privacyReadMenu(ctx, c, client, cred)
	if err != nil {
		return nil
	}

	item := pickPrivacyItem(menu.Items, no)
	if item == nil {
		return c.Reply(ctx, fmt.Sprintf("没有第 %d 项，发 #隐私帮助 看编号", no))
	}
	if item.Unwritable {
		return c.Reply(ctx, fmt.Sprintf("「%s」营地不让用指令改，只能在营地 App 里改", item.Name))
	}
	if item.Ineffective {
		return c.Reply(ctx, fmt.Sprintf("「%s」这项营地不受它约束：设成隐藏，别人照样能查到。\n这条指令就不改了，免得你以为藏住了", item.Name))
	}

	var only *privacyDetail
	if detailName != "" {
		only = matchPrivacyDetail(*item, detailName)
		if only == nil {
			names := make([]string, 0, len(item.Details))
			for _, detail := range item.Details {
				names = append(names, detail.Name)
			}
			return c.Reply(ctx, fmt.Sprintf("「%s」里对不上「%s」是哪一档，它有：\n%s\n照上面的名字写，不写档位名就是改全部",
				item.Name, detailName, strings.Join(names, " / ")))
		}
	}

	status := privacyVisible
	if hidden {
		status = privacyHidden
	}
	return privacyApplyWrites(ctx, c, client, campID, cred,
		[]privacyJob{{Item: *item, Status: status, Only: only}}, action)
}

func privacyActionText(hidden bool) string {
	if hidden {
		return "隐藏"
	}
	return "公开"
}

type privacyJob struct {
	Item   privacyItem
	Status int
	Only   *privacyDetail
}

// privacyApplyWrites 真正发写请求的那一段（单项和快捷共用）。
func privacyApplyWrites(ctx context.Context, c sdk.Call, client *campClient, campID string, cred *credential, jobs []privacyJob, action string) error {
	type plannedJob struct {
		job    privacyJob
		writes []privacyWrite
		locked []privacyDetail
	}

	plans := make([]plannedJob, 0, len(jobs))
	var writes []privacyWrite
	var lockedNames []string
	for _, job := range jobs {
		jobWrites, jobLocked := planPrivacyWrites(job.Item, job.Status, job.Only)
		plans = append(plans, plannedJob{job: job, writes: jobWrites, locked: jobLocked})
		writes = append(writes, jobWrites...)
		for _, detail := range jobLocked {
			lockedNames = append(lockedNames, job.Item.Name+"·"+detail.Name)
		}
	}

	label := ""
	if len(jobs) == 1 {
		label = "「" + jobs[0].Item.Name + "」"
		if jobs[0].Only != nil {
			label += "的「" + jobs[0].Only.Name + "」"
		}
	} else {
		label = fmt.Sprintf("战绩相关 %d 项", len(jobs))
	}

	if len(writes) == 0 {
		if len(lockedNames) > 0 {
			return c.Reply(ctx, fmt.Sprintf("%s这几档营地不让改：%s", label, strings.Join(lockedNames, "、")))
		}
		return c.Reply(ctx, fmt.Sprintf("%s没有可改的档位", label))
	}

	estimate := int((int64(len(writes)-1) * int64(privacyWriteGap/time.Second)))
	if len(writes) >= 3 {
		_ = c.Reply(ctx, fmt.Sprintf("正在把%s设成「%s」，要改 %d 档、约 %d 秒，改完我再说一声", label, action, len(writes), estimate))
	}

	// 「放开」方向只有主网关走得通，而它必须带主角色 roleId：一次性取好。
	wantsVisible := false
	for _, write := range writes {
		if write.Status == privacyVisible {
			wantsVisible = true
			break
		}
	}
	roleID := ""
	if wantsVisible {
		roleID = client.mainRoleID(ctx, cred.auth())
	}

	privacyWriting.Store(campID, struct{}{})
	defer privacyWriting.Delete(campID)

	var done []string
	var failed []string
	for index, write := range writes {
		// 第一发不等，之后每发之间隔 3 秒。
		if index > 0 {
			time.Sleep(privacyWriteGap)
		}
		err := client.setPrivacy(ctx, write.PrivacyID, write.FriendTypeID, write.Status, roleID, cred.auth())
		if err == nil {
			done = append(done, write.Name)
			continue
		}
		failed = append(failed, write.Name)
		// 频控之后每发都会失败，而且会把这个号静默 12 小时，立刻停。
		if errors.Is(err, ErrRateLimited) || strings.Contains(err.Error(), "30107") {
			break
		}
	}

	var lines []string
	if len(done) > 0 {
		lines = append(lines, fmt.Sprintf("%s已设成「%s」：%s", label, action, strings.Join(done, "、")))
	}
	if len(failed) > 0 {
		lines = append(lines, "没改成的："+strings.Join(failed, "、"))
	}
	if len(lockedNames) > 0 {
		lines = append(lines, "营地不让改的："+strings.Join(lockedNames, "、"))
	}
	if len(done) > 0 {
		lines = append(lines, "", fmt.Sprintf("营地那边 %d 秒左右生效，别反复发", privacyEffectDelaySec))
		if wantsVisible && roleID == "" {
			lines = append(lines, "这个号没绑王者角色，「公开」这个方向可能改不动 —— 去营地 App 里改")
		}

		var verified []string
		var unverified []string
		for _, job := range plans {
			if job.job.Item.Verified {
				verified = append(verified, "「"+job.job.Item.Name+"」")
			} else {
				unverified = append(unverified, "「"+job.job.Item.Name+"」")
			}
		}
		switch {
		case len(verified) > 0 && len(unverified) == 0:
			lines = append(lines, "这一项实测有效，过会儿发 #隐私帮助 能看出效果")
		case len(unverified) > 0 && len(verified) == 0:
			lines = append(lines, "这一项营地查不出来到底有没有作用，建议再进营地 App 里看一眼")
		case len(verified) > 0 && len(unverified) > 0:
			lines = append(lines, fmt.Sprintf("其中%s实测有效；%s营地查不出来，去 App 里看一眼更稳",
				strings.Join(verified, ""), strings.Join(unverified, "")))
		}

		for _, job := range plans {
			if containsInt(battlePrivacyIDs, job.job.Item.PrivacyID) {
				lines = append(lines, "过半分钟发 #隐私帮助，图上第一行会告诉你别人还看不看得到你的战绩")
				break
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return c.Reply(ctx, strings.Join(lines, "\n"))
}

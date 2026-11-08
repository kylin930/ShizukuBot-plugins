// Package gloryofkings 王者荣耀营地：账号绑定、扫码登录与战绩查询。
// 协议、文案与出图模板移植自上游 GloryOfKings-Plugin（MIT, cchanlan）。
package gloryofkings

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	platformWX = "wx"
	platformQQ = "qq"

	campIDGuide       = "绑定方式：#绑定营地 + 营地ID（纯数字）\n示例：#绑定营地 123456"
	bindOnlyDigits    = "营地ID仅支持数字，示例: #绑定营地123 或 #绑定营地 123"
	invalidIndex      = "序号无效，请输入正确的序号"
	loginBusyHint     = "当前已有一个营地登录任务在进行中，请先完成当前二维码或稍后再试"
	noCredentialHint  = "还没有可用的营地登录态\n发送 #营地wx全局登录 或 #营地QQ全局登录 扫码后就能查数据"
	loginTotalTimeout = 4 * time.Minute
)

// pendingLogins 是「同一用户同时只能发起一个扫码登录」的并发锁。
var pendingLogins sync.Map

var (
	slotStatsRe = regexp.MustCompile(`^(?:查询|王者)\s*([0-9]+)\s*(排位|巅峰)?\s*战绩\s*([0-9]*)$`)
	modeStatsRe = regexp.MustCompile(`^(排位|巅峰)\s*战绩\s*([0-9]*)$`)
	anyStatsRe  = regexp.MustCompile(`^(?:查询|王者|全部)?\s*战绩\s*([0-9]*)$`)
	helpRe      = regexp.MustCompile(`^(?i)王者(荣耀|农药)?(插件|plugin)?(帮助|help)\s*(.*)$`)
	digitsRe    = regexp.MustCompile(`^[0-9]+$`)
)

var idAliases = []string{
	"营地ID", "王者ID", "荣耀ID", "农药ID",
	"我的王者ID", "我的荣耀ID", "我的农药ID",
}

// Definition subscribes to the P1 message commands.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "gloryofkings",
		Name:        "王者荣耀营地",
		Version:     "1.0.0",
		Description: "绑定营地号、QQ/微信扫码登录、查询王者荣耀近期战绩并出图",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "gloryofkings", Trigger: "#王者帮助"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle routes one chat message to the matching command.
func Handle(ctx context.Context, c sdk.Call) error {
	body := commandBody(sdk.Text(c.Event))
	if body == "" {
		return nil
	}

	switch {
	case equalFold(body, "营地wx全局登录"):
		return startScanLogin(ctx, c, platformWX)
	case equalFold(body, "营地QQ全局登录"):
		return startScanLogin(ctx, c, platformQQ)
	case helpRe.MatchString(body):
		return c.Reply(ctx, helpText())
	}

	if typ, scope, force, ok := parseRankCommand(body); ok {
		return rankCommand(ctx, c, typ, scope, force)
	}

	if isIDQuery(body) {
		return idListCommand(ctx, c)
	}
	if isAddTokenCommand(body) {
		if campID, token, ok := parseAddToken(body); ok {
			return addTokenCommand(ctx, c, campID, token)
		}
		return c.Reply(ctx, addTokenUsage)
	}
	if rest, ok := cutCommand(body, "绑定营地"); ok {
		return bindCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "切换营地"); ok {
		return switchCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "删除营地"); ok {
		return deleteCommand(ctx, c, rest)
	}

	// P3 指令：营地币余额与营地币兑换。兑换命令先于余额，避免余额规则写宽后抢走。
	if rest, ok := cutCommand(body, "营地兑换"); ok {
		return exchangeCommand(ctx, c, rest)
	}
	if _, ok := cutCommand(body, "营地币"); ok {
		return coinCommand(ctx, c)
	}
	if _, ok := cutCommand(body, "营地余额"); ok {
		return coinCommand(ctx, c)
	}

	// P3 指令：营地隐私。帮助必须先于公开/隐藏，否则会被参数规则抢走。
	if isPrivacyHelpCommand(body) {
		return privacyHelpCommand(ctx, c)
	}
	if hidden, ok := privacyBattleCommand(body); ok {
		return privacyBattleShortcut(ctx, c, hidden)
	}
	if hidden, no, detail, ok := parsePrivacySetCommand(body); ok {
		return privacySetCommand(ctx, c, hidden, no, detail)
	}

	// P3 指令：观战降级为单次在线状态查询（上游推流服务与群名单不适用）。
	if isOnlineCommand(body) {
		return watchCommand(ctx, c)
	}

	// P3 指令：营地签到。状态命令必须先于签到命令，否则 #王者签到状态 会被当成签到。
	if _, ok := cutCommand(body, "签到状态"); ok {
		return signCommand(ctx, c, true)
	}
	if rest, ok := matchPrefixAny(body, "王者签到", "营地签到", "签到"); ok {
		_ = rest
		return signCommand(ctx, c, false)
	}

	// P2 指令：英雄 / 皮肤 / 赛季表现 / 趋势图。
	if rest, ok := cutCommand(body, "我的英雄"); ok {
		return myHeroCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "常用英雄"); ok {
		return heroListCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "英雄战力榜"); ok {
		return heroListCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "查皮肤"); ok {
		return heroSkinCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "皮肤墙"); ok {
		return skinWallCommand(ctx, c, rest, false)
	}
	if rest, ok := cutCommand(body, "全部皮肤"); ok {
		return skinWallCommand(ctx, c, rest, true)
	}
	if rest, ok := matchPrefixAny(body, "缺皮肤", "皮肤缺失", "还差什么皮肤", "还差哪些皮肤", "缺哪些皮肤"); ok {
		return skinMissingCommand(ctx, c, rest)
	}
	if rest, ok := matchPrefixAny(body, "全部排位表现", "全部赛季表现"); ok {
		return allSeasonCommand(ctx, c, false, rest)
	}
	if rest, ok := cutCommand(body, "全部巅峰表现"); ok {
		return allSeasonCommand(ctx, c, true, rest)
	}
	if rest, ok := matchPrefixAny(body, "巅峰趋势", "巅峰分趋势", "上分趋势", "分数趋势", "巅峰曲线"); ok {
		return scoreTrendCommand(ctx, c, rest)
	}
	if rest, ok := matchPrefixAny(body, "段位趋势", "段位曲线", "排位趋势", "段位走势"); ok {
		return rankTrendCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "巅峰表现"); ok {
		return peakCommand(ctx, c, rest)
	}
	if rest, ok := cutCommand(body, "赛季表现"); ok {
		return seasonCommand(ctx, c, "all", rest)
	}
	if rest, ok := cutCommand(body, "排位表现"); ok {
		return seasonCommand(ctx, c, "rank", rest)
	}
	if slot, option, detail, ok := parseStatsQuery(body); ok {
		return queryCommand(ctx, c, slot, option, detail)
	}
	// 英雄战绩放在战绩规则之后，保证 #查询2战绩 不会被当成英雄名。
	if hero, option, ok := parseHeroBattle(body); ok {
		return heroBattleCommand(ctx, c, hero, option)
	}
	return nil
}

// matchPrefixAny 依次尝试多个指令前缀。
func matchPrefixAny(body string, names ...string) (string, bool) {
	for _, name := range names {
		if rest, ok := cutCommand(body, name); ok {
			return rest, true
		}
	}
	return "", false
}

/* ------------------------------------------------------------------ *
 * 指令解析
 * ------------------------------------------------------------------ */

// commandBody 去掉开头的 #/＃ 与空白；上游对查询类指令允许省略 #。
func commandBody(text string) string {
	body := strings.TrimSpace(text)
	body = strings.TrimPrefix(body, "＃")
	body = strings.TrimPrefix(body, "#")
	return strings.TrimSpace(body)
}

func equalFold(value, target string) bool { return strings.EqualFold(value, target) }

// cutCommand 匹配指令前缀；上游正则普遍带 (王者)? 可选前缀，#王者皮肤墙 也有效。
func cutCommand(body, name string) (string, bool) {
	if rest, ok := cutCommandRaw(body, name); ok {
		return rest, true
	}
	if trimmed := strings.TrimPrefix(body, "王者"); trimmed != body {
		return cutCommandRaw(trimmed, name)
	}
	return "", false
}

func cutCommandRaw(body, name string) (string, bool) {
	if !strings.HasPrefix(body, name) {
		return "", false
	}
	return strings.TrimSpace(body[len(name):]), true
}

func isIDQuery(body string) bool {
	for _, alias := range idAliases {
		if equalFold(body, alias) {
			return true
		}
	}
	return false
}

// parseStatsQuery 解析 #查询战绩 / #排位战绩 / #查询N战绩 / #查询N排位战绩；
// 末尾的场次序号（如 #排位战绩3）用于查看单场详情，0 表示只看列表。
func parseStatsQuery(body string) (slot int, option int, detail int, ok bool) {
	if matched := slotStatsRe.FindStringSubmatch(body); matched != nil {
		slot, _ = strconv.Atoi(matched[1])
		detail, _ = strconv.Atoi(matched[3])
		return slot, modeOption(matched[2]), detail, true
	}
	if matched := modeStatsRe.FindStringSubmatch(body); matched != nil {
		detail, _ = strconv.Atoi(matched[2])
		return 0, modeOption(matched[1]), detail, true
	}
	if matched := anyStatsRe.FindStringSubmatch(body); matched != nil {
		detail, _ = strconv.Atoi(matched[1])
		return 0, 0, detail, true
	}
	return 0, 0, 0, false
}

func modeOption(mode string) int {
	switch mode {
	case "排位":
		return 1
	case "巅峰":
		return 4
	default:
		return 0
	}
}

// parseIndex 把用户输入的 1 起序号转成 0 起下标；空串、非数字、0 一律无效。
func parseIndex(rest string) (int, bool) {
	if !digitsRe.MatchString(rest) {
		return 0, false
	}
	value, err := strconv.Atoi(rest)
	if err != nil || value < 1 {
		return 0, false
	}
	return value - 1, true
}

/* ------------------------------------------------------------------ *
 * 账号管理
 * ------------------------------------------------------------------ */

func bindCommand(ctx context.Context, c sdk.Call, rest string) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if rest == "" {
		return sendBindGuide(ctx, c, "")
	}
	if !digitsRe.MatchString(rest) {
		return c.Reply(ctx, bindOnlyDigits)
	}
	if state.hasID(rest) {
		name := refreshRoleName(ctx, newCampClient(), state, rest)
		message := "该ID已经绑定过了"
		if name != "" {
			message += "：" + rest + " " + name
		}
		return c.Reply(ctx, message)
	}

	state.bindID(rest)
	if name := refreshRoleName(ctx, newCampClient(), state, rest); name != "" {
		_ = name
	}
	if err := saveUserState(ctx, c.State, c.Event.UserID, state); err != nil {
		return c.Reply(ctx, "保存绑定失败："+err.Error())
	}
	registerRankAccounts(ctx, c.State, c.Event.UserID, state.IDs)
	return sendAccountCard(ctx, c, state, "绑定", rest, "")
}

func switchCommand(ctx context.Context, c sdk.Call, rest string) error {
	index, ok := parseIndex(rest)
	if !ok {
		return c.Reply(ctx, invalidIndex)
	}
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return c.Reply(ctx, "您还没有绑定任何ID，请先绑定")
	}
	if index >= len(state.IDs) {
		return c.Reply(ctx, invalidIndex)
	}
	state.Current = index
	if err := saveUserState(ctx, c.State, c.Event.UserID, state); err != nil {
		return c.Reply(ctx, "保存切换结果失败："+err.Error())
	}
	return sendAccountCard(ctx, c, state, "切换", "", "")
}

func deleteCommand(ctx context.Context, c sdk.Call, rest string) error {
	index, ok := parseIndex(rest)
	if !ok {
		return c.Reply(ctx, invalidIndex)
	}
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return c.Reply(ctx, "您还没有绑定任何ID")
	}
	if index >= len(state.IDs) {
		return c.Reply(ctx, invalidIndex)
	}

	removed := state.IDs[index]
	removedName := state.Names[removed]
	state.removeID(index)
	if err := saveUserState(ctx, c.State, c.Event.UserID, state); err != nil {
		return c.Reply(ctx, "保存删除结果失败："+err.Error())
	}
	if len(state.IDs) == 0 {
		return c.Reply(ctx, "已删除营地ID："+removed)
	}
	return sendAccountCard(ctx, c, state, "删除", removed, removedName)
}

func idListCommand(ctx context.Context, c sdk.Call) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return sendBindGuide(ctx, c, "")
	}
	return sendAccountCard(ctx, c, state, "查询", "", "")
}

// sendAccountCard 渲染账号状态看板；出图失败时退回纯文本列表。
func sendAccountCard(ctx context.Context, c sdk.Call, state *userState, typ, targetID, targetName string) error {
	idList, current := accountCardText(state)
	if current == "" {
		return sendBindGuide(ctx, c, "")
	}
	if targetID == "" {
		targetID = current
	}
	if targetName == "" {
		targetName = state.Names[targetID]
	}
	image, err := renderAccountCard(ctx, accountCardData{
		Type:      typ,
		WzryID:    targetID,
		WzryName:  html.EscapeString(targetName),
		IDList:    idList,
		Funcs:     accountCardFuncs,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return c.Reply(ctx, "账号卡片出图失败，稍后再试\n\n"+idList)
	}
	return c.ReplyImageBytes(ctx, image)
}

// refreshRoleName 用可用登录态补一次角色名；失败不影响主流程。
func refreshRoleName(ctx context.Context, client *campClient, state *userState, id string) string {
	cred := state.bestCredential(id)
	if cred == nil {
		return ""
	}
	profile, err := client.getProfile(ctx, id, cred.auth())
	if err != nil {
		return ""
	}
	var role profileRole
	if len(profile.RoleList) > 0 {
		role = profile.RoleList[0]
	}
	for _, item := range profile.RoleList {
		if item.RoleID.String() == profile.TargetRoleID.String() {
			role = item
			break
		}
	}
	name := cleanRoleName(role.RoleName)
	if name != "" {
		state.Names[id] = name
	}
	return name
}

// cleanRoleName 去掉营地昵称里的控制字符与私有区图标，避免出图显示空白或豆腐块。
func cleanRoleName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20:
			continue
		case r >= 0x7f && r < 0xa0:
			continue
		case r >= 0xe000 && r <= 0xf8ff:
			continue
		case r == 0x200b || r == 0xfeff:
			continue
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

/* ------------------------------------------------------------------ *
 * 扫码登录
 * ------------------------------------------------------------------ */

func startScanLogin(ctx context.Context, c sdk.Call, platform string) error {
	key := c.Event.BotID + "|" + c.Event.UserID
	if _, loaded := pendingLogins.LoadOrStore(key, struct{}{}); loaded {
		return c.Reply(ctx, loginBusyHint)
	}
	go runScanLogin(c, platform)
	return nil
}

// runScanLogin 在后台完成「生成二维码 → 等扫码 → 落库」，不占用事件处理超时。
func runScanLogin(c sdk.Call, platform string) {
	key := c.Event.BotID + "|" + c.Event.UserID
	defer pendingLogins.Delete(key)

	ctx, cancel := context.WithTimeout(context.Background(), loginTotalTimeout)
	defer cancel()

	var (
		qr      []byte
		err     error
		wait    func(context.Context, func(string)) (*credential, error)
		closeFn func()
	)
	if platform == platformQQ {
		var session *qqSession
		session, qr, err = createQQSession(ctx)
		if err == nil {
			wait = session.wait
			closeFn = session.Close
		}
	} else {
		var session *wechatSession
		session, qr, err = createWechatSession(ctx)
		if err == nil {
			wait = session.wait
		}
	}
	if closeFn != nil {
		defer closeFn()
	}
	if err != nil {
		if platform == platformQQ {
			_ = c.Reply(ctx, "生成营地登录二维码失败，请稍后重试")
		} else {
			_ = c.Reply(ctx, "生成营地登录二维码失败："+err.Error())
		}
		return
	}

	prompt := "请扫描二维码完成营地登录"
	if platform == platformQQ {
		prompt = "请用手机 QQ 扫描二维码完成营地登录"
	}
	_ = c.Reply(ctx, prompt+"，二维码 3 分钟内有效。\n登录成功后会自动保存登录态并绑定这个营地号，之后就能查数据了。")
	if err := c.ReplyImageBytes(ctx, qr); err != nil {
		_ = c.Reply(ctx, "二维码发送失败，请稍后重试")
		return
	}

	scanned := false
	cred, err := wait(ctx, func(status string) {
		if status == "404" {
			scanned = true
			_ = c.Reply(ctx, "已扫码，请在手机上确认营地登录。若长时间未确认，本次登录会自动超时。")
		}
	})
	if err != nil {
		_ = c.Reply(ctx, loginErrorMessage(err, scanned))
		return
	}
	if cred == nil || cred.UserID == "" {
		_ = c.Reply(ctx, "登录成功，但营地没有返回营地ID，请稍后重试")
		return
	}

	state := loadUserState(ctx, c.State, c.Event.UserID)
	state.putCredential(cred)
	state.bindID(cred.UserID)
	if cred.Nickname != "" {
		if name := cleanRoleName(cred.Nickname); name != "" {
			state.Names[cred.UserID] = name
		}
	}
	if err := saveUserState(ctx, c.State, c.Event.UserID, state); err != nil {
		_ = c.Reply(ctx, "保存登录态失败："+err.Error())
		return
	}
	registerRankAccounts(ctx, c.State, c.Event.UserID, state.IDs)

	nickname := cred.Nickname
	if nickname == "" {
		nickname = "未命名"
	}
	_ = c.Reply(ctx, fmt.Sprintf(
		"登录成功，已绑定这个营地号。\n\n营地ID：%s\n\n昵称：%s\n\n现在可以查数据了。\n\n有效期约 30 天，失效了重发这条指令。",
		cred.UserID, nickname,
	))
}

func loginErrorMessage(err error, scanned bool) string {
	message := err.Error()
	if strings.Contains(message, "超时") {
		if scanned {
			return "已扫码，但长时间未确认，营地登录已超时，请重新发起"
		}
		return "营地登录等待超时，请重新发起"
	}
	return message
}

/* ------------------------------------------------------------------ *
 * 战绩查询
 * ------------------------------------------------------------------ */

func queryCommand(ctx context.Context, c sdk.Call, slot, option, detailNo int) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return sendBindGuide(ctx, c, "你还没有绑定营地ID")
	}

	id := state.currentID()
	if slot > 0 {
		if slot > 9999 {
			id = strconv.Itoa(slot)
		} else {
			if slot > len(state.IDs) {
				return c.Reply(ctx, fmt.Sprintf("序号无效，你当前只绑定了 %d 个营地ID", len(state.IDs)))
			}
			id = state.IDs[slot-1]
		}
	}
	if id == "" {
		return sendBindGuide(ctx, c, "")
	}

	cred := state.bestCredential(id)
	if cred == nil {
		return c.Reply(ctx, noCredentialHint)
	}

	data, err := newCampClient().getMoreBattleList(ctx, id, cred.auth(), option)
	if err != nil {
		if errors.Is(err, ErrAuthInvalid) {
			return c.Reply(ctx, "营地登录态已失效，请重新发送 #营地wx全局登录 或 #营地QQ全局登录")
		}
		if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrProfileHidden) {
			return c.Reply(ctx, err.Error())
		}
		return c.Reply(ctx, "查询失败："+err.Error())
	}

	// 查战绩顺手归档，供 #巅峰趋势 / #段位趋势 取数（P2）。
	archiveBattles(ctx, c.State, id, data.List)

	if detailNo > 0 {
		if detailNo > len(data.List) {
			return c.Reply(ctx, fmt.Sprintf("序号无效，当前列表只有 %d 场", len(data.List)))
		}
		return battleDetailCommand(ctx, c, id, cred, data.List[detailNo-1])
	}

	view := buildBattleView(data, "", data.RoleJobName, option)
	if view.EmptyState {
		view.EmptyTitle = "暂无可查询战绩"
		switch {
		case option == 1:
			view.EmptyDescription = "ID: " + id + " 最近没有排位战绩"
		case option == 4:
			view.EmptyDescription = "ID: " + id + " 最近没有巅峰战绩"
		case data.InvisDes != "":
			view.EmptyDescription = html.EscapeString(data.InvisDes)
		default:
			view.EmptyDescription = "ID: " + id + " 当前没有可展示的战绩数据"
		}
	}

	image, err := renderBattleList(ctx, view)
	if err != nil {
		return c.ReplyKeyboardOrText(ctx, "战绩列表出图失败，稍后再试\n\n"+battleSummary(data, id), campKeyboard())
	}
	// 图片消息不能带键盘：先发一条带快捷按钮的 Markdown，再发战绩图。
	if err := c.ReplyKeyboardOrText(ctx, "营地ID "+id+" 战绩已生成", campKeyboard()); err != nil {
		return err
	}
	return c.ReplyImageBytes(ctx, image)
}

// battleSummary 是没有浏览器时退回的纯文本战绩摘要。
func battleSummary(data *battleListData, id string) string {
	if data == nil || len(data.List) == 0 {
		return "ID: " + id + " 暂无可查询战绩"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "营地ID %s 最近 %d 场战绩：\n", id, len(data.List))
	for index, item := range data.List {
		if index >= 8 {
			fmt.Fprintf(&builder, "……共 %d 场", len(data.List))
			break
		}
		result := "负"
		if item.GameResult == 1 {
			result = "胜"
		}
		mode := item.MapName
		if mode == "" {
			mode = "对局"
		}
		fmt.Fprintf(&builder, "%d. %s %s %d/%d/%d\n", index+1, result, mode, item.KillCnt, item.DeadCnt, item.AssistCnt)
	}
	return strings.TrimRight(builder.String(), "\n")
}

/* ------------------------------------------------------------------ *
 * 帮助
 * ------------------------------------------------------------------ */

func helpText() string {
	return strings.Join([]string{
		"王者营地指令：",
		"#绑定营地 <营地ID>",
		"#切换营地<序号> / #删除营地<序号>",
		"#营地ID（别名 #王者ID / #我的王者ID）",
		"#营地wx全局登录 / #营地QQ全局登录",
		"#王者添加tk <营地ID> <token>（私聊）",
		"#查询战绩 / #排位战绩 / #巅峰战绩 / #查询N战绩",
		"#查询战绩<序号> / #排位战绩<序号>（单场详情）",
		"#排位bot排名 / #巅峰bot排名（本机排名）",
		"#排位总排名 / #巅峰总排名（全部用户排名）",
		"#排位排名 / #巅峰排名（群内排名，当前平台不可用）",
		"#查<英雄>战绩 / #查战绩 <英雄>",
		"#我的英雄[N] / #常用英雄 / #英雄战力榜",
		"#皮肤墙[N] / #全部皮肤 / #缺皮肤[英雄名] / #查皮肤 <英雄名>",
		"#赛季表现[sN] / #排位表现[sN] / #巅峰表现[sN]",
		"#全部排位表现 / #全部巅峰表现",
		"#巅峰趋势 / #段位趋势",
		"#王者签到 / #王者签到状态",
		"#营地币 / #营地余额",
		"#营地兑换[N] [确认]",
		"#隐私帮助 / #隐私状态（私聊）",
		"#隐私隐藏 N [档位] / #隐私公开 N [档位]（私聊）",
		"#隐藏我的战绩 / #公开我的战绩（私聊）",
		"#王者在线 / #在线状态",
		"#王者帮助",
	}, "\n")
}

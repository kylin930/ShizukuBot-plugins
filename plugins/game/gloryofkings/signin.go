package gloryofkings

// 营地签到：与上游 apps/signIn.js、utils/campSignTask.js、utils/campDailyTask.js 口径一致。
// 上游的自动签到定时任务在本插件里不做（QQ 官方机器人无法主动推送），只保留被动指令。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	campTaskGameID    = "50001"
	feedTypeInfo      = 14
	conditionShare    = 1
	taskFuncViewNews  = "todayViewNews"
	taskFuncAddLikes  = "todayAddLikes"
	taskFuncShare     = "shareByHelper"
	signCodeOK        = 0
	signCodeAlready   = -105203
	signCodeTooFast   = -105206
	signCodeMissingID = -105204
)

var weekNames = []string{"一", "二", "三", "四", "五", "六", "日"}

// ---------- 接口数据结构 ----------

type campGift struct {
	Name        string      `json:"name"`
	GiftText    string      `json:"giftText"`
	PackageNum  scalarValue `json:"packageNum"`
	GiftNum     scalarValue `json:"giftNum"`
	IconURL     string      `json:"iconUrl"`
	GiftIconURL string      `json:"giftIconUrl"`
	Desc        string      `json:"desc"`
	GiftDesc    string      `json:"giftDesc"`
}

func (gift campGift) displayName() string {
	return strings.TrimSpace(firstNonEmpty(gift.Name, gift.GiftText))
}

func (gift campGift) displayNum() string {
	return strings.TrimSpace(firstNonEmpty(gift.PackageNum.String(), gift.GiftNum.String()))
}

func (gift campGift) displayIcon() string {
	return strings.TrimSpace(firstNonEmpty(gift.IconURL, gift.GiftIconURL))
}

func (gift campGift) displayDesc() string {
	raw := strings.TrimSpace(firstNonEmpty(gift.Desc, gift.GiftDesc))
	if index := strings.Index(raw, "##"); index >= 0 {
		raw = raw[:index]
	}
	return strings.TrimSpace(raw)
}

type campRole struct {
	RoleID   scalarValue `json:"roleId"`
	RoleName string      `json:"roleName"`
	AreaID   scalarValue `json:"areaId"`
	ServerID scalarValue `json:"serverId"`
	OpenID   scalarValue `json:"openId"`
	IsMain   scalarValue `json:"isMain"`
}

type campGameRoleGroup struct {
	GameID scalarValue `json:"gameId"`
	Roles  []campRole  `json:"roles"`
}

type campWeekDay struct {
	Date scalarValue `json:"date"`
	Gift []campGift  `json:"gift"`
}

type campTotalDay struct {
	Date scalarValue `json:"date"`
	Days scalarValue `json:"days"`
	Gift []campGift  `json:"gift"`
}

type campSignInfo struct {
	WeekSignMap   string         `json:"weekSignMap"`
	SeqSignDays   scalarValue    `json:"seqSignDays"`
	UserTotalSign scalarValue    `json:"userTotalSign"`
	CanExtraSign  scalarValue    `json:"canExtraSign"`
	WeekList      []campWeekDay  `json:"weekList"`
	TotalList     []campTotalDay `json:"totalList"`
}

type campSignResult struct {
	UserSign      string      `json:"userSign"`
	SeqSignDays   scalarValue `json:"seqSignDays"`
	TotalSignDays scalarValue `json:"totalSignDays"`
	SignDate      string      `json:"signDate"`
	GiftList      []campGift  `json:"giftList"`
}

type campTaskGift struct {
	Name       string      `json:"name"`
	PackageNum scalarValue `json:"packageNum"`
}

type campTask struct {
	TaskID        scalarValue    `json:"taskId"`
	Func          string         `json:"func"`
	Title         string         `json:"title"`
	Desc          string         `json:"desc"`
	Currency      scalarValue    `json:"currency"`
	TaskScore     scalarValue    `json:"taskScore"`
	UserScore     scalarValue    `json:"userScore"`
	FinishStatus  scalarValue    `json:"finishStatus"`
	PackageStatus scalarValue    `json:"packageStatus"`
	JumpURL       string         `json:"jumpUrl"`
	Gifts         []campTaskGift `json:"gifts"`
}

func (task campTask) finished() bool { return task.FinishStatus.String() == "1" }

func (task campTask) claimed() bool { return task.PackageStatus.String() == "1" }

func (task campTask) claimable() bool { return task.finished() && !task.claimed() }

type campTaskData struct {
	TaskList   []campTask  `json:"taskList"`
	MyCurrency scalarValue `json:"myCurrency"`
}

// ---------- 接口调用 ----------

// getRoleList 查营地号绑定的游戏角色。rolelist 的 gameList 可能在外层（实测），
// 也可能被塞进 data，两种都兜住。
func (c *campClient) getRoleList(ctx context.Context, a *campAuth) ([]campGameRoleGroup, error) {
	code, msg, data, full, err := c.callRaw(ctx, "/game/rolelist", struct{}{}, a)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, businessError(code, msg)
	}
	var out struct {
		GameList []campGameRoleGroup `json:"gameList"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	if len(out.GameList) == 0 && len(full) > 0 {
		_ = json.Unmarshal(full, &out)
	}
	return out.GameList, nil
}

func (c *campClient) getSignInfo(ctx context.Context, a *campAuth) (*campSignInfo, error) {
	data, err := c.call(ctx, "/operation/action/signinfo", struct{}{}, a)
	if err != nil {
		return nil, err
	}
	var info campSignInfo
	if len(data) > 0 {
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, err
		}
	}
	return &info, nil
}

// doSign 发签到写请求。roleId 为空时整个字段都不发（空串会撞另一个错误码）。
func (c *campClient) doSign(ctx context.Context, roleID string, a *campAuth) (int, string, *campSignResult, error) {
	body := map[string]any{}
	if strings.TrimSpace(roleID) != "" {
		body["roleId"] = roleID
	}
	code, msg, data, _, err := c.callRaw(ctx, "/operation/action/newsignin", body, a)
	if err != nil {
		return 0, "", nil, err
	}
	var result campSignResult
	if len(data) > 0 {
		_ = json.Unmarshal(data, &result)
	}
	return code, msg, &result, nil
}

// taskAuth 是福利中心接口专用的登录态（header 的 gameid 要是 50001）。
func taskAuth(a *campAuth) *campAuth {
	copied := *a
	copied.CGameID = campTaskGameID
	return &copied
}

func (c *campClient) getTaskList(ctx context.Context, a *campAuth) (*campTaskData, error) {
	data, err := c.call(ctx, "/operation/action/tasklist", map[string]any{"gameId": campTaskGameID}, taskAuth(a))
	if err != nil {
		return nil, err
	}
	var out campTaskData
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// claimTasks 领取一批已完成任务的奖励，返回业务码（-10530338 之类要靠它分支时用）。
func (c *campClient) claimTasks(ctx context.Context, taskIDs []string, a *campAuth) (int, string, error) {
	ids := make([]scalarValue, 0, len(taskIDs))
	for _, id := range taskIDs {
		ids = append(ids, scalarValue(id))
	}
	body := map[string]any{
		"gameId":  campTaskGameID,
		"taskIds": ids,
	}
	code, msg, _, _, err := c.callRaw(ctx, "/operation/action/rewardtask", body, taskAuth(a))
	return code, msg, err
}

func (c *campClient) getInfoFeed(ctx context.Context, a *campAuth) (string, error) {
	data, err := c.call(ctx, "/info/followinfo", struct{}{}, a)
	if err != nil {
		return "", err
	}
	var out struct {
		List []struct {
			Type        scalarValue `json:"type"`
			InfoContent struct {
				InfoID scalarValue `json:"infoId"`
			} `json:"infoContent"`
		} `json:"list"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
	}
	for _, item := range out.List {
		if item.Type.Int() == feedTypeInfo && item.InfoContent.InfoID.String() != "" {
			return item.InfoContent.InfoID.String(), nil
		}
	}
	return "", nil
}

func (c *campClient) viewInfo(ctx context.Context, infoID string, a *campAuth) error {
	_, err := c.call(ctx, "/info/detailinfo", map[string]any{"iInfoId": infoID}, a)
	return err
}

func (c *campClient) likeInfo(ctx context.Context, infoID string, a *campAuth) error {
	_, err := c.call(ctx, "/info/addlike", map[string]any{"iInfoId": infoID}, a)
	return err
}

func (c *campClient) reportShare(ctx context.Context, a *campAuth) error {
	_, err := c.gameFormCall(ctx, "/play/gettaskconditiondata", map[string]string{"type": "1"}, a)
	return err
}

// signErrorText 把内部错误翻译成能给用户看的一句话。
func signErrorText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrAuthInvalid) {
		return "营地登录态已失效，重新登录后再试"
	}
	return err.Error()
}

// accountLabel 是出图用的营地号显示名：优先绑定时的昵称，其次登录态昵称。
func accountLabel(state *userState, campID string) string {
	if name := cleanRoleName(state.Names[campID]); name != "" {
		return name
	}
	if cred := state.Credentials[campID]; cred != nil {
		if name := cleanRoleName(cred.Nickname); name != "" {
			return name
		}
	}
	return campID
}

// ---------- 纯逻辑 ----------

// isSignedToday 判断今天签没签：weekSignMap 下标是「周一=0」，
// 而 time.Weekday 是「周日=0」，必须换算。返回 nil 表示判不了。
func isSignedToday(weekSignMap string, now time.Time) *bool {
	text := strings.TrimSpace(weekSignMap)
	if len(text) != 7 {
		return nil
	}
	index := (int(now.Weekday()) + 6) % 7
	char := text[index]
	if char != '0' && char != '1' {
		return nil
	}
	value := char == '1'
	return &value
}

func pickMainRole(groups []campGameRoleGroup) *campRole {
	for _, group := range groups {
		if group.GameID.String() != "20001" {
			continue
		}
		for index := range group.Roles {
			return &group.Roles[index]
		}
	}
	return nil
}

func classifySignCode(code int) string {
	switch code {
	case signCodeOK:
		return "ok"
	case signCodeAlready:
		return "already"
	case signCodeTooFast:
		return "too-fast"
	case signCodeMissingID:
		return "missing-role"
	default:
		return "error"
	}
}

// indexWeekGifts 按 date(1~7) 索引本周奖励，别用数组下标当周几。
func indexWeekGifts(info *campSignInfo) map[int][]campGift {
	out := make(map[int][]campGift)
	if info == nil {
		return out
	}
	for _, day := range info.WeekList {
		date := day.Date.Int()
		if date < 1 || date > 7 {
			continue
		}
		out[date] = day.Gift
	}
	return out
}

// giftSummary 是七格里的奖励摘要：去掉括号里的天数再截断。
func giftSummary(gifts []campGift, maxLength int) string {
	var names []string
	for _, gift := range gifts {
		name := gift.displayName()
		if name == "" {
			continue
		}
		name = stripBrackets(name)
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	text := strings.Join(names, "|")
	runes := []rune(text)
	if len(runes) > maxLength {
		return string(runes[:maxLength-1]) + "…"
	}
	return text
}

func stripBrackets(text string) string {
	var builder strings.Builder
	depth := 0
	for _, char := range text {
		switch {
		case char == '（' || char == '(':
			depth++
		case char == '）' || char == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			builder.WriteRune(char)
		}
	}
	return strings.TrimSpace(builder.String())
}

// ---------- 签到编排 ----------

// signAccountResult 是一个营地号的签到结果。
type signAccountResult struct {
	Name       string
	CampID     string
	StateClass string // done / new / fail
	StateText  string
	RoleName   string
	Role       *campRole
	Info       *campSignInfo

	Signed  bool
	Already bool
	NoRole  bool

	ActedText   string
	ClaimedText string
	Coin        int
	HasCoin     bool
	RewardNote  string
	FailReason  string
}

// probeSignAccount 只查不写：查角色 + 查签到状态。
// 「没绑王者角色」只能由 rolelist 判定，weekSignMap 全 0 不代表没角色。
func probeSignAccount(ctx context.Context, client *campClient, state *userState, campID string) signAccountResult {
	result := signAccountResult{
		Name:   accountLabel(state, campID),
		CampID: campID,
	}
	credential := state.Credentials[campID]
	if credential == nil {
		result.StateClass = "fail"
		result.StateText = "没有登录态"
		result.FailReason = "这个营地号还没有可用的登录态，重新绑定后再试"
		return result
	}
	auth := credential.auth()

	groups, err := client.getRoleList(ctx, auth)
	if err != nil {
		result.StateClass = "fail"
		result.StateText = "查询失败"
		result.FailReason = signErrorText(err)
		return result
	}
	role := pickMainRole(groups)
	if role == nil {
		result.NoRole = true
		result.StateClass = "fail"
		result.StateText = "没有王者角色"
		result.FailReason = "这个营地号还没绑定王者角色，先在游戏里登录一次再来签"
		return result
	}
	result.RoleName = role.RoleName
	result.Role = role

	info, err := client.getSignInfo(ctx, auth)
	if err != nil {
		result.StateClass = "fail"
		result.StateText = "查询失败"
		result.FailReason = signErrorText(err)
		return result
	}
	result.Info = info

	signed := isSignedToday(info.WeekSignMap, time.Now())
	if signed == nil {
		result.StateClass = "fail"
		result.StateText = "数据异常"
		result.FailReason = "签到状态没读懂，过一会儿再试"
		return result
	}
	if *signed {
		result.Already = true
		result.StateClass = "done"
		result.StateText = "今天已签"
	} else {
		result.StateClass = "new"
		result.StateText = "今天还没签"
	}
	return result
}

// signOneAccount 给一个营地号签一次。顺序不能换：
// rolelist 取 roleId → signinfo 判今日 → 未签才 newsignin。
func signOneAccount(ctx context.Context, client *campClient, state *userState, campID string) signAccountResult {
	result := probeSignAccount(ctx, client, state, campID)
	if result.NoRole || result.FailReason != "" {
		return result
	}

	auth := state.Credentials[campID].auth()

	// 「今天已签」也要去领一次奖励：签过 ≠ 领过。
	if result.Already {
		return withSignRewards(ctx, client, result, auth)
	}

	role := result.Role
	if role == nil {
		result.StateClass = "fail"
		result.StateText = "查询失败"
		result.FailReason = "没取到角色信息，稍后再试"
		return result
	}

	code, msg, signResult, err := client.doSign(ctx, role.RoleID.String(), auth)
	if err != nil {
		result.StateClass = "fail"
		result.StateText = "签到失败"
		result.FailReason = signErrorText(err)
		return result
	}

	switch classifySignCode(code) {
	case "ok":
		result.Signed = true
		result.StateClass = "new"
		result.StateText = "签到成功"
		info := result.Info
		if info == nil {
			info = &campSignInfo{}
		}
		if signResult != nil {
			if signResult.UserSign != "" {
				info.WeekSignMap = signResult.UserSign
			}
			if signResult.SeqSignDays.String() != "" {
				info.SeqSignDays = signResult.SeqSignDays
			}
			if signResult.TotalSignDays.String() != "" {
				info.UserTotalSign = signResult.TotalSignDays
			}
		}
		result.Info = info
		return withSignRewards(ctx, client, result, auth)
	case "already":
		result.Already = true
		result.StateClass = "done"
		result.StateText = "今天已签"
		return withSignRewards(ctx, client, result, auth)
	case "too-fast":
		result.StateClass = "fail"
		result.StateText = "操作太频繁"
		result.FailReason = "这次请求得太快了，过一会儿再试"
	case "missing-role":
		result.StateClass = "fail"
		result.StateText = "角色签不了"
		result.FailReason = "这个角色签不了，去营地 App 里确认一下角色绑定"
	default:
		result.StateClass = "fail"
		result.StateText = "签到失败"
		result.FailReason = firstNonEmpty(msg, fmt.Sprintf("错误码 %d", code))
	}
	return result
}

// withSignRewards 补上福利中心奖励：先做每日任务，再领已完成的奖励。
func withSignRewards(ctx context.Context, client *campClient, view signAccountResult, auth *campAuth) signAccountResult {
	acted := completeDailyTasks(ctx, client, auth)
	view.ActedText = describeActed(acted)

	claimed, coin, hasCoin, rewardNote, failReason := claimTaskRewards(ctx, client, auth)
	view.ClaimedText = describeClaimed(claimed)
	view.Coin = coin
	view.HasCoin = hasCoin
	view.RewardNote = rewardNote
	if view.FailReason == "" {
		view.FailReason = failReason
	}
	return view
}

type dailyTaskResult struct {
	Viewed     bool
	Liked      bool
	Shared     bool
	Skipped    bool
	FailReason string
}

func describeActed(result dailyTaskResult) string {
	var parts []string
	if result.Viewed {
		parts = append(parts, "浏览资讯")
	}
	if result.Liked {
		parts = append(parts, "点赞")
	}
	if result.Shared {
		parts = append(parts, "分享")
	}
	return strings.Join(parts, "、")
}

// completeDailyTasks 把还没做的每日任务做掉：分享只上报，浏览/点赞要真调用。
func completeDailyTasks(ctx context.Context, client *campClient, auth *campAuth) dailyTaskResult {
	var result dailyTaskResult

	taskData, err := client.getTaskList(ctx, auth)
	if err != nil {
		result.FailReason = signErrorText(err)
		return result
	}

	needView := hasUndoneTask(taskData.TaskList, taskFuncViewNews)
	needLike := hasUndoneTask(taskData.TaskList, taskFuncAddLikes)
	needShare := hasUndoneTask(taskData.TaskList, taskFuncShare)
	if !needView && !needLike && !needShare {
		return result
	}

	if needShare {
		if err := client.reportShare(ctx, auth); err != nil {
			result.FailReason = signErrorText(err)
		} else {
			result.Shared = true
		}
	}

	if !needView && !needLike {
		return result
	}

	infoID, err := client.getInfoFeed(ctx, auth)
	if err != nil {
		if result.FailReason == "" {
			result.FailReason = signErrorText(err)
		}
		return result
	}
	if infoID == "" {
		return result
	}

	if needView {
		if err := client.viewInfo(ctx, infoID, auth); err != nil {
			result.FailReason = signErrorText(err)
		} else {
			result.Viewed = true
		}
	}
	if needLike {
		if err := client.likeInfo(ctx, infoID, auth); err != nil {
			if result.FailReason == "" {
				result.FailReason = signErrorText(err)
			}
		} else {
			result.Liked = true
		}
	}
	return result
}

func hasUndoneTask(tasks []campTask, function string) bool {
	for _, task := range tasks {
		if task.Func == function && !task.finished() {
			return true
		}
	}
	return false
}

// claimTaskRewards 领取所有「已完成且未领取」的奖励，返回领取清单与当前余额。
func claimTaskRewards(ctx context.Context, client *campClient, auth *campAuth) ([]campTask, int, bool, string, string) {
	taskData, err := client.getTaskList(ctx, auth)
	if err != nil {
		return nil, 0, false, "", signErrorText(err)
	}

	coin := taskData.MyCurrency.Int()
	hasCoin := taskData.MyCurrency.String() != ""

	var claimable []campTask
	for _, task := range taskData.TaskList {
		if task.claimable() {
			claimable = append(claimable, task)
		}
	}
	if len(claimable) == 0 {
		return nil, coin, hasCoin, "", ""
	}

	ids := make([]string, 0, len(claimable))
	for _, task := range claimable {
		ids = append(ids, task.TaskID.String())
	}
	code, msg, err := client.claimTasks(ctx, ids, auth)
	if err != nil {
		return nil, coin, hasCoin, "", signErrorText(err)
	}
	if code != 0 {
		return nil, coin, hasCoin, firstNonEmpty(msg, "奖励领取失败"), ""
	}

	gained := 0
	for _, task := range claimable {
		gained += task.Currency.Int()
	}
	return claimable, coin + gained, true, "", ""
}

func describeClaimed(tasks []campTask) string {
	if len(tasks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		name := strings.TrimSpace(task.Title)
		if name == "" {
			name = "任务奖励"
		}
		if currency := task.Currency.Int(); currency > 0 {
			name = fmt.Sprintf("%s(%d币)", name, currency)
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, "、")
}

// ---------- 视图 ----------

type signGiftView struct {
	Name string
	Desc string
	Icon string
	Num  string
}

type signDayView struct {
	Week string
	Mark string
	Gift string
	Cls  string
	Tag  string
}

type signTotalView struct {
	Days        string
	Percent     int
	Cls         string
	Name        string
	Desc        string
	StatusText  string
	StatusClass string
}

type signAccountView struct {
	Name        string
	CampID      string
	StateClass  string
	StateText   string
	HasWeek     bool
	Days        []signDayView
	Gifts       []signGiftView
	Totals      []signTotalView
	ActedText   string
	ClaimedText string
	CoinText    string
	RewardNote  string
	Note        string
	RoleName    string
}

type signPageView struct {
	Avatar       string
	Username     string
	DateText     string
	AccountCount int
	Title        string
	SubText      string
	TodayClass   string
	TodayVal     string
	MaxSeq       string
	TotalSign    string
	GiftCount    string
	GiftKey      string
	Accounts     []signAccountView
	EmptyText    string
	FootText     string
}

func buildSignPages(results []signAccountResult, avatar, username string, statusOnly bool) signPageView {
	view := signPageView{
		Avatar:       avatar,
		Username:     html.EscapeString(username),
		DateText:     time.Now().Format("2006-01-02 Mon"),
		AccountCount: len(results),
		Title:        "营地签到",
		SubText:      "王者荣耀 · 营地福利中心",
		GiftKey:      "今日奖励",
		FootText:     "数据来自王者营地 · 每天 0 点重置",
	}
	if statusOnly {
		view.Title = "签到状态"
		view.FootText = "只查状态，不会替你签到"
	}
	if username == "" {
		view.Username = "营地用户"
	}

	doneCount := 0
	for _, result := range results {
		account := buildSignAccountView(result)
		if result.Already || result.Signed {
			doneCount++
		}
		view.Accounts = append(view.Accounts, account)
	}

	view.TodayVal = fmt.Sprintf("%d/%d", doneCount, len(results))
	if len(results) > 0 && doneCount == len(results) {
		view.TodayClass = "gold"
	}
	if len(results) == 0 {
		view.TodayVal = "0/0"
		view.TodayClass = "bad"
		view.EmptyText = "还没有可签到的营地号<br>先发送 #绑定营地 绑定一个营地 ID"
	}

	allNoRole := len(results) > 0
	for _, result := range results {
		if !result.NoRole {
			allNoRole = false
		}
	}
	if allNoRole {
		view.EmptyText = "这些营地号都还没绑定王者角色<br>先在游戏里登录一次再来签"
	}

	maxSeq, totalSign, hasStat := 0, 0, false
	weekCount, totalCount := 0, 0
	for _, result := range results {
		if result.Info == nil {
			continue
		}
		if result.Info.SeqSignDays.Int() > maxSeq {
			maxSeq = result.Info.SeqSignDays.Int()
		}
		if result.Info.UserTotalSign.Int() > totalSign {
			totalSign = result.Info.UserTotalSign.Int()
		}
		hasStat = true
		if len(result.Info.WeekList) > 0 {
			weekCount = len(result.Info.WeekList)
		}
		if len(result.Info.TotalList) > 0 {
			totalCount = len(result.Info.TotalList)
		}
	}
	if hasStat {
		view.MaxSeq = fmt.Sprintf("%d天", maxSeq)
		view.TotalSign = fmt.Sprintf("%d天", totalSign)
	}
	if weekCount > 0 {
		view.GiftCount = fmt.Sprintf("%d档", weekCount)
		view.GiftKey = "本周奖励"
	} else if totalCount > 0 {
		view.GiftCount = fmt.Sprintf("%d档", totalCount)
		view.GiftKey = "累计奖励"
	}

	return view
}

func buildSignAccountView(result signAccountResult) signAccountView {
	view := signAccountView{
		Name:        html.EscapeString(result.Name),
		CampID:      html.EscapeString(result.CampID),
		StateClass:  result.StateClass,
		StateText:   result.StateText,
		RoleName:    html.EscapeString(result.RoleName),
		ActedText:   result.ActedText,
		ClaimedText: result.ClaimedText,
		RewardNote:  result.RewardNote,
	}
	if view.Name == "" {
		view.Name = view.CampID
	}
	if result.HasCoin {
		view.CoinText = fmt.Sprintf("%d", result.Coin)
	}

	if result.Info == nil {
		view.Note = result.FailReason
		return view
	}

	indexed := indexWeekGifts(result.Info)
	signed := isSignedToday(result.Info.WeekSignMap, time.Now())
	todayIndex := (int(time.Now().Weekday()) + 6) % 7

	view.HasWeek = len(result.Info.WeekList) > 0 || len(result.Info.WeekSignMap) == 7
	for index := 0; index < 7; index++ {
		day := signDayView{
			Week: "周" + weekNames[index],
			Mark: "○",
			Cls:  "future",
		}
		signedDay := false
		if len(result.Info.WeekSignMap) == 7 {
			switch result.Info.WeekSignMap[index] {
			case '1':
				signedDay = true
			case '0':
				signedDay = false
			default:
				signedDay = false
			}
		}
		if signedDay {
			day.Mark = "✓"
			day.Cls = "done"
		} else if index == todayIndex {
			day.Cls = "today-pending"
			if signed != nil && *signed {
				day.Cls = "done today-done"
			}
		} else if index < todayIndex {
			day.Cls = "miss"
		}

		gifts := indexed[index+1]
		day.Gift = html.EscapeString(giftSummary(gifts, 22))
		view.Days = append(view.Days, day)
	}

	for _, gift := range result.Info.WeekList {
		if gift.Date.Int() != todayIndex+1 {
			continue
		}
		for _, item := range gift.Gift {
			view.Gifts = append(view.Gifts, toSignGiftView(item))
		}
	}
	if len(view.Gifts) == 0 {
		view.Gifts = append(view.Gifts, toSignGiftView(campGift{Name: "营地币", PackageNum: "25"}))
	}

	for _, total := range result.Info.TotalList {
		days := total.Days.Int()
		if days == 0 {
			days = total.Date.Int()
		}
		percent := 0
		if days > 0 {
			percent = totalSignPercent(result.Info.UserTotalSign.Int(), days)
		}
		item := signTotalView{
			Days:    fmt.Sprintf("%d", days),
			Percent: percent,
		}
		if len(total.Gift) > 0 {
			gift := total.Gift[0]
			item.Name = html.EscapeString(gift.displayName())
			item.Desc = html.EscapeString(gift.displayDesc())
		}
		if result.Info.UserTotalSign.Int() >= days && days > 0 {
			item.Cls = "reached"
			item.StatusText = "已达成"
			item.StatusClass = "ok"
		} else {
			item.StatusText = "未达成"
			item.StatusClass = "no"
		}
		view.Totals = append(view.Totals, item)
	}

	if result.FailReason != "" {
		view.Note = result.FailReason
	}
	return view
}

func toSignGiftView(gift campGift) signGiftView {
	name := gift.displayName()
	num := gift.displayNum()
	if num == "" {
		num = "1"
	}
	return signGiftView{
		Name: html.EscapeString(name),
		Desc: html.EscapeString(gift.displayDesc()),
		Icon: html.EscapeString(gift.displayIcon()),
		Num:  html.EscapeString(num),
	}
}

func totalSignPercent(current, target int) int {
	if target <= 0 {
		return 0
	}
	percent := current * 100 / target
	if percent > 100 {
		return 100
	}
	if percent < 0 {
		return 0
	}
	return percent
}

// ---------- 指令 ----------

// signCommand 处理 #王者签到 / #王者签到状态：给当前 QQ 用户名下所有营地号签一遍。
func signCommand(ctx context.Context, c sdk.Call, statusOnly bool) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return c.Reply(ctx, "还没有绑定营地ID，先发送 #绑定营地 营地ID")
	}

	client := newCampClient()
	results := make([]signAccountResult, 0, len(state.IDs))
	hasUsable := false
	for _, campID := range state.IDs {
		var result signAccountResult
		if statusOnly {
			result = probeSignAccount(ctx, client, state, campID)
		} else {
			result = signOneAccount(ctx, client, state, campID)
		}
		if result.NoRole {
			continue
		}
		hasUsable = true
		results = append(results, result)
	}

	if !hasUsable {
		return c.Reply(ctx, "这些营地号都还没绑定王者角色\n先在游戏里登录一次再来签")
	}

	view := buildSignPages(results, "https://q1.qlogo.cn/g?b=qq&s=100&nk="+c.Event.UserID, c.Event.Nickname, statusOnly)
	image, err := renderCampSign(ctx, view)
	if err == nil {
		// 图片消息不能带键盘：先发一条带快捷按钮的 Markdown，再发签到结果图。
		if err := c.ReplyKeyboardOrText(ctx, "营地签到结果", campKeyboard()); err != nil {
			return err
		}
		return c.ReplyImageBytes(ctx, image)
	}
	return c.ReplyKeyboardOrText(ctx, signTextFallback(results), campKeyboard())
}

// signTextFallback 是出图失败时的纯文本兜底，保证用户至少能看到结果。
func signTextFallback(results []signAccountResult) string {
	var builder strings.Builder
	builder.WriteString("营地签到结果")
	for _, result := range results {
		builder.WriteString("\n· ")
		builder.WriteString(result.Name)
		if result.Name != result.CampID {
			builder.WriteString("(" + result.CampID + ")")
		}
		builder.WriteString("：")
		builder.WriteString(result.StateText)
		if result.Signed {
			builder.WriteString("，连续 " + result.Info.SeqSignDays.String() + " 天")
		}
		if result.ClaimedText != "" {
			builder.WriteString("\n  已领取：" + result.ClaimedText)
		}
		if result.FailReason != "" {
			builder.WriteString("\n  " + result.FailReason)
		}
	}
	return builder.String()
}

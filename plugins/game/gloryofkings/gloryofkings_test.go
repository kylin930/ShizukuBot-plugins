package gloryofkings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionValid(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	if !Definition().Manifest.Allows("state.read") || !Definition().Manifest.Allows("state.write") {
		t.Fatal("state permissions missing")
	}
}

func TestCommandBody(t *testing.T) {
	cases := map[string]string{
		"#查询战绩":     "查询战绩",
		"＃绑定营地 123": "绑定营地 123",
		"  #排位战绩  ": "排位战绩",
		"王者帮助":      "王者帮助",
	}
	for input, want := range cases {
		if got := commandBody(input); got != want {
			t.Errorf("commandBody(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseStatsQuery(t *testing.T) {
	cases := []struct {
		body   string
		slot   int
		option int
		detail int
		ok     bool
	}{
		{"查询战绩", 0, 0, 0, true},
		{"王者战绩", 0, 0, 0, true},
		{"战绩", 0, 0, 0, true},
		{"排位战绩", 0, 1, 0, true},
		{"巅峰战绩", 0, 4, 0, true},
		{"巅峰战绩3", 0, 4, 3, true},
		{"查询战绩 3", 0, 0, 3, true},
		{"查询2战绩", 2, 0, 0, true},
		{"查询2排位战绩", 2, 1, 0, true},
		{"查询2排位战绩3", 2, 1, 3, true},
		{"查询12345战绩", 12345, 0, 0, true},
		{"查询排位战绩", 0, 0, 0, false},
		{"绑定营地123", 0, 0, 0, false},
	}
	for _, tc := range cases {
		slot, option, detail, ok := parseStatsQuery(tc.body)
		if slot != tc.slot || option != tc.option || detail != tc.detail || ok != tc.ok {
			t.Errorf("parseStatsQuery(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				tc.body, slot, option, detail, ok, tc.slot, tc.option, tc.detail, tc.ok)
		}
	}
}

func TestHelpTrigger(t *testing.T) {
	for _, input := range []string{"#王者帮助", "王者荣耀帮助", "王者插件help", "王者农药插件帮助 推送"} {
		if !helpRe.MatchString(commandBody(input)) {
			t.Errorf("help trigger missed %q", input)
		}
	}
	for _, input := range []string{"帮助", "王者荣耀", "#营地帮助"} {
		if helpRe.MatchString(commandBody(input)) {
			t.Errorf("help trigger should not match %q", input)
		}
	}
}

func TestParseIndexAndIDAliases(t *testing.T) {
	if index, ok := parseIndex("2"); !ok || index != 1 {
		t.Fatalf("parseIndex(2) = %d,%v", index, ok)
	}
	for _, invalid := range []string{"", "0", "abc", "-1", "1.5"} {
		if _, ok := parseIndex(invalid); ok {
			t.Errorf("parseIndex(%q) should be invalid", invalid)
		}
	}
	for _, alias := range []string{"营地ID", "王者ID", "我的王者ID", "农药id"} {
		if !isIDQuery(alias) {
			t.Errorf("isIDQuery(%q) = false", alias)
		}
	}
	if isIDQuery("绑定营地") {
		t.Error("isIDQuery should not match 绑定营地")
	}
}

func TestAccountStateMutations(t *testing.T) {
	state := &userState{IDs: []string{"100", "200", "300"}, Current: 2,
		Names: map[string]string{"200": "阿狸"}}
	if index := state.bindID("100"); index != 0 || state.Current != 0 {
		t.Fatalf("bindID existing = (%d,%d)", index, state.Current)
	}
	state.Current = 2
	state.removeID(0)
	if want := []string{"200", "300"}; strings.Join(state.IDs, ",") != strings.Join(want, ",") {
		t.Fatalf("IDs after remove = %v", state.IDs)
	}
	if state.Current != 1 {
		t.Fatalf("Current after remove = %d, want 1", state.Current)
	}
	state.removeID(0)
	if state.Current != 0 || len(state.IDs) != 1 {
		t.Fatalf("Current/length after last remove = %d/%d", state.Current, len(state.IDs))
	}
	if _, ok := state.Names["200"]; ok {
		t.Fatal("removed name should be deleted")
	}
}

func TestAccountCardText(t *testing.T) {
	state := &userState{IDs: []string{"100", "200"}, Current: 1,
		Names: map[string]string{"200": "<b>阿狸</b>"}}
	text, current := accountCardText(state)
	if current != "200" {
		t.Fatalf("current = %q", current)
	}
	for _, want := range []string{"当前使用：第 2 个", "1. 100", "2. 200（&lt;b&gt;阿狸&lt;/b&gt;） ← 当前"} {
		if !strings.Contains(text, want) {
			t.Errorf("accountCardText missing %q:\n%s", want, text)
		}
	}
}

func TestXXTEARoundTrip(t *testing.T) {
	key := []byte("camp-secret-key")
	plain := []byte(`{"userId":"123456","needGzip":1}`)
	encrypted := xxteaEncrypt(plain, key)
	decrypted := xxteaDecrypt(encrypted, key)
	if string(decrypted) != string(plain) {
		t.Fatalf("round trip = %q", decrypted)
	}
}

func TestBuildBattleView(t *testing.T) {
	data := &battleListData{RoleJobName: "打野", List: []battleItem{
		{MapName: "王者峡谷", GameResult: 1, Desc: "实力局,MVP", KillCnt: 8, DeadCnt: 2, AssistCnt: 10,
			UsedTime: 754, GameTime: "1750000000", GradeGame: scalarValue("12.5"),
			GodLikeCnt: 1, RampageCnt: 1, UltraKillCnt: 1, TripleKillCnt: 1},
		{MapName: "王者峡谷", GameResult: 1, Desc: "翻盘局"},
		{MapName: "王者峡谷", GameResult: 2},
	}}
	view := buildBattleView(data, "", data.RoleJobName, 1)
	if view.ModeLabel != "排位赛" || view.WinningStreak != 2 {
		t.Fatalf("view mode/streak = %q/%d", view.ModeLabel, view.WinningStreak)
	}
	if len(view.Battles) != 3 || !view.Battles[0].Win {
		t.Fatalf("battles = %d", len(view.Battles))
	}
	first := view.Battles[0]
	if len(first.SpecialTags) != 1 || first.SpecialTags[0] != "实力局" {
		t.Fatalf("special tags = %v", first.SpecialTags)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "MVP" {
		t.Fatalf("tags = %v", first.Tags)
	}
	if len(first.KillTags) != 4 {
		t.Fatalf("kill tags = %d", len(first.KillTags))
	}
	if first.Duration != "12:34" || first.Grade != "12.5" {
		t.Fatalf("duration/grade = %q/%q", first.Duration, first.Grade)
	}

	empty := buildBattleView(&battleListData{}, "", "", 0)
	if !empty.EmptyState {
		t.Fatal("empty view should be EmptyState")
	}
}

func TestParseAddToken(t *testing.T) {
	cases := []struct {
		body   string
		campID string
		token  string
		ok     bool
	}{
		{"王者添加tk 123456 abcdefghijklmnop", "123456", "abcdefghijklmnop", true},
		{"添加TK 654321 token_0123456789", "654321", "token_0123456789", true},
		{"王者添加tk 12345", "", "", false},
		{"绑定营地 123456", "", "", false},
	}
	for _, tc := range cases {
		campID, token, ok := parseAddToken(tc.body)
		if campID != tc.campID || token != tc.token || ok != tc.ok {
			t.Errorf("parseAddToken(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.body, campID, token, ok, tc.campID, tc.token, tc.ok)
		}
	}
}

func TestParseHeroBattle(t *testing.T) {
	cases := []struct {
		body   string
		hero   string
		option int
		ok     bool
	}{
		{"查妲己战绩", "妲己", 0, true},
		{"查妲己排位战绩", "妲己", 1, true},
		{"查孙尚香巅峰战绩", "孙尚香", 4, true},
		{"查战绩 王昭君", "王昭君", 0, true},
		{"查战绩", "", 0, false},
		{"查询2战绩", "", 0, false},
		{"查询战绩", "", 0, false},
		{"绑定营地", "", 0, false},
	}
	for _, tc := range cases {
		hero, option, ok := parseHeroBattle(tc.body)
		if hero != tc.hero || option != tc.option || ok != tc.ok {
			t.Errorf("parseHeroBattle(%q) = (%q,%d,%v), want (%q,%d,%v)",
				tc.body, hero, option, ok, tc.hero, tc.option, tc.ok)
		}
	}
}

func TestHeroOwnedBattle(t *testing.T) {
	if !heroOwnedBattle(battleItem{HeroID: scalarValue("166")}, "166") {
		t.Fatal("heroId match failed")
	}
	if !heroOwnedBattle(battleItem{HeroIcon: "https://x/16600.jpg"}, "166") {
		t.Fatal("heroIcon fallback failed")
	}
	if heroOwnedBattle(battleItem{HeroIcon: "https://x/16600.jpg"}, "167") {
		t.Fatal("mismatched hero should not match")
	}
}

func TestBuildBattleDetailView(t *testing.T) {
	detail := &battleDetailData{
		Head: battleDetailHead{AcntCamp: scalarValue("2"), GameResult: scalarValue("1"),
			MapName: "王者峡谷", MatchDesc: "排位赛", Tips: "实力局"},
		Battle: battleDetailBattle{UsedTime: scalarValue("754")},
		BlueTeam: battleDetailTeam{AcntCamp: scalarValue("2"), KillCnt: scalarValue("12"),
			DeadCnt: scalarValue("5"), AssistCnt: scalarValue("30"), TowerCnt: scalarValue("9"),
			Money: scalarValue("52000"), Bdragon1: scalarValue("2")},
		RedTeam: battleDetailTeam{AcntCamp: scalarValue("1"), KillCnt: scalarValue("5"),
			DeadCnt: scalarValue("12"), AssistCnt: scalarValue("10")},
	}
	var me battleDetailRole
	me.BasicInfo.RoleName = "阿狸"
	me.BasicInfo.IsMe = true
	me.BattleStats.KillCnt = scalarValue("8")
	me.BattleStats.DeadCnt = scalarValue("2")
	me.BattleStats.AssistCnt = scalarValue("10")
	me.BattleStats.GradeGame = scalarValue("12.5")
	me.BattleStats.Mvp = true
	me.BattleStats.JoinGamePercent = scalarValue("0.75")
	me.BattleStats.MaxMoney = true
	me.BattleRecords.UsedHero.HeroName = "妲己"
	me.BattleRecords.UsedHero.HeroIcon = "https://x/16600.jpg"
	detail.BlueRoles = []battleDetailRole{me}

	view := buildBattleDetailView(detail, battleItem{})
	if view.Result != "胜利" || view.MySide != "蓝方" || view.EnemySide != "红方" {
		t.Fatalf("result/side = %q/%q/%q", view.Result, view.MySide, view.EnemySide)
	}
	if len(view.MyTeam.Players) != 1 {
		t.Fatalf("players = %d", len(view.MyTeam.Players))
	}
	player := view.MyTeam.Players[0]
	if player.MvpTag != "MVP" || player.JoinRate != 75 || player.GradeKey != "s" {
		t.Fatalf("player = %+v", player)
	}
	if len(player.MaxTags) != 1 || player.MaxTags[0] != "经济最高" {
		t.Fatalf("max tags = %v", player.MaxTags)
	}
	if view.MyDragons == "" || view.UsedTime != "12 分钟" {
		t.Fatalf("dragons/used = %q/%q", view.MyDragons, view.UsedTime)
	}
}

func TestResolveEvaluate(t *testing.T) {
	if label, _ := resolveEvaluate("", "https://x/gold_mage_3.png", ""); label != "金牌法师" {
		t.Fatalf("medal label = %q", label)
	}
	if label, _ := resolveEvaluate("https://x/custom_wzry_battledetail_tags/5db4fef1bfc72dd2c5ae71b01ef3951b.png"); label != "顶级对抗路" {
		t.Fatalf("branch label = %q", label)
	}
	if label, icon := resolveEvaluate("", "", ""); label != "" || icon != "" {
		t.Fatalf("empty evaluate = %q/%q", label, icon)
	}
}

func TestTemplatesExecute(t *testing.T) {
	account := accountCardData{
		Type:      "绑定",
		WzryID:    "123456",
		WzryName:  "阿狸",
		IDList:    "当前使用：第 1 个\n\n1. 123456（阿狸） ← 当前\n",
		Funcs:     accountCardFuncs,
		Timestamp: "2026-01-01 00:00:00",
	}
	accountHTML, err := executeTemplate(tplAccount, account)
	if err != nil {
		t.Fatalf("account template: %v", err)
	}
	if !strings.Contains(accountHTML, "123456") || !strings.Contains(accountHTML, "#绑定营地") {
		t.Fatal("account template output missing content")
	}
	if !strings.Contains(inlineImages(accountHTML), "data:image/png;base64,") {
		t.Fatal("account background was not inlined")
	}

	view := buildBattleView(&battleListData{RoleJobName: "打野", List: []battleItem{
		{MapName: "王者峡谷", GameResult: 1, Desc: "实力局"},
	}}, "", "打野", 0)
	battleHTML, err := executeTemplate(tplBattleList, view)
	if err != nil {
		t.Fatalf("battle template: %v", err)
	}
	for _, want := range []string{"win-bg", "special-tag win", "胜利", "王者峡谷"} {
		if !strings.Contains(battleHTML, want) {
			t.Errorf("battle template missing %q", want)
		}
	}
}

func TestParsePrivacyMenu(t *testing.T) {
	raw := []byte(`{"data":[{"className":"战绩","privacyItem":[
		{"id":9,"privacyId":9,"name":"我的比赛","bottomText":"设置","detailInfo":[
			{"friendTypeId":0,"name":"全部联系人"},{"friendTypeId":2,"name":"允许观战","disableModify":true},{"friendTypeId":-1,"name":"无效"}]},
		{"id":11,"privacyId":11,"name":"皮肤展示"}
	]},{"className":"","privacyItem":[{"id":0,"name":"无名项"}]}]}`)

	menu := parsePrivacyMenu(raw)
	if len(menu.Classes) != 1 || menu.Classes[0].ClassName != "战绩" {
		t.Fatalf("classes = %+v", menu.Classes)
	}
	if len(menu.Items) != 2 {
		t.Fatalf("items = %d", len(menu.Items))
	}
	first := menu.Items[0]
	if first.No != 9 || !first.Verified || len(first.Details) != 2 || !first.Details[1].Locked {
		t.Fatalf("first item = %+v", first)
	}
	if !isVisiblePrivacyItem(first) {
		t.Fatal("item 9 should be visible in help")
	}
	for _, hiddenID := range []int{4, 5, 7, 8, 15, 16, 17} {
		if isVisiblePrivacyItem(privacyItem{PrivacyID: hiddenID}) {
			t.Errorf("privacy %d should be hidden from help", hiddenID)
		}
	}
}

func TestPrivacyPickAndMatch(t *testing.T) {
	item := privacyItem{No: 9, PrivacyID: 9, Name: "我的比赛", Details: []privacyDetail{
		{FriendTypeID: 0, Name: "全部联系人"},
		{FriendTypeID: 1, Name: "陌生人"},
		{FriendTypeID: 2, Name: "好友"},
	}}
	if got := pickPrivacyItem([]privacyItem{item}, 9); got == nil || got.Name != "我的比赛" {
		t.Fatalf("pick = %+v", got)
	}
	if got := pickPrivacyItem([]privacyItem{item}, 8); got != nil {
		t.Fatalf("pick missing = %+v", got)
	}
	if got := matchPrivacyDetail(item, "陌生人"); got == nil || got.FriendTypeID != 1 {
		t.Fatalf("exact match = %+v", got)
	}
	if got := matchPrivacyDetail(item, "好"); got == nil || got.FriendTypeID != 2 {
		t.Fatalf("contains match = %+v", got)
	}
	if got := matchPrivacyDetail(item, "人"); got != nil {
		t.Fatalf("ambiguous match should be nil, got %+v", got)
	}
}

func TestPrivacyPlanWritesSkipsOnlyLocked(t *testing.T) {
	item := privacyItem{PrivacyID: 9, Details: []privacyDetail{
		{FriendTypeID: 0, Name: "全部联系人"},
		{FriendTypeID: 2, Name: "允许观战", Locked: true},
		{FriendTypeID: 1, Name: "陌生人"},
	}}
	writes, locked := planPrivacyWrites(item, privacyHidden, nil)
	if len(writes) != 2 || len(locked) != 1 {
		t.Fatalf("writes/locked = %d/%d", len(writes), len(locked))
	}
	for _, write := range writes {
		if write.Status != privacyHidden || write.PrivacyID != 9 {
			t.Fatalf("write = %+v", write)
		}
	}

	only := privacyDetail{FriendTypeID: 1, Name: "陌生人"}
	writes, locked = planPrivacyWrites(item, privacyVisible, &only)
	if len(writes) != 1 || writes[0].FriendTypeID != 1 || len(locked) != 0 {
		t.Fatalf("only writes = %+v, locked = %+v", writes, locked)
	}

	writes, locked = planPrivacyWrites(item, privacyVisible, &item.Details[1])
	if len(writes) != 0 || len(locked) != 1 {
		t.Fatalf("locked write = %+v / %+v", writes, locked)
	}
}

func TestBattleVisibility(t *testing.T) {
	if got := battleVisibility(&battleListData{Invisible: true}); !got.Known || !got.Hidden {
		t.Fatalf("hidden view = %+v", got)
	}
	if got := battleVisibility(&battleListData{List: []battleItem{{}, {}}}); !got.Known || got.Hidden || got.Count != 2 {
		t.Fatalf("visible view = %+v", got)
	}
	if got := battleVisibility(nil); got.Known {
		t.Fatalf("nil view = %+v", got)
	}
	if text := battleVisibilityText(battleVisibilityView{}); !strings.Contains(text, "没查出来") {
		t.Fatalf("unknown text = %q", text)
	}
}

func TestParsePrivacyCommands(t *testing.T) {
	hidden, no, detail, ok := parsePrivacySetCommand("隐私隐藏 9 陌生人")
	if !ok || !hidden || no != 9 || detail != "陌生人" {
		t.Fatalf("set parse = %v/%d/%q/%v", hidden, no, detail, ok)
	}
	if hidden, no, detail, ok = parsePrivacySetCommand("隐私公开12"); !ok || hidden || no != 12 || detail != "" {
		t.Fatalf("no-space parse = %v/%d/%q/%v", hidden, no, detail, ok)
	}
	if _, no, _, ok = parsePrivacySetCommand("隐私隐藏"); !ok || no != 0 {
		t.Fatalf("missing number should be recognized, got %d/%v", no, ok)
	}
	for _, input := range []string{"隐私帮助", "隐私设置", "隐私状态", "隐私列表"} {
		if !isPrivacyHelpCommand(input) {
			t.Errorf("help command missed %q", input)
		}
	}
	if !isPrivacyHelpCommand("隐私帮助 ") {
		t.Error("help command should tolerate trailing spaces")
	}
	if hidden, ok := privacyBattleCommand("隐藏我的战绩"); !ok || !hidden {
		t.Error("battle hide shortcut missed")
	}
	if hidden, ok := privacyBattleCommand("公开我的战绩"); !ok || hidden {
		t.Error("battle show shortcut missed")
	}
	if _, ok := privacyBattleCommand("隐私公开 1"); ok {
		t.Error("set command should not be treated as shortcut")
	}
}

func TestCleanRoleName(t *testing.T) {
	input := "阿狸\u0007\uE000\u200b "
	if got := cleanRoleName(input); got != "阿狸" {
		t.Fatalf("cleanRoleName = %q", got)
	}
}

func TestOnlineStatusOf(t *testing.T) {
	gaming := &gamingInfo{IsGaming: true}
	cases := []struct {
		name       string
		gameOnline int
		online     int64
		offline    int64
		gaming     *gamingInfo
		wantKey    string
	}{
		{name: "对局中", gameOnline: 2, gaming: gaming, wantKey: onlineKeyPlaying},
		{name: "客户端开着但没在对局", gameOnline: 2, wantKey: onlineKeyIdle},
		{name: "在线", gameOnline: 1, wantKey: onlineKeyOnline},
		{name: "离线", gameOnline: 0, online: 1700000000, offline: 1700000100, wantKey: onlineKeyOffline},
		{name: "三态全空", gameOnline: 0, wantKey: onlineKeyUnknown},
		{name: "isGaming 为假", gameOnline: 2, gaming: &gamingInfo{}, wantKey: onlineKeyIdle},
	}
	for _, item := range cases {
		got := onlineStatusOf(item.gameOnline, item.online, item.offline, item.gaming)
		if got.Key != item.wantKey {
			t.Fatalf("%s: got %s, want %s", item.name, got.Key, item.wantKey)
		}
	}
}

func TestProfileMainRole(t *testing.T) {
	data := &profileData{
		TargetRoleID: scalarValue("1002"),
		RoleList: []profileRole{
			{RoleID: scalarValue("1001"), RoleName: "小号"},
			{RoleID: scalarValue("1002"), RoleName: "大号"},
		},
	}
	if role := profileMainRole(data); role == nil || role.RoleName != "大号" {
		t.Fatalf("应取 targetRoleId 对应的角色，got %+v", role)
	}
	if role := profileMainRole(&profileData{RoleList: data.RoleList}); role == nil || role.RoleName != "小号" {
		t.Fatalf("无 targetRoleId 时应取第一个，got %+v", role)
	}
	if role := profileMainRole(&profileData{}); role != nil {
		t.Fatalf("没有角色时应返回 nil，got %+v", role)
	}
}

func TestIsOnlineCommand(t *testing.T) {
	for _, body := range []string{"王者在线", "营地在线", "在线状态", "王者在线状态"} {
		if !isOnlineCommand(body) {
			t.Fatalf("%q 应识别为在线指令", body)
		}
	}
	for _, body := range []string{"谁在玩", "王者在线列表", "在线"} {
		if isOnlineCommand(body) {
			t.Fatalf("%q 不应识别为在线指令", body)
		}
	}
}

func TestOnlineTextFallback(t *testing.T) {
	text := onlineTextFallback([]onlineEntry{
		{CampID: "1", Name: "在打的", Status: onlineStatus{Key: onlineKeyPlaying, Label: "对局中"}, Mode: "王者峡谷", Minutes: 12},
		{CampID: "2", Name: "失败的", Status: onlineStatus{Key: onlineKeyUnknown, Label: "查询失败"}, Note: "<boom>"},
	})
	for _, want := range []string{"在打的（1）：对局中", "王者峡谷 · 已进行 12 分钟", "失败的（2）：查询失败 —— <boom>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("文案缺少 %q：%s", want, text)
		}
	}
	if got := onlineTextFallback(nil); got != "没有查到在线状态" {
		t.Fatalf("空列表文案不对：%s", got)
	}
}

func TestBannerLinks(t *testing.T) {
	banners := []json.RawMessage{
		json.RawMessage(`{"sJumpUrl":"https://camp.qq.com/a","icon":"https://cdn.example.com/a.png"}`),
		json.RawMessage(`{"url":"https://camp.qq.com/a"}`),
		json.RawMessage(`{"jumpUrl":"smobagamehelper://open?x=1"}`),
		json.RawMessage(`not-json`),
	}
	got := bannerLinks(banners)
	if len(got) != 1 || got[0] != "https://camp.qq.com/a" {
		t.Fatalf("bannerLinks = %v", got)
	}
}

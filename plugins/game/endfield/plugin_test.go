package endfield

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/events"
	"shizukubot/sdk"
)

type memState struct {
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (s *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return s.data[key], nil
}

func (s *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	if s.data == nil {
		s.data = map[string]json.RawMessage{}
	}
	s.data[key] = value
	return nil
}

type testEnv struct {
	t       *testing.T
	state   *memState
	cfg     map[string]any
	replies []sdk.Reply
	groupID string
}

func newEnv(t *testing.T, cfg map[string]any) *testEnv {
	t.Helper()
	return &testEnv{t: t, state: newMemState(), cfg: cfg, groupID: "group-1"}
}

func (e *testEnv) send(userID, text string) {
	e.t.Helper()
	e.replies = nil
	event := sdk.Event{
		BotID:     "bot",
		Source:    events.Group,
		GroupID:   e.groupID,
		UserID:    userID,
		MessageID: "msg",
		Message:   text,
		Reply: func(_ context.Context, reply sdk.Reply) error {
			e.replies = append(e.replies, reply)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, Config: e.cfg, State: e.state}); err != nil {
		e.t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (e *testEnv) texts() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "text" {
			out = append(out, reply.Text)
		}
	}
	return out
}

func (e *testEnv) lastText() string {
	texts := e.texts()
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func (e *testEnv) images() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "image" {
			out = append(out, reply.URL)
		}
	}
	return out
}

func stubRenderFailure(t *testing.T) {
	t.Helper()
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, fmt.Errorf("render disabled in tests")
	}
	t.Cleanup(func() { renderHTML = old })
}

func seedBinding(t *testing.T, env *testEnv, userID, token, roleID string) {
	t.Helper()
	accounts := []binding{{
		FrameworkToken: token,
		RoleID:         roleID,
		ServerID:       1,
		Nickname:       "测试角色",
		IsPrimary:      true,
		LoginType:      "cred",
	}}
	if err := saveBindings(context.Background(), env.state, userID, accounts); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "endfield" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, key := range []string{"apiKey", "backendBaseUrl", "gachaSource", "adminUsers"} {
		if _, ok := def.Manifest.ConfigSchema.Properties[key]; !ok {
			t.Fatalf("missing config key %q", key)
		}
	}
}

func TestConfigDecode(t *testing.T) {
	cfg := decodeConfig(nil)
	if cfg.BackendBaseURL != "https://end-api.shallow.ink" || cfg.GachaSource != "backend_api" || !cfg.UseWikiStrategy {
		t.Fatalf("defaults = %+v", cfg)
	}
	override := decodeConfig(map[string]any{
		"apiKey":                 " key ",
		"backendBaseUrl":         "http://localhost:1/",
		"useWikiStrategy":        false,
		"simulateEnabled":        false,
		"simulateGroupWhitelist": []any{"g1", 2.0},
		"dailyLimitLimited":      float64(3),
		"adminUsers":             "u1,u2",
	})
	if override.APIKey != "key" || override.BackendBaseURL != "http://localhost:1/" || override.UseWikiStrategy {
		t.Fatalf("override = %+v", override)
	}
	if override.SimulateEnabled || len(override.SimulateGroups) != 2 || override.DailyLimitLimited != 3 {
		t.Fatalf("simulate override = %+v", override)
	}
	if !override.isAdmin("u2") || override.isAdmin("u3") {
		t.Fatal("admin list mismatch")
	}
	if override.simulateAllowed("g1") || override.simulateAllowed("g3") {
		t.Fatal("simulate whitelist/switch mismatch")
	}
}

func TestMessages(t *testing.T) {
	if got := getMessage("common.unknown"); got == "" || strings.HasPrefix(got, "[消息未配置") {
		t.Fatalf("common.unknown = %q", got)
	}
	if got := getMessage("announcement.list_subtitle", map[string]any{"count": 3}); !strings.Contains(got, "3") {
		t.Fatalf("placeholder = %q", got)
	}
	if got := getMessage("not.exist"); !strings.Contains(got, "未配置") {
		t.Fatalf("missing message = %q", got)
	}
}

func TestStripPrefix(t *testing.T) {
	cases := map[string]string{
		":帮助":          "帮助",
		"：公告最新":        "公告最新",
		"#zmd日历":       "日历",
		"#终末地蓝图":       "蓝图",
		"/zmd wiki 干员": "wiki 干员",
	}
	for input, want := range cases {
		got, ok := stripPrefix(input)
		if !ok || got != want {
			t.Fatalf("stripPrefix(%q) = (%q,%v)", input, got, ok)
		}
	}
	if _, ok := stripPrefix("帮助"); ok {
		t.Fatal("prefix should be required")
	}
}

func TestRoutePriority(t *testing.T) {
	env := newEnv(t, nil)
	if err := saveBindings(context.Background(), env.state, "u1", []binding{{
		FrameworkToken: "ft", RoleID: "r1", ServerID: 1, Nickname: "角色甲", IsPrimary: true,
	}}); err != nil {
		t.Fatal(err)
	}
	env.send("u1", ":绑定列表")
	if !strings.Contains(env.lastText(), "角色甲") {
		t.Fatalf("bind list text = %q", env.lastText())
	}
}

func TestAPIURLsAndHeaders(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotKey = r.Header.Get("X-API-Key")
		gotToken = r.Header.Get("X-Framework-Token")
		fmt.Fprint(w, `{"code":0,"data":{"ok":true}}`)
	}))
	defer server.Close()

	cfg := config{APIKey: "key", BackendBaseURL: server.URL}
	client := newAPIClient(cfg, binding{FrameworkToken: "ft", RoleID: "role1", ServerID: 2})
	res, err := client.getData(context.Background(), "note", nil)
	if err != nil || !res.ok() {
		t.Fatalf("note request: %v %+v", err, res)
	}
	if gotPath != "/api/endfield/note" || !strings.Contains(gotQuery, "roleId=role1") || !strings.Contains(gotQuery, "serverId=2") {
		t.Fatalf("path/query = %q?%q", gotPath, gotQuery)
	}
	if gotKey != "key" || gotToken != "ft" {
		t.Fatalf("headers key=%q token=%q", gotKey, gotToken)
	}

	if _, err := client.getData(context.Background(), "stamina", nil); err != nil {
		t.Fatalf("stamina request: %v", err)
	}
	if !strings.Contains(gotQuery, "serverId=2") {
		t.Fatalf("stamina query = %q", gotQuery)
	}

	noToken := newAPIClient(cfg, binding{RoleID: "r"})
	if _, err := noToken.getData(context.Background(), "note", nil); err == nil {
		t.Fatal("missing framework token should fail")
	}
	if _, err := noToken.getWikiData(context.Background(), "wiki_items", nil); err != nil {
		t.Fatalf("wiki items should only need api key: %v", err)
	}
}

func TestBindingCleaning(t *testing.T) {
	inactive := false
	accounts := []binding{
		{FrameworkToken: "a", RoleID: "1", IsPrimary: false, LastSync: 10},
		{FrameworkToken: "b", RoleID: "1", IsPrimary: true},
		{FrameworkToken: "c", RoleID: "2", IsActive: &inactive},
		{FrameworkToken: "d", RoleID: "3"},
	}
	cleaned := cleanBindings(accounts)
	if len(cleaned) != 2 {
		t.Fatalf("cleaned = %+v", cleaned)
	}
	if cleaned[0].FrameworkToken != "b" || cleaned[0].RoleID != "1" {
		t.Fatalf("primary preference failed: %+v", cleaned)
	}
	if !cleaned[0].IsPrimary {
		t.Fatal("primary flag lost")
	}
	env := newEnv(t, nil)
	if err := saveBindings(context.Background(), env.state, "u1", accounts); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBindings(context.Background(), env.state, "u1")
	if err != nil || len(loaded) != 2 {
		t.Fatalf("loaded = %+v err=%v", loaded, err)
	}
}

func TestAnnouncementListAndDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/announcements":
			fmt.Fprint(w, `{"code":0,"data":{"total":2,"list":[{"item_id":"a1","title":"公告一","published_at_ts":1700000000},{"item_id":"a2","title":"公告二","published_at_ts":1700000100}]}}`)
		case r.URL.Path == "/api/announcements/latest":
			fmt.Fprint(w, `{"code":0,"data":{"item_id":"a2","title":"最新公告","published_at_ts":1700000100,"texts":[{"content":"正文内容"}]}}`)
		case r.URL.Path == "/api/announcements/a1":
			fmt.Fprint(w, `{"code":0,"data":{"item_id":"a1","title":"公告一","published_at_ts":1700000000,"texts":[{"content":"公告一正文"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stubRenderFailure(t)
	env := newEnv(t, map[string]any{"apiKey": "key", "backendBaseUrl": server.URL})
	env.send("u1", ":公告")
	text := env.lastText()
	if !strings.Contains(text, "公告一") || !strings.Contains(text, "公告二") {
		t.Fatalf("list = %q", text)
	}
	env.send("u1", ":公告 1")
	if !strings.Contains(env.lastText(), "公告一正文") {
		t.Fatalf("detail = %q", env.lastText())
	}
	env.send("u1", ":公告最新")
	if !strings.Contains(env.lastText(), "正文内容") {
		t.Fatalf("latest = %q", env.lastText())
	}
	env.send("u1", ":公告 9")
	if !strings.Contains(env.lastText(), "序号超出范围") {
		t.Fatalf("out of range = %q", env.lastText())
	}
}

func TestAnnouncementNeedsAPIKey(t *testing.T) {
	env := newEnv(t, nil)
	env.send("u1", ":公告")
	if !strings.Contains(env.lastText(), "未配置 API") {
		t.Fatalf("text = %q", env.lastText())
	}
}

func TestCalendar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/wiki/activities" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"activities":[{"name":"测试活动","description":"说明","activity_start_at_ts":1700000000,"activity_end_at_ts":1800000000}]}}`)
	}))
	defer server.Close()
	env := newEnv(t, map[string]any{"apiKey": "key", "backendBaseUrl": server.URL})
	env.send("u1", ":日历")
	text := env.lastText()
	if !strings.Contains(text, "测试活动") || !strings.Contains(text, "说明") {
		t.Fatalf("calendar = %q", text)
	}
}

func TestWikiQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/wiki/items":
			if r.URL.Query().Get("sub_type_id") != "1" {
				http.Error(w, "bad subtype", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"code":0,"data":{"items":[{"name":"莱万汀","item_id":"77"}]}}`)
		case "/api/wiki/items/77":
			fmt.Fprint(w, `{"code":0,"data":{"name":"莱万汀","caption":[{"kind":"text","text":"干员简介"}],"content":{"document_map":{"d1":{"block_ids":["b1","b2"],"block_map":{"b1":{"kind":"text","text":{"inline_elements":[{"kind":"text","text":"基础资料"}]}},"b2":{"kind":"image","image":{"url":"https://example.com/a.png"}}}}}}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	env := newEnv(t, map[string]any{"apiKey": "key", "backendBaseUrl": server.URL})
	env.send("u1", ":wiki 干员 莱万汀")
	text := env.lastText()
	if !strings.Contains(text, "莱万汀") || !strings.Contains(text, "基础资料") {
		t.Fatalf("wiki = %q", text)
	}
	if images := env.images(); len(images) != 1 || images[0] != "https://example.com/a.png" {
		t.Fatalf("wiki images = %v", images)
	}
}

func TestStrategyLocal(t *testing.T) {
	env := newEnv(t, nil)
	env.send("u1", ":攻略列表")
	if !strings.Contains(env.lastText(), "洁尔佩塔") || !strings.Contains(env.lastText(), "冰队") {
		t.Fatalf("strategy list = %q", env.lastText())
	}
	env.send("u1", ":伊冯攻略")
	if len(env.images()) == 0 {
		t.Fatalf("strategy images empty; texts=%v", env.texts())
	}
}

func TestNoteTextFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/endfield/note" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"base":{"name":"管理员","roleId":"r1","level":20,"exp":100,"worldLevel":3,"serverName":"亚服","charNum":1},"chars":[{"name":"黎风","level":60,"rarity":6,"potentialLevel":2,"evolvePhase":2,"profession":{"value":"先锋"},"property":{"value":"物理"},"weapon":{"name":"长枪","level":50,"breakthroughLevel":1}}],"achieve":{"count":5},"bpSystem":{"curLevel":3,"maxLevel":60}}}`)
	}))
	defer server.Close()

	stubRenderFailure(t)
	env := newEnv(t, map[string]any{"apiKey": "key", "backendBaseUrl": server.URL})
	seedBinding(t, env, "u1", "ft", "r1")
	env.send("u1", ":便签")
	text := env.lastText()
	if !strings.Contains(text, "管理员") || !strings.Contains(text, "黎风") {
		t.Fatalf("note text = %q", text)
	}
	env.send("u1", ":角色")
	if !strings.Contains(env.lastText(), "黎风") {
		t.Fatalf("chars text = %q", env.lastText())
	}
}

func TestStaminaAndSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/endfield/stamina":
			fmt.Fprint(w, `{"code":0,"data":{"stamina":{"current":100,"max":120,"maxTs":1800000000,"recover":360},"dailyMission":{"activation":80,"maxActivation":100},"weeklyMission":{"score":1000,"total":2000},"role":{"name":"管理员"}}}`)
		case "/api/endfield/note":
			fmt.Fprint(w, `{"code":0,"data":{"base":{"name":"管理员","serverName":"亚服"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	stubRenderFailure(t)
	env := newEnv(t, map[string]any{"apiKey": "key", "backendBaseUrl": server.URL})
	seedBinding(t, env, "u1", "ft", "r1")
	env.send("u1", ":理智")
	text := env.lastText()
	if !strings.Contains(text, "100") || !strings.Contains(text, "120") {
		t.Fatalf("stamina text = %q", text)
	}

	env.send("u1", ":订阅理智 50")
	if !strings.Contains(env.lastText(), "订阅") {
		t.Fatalf("subscribe text = %q", env.lastText())
	}
	raw, err := env.state.Get(context.Background(), "endfield:stamina:sub:u1")
	if err != nil || len(raw) == 0 {
		t.Fatalf("subscription state missing: %v", err)
	}
	env.send("u1", ":取消订阅理智")
	if len(env.replies) == 0 {
		t.Fatal("unsubscribe produced no reply")
	}
}

func TestUnboundMessage(t *testing.T) {
	env := newEnv(t, map[string]any{"apiKey": "key"})
	env.send("u1", ":便签")
	if !strings.Contains(env.lastText(), "尚未绑定") {
		t.Fatalf("text = %q", env.lastText())
	}
}

func TestSimulateParseAndHardPity(t *testing.T) {
	command := parseSimulateCommand("十连 限定")
	if !command.valid || command.mode != "ten" || command.pool != "limited" {
		t.Fatalf("command = %+v", command)
	}
	if command := parseSimulateCommand("单抽"); !command.valid || command.mode != "single" {
		t.Fatalf("single = %+v", command)
	}
	if command := parseSimulateCommand("百连 武器"); !command.valid || command.mode != "hundred" || command.pool != "weapon" {
		t.Fatalf("hundred = %+v", command)
	}

	rules := simulateRulesFor("limited")
	state := &simulateState{SixStarPity: rules.SixStarPity - 1}
	result := simulatePull(state, rules, true, true)
	if result.Rarity != 6 {
		t.Fatalf("hard pity result = %+v", result)
	}
	if state.SixStarPity != 0 || state.SixStarCount != 1 || state.TotalPulls != 1 {
		t.Fatalf("state = %+v", state)
	}
	stats := simulateStatsFrom(state)
	if stats.TotalPulls != 1 || stats.SixStarCount != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestMaaStoreHelpers(t *testing.T) {
	store := maaStore{}
	maaAddDevice(&store, "dev1")
	maaAddDevice(&store, "dev2")
	maaAddDevice(&store, "dev1")
	if len(store.DeviceIDs) != 2 {
		t.Fatalf("devices = %v", store.DeviceIDs)
	}
	maaSaveJobID(&store, "job1")
	maaSaveJobID(&store, "job2")
	if len(store.JobIDs) != 2 || store.JobIDs[0] != "job2" {
		t.Fatalf("jobs = %v", store.JobIDs)
	}
	if got := maaResolveJobID(store, "1"); got != "job2" {
		t.Fatalf("resolve job = %q", got)
	}
	if got := maaResolveJobID(store, "job1"); got != "job1" {
		t.Fatalf("resolve direct = %q", got)
	}
	maaRemoveDevice(&store, "dev1")
	if len(store.DeviceIDs) != 1 || store.DeviceIDs[0] != "dev2" {
		t.Fatalf("after remove = %v", store.DeviceIDs)
	}
	if maaStatusText("idle") == "" || maaJobStatusText("done") == "" {
		t.Fatal("status text missing")
	}
}

func TestWikiContentText(t *testing.T) {
	raw := json.RawMessage(`{"document_map":{"d1":{"block_ids":["b1","b2"],"block_map":{"b1":{"kind":"text","text":{"inline_elements":[{"kind":"text","text":"第一段"},{"kind":"entry","entry":{"name":"词条"}}]}},"b2":{"kind":"image","image":{"url":"https://example.com/x.png"}}}}}}`)
	text, images := wikiContentText(raw)
	if !strings.Contains(text, "第一段") || !strings.Contains(text, "词条") {
		t.Fatalf("text = %q", text)
	}
	if len(images) != 1 || images[0] != "https://example.com/x.png" {
		t.Fatalf("images = %v", images)
	}
}

func TestBuildPage(t *testing.T) {
	page := buildPage(pageOptions{
		title:    "标题",
		subtitle: "副标题",
		seal:     "印",
		tags:     []string{"标签"},
		body:     "<div class=\"section\"><h2>正文</h2></div>",
		width:    800,
	})
	if !strings.Contains(page, "<!DOCTYPE html>") || !strings.Contains(page, "标题") || !strings.Contains(page, "标签") {
		t.Fatal("page skeleton missing")
	}
	if !strings.Contains(page, "终末地协议终端") || !strings.Contains(page, "Endfield-plugin") {
		t.Fatal("page header/footer missing")
	}
}

package cnbimagebed

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shizukubot/events"
	"shizukubot/sdk"
)

var pngBytes = mustDecode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

type stubRegistrar struct {
	commands map[string]sdk.CommandHandler
}

func (r *stubRegistrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

func (r *stubRegistrar) Command(name string, handler sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = handler
	return nil
}

type stubHost struct{ *stubRegistrar }

func (h *stubHost) Context() context.Context { return context.Background() }
func (h *stubHost) BotID() string            { return "bot" }
func (h *stubHost) Config() map[string]any   { return nil }
func (h *stubHost) State() sdk.State         { return nil }
func (h *stubHost) Log(string)               {}

func assertCommands(t *testing.T, def sdk.Definition, names ...string) {
	t.Helper()
	if err := def.Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	registrar := &stubRegistrar{}
	if err := def.New().Load(context.Background(), &stubHost{stubRegistrar: registrar}); err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range names {
		if _, ok := registrar.commands[name]; !ok {
			t.Fatalf("missing command %q in %v", name, registrar.commands)
		}
	}
}

func TestTriggerCommands(t *testing.T) {
	assertCommands(t, Definition(), "cnb-image-bed")
}

func mustDecode(s string) []byte {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return data
}

type memState struct {
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return m.data[key], nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.data[key] = value
	return nil
}

func seedState(t *testing.T, state *memState, key string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	state.data[key] = data
}

type testEnv struct {
	t       *testing.T
	botID   string
	groupID string
	userID  string
	source  events.Source
	cfg     map[string]any
	state   *memState
	replies []sdk.Reply
}

func newEnv(t *testing.T, cfg map[string]any) *testEnv {
	t.Helper()
	return &testEnv{
		t:       t,
		botID:   "bot-" + t.Name(),
		groupID: "group-1",
		userID:  "user-1",
		source:  events.Group,
		cfg:     cfg,
		state:   newMemState(),
	}
}

func attachments(urls []string) json.RawMessage {
	if len(urls) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		items = append(items, map[string]any{"url": u, "content_type": "image/png", "width": 1, "height": 1})
	}
	raw, err := json.Marshal(map[string]any{"attachments": items})
	if err != nil {
		panic(err)
	}
	return raw
}

func (e *testEnv) send(text string, urls ...string) {
	e.t.Helper()
	e.replies = nil
	ev := sdk.Event{
		BotID:     e.botID,
		Source:    e.source,
		GroupID:   e.groupID,
		UserID:    e.userID,
		MessageID: "msg-1",
		Message:   text,
		Raw:       attachments(urls),
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: e.cfg, State: e.state}); err != nil {
		e.t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) lastReply() (sdk.Reply, bool) {
	if len(e.replies) == 0 {
		return sdk.Reply{}, false
	}
	return e.replies[len(e.replies)-1], true
}

func (e *testEnv) lastText() string {
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" || e.replies[i].Kind == "keyboard" {
			return e.replies[i].Text
		}
	}
	return ""
}

type cnbServer struct {
	t *testing.T
	*httptest.Server
	posts      []string
	postBodies []map[string]any
	puts       []string
	putBodies  [][]byte
	putAuth    []string
	putQuery   []string
	deletes    []string
	postStatus int
	assetPath  string
}

func newCNBServer(t *testing.T) *cnbServer {
	t.Helper()
	s := &cnbServer{t: t, assetPath: "/demo/repo/-/assets/pic.png"}
	mux := http.NewServeMux()
	mux.HandleFunc("/image.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	})
	mux.HandleFunc("/image2.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append(append([]byte{}, pngBytes...), 0x00))
	})
	mux.HandleFunc("/file.pdf", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4\n%fake\n"))
	})
	mux.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.puts = append(s.puts, r.Method)
		s.putBodies = append(s.putBodies, body)
		s.putAuth = append(s.putAuth, r.Header.Get("Authorization"))
		s.putQuery = append(s.putQuery, r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/-/upload/"):
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			s.posts = append(s.posts, r.URL.Path)
			s.postBodies = append(s.postBodies, payload)
			if s.postStatus != 0 {
				http.Error(w, "boom", s.postStatus)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"upload_url": s.URL + "/put",
				"token":      "put-token",
				"form":       map[string]any{"key": "value"},
				"assets":     map[string]any{"path": s.assetPath, "url": s.URL + s.assetPath},
			})
		case r.Method == http.MethodDelete:
			s.deletes = append(s.deletes, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if err := def.Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	if !def.Manifest.Allows("config.read") || !def.Manifest.Allows("state.write") {
		t.Fatalf("permissions missing: %v", def.Manifest.Permissions)
	}
}

func TestFormatHelpers(t *testing.T) {
	cases := []struct {
		got  any
		want any
	}{
		{speedLevel(600), "⚡ 很快"},
		{speedLevel(601), "🚀 良好"},
		{speedLevel(1501), "🟡 一般"},
		{speedLevel(3001), "🐢 偏慢"},
		{percent(0, 0), "0.00%"},
		{percent(2, 3), "66.67%"},
		{percent(3, 3), "100.00%"},
		{formatBytes(0), "0 B"},
		{formatBytes(512), "512 B"},
		{formatBytes(1024), "1.00 KB"},
		{formatBytes(1536), "1.50 KB"},
		{formatBytes(1024 * 1024), "1.00 MB"},
		{formatBytes(1024 * 1024 * 1024), "1.00 GB"},
		{formatMs(999), "999ms"},
		{formatMs(1500), "1.50s"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("got %v want %v", tc.got, tc.want)
		}
	}
}

func TestParseDays(t *testing.T) {
	cases := map[string]int{
		"":    1,
		"7":   7,
		"7天":  7,
		"0":   1,
		"2.2": 3,
		"99":  30,
		"abc": 1,
		" 5 ": 5,
		"-3":  1,
	}
	for input, want := range cases {
		if got := parseDays(input, 30); got != want {
			t.Fatalf("parseDays(%q) = %d, want %d", input, got, want)
		}
	}
	if got := parseDays("7", 0); got != 7 {
		t.Fatalf("parseDays with zero max = %d", got)
	}
}

func TestMatchKeyword(t *testing.T) {
	cases := []struct {
		text    string
		keyword string
		rest    string
		ok      bool
	}{
		{"图床状态", "图床状态", "", true},
		{"图床状态7天", "图床状态", "7天", true},
		{"#图床状态", "图床状态", "", true},
		{"cnb图床状态", "图床状态", "", true},
		{"#cnb图床状态30天", "图床状态", "30天", true},
		{"#图床上传", "图床上传", "", true},
		{"图床状态", "图床失败", "", false},
	}
	for _, tc := range cases {
		rest, ok := matchKeyword(tc.text, tc.keyword)
		if ok != tc.ok || rest != tc.rest {
			t.Fatalf("matchKeyword(%q,%q) = %q,%v want %q,%v", tc.text, tc.keyword, rest, ok, tc.rest, tc.ok)
		}
	}
}

func TestBuildFileInfo(t *testing.T) {
	fixedNow(t, time.UnixMilli(1_700_000_000_000))
	info := buildFileInfo(pngBytes)
	if info.MIME != "image/png" {
		t.Fatalf("mime = %q", info.MIME)
	}
	if len(info.MD5) != 32 {
		t.Fatalf("md5 = %q", info.MD5)
	}
	wantPrefix := base36(1_700_000_000_000) + "." + info.MD5[:8] + "."
	if !strings.HasPrefix(info.Name, wantPrefix) || !strings.HasSuffix(info.Name, ".png") {
		t.Fatalf("name = %q, want prefix %q", info.Name, wantPrefix)
	}
}

func TestEncodeURIComponent(t *testing.T) {
	if got := encodeURIComponent("a b/c?d=e&f.png"); got != "a%20b%2Fc%3Fd%3De%26f.png" {
		t.Fatalf("encoded = %q", got)
	}
	if got := encodeURIComponent("a-_.!~*'()"); got != "a-_.!~*'()" {
		t.Fatalf("reserved chars changed: %q", got)
	}
}

func TestUploadImage(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{
		"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo",
	})
	env.send("图床上传", srv.URL+"/image.png")

	if len(srv.posts) != 1 {
		t.Fatalf("posts = %v", srv.posts)
	}
	if !strings.Contains(srv.posts[0], "/demo/repo/-/upload/imgs") {
		t.Fatalf("post path = %q", srv.posts[0])
	}
	if got := srv.postBodies[0]["size"]; got != float64(len(pngBytes)) {
		t.Fatalf("post size = %v", got)
	}
	if name, _ := srv.postBodies[0]["name"].(string); !strings.HasSuffix(name, ".png") {
		t.Fatalf("post name = %q", name)
	}
	if len(srv.puts) != 1 || !bytes.Equal(srv.putBodies[0], pngBytes) {
		t.Fatalf("put not received: %v", srv.puts)
	}
	if srv.putAuth[0] != "Bearer put-token" {
		t.Fatalf("put auth = %q", srv.putAuth[0])
	}
	if srv.putQuery[0] != "key=value" {
		t.Fatalf("put query = %q", srv.putQuery[0])
	}
	if got := env.lastText(); got != "https://cnb.cool"+srv.assetPath {
		t.Fatalf("reply = %q", got)
	}

	var stats []statRecord
	if err := sdk.LoadJSON(context.Background(), env.state, stateStatsKey, &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || !stats[0].Success || stats[0].Size != int64(len(pngBytes)) {
		t.Fatalf("stats = %+v", stats)
	}
	var cache map[string]cacheEntry
	if err := sdk.LoadJSON(context.Background(), env.state, stateCacheKey, &cache); err != nil {
		t.Fatal(err)
	}
	if len(cache) != 1 {
		t.Fatalf("cache = %+v", cache)
	}
}

func TestUploadCacheHit(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo"})
	env.send("图床上传", srv.URL+"/image.png")
	first := env.lastText()
	env.send("图床上传", srv.URL+"/image.png")
	if len(srv.posts) != 1 {
		t.Fatalf("cache miss, posts = %v", srv.posts)
	}
	if env.lastText() != first {
		t.Fatalf("cached reply = %q want %q", env.lastText(), first)
	}
}

func TestUploadFailureFallsBackToLocal(t *testing.T) {
	srv := newCNBServer(t)
	srv.postStatus = http.StatusInternalServerError
	env := newEnv(t, map[string]any{"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo"})
	env.send("图床上传", srv.URL+"/image.png")

	got := env.lastText()
	if !strings.HasPrefix(got, defaultLocalBase+"/File/") || !strings.HasSuffix(got, ".png") {
		t.Fatalf("reply = %q, want local fallback URL", got)
	}
	var stats []statRecord
	_ = sdk.LoadJSON(context.Background(), env.state, stateStatsKey, &stats)
	if len(stats) != 1 || stats[0].Success || !strings.Contains(stats[0].FailReason, "获取上传URL失败") {
		t.Fatalf("stats = %+v", stats)
	}
	var cache map[string]cacheEntry
	_ = sdk.LoadJSON(context.Background(), env.state, stateCacheKey, &cache)
	if len(cache) != 0 {
		t.Fatalf("cache should be empty: %+v", cache)
	}
}

func TestUploadMissingTokenFallsBackToLocal(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{
		"baseUrl": srv.URL, "defaultRepo": "demo/repo",
		"localBaseUrl": "https://local.test/",
	})
	env.send("图床上传", srv.URL+"/image.png")
	got := env.lastText()
	if !strings.HasPrefix(got, "https://local.test/File/") || !strings.HasSuffix(got, ".png") {
		t.Fatalf("reply = %q", got)
	}
	if len(srv.posts) != 0 {
		t.Fatalf("unexpected posts: %v", srv.posts)
	}
}

func TestUploadWithoutRecordingSchedulesDelete(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{
		"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo",
		"recordurl": false, "autodelete": 12,
	})
	var delay time.Duration
	var deletedPath string
	old := scheduleDelete
	scheduleDelete = func(d time.Duration, path string, _ config) {
		delay = d
		deletedPath = path
	}
	t.Cleanup(func() { scheduleDelete = old })

	env.send("图床上传", srv.URL+"/image.png")
	if delay != 12*time.Second || deletedPath != srv.assetPath {
		t.Fatalf("schedule = %v %q", delay, deletedPath)
	}
	var cache map[string]cacheEntry
	_ = sdk.LoadJSON(context.Background(), env.state, stateCacheKey, &cache)
	if len(cache) != 0 {
		t.Fatalf("cache should stay empty: %+v", cache)
	}
}

func TestUploadNonImageUsesFilesChannel(t *testing.T) {
	srv := newCNBServer(t)
	srv.assetPath = "/demo/repo/-/assets/pic.pdf"
	env := newEnv(t, map[string]any{"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo"})
	env.send("图床上传", srv.URL+"/file.pdf")

	if len(srv.posts) != 1 || !strings.HasSuffix(srv.posts[0], "/-/upload/files") {
		t.Fatalf("posts = %v", srv.posts)
	}
	if got := env.lastText(); got != "https://cnb.cool/demo/repo/-/assets/pic.pdf" {
		t.Fatalf("reply = %q", got)
	}
}

func TestUploadMultipleImages(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo"})
	env.send("图床上传", srv.URL+"/image.png", srv.URL+"/image2.png")

	if len(srv.posts) != 2 {
		t.Fatalf("posts = %d", len(srv.posts))
	}
	lines := strings.Split(env.lastText(), "\n")
	if len(lines) != 2 || lines[0] != lines[1] {
		t.Fatalf("reply = %q", env.lastText())
	}
}

func TestUploadPendingImage(t *testing.T) {
	srv := newCNBServer(t)
	env := newEnv(t, map[string]any{"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo"})
	env.send("图床上传")
	if got := env.lastText(); got != "请发送图片：" {
		t.Fatalf("prompt = %q", got)
	}
	env.send("", srv.URL+"/image.png")
	if got := env.lastText(); got != "https://cnb.cool"+srv.assetPath {
		t.Fatalf("reply = %q", got)
	}
	if len(srv.posts) != 1 {
		t.Fatalf("posts = %v", srv.posts)
	}
}

func TestCurrentPathIgnoredOutsideQQ(t *testing.T) {
	env := newEnv(t, map[string]any{"admin_users": []any{"user-1"}})
	env.source = events.Source("web")
	env.send("图床状态")
	if len(env.replies) != 0 {
		t.Fatalf("unexpected replies: %+v", env.replies)
	}
}

func seededEnv(t *testing.T, at time.Time) *testEnv {
	t.Helper()
	fixedNow(t, at)
	env := newEnv(t, map[string]any{"admin_users": []any{"user-1"}})
	seedState(t, env.state, stateStatsKey, []statRecord{
		{TS: at.Add(-time.Hour).UnixMilli(), Type: "image/png", Size: 1024, Cost: 500, Success: true},
		{TS: at.Add(-2 * time.Hour).UnixMilli(), Type: "image/png", Size: 2048, Cost: 1500, Success: true},
		{TS: at.Add(-3 * time.Hour).UnixMilli(), Size: 500, Cost: 200, Success: false, FailReason: "boom"},
		{TS: at.Add(-40 * 24 * time.Hour).UnixMilli(), Size: 999, Cost: 999, Success: true},
	})
	return env
}

func TestStatusMessage(t *testing.T) {
	env := seededEnv(t, time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC))
	env.send("图床状态")

	reply, ok := env.lastReply()
	if !ok || reply.Kind != "keyboard" {
		t.Fatalf("reply = %+v", reply)
	}
	checks := []string{
		"# 📅 图床状态",
		">统计区间：**1天**",
		">数据保留：**30天**",
		"> 上传表现：**🚀 良好**",
		"| 上传成功 | **2** 张 |",
		"| 上传失败 | **1** 次 |",
		"| 成功率 | **66.67%** |",
		"| 最大 | **2.00 KB** |",
		"| 最小 | **1.00 KB** |",
		"| 总大小 | **3.00 KB** |",
		"| 平均单张大小 | **1.50 KB** |",
		"| 最短上传耗时 | **500ms** |",
		"| 最长上传耗时 | **1.50s** |",
		"| 平均上传耗时 | **1.00s** |",
	}
	for _, want := range checks {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("missing %q in %q", want, reply.Text)
		}
	}
	if !strings.Contains(reply.Keyboard, "图床状态7天") || !strings.Contains(reply.Keyboard, "图床失败原因30天") {
		t.Fatalf("keyboard = %q", reply.Keyboard)
	}
}

func TestStatusDaysClamped(t *testing.T) {
	env := seededEnv(t, time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC))
	env.send("图床状态99天")
	if !strings.Contains(env.lastText(), ">统计区间：**30天**") {
		t.Fatalf("reply = %q", env.lastText())
	}
}

func TestStatusRequiresAdmin(t *testing.T) {
	env := newEnv(t, map[string]any{"admin_users": []any{}})
	env.send("图床状态")
	if got := env.lastText(); got != noPermission {
		t.Fatalf("reply = %q", got)
	}
}

func TestFailReasonsMessage(t *testing.T) {
	env := seededEnv(t, time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC))
	env.send("图床失败原因")
	text := env.lastText()
	for _, want := range []string{"#❌图床失败记录", "> 统计区间：**1 天**", "> 失败总数：**1 次**", "| boom | 1 |"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	reply, _ := env.lastReply()
	if reply.Kind != "keyboard" {
		t.Fatalf("kind = %q", reply.Kind)
	}
}

func TestFailReasonsEmpty(t *testing.T) {
	env := newEnv(t, map[string]any{"admin_users": []any{"user-1"}})
	env.send("#图床失败记录")
	if got := env.lastText(); got != "📉 近 1 天没有记录到任何上传失败" {
		t.Fatalf("reply = %q", got)
	}
}

func TestTopFailReasonsOrderAndTruncation(t *testing.T) {
	at := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	longReason := strings.Repeat("x", 90) + "\nline"
	records := []statRecord{
		{TS: at, Success: false, FailReason: "b"},
		{TS: at, Success: false, FailReason: "a"},
		{TS: at, Success: false, FailReason: "b"},
		{TS: at, Success: false, FailReason: longReason},
		{TS: at, Success: true},
	}
	total, reasons := topFailReasons(records, at-dayMillis)
	if total != 4 {
		t.Fatalf("total = %d", total)
	}
	if len(reasons) != 3 || reasons[0].Reason != "b" || reasons[1].Reason != "a" {
		t.Fatalf("reasons = %+v", reasons)
	}
	got := truncateReason(longReason)
	if len([]rune(got)) != 80 || strings.ContainsAny(got, "\r\n") {
		t.Fatalf("truncated = %q", got)
	}

	many := make([]statRecord, 0, 11)
	for i := 0; i < 11; i++ {
		many = append(many, statRecord{TS: at, Success: false, FailReason: string(rune('a' + i))})
	}
	_, top := topFailReasons(many, at-dayMillis)
	if len(top) != 10 {
		t.Fatalf("top = %d", len(top))
	}
}

func TestCleanupExpiredCache(t *testing.T) {
	srv := newCNBServer(t)
	at := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	fixedNow(t, at)
	env := newEnv(t, map[string]any{
		"baseUrl": srv.URL, "token": "tok", "defaultRepo": "demo/repo",
		"cache_expire_days": 7,
	})
	seedState(t, env.state, stateCacheKey, map[string]cacheEntry{
		"old": {URL: "/demo/repo/-/assets/old.png", Path: "/demo/repo/-/assets/old.png", LastUsed: at.Add(-8 * 24 * time.Hour).UnixMilli()},
	})

	env.send("图床上传", srv.URL+"/image.png")
	if len(srv.deletes) != 1 || srv.deletes[0] != "/demo/repo/-/assets/old.png" {
		t.Fatalf("deletes = %v", srv.deletes)
	}
	var cache map[string]cacheEntry
	_ = sdk.LoadJSON(context.Background(), env.state, stateCacheKey, &cache)
	if _, ok := cache["old"]; ok {
		t.Fatalf("expired entry kept: %+v", cache)
	}
}

func TestDecodeConfigDefaults(t *testing.T) {
	cfg := decodeConfig(nil)
	if cfg.BaseURL != defaultBaseURL || cfg.LocalBaseURL != defaultLocalBase ||
		cfg.AutoDelete != 30 || !cfg.Stats || !cfg.RecordURL ||
		cfg.CacheExpireDays != 7 || cfg.StatsMaxDays != 30 {
		t.Fatalf("defaults = %+v", cfg)
	}
	cfg = decodeConfig(map[string]any{
		"baseUrl": "http://x", "localBaseUrl": "http://local", "token": "t", "defaultRepo": "a/b",
		"autodelete": float64(5), "stats": false, "recordurl": false,
		"cache_expire_days": float64(3), "stats_max_days": float64(9),
		"admin_users": []any{"u1", "u2"},
	})
	if cfg.BaseURL != "http://x" || cfg.LocalBaseURL != "http://local" || cfg.Token != "t" || cfg.DefaultRepo != "a/b" ||
		cfg.AutoDelete != 5 || cfg.Stats || cfg.RecordURL ||
		cfg.CacheExpireDays != 3 || cfg.StatsMaxDays != 9 || len(cfg.AdminUsers) != 2 {
		t.Fatalf("decoded = %+v", cfg)
	}
}

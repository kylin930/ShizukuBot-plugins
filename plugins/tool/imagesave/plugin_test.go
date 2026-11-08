package imagesave

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

type memState struct {
	data map[string]json.RawMessage
}

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
	assertCommands(t, Definition(), "image-save-path")
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return m.data[key], nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.data[key] = value
	return nil
}

type testEnv struct {
	t       *testing.T
	botID   string
	groupID string
	userID  string
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

func (e *testEnv) lastText() string {
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	return ""
}

func (e *testEnv) texts() []string {
	out := make([]string, 0, len(e.replies))
	for _, r := range e.replies {
		if r.Kind == "text" {
			out = append(out, r.Text)
		}
	}
	return out
}

func stubFetch(t *testing.T, data []byte, ext string, err error) {
	t.Helper()
	old := fetchImage
	fetchImage = func(string) ([]byte, string, error) {
		if err != nil {
			return nil, "", err
		}
		return data, ext, nil
	}
	t.Cleanup(func() { fetchImage = old })
}

// modeCfg 构造指定交互模式与保存路径的配置。
func modeCfg(mode string, paths ...savePath) map[string]any {
	arr := make([]any, 0, len(paths))
	for _, p := range paths {
		arr = append(arr, map[string]any{"name": p.Name, "path": p.Path})
	}
	return map[string]any{
		"Interaction_mode": mode,
		"commandName":      "保存图片",
		"savePaths":        arr,
	}
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatalf("invalid definition: %v", err)
	}
}

func TestSaveWithDefaultPath(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("png-data"), ".png", nil)
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片", "https://example.test/a.png")

	if got := e.lastText(); got != "图片已成功保存。" {
		t.Fatalf("unexpected reply: %q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "我的图片.png"))
	if err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	if string(data) != "png-data" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestSaveWithInlineName(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片 -n 自定义", "https://example.test/a.png")
	if _, err := os.Stat(filepath.Join(dir, "自定义.png")); err != nil {
		t.Fatalf("inline name not applied: %v", err)
	}

	e.send("保存图片 我的图片 --name=第二个名字", "https://example.test/a.png")
	if _, err := os.Stat(filepath.Join(dir, "第二个名字.png")); err != nil {
		t.Fatalf("--name= form not applied: %v", err)
	}
}

func TestShowSavePath(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	cfg := modeCfg("3", savePath{Name: "第一个", Path: dir})
	cfg["showSavePath"] = true
	e := newEnv(t, cfg)

	e.send("保存图片 我的图片", "https://example.test/a.png")

	want := "图片已保存到：" + filepath.Join(dir, "我的图片.png")
	if got := e.lastText(); got != want {
		t.Fatalf("unexpected reply: %q, want %q", got, want)
	}
}

func TestDuplicateNaming(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	target := filepath.Join(dir, "我的图片.png")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片", "https://example.test/a.png")

	texts := e.texts()
	if len(texts) != 1 {
		t.Fatalf("expected only the rename notice, got %v", texts)
	}
	if texts[0] != "出现同名文件，已保存为 我的图片(1).png" {
		t.Fatalf("unexpected rename notice: %q", texts[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "我的图片(1).png")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	e.send("保存图片 我的图片", "https://example.test/a.png")
	if got := e.lastText(); got != "出现同名文件，已保存为 我的图片(2).png" {
		t.Fatalf("unexpected second rename notice: %q", got)
	}
}

func TestMultipleImagesWithoutDuplicateCheck(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	cfg := modeCfg("3", savePath{Name: "第一个", Path: dir})
	cfg["checkDuplicate"] = false
	e := newEnv(t, cfg)

	e.send("保存图片 我的图片", "https://example.test/a.png", "https://example.test/b.png")

	if got := e.lastText(); got != "未开启重名检查时不允许一次性输入多张图片。" {
		t.Fatalf("unexpected reply: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("expected no files, got %d", len(entries))
	}
}

func TestMultipleImagesSaved(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片", "https://example.test/a.png", "https://example.test/b.png")

	if got := e.lastText(); got != "出现同名文件，已保存为 我的图片(1).png" {
		t.Fatalf("unexpected last reply: %q", got)
	}
	if got := e.texts()[0]; got != "图片已成功保存。" {
		t.Fatalf("unexpected first reply: %q", got)
	}
	for _, name := range []string{"我的图片.png", "我的图片(1).png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

func TestPromptThenSendImage(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片")
	if got := e.lastText(); got != "请发送图片：" {
		t.Fatalf("unexpected prompt: %q", got)
	}

	e.send("", "https://example.test/a.png")
	if got := e.lastText(); got != "图片已成功保存。" {
		t.Fatalf("unexpected reply: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "我的图片.png")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
}

func TestPromptInvalidImage(t *testing.T) {
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: t.TempDir()}))

	e.send("保存图片 我的图片")
	e.send("这不是图片")

	if got := e.lastText(); got != "输入的图片无效。" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

// TestInvalidImageArgDoesNotWait 校验指令中显式给出无效图片参数时直接报错，
// 不进入等待，后续无关图片不会被误保存。
func TestInvalidImageArgDoesNotWait(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: dir}))

	e.send("保存图片 我的图片 这不是图片")
	if got := e.lastText(); got != "输入的图片无效。" {
		t.Fatalf("unexpected reply: %q", got)
	}

	e.send("", "https://example.test/later.png")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unexpected saved files: %d", len(entries))
	}
	if len(e.replies) != 0 {
		t.Fatalf("unexpected replies after invalid image: %v", e.texts())
	}
}

func TestGetLinkInvalidArgDoesNotWait(t *testing.T) {
	cfg := modeCfg("1")
	cfg["command_of_get_link"] = true
	e := newEnv(t, cfg)

	e.send("获取链接 这不是图片")
	if got := e.lastText(); got != "输入的图片无效。" {
		t.Fatalf("unexpected reply: %q", got)
	}

	e.send("", "https://example.test/later.png")
	if len(e.replies) != 0 {
		t.Fatalf("unexpected replies after invalid getlink: %v", e.texts())
	}
}

func TestPathSelectionPrompt(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("1",
		savePath{Name: "第一个", Path: first},
		savePath{Name: "第二个", Path: second},
	))

	e.send("保存图片 我的图片 不存在", "https://example.test/a.png")
	want := "路径名称无效，请选择路径的名称（冒号左侧为名称）：\n第一个: " + first + "\n第二个: " + second
	if got := e.lastText(); got != want {
		t.Fatalf("unexpected prompt: %q, want %q", got, want)
	}

	e.send("第二个")
	if got := e.lastText(); got != "图片已成功保存。" {
		t.Fatalf("unexpected reply: %q", got)
	}
	if _, err := os.Stat(filepath.Join(second, "我的图片.png")); err != nil {
		t.Fatalf("saved file missing in second path: %v", err)
	}
}

func TestPathSelectionInvalid(t *testing.T) {
	e := newEnv(t, modeCfg("1", savePath{Name: "第一个", Path: t.TempDir()}))

	e.send("保存图片 我的图片", "https://example.test/a.png")
	e.send("错误名字")

	if got := e.lastText(); got != "请选择正确的路径名称。" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestMode4AutoRename(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("4", savePath{Name: "表情包", Path: dir}))

	e.send("保存图片 表情包", "https://example.test/a.png")

	if got := e.lastText(); got != "图片已成功保存。" {
		t.Fatalf("unexpected reply: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one file, got %d (err=%v)", len(entries), err)
	}
	pattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-\d{2}-\d{2}-\d{2}-\d{3}\.png$`)
	if !pattern.MatchString(entries[0].Name()) {
		t.Fatalf("auto name does not match rules: %q", entries[0].Name())
	}
}

func TestMode2PathThenName(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("2", savePath{Name: "第二个", Path: dir}))

	e.send("保存图片 第二个 命名图片", "https://example.test/a.png")

	if _, err := os.Stat(filepath.Join(dir, "命名图片.png")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
}

func TestNoDefaultPath(t *testing.T) {
	e := newEnv(t, modeCfg("3"))

	e.send("保存图片 我的图片", "https://example.test/a.png")

	if got := e.lastText(); got != "没有设置默认保存路径。" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestImageSaveModeUsesFirstPath(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	cfg := modeCfg("1", savePath{Name: "第一个", Path: dir})
	cfg["imageSaveMode"] = true
	e := newEnv(t, cfg)

	e.send("保存图片 我的图片", "https://example.test/a.png")

	if _, err := os.Stat(filepath.Join(dir, "我的图片.png")); err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
}

func TestSaveDownloadError(t *testing.T) {
	stubFetch(t, nil, "", errors.New("boom"))
	e := newEnv(t, modeCfg("3", savePath{Name: "第一个", Path: t.TempDir()}))

	e.send("保存图片 我的图片", "https://example.test/a.png")

	if got := e.lastText(); got != "保存图片时出错：boom" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestAutosave(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("same-image"), ".png", nil)
	cfg := map[string]any{
		"Interaction_mode": "5",
		"commandName":      "保存图片",
		"savePaths":        []any{map[string]any{"name": "第一个", "path": dir}},
		"autosavePics":     true,
		"groupListmapping": []any{map[string]any{
			"enable":          true,
			"groupList":       "autosave-group",
			"count":           float64(2),
			"defaultsavepath": dir,
		}},
	}
	e := newEnv(t, cfg)
	e.groupID = "autosave-group"

	e.send("", "https://example.test/a.png")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("first occurrence should not save, got %d files", len(entries))
	}

	e.send("", "https://example.test/a.png")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one autosaved file, got %d (err=%v)", len(entries), err)
	}

	e.send("", "https://example.test/a.png")
	entries, _ = os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("repeated image should be saved once, got %d", len(entries))
	}

	records := map[string]*hashRecord{}
	if err := sdk.LoadJSON(context.Background(), e.state, "hash_records", &records); err != nil {
		t.Fatalf("load records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("unexpected hash records: %+v", records)
	}
	for _, rec := range records {
		if rec.Count != 3 || !rec.Saved || rec.Path == "" {
			t.Fatalf("unexpected record: %+v", rec)
		}
	}
}

func TestAutosaveIgnoresOtherGroups(t *testing.T) {
	dir := t.TempDir()
	stubFetch(t, []byte("x"), ".png", nil)
	cfg := map[string]any{
		"Interaction_mode": "5",
		"savePaths":        []any{map[string]any{"name": "第一个", "path": dir}},
		"autosavePics":     true,
		"groupListmapping": []any{map[string]any{
			"enable":          true,
			"groupList":       "other-group",
			"count":           float64(1),
			"defaultsavepath": dir,
		}},
	}
	e := newEnv(t, cfg)

	e.send("", "https://example.test/a.png")

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("expected no files for unmatched group, got %d", len(entries))
	}
}

func TestGetLink(t *testing.T) {
	cfg := modeCfg("1")
	cfg["command_of_get_link"] = true
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, cfg)

	e.send("获取链接", "https://example.test/a.png", "https://example.test/b.png")

	want := "图片信息:\nURL: https://example.test/a.png\nType: .png\nFilename: temp.png\n\n" +
		"URL: https://example.test/b.png\nType: .png\nFilename: temp.png\n\n"
	if got := e.lastText(); got != want {
		t.Fatalf("unexpected reply:\n%q\nwant:\n%q", got, want)
	}
}

func TestGetLinkDisabled(t *testing.T) {
	stubFetch(t, []byte("x"), ".png", nil)
	e := newEnv(t, modeCfg("1"))

	e.send("获取链接", "https://example.test/a.png")

	if len(e.replies) != 0 {
		t.Fatalf("expected no reply when command disabled, got %v", e.texts())
	}
}

func TestGenerateFilename(t *testing.T) {
	cfg := defaultConfig()
	cfg.AutoRenameRules = "${YYYY}-${session.userId}-${session.platform}-${unknown}"
	ev := sdk.Event{UserID: "u42", Source: "group", BotID: "bot"}
	name := generateFilename(cfg, ev)

	if !strings.HasPrefix(name, time.Now().Format("2006")+"-u42-group-") {
		t.Fatalf("unexpected generated name: %q", name)
	}
	if !strings.HasSuffix(name, "${unknown}") {
		t.Fatalf("unknown placeholder should be preserved: %q", name)
	}

	cfg.AutoRenameRules = "${session.content}"
	got := generateFilename(cfg, sdk.Event{Message: `a/b:c*?"<>|`})
	want := "a_b_c______"
	if got != want {
		t.Fatalf("sanitize = %q, want %q", got, want)
	}
}

func TestCommandRestAndSplitArgs(t *testing.T) {
	cases := []struct {
		text, name, rest string
		ok               bool
	}{
		{"保存图片 我的图片", "保存图片", "我的图片", true},
		{"保存图片", "保存图片", "", true},
		{"保存图片X", "保存图片", "", false},
		{"其他指令", "保存图片", "", false},
	}
	for _, c := range cases {
		rest, ok := commandRest(c.text, c.name)
		if rest != c.rest || ok != c.ok {
			t.Fatalf("commandRest(%q, %q) = (%q, %v), want (%q, %v)",
				c.text, c.name, rest, ok, c.rest, c.ok)
		}
	}

	args := splitArgs(`a "b c" 'd e' f\ g`)
	want := []string{"a", "b c", "d e", "f g"}
	if len(args) != len(want) {
		t.Fatalf("splitArgs = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("splitArgs = %v, want %v", args, want)
		}
	}
}

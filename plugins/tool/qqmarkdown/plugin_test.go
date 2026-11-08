package qqmarkdown

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shizukubot/events"
	"shizukubot/sdk"
)

type testEnv struct {
	t       *testing.T
	cfg     map[string]any
	source  string
	replies []sdk.Reply
	logs    []string
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
	assertCommands(t, Definition(), "qq-markdown-button")
}

func newEnv(t *testing.T, cfg map[string]any) *testEnv {
	t.Helper()
	return &testEnv{t: t, cfg: cfg, source: "group"}
}

func (e *testEnv) send(text string) error {
	e.t.Helper()
	e.replies = nil
	ev := sdk.Event{
		BotID:     "bot-1",
		GroupID:   "group-1",
		ChannelID: "channel-1",
		UserID:    "user-42",
		MessageID: "msg-100",
		Message:   text,
		Source:    events.Source(e.source),
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	return Handle(context.Background(), sdk.Call{
		Event:  ev,
		Config: e.cfg,
		Log:    func(msg string) { e.logs = append(e.logs, msg) },
	})
}

func toAny(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func baseCfg(dir string, sequence ...string) map[string]any {
	return map[string]any{
		"command_name":  "按钮菜单",
		"file_name_v2":  []any{dir},
		"send_sequence": toAny(sequence),
	}
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatalf("invalid definition: %v", err)
	}
}

func TestRawWithoutKeyboard(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/raw-without-keyboard"))

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(e.replies))
	}
	reply := e.replies[0]
	if reply.Kind != "markdown" {
		t.Fatalf("unexpected kind: %q", reply.Kind)
	}
	if reply.Text != "# 你好啊\n\n这是第一个markdown消息哦~\n" {
		t.Fatalf("unexpected markdown: %q", reply.Text)
	}
	if reply.Keyboard != "" {
		t.Fatalf("unexpected keyboard: %q", reply.Keyboard)
	}
}

func TestRawWithKeyboard(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/raw-markdown"))

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(e.replies))
	}
	reply := e.replies[0]
	if reply.Kind != "keyboard" {
		t.Fatalf("unexpected kind: %q", reply.Kind)
	}
	if reply.Text != "# 你好啊\n\n这是一个markdown消息哦~\n" {
		t.Fatalf("unexpected markdown: %q", reply.Text)
	}
	if !strings.Contains(reply.Keyboard, `"label":"再来一次"`) {
		t.Fatalf("keyboard missing button label: %s", reply.Keyboard)
	}
	if !strings.Contains(reply.Keyboard, `"data":"按钮菜单"`) {
		t.Fatalf("keyboard missing command data: %s", reply.Keyboard)
	}
	if !strings.Contains(reply.Keyboard, `"rows"`) {
		t.Fatalf("keyboard missing rows: %s", reply.Keyboard)
	}
}

func TestJSONTemplate(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "json/json"))

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	reply := e.replies[0]
	if reply.Kind != "keyboard" {
		t.Fatalf("unexpected kind: %q", reply.Kind)
	}
	if reply.Text != "" {
		t.Fatalf("expected empty markdown, got %q", reply.Text)
	}
	if !strings.Contains(reply.Keyboard, `"id":"引号内容请修改为你的json模板ID"`) {
		t.Fatalf("unexpected keyboard: %s", reply.Keyboard)
	}
}

func TestMarkdownTemplate(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "markdown/markdown"))

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	reply := e.replies[0]
	if reply.Kind != "keyboard" {
		t.Fatalf("unexpected kind: %q", reply.Kind)
	}
	for _, want := range []string{"第一个文字参数", "第二个文字参数", "![img#338px #250px]", "https://i0.hdslb.com/bfs/note/457c42064e08c44ffef1b047478671db3f06412f.jpg"} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("markdown missing %q: %q", want, reply.Text)
		}
	}
	if !strings.Contains(reply.Keyboard, "json模板ID") {
		t.Fatalf("unexpected keyboard: %s", reply.Keyboard)
	}
}

func TestSequenceSendsMultiple(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/raw-without-keyboard", "raw/raw-markdown"))

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 2 {
		t.Fatalf("expected two replies, got %d", len(e.replies))
	}
	if e.replies[0].Kind != "markdown" || e.replies[1].Kind != "keyboard" {
		t.Fatalf("unexpected reply kinds: %q, %q", e.replies[0].Kind, e.replies[1].Kind)
	}
}

func TestCustomTemplateAndPlaceholders(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/custom"))
	if err := ensureTemplateFiles(dir); err != nil {
		t.Fatalf("ensure templates: %v", err)
	}

	template := `{
		"msg_id": "${session.messageId}",
		"event_id": "${INTERACTION_CREATE}",
		"markdown": {"content": "${markdown}"},
		"keyboard": {"content": {"rows": [{"buttons": [{"render_data": {"label": "${session.userId}-${0}-${1}"}}]}]}}
	}`
	if err := os.WriteFile(filepath.Join(dir, "raw", "custom.json"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	md := "# ${config.command_name} / ${session.platform}"
	if err := os.WriteFile(filepath.Join(dir, "raw", "custom.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.send(`按钮菜单 一 "二 三"`); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	reply := e.replies[0]
	if reply.Text != "# 按钮菜单 / qq" {
		t.Fatalf("unexpected markdown: %q", reply.Text)
	}
	if !strings.Contains(reply.Keyboard, `"label":"user-42-一-二 三"`) {
		t.Fatalf("unexpected keyboard: %s", reply.Keyboard)
	}
}

func TestTemplateFilesPreserved(t *testing.T) {
	dir := t.TempDir()
	if err := ensureTemplateFiles(dir); err != nil {
		t.Fatalf("ensure templates: %v", err)
	}
	for _, rel := range defaultTemplates {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}

	custom := filepath.Join(dir, "raw", "raw-markdown.md")
	if err := os.WriteFile(custom, []byte("自定义内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureTemplateFiles(dir); err != nil {
		t.Fatalf("ensure templates: %v", err)
	}
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "自定义内容" {
		t.Fatalf("existing template was overwritten: %q", data)
	}
}

func TestMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/not-exist"))

	if err := e.send("按钮菜单"); err == nil {
		t.Fatal("expected error for missing template")
	}
	if len(e.replies) != 0 {
		t.Fatalf("expected no reply, got %v", e.replies)
	}
}

func TestInvalidTemplateRef(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "bad", "unknown/name"))
	if err := e.send("按钮菜单"); err == nil {
		t.Fatal("expected error for invalid template refs")
	}
}

func TestEmptySequence(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg(dir)
	e := newEnv(t, cfg)

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 1 || e.replies[0].Text != "当前未配置发送步骤。" {
		t.Fatalf("unexpected replies: %+v", e.replies)
	}
}

func TestKeyboardFailureDegradesToText(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg(dir, "raw/raw-markdown")
	e := newEnv(t, cfg)
	ev := sdk.Event{
		BotID: "bot-1", GroupID: "group-1", UserID: "user-42",
		MessageID: "msg-100", Message: "按钮菜单", Source: events.Group,
		Reply: func(_ context.Context, r sdk.Reply) error {
			if r.Kind == "keyboard" {
				return errors.New("keyboard unsupported")
			}
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: cfg}); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 1 || e.replies[0].Kind != "text" ||
		!strings.Contains(e.replies[0].Text, "这是一个markdown消息哦") {
		t.Fatalf("fallback replies = %+v", e.replies)
	}
}

func TestNonCommandMessageIgnored(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/raw-markdown"))

	if err := e.send("不是按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 0 {
		t.Fatalf("expected no reply, got %+v", e.replies)
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, baseCfg(dir, "raw/raw-markdown"))
	e.source = ""

	if err := e.send("按钮菜单"); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if len(e.replies) != 1 || e.replies[0].Text != "仅支持QQ官方平台使用本指令。" {
		t.Fatalf("unexpected replies: %+v", e.replies)
	}
}

func TestResolveBaseDir(t *testing.T) {
	got := resolveBaseDir([]string{"data", "qq-markdown-button", "配置"})
	want := filepath.Join("data", "qq-markdown-button-v2", "配置")
	if got != want {
		t.Fatalf("resolveBaseDir = %q, want %q", got, want)
	}

	got = resolveBaseDir([]string{"data", "qq-markdown-button-v2", "配置"})
	if got != want {
		t.Fatalf("resolveBaseDir = %q, want %q", got, want)
	}
}

func TestParseTemplateRef(t *testing.T) {
	cases := []struct {
		ref, typ, name string
		ok             bool
	}{
		{"raw/raw-markdown", "raw", "raw-markdown", true},
		{"json/json", "json", "json", true},
		{"markdown/markdown", "markdown", "markdown", true},
		{"raw", "", "", false},
		{"raw/", "", "", false},
		{"/name", "", "", false},
		{"other/name", "", "", false},
		{"raw/a/b", "", "", false},
	}
	for _, c := range cases {
		typ, name, err := parseTemplateRef(c.ref)
		if c.ok {
			if err != nil || typ != c.typ || name != c.name {
				t.Fatalf("parseTemplateRef(%q) = (%q, %q, %v)", c.ref, typ, name, err)
			}
		} else if err == nil {
			t.Fatalf("parseTemplateRef(%q) expected error", c.ref)
		}
	}
}

func TestLookupVariable(t *testing.T) {
	vars := map[string]any{
		"session": map[string]any{
			"userId": "u1",
			"event":  map[string]any{"guild": map[string]any{"id": "g1"}},
			"list":   []any{"first", "second"},
			"flag":   true,
			"count":  float64(3),
			"none":   nil,
		},
	}
	args := []string{"a", "b"}

	cases := []struct {
		key, want string
		ok        bool
	}{
		{"session.userId", "u1", true},
		{"session.event.guild.id", "g1", true},
		{"session.list.1", "second", true},
		{"session.flag", "true", true},
		{"session.count", "3", true},
		{"session.none", "undefined", true},
		{"session.missing", "", false},
		{"0", "a", true},
		{"1", "b", true},
		{"2", "undefined", true},
	}
	for _, c := range cases {
		got, ok := lookupVariable(c.key, vars, args)
		if got != c.want || ok != c.ok {
			t.Fatalf("lookupVariable(%q) = (%q, %v), want (%q, %v)", c.key, got, ok, c.want, c.ok)
		}
	}
}

func TestReplacePlaceholdersPreservesUnknown(t *testing.T) {
	vars := map[string]any{"session": map[string]any{"userId": "u1"}}
	out := replacePlaceholders(map[string]any{
		"known":   "${session.userId}",
		"unknown": "${session.nope}",
		"list":    []any{"${session.userId}-x"},
	}, vars, nil)

	m := out.(map[string]any)
	if m["known"] != "u1" {
		t.Fatalf("known = %v", m["known"])
	}
	if m["unknown"] != "${session.nope}" {
		t.Fatalf("unknown = %v", m["unknown"])
	}
	if got := m["list"].([]any)[0]; got != "u1-x" {
		t.Fatalf("list = %v", got)
	}
}

func TestCommandRest(t *testing.T) {
	cases := []struct {
		text, rest string
		ok         bool
	}{
		{"按钮菜单", "", true},
		{"按钮菜单 arg1 arg2", "arg1 arg2", true},
		{"按钮菜单\targ", "arg", true},
		{"按钮菜单X", "", false},
		{"其他", "", false},
	}
	for _, c := range cases {
		rest, ok := commandRest(c.text, "按钮菜单")
		if rest != c.rest || ok != c.ok {
			t.Fatalf("commandRest(%q) = (%q, %v), want (%q, %v)", c.text, rest, ok, c.rest, c.ok)
		}
	}
}

func TestSplitArgs(t *testing.T) {
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
	if got := splitArgs("   "); len(got) != 0 {
		t.Fatalf("splitArgs blank = %v", got)
	}
}

func TestTemplateParamsText(t *testing.T) {
	params := []any{
		map[string]any{"key": "text1", "values": []any{"甲"}},
		map[string]any{"key": "text2", "values": []any{"乙", "丙"}},
	}
	if got := templateParamsText(params); got != "甲\n乙\n丙" {
		t.Fatalf("templateParamsText = %q", got)
	}
	if got := templateParamsText(nil); got != "" {
		t.Fatalf("templateParamsText(nil) = %q", got)
	}
}

func TestInteractionID(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"_data": map[string]any{"id": "interaction-1"}})
	if got := interactionID(sdk.Event{Raw: raw}); got != "interaction-1" {
		t.Fatalf("interactionID = %q", got)
	}
	if got := interactionID(sdk.Event{}); got != "" {
		t.Fatalf("interactionID empty = %q", got)
	}
}

package wordcount

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

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
	assertCommands(t, Definition(), "wordcount")
}

func run(t *testing.T, message string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStats(t *testing.T) {
	got := run(t, "字数 Hello, world! 你好世界 123")
	for _, want := range []string{
		"总字符数：22",
		"汉字数：4",
		"英文字母数：10",
		"英文单词数：2",
		"数字个数：3",
		"标点数：2",
		"行数：1",
		"去重字符数：16",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply missing %q: %q", want, got)
		}
	}
}

func TestStatsAlias(t *testing.T) {
	got := run(t, "字数统计 你好")
	if !strings.Contains(got, "总字符数：2") || !strings.Contains(got, "汉字数：2") {
		t.Fatalf("reply = %q", got)
	}
}

func TestFrequencyRanking(t *testing.T) {
	got := run(t, "词频 苹果苹果香蕉 Hello hello world 3")
	for _, want := range []string{"hello ×2", "苹果 ×2", "world ×1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "香蕉") {
		t.Fatalf("N=3 should drop 香蕉: %q", got)
	}
	if strings.Index(got, "hello ×2") > strings.Index(got, "苹果 ×2") {
		t.Fatalf("tie should be sorted by word: %q", got)
	}
}

func TestFrequencyDefaultLimit(t *testing.T) {
	got := run(t, "词频 你好")
	if !strings.Contains(got, "词频统计（前 10 名）") || !strings.Contains(got, "你好 ×1") {
		t.Fatalf("reply = %q", got)
	}
}

func TestUsage(t *testing.T) {
	for _, message := range []string{"字数", "字数统计", "词频"} {
		if got := run(t, message); !strings.Contains(got, "用法") {
			t.Fatalf("%q reply = %q", message, got)
		}
	}
}

// 热词入口需要宿主历史消息，当前只做参数解析并给出明确提示。
func TestHotwordEntry(t *testing.T) {
	for _, tc := range []struct {
		message string
		limit   string
	}{
		{"热词", "1000"},
		{"热词 500", "500"},
		{"热词500", "500"},
		{"热词 0", "1000"},
		{"热词 999999", "10000"},
	} {
		got := run(t, tc.message)
		if !strings.Contains(got, "需要宿主提供历史消息") || !strings.Contains(got, tc.limit) {
			t.Fatalf("%q reply = %q", tc.message, got)
		}
	}
	// 非数字参数不触发，保持普通聊天。
	if got := run(t, "热词 中文"); got != "" {
		t.Fatalf("invalid hotword replied %q", got)
	}
}

func TestIgnoresOtherMessages(t *testing.T) {
	if got := run(t, "今天天气不错"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

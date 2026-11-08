package chouxianghua

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
	assertCommands(t, Definition(), "chouxianghua")
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

// 上游 cxh.db 导入后应保留完整拼音表与表情表。
func TestDataLoaded(t *testing.T) {
	if len(pinyinMap) < 20000 {
		t.Fatalf("pinyin table too small: %d", len(pinyinMap))
	}
	if len(emojiMap) < 300 {
		t.Fatalf("emoji table too small: %d", len(emojiMap))
	}
	if pinyinMap["微"] != "wei" || pinyinMap["笑"] != "xiao" {
		t.Fatalf("unexpected pinyin: %q %q", pinyinMap["微"], pinyinMap["笑"])
	}
}

// 双字拼音优先：wei+xiao -> weixiao -> 😊。
func TestDoubleWordPinyin(t *testing.T) {
	if got := run(t, "抽象翻译 微笑"); got != "😊" {
		t.Fatalf("reply = %q", got)
	}
}

// 双字未命中时回退到单字拼音。
func TestSingleWordPinyin(t *testing.T) {
	if got := run(t, "抽象翻译 牛"); got != "🐮" {
		t.Fatalf("reply = %q", got)
	}
	if got := run(t, "抽象翻译 你好"); got != "👇🐵" {
		t.Fatalf("reply = %q", got)
	}
}

// 没有任何拼音/表情匹配时原样输出。
func TestPassthrough(t *testing.T) {
	if got := run(t, "抽象翻译 天"); got != "天" {
		t.Fatalf("reply = %q", got)
	}
	if got := run(t, "抽象翻译 abc123"); got != "abc123" {
		t.Fatalf("reply = %q", got)
	}
}

func TestUsage(t *testing.T) {
	if got := run(t, "抽象翻译"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "抽象"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

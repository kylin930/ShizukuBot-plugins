package furrycode

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type testEnv struct {
	replies []sdk.Reply
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
	assertCommands(t, Definition(), "furrycode")
}

func (e *testEnv) run(t *testing.T, text string, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{
		Message: text,
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: cfg}); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) text() string {
	if len(e.replies) == 0 {
		return ""
	}
	return e.replies[len(e.replies)-1].Text
}

func TestLegacyKnownValue(t *testing.T) {
	e := &testEnv{}
	e.run(t, "兽语加密 a", nil)
	if got, want := e.text(), "~呜嗷嗷嗷嗷呜啊嗷呜嗷啊"; got != want {
		t.Fatalf("encode(a) = %q, want %q", got, want)
	}
}

func TestLegacyRoundTrip(t *testing.T) {
	e := &testEnv{}
	input := "测试 abc 123 ！"
	e.run(t, "兽语加密 "+input, nil)
	encoded := e.text()
	if !strings.HasPrefix(encoded, "~呜嗷") || !strings.HasSuffix(encoded, "啊") {
		t.Fatalf("unexpected prefix/suffix: %q", encoded)
	}
	e.run(t, "兽语解密 "+encoded, nil)
	if got := e.text(); got != input {
		t.Fatalf("round trip = %q, want %q", got, input)
	}
}

func TestLegacyDecodeErrors(t *testing.T) {
	e := &testEnv{}
	e.run(t, "兽语解密 hello", nil)
	if got := e.text(); got != "输入格式不正确" {
		t.Fatalf("reply = %q, want format error", got)
	}

	e.run(t, "兽语解密 ~呜嗷嗷嗷啊", map[string]any{})
	if got := e.text(); got != "输入格式不正确" {
		t.Fatalf("reply = %q, want format error for wrong length", got)
	}
}

func TestLegacyEmojiIsLossy(t *testing.T) {
	e := &testEnv{}
	e.run(t, "兽语加密 😀", nil)
	encoded := e.text()
	if n := len([]rune(encoded)); n != 3+8+1 {
		t.Fatalf("encoded rune length = %d, want 12", n)
	}
	e.run(t, "兽语解密 "+encoded, nil)
	if got := e.text(); got != "\uFFFD" {
		t.Fatalf("decoded emoji = %q, want replacement char", got)
	}
}

func TestMSBTRoundTrip(t *testing.T) {
	cfg := map[string]any{"encryptMethod": "msbt"}
	e := &testEnv{}
	input := "兽语 msbt 测试"
	e.run(t, "兽语加密 "+input, cfg)
	encoded := e.text()
	if encoded == input || len([]rune(encoded))%2 != 0 {
		t.Fatalf("unexpected msbt encode: %q", encoded)
	}
	for _, r := range encoded {
		if indexRune(msbtCodebook, r) < 0 {
			t.Fatalf("encoded contains char outside codebook: %q", r)
		}
	}
	e.run(t, "兽语解密 "+encoded, cfg)
	if got := e.text(); got != input {
		t.Fatalf("msbt round trip = %q, want %q", got, input)
	}
}

func TestMSBTDecodeErrors(t *testing.T) {
	cfg := map[string]any{"encryptMethod": "msbt"}
	e := &testEnv{}
	e.run(t, "兽语解密 齁", cfg)
	if got := e.text(); got != "输入长度必须为偶数" {
		t.Fatalf("reply = %q, want even length error", got)
	}
	e.run(t, "兽语解密 齁a", cfg)
	if got := e.text(); got != "输入包含非法字符" {
		t.Fatalf("reply = %q, want invalid char error", got)
	}
	// 0xff 0xff 不是合法 UTF-8。
	e.run(t, "兽语解密 呼呼", cfg)
	if got := e.text(); got != "无法正确解码为 UTF-8 文本" {
		t.Fatalf("reply = %q, want utf8 error", got)
	}
}

func TestCustomCodebook(t *testing.T) {
	e := &testEnv{}
	e.run(t, "兽语加密 abc", map[string]any{
		"encryptMethod":  "custom",
		"customCodebook": "甲乙丙丁",
	})
	encoded := e.text()
	if !strings.HasPrefix(encoded, "丁乙甲") || !strings.HasSuffix(encoded, "丙") {
		t.Fatalf("custom encode = %q", encoded)
	}
	e.run(t, "兽语解密 "+encoded, map[string]any{
		"encryptMethod":  "custom",
		"customCodebook": "甲乙丙丁",
	})
	if got := e.text(); got != "abc" {
		t.Fatalf("custom round trip = %q", got)
	}
}

func TestConfigErrors(t *testing.T) {
	e := &testEnv{}
	const notice = "插件配置有误，请检查“加密方式”和“自定义加密字符串”"

	e.run(t, "兽语加密 abc", map[string]any{
		"encryptMethod":  "custom",
		"customCodebook": "甲乙丙",
	})
	if got := e.text(); got != notice {
		t.Fatalf("short codebook reply = %q", got)
	}

	e.run(t, "兽语加密 abc", map[string]any{
		"encryptMethod":  "custom",
		"customCodebook": "甲乙甲丁",
	})
	if got := e.text(); got != notice {
		t.Fatalf("duplicate codebook reply = %q", got)
	}
}

func TestEmptyInput(t *testing.T) {
	e := &testEnv{}
	e.run(t, "兽语加密", nil)
	if got := e.text(); got != "请输入要处理的内容" {
		t.Fatalf("reply = %q", got)
	}
}

func TestNoTrigger(t *testing.T) {
	e := &testEnv{}
	e.run(t, "随便聊聊", nil)
	if len(e.replies) != 0 {
		t.Fatalf("expected no replies, got %+v", e.replies)
	}
}

func TestOffsetWrapAround(t *testing.T) {
	// 超过 16 个半字节后 offset 回绕，验证解码仍可还原。
	e := &testEnv{}
	input := "0123456789abcdef中文测试"
	e.run(t, "兽语加密 "+input, nil)
	encoded := e.text()
	e.run(t, "兽语解密 "+encoded, nil)
	if got := e.text(); got != input {
		t.Fatalf("round trip = %q, want %q", got, input)
	}
}

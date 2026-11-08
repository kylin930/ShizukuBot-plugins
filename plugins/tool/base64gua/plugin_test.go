package base64gua

import (
	"bytes"
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
	assertCommands(t, Definition(), "base64gua")
}

func replyText(t *testing.T, msg string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{
		Message: msg,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// 上游 unibase2n Base64Gua.EncodeString 的已知向量，用于确认逐字符一致。
func TestEncodeVectors(t *testing.T) {
	cases := map[string]string{
		"1":       "䷌䷐☰",
		"12":      "䷌䷓䷈☱",
		"123":     "䷌䷓䷈䷳",
		"1234":    "䷌䷓䷈䷳䷍䷀☰",
		"1234567": "䷌䷓䷈䷳䷍䷃䷔䷶䷍䷰☰",
		"hello":   "䷚䷆䷕䷬䷛䷆䷼☱",
		"中文":      "䷹䷋䷢䷭䷹䷩䷚䷇",
	}
	for plain, want := range cases {
		if got := replyText(t, "六十四卦加密"+plain); got != want {
			t.Fatalf("加密%s = %q, want %q", plain, got, want)
		}
	}
}

// 原始字节向量同样来自上游 Encode。
func TestRawByteVectors(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0x00}, "\u4dc0\u4dc0\u2630"},
		{[]byte{0xff}, "\u4dff\u4df0\u2630"},
		{[]byte{0x01, 0x02}, "\u4dc0\u4dd0\u4dc8\u2631"},
		{[]byte{0xff, 0xfe, 0xfd}, "\u4dff\u4dff\u4dfb\u4dfd"},
		{[]byte{0x00, 0x00, 0x00, 0x00}, "\u4dc0\u4dc0\u4dc0\u4dc0\u4dc0\u4dc0\u2630"},
	}
	for _, tc := range cases {
		got := encodeGuaString(tc.in)
		if got != tc.want {
			t.Fatalf("encode(%x) = %q, want %q", tc.in, got, tc.want)
		}
		dec, ok := decodeGua(got)
		if !ok || !bytes.Equal(dec, tc.in) {
			t.Fatalf("decode(%q) = %x, %v, want %x", got, dec, ok, tc.in)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, plain := range []string{"hello", "一个测试293", "带空格 and English", "😀 emoji", "a"} {
		enc := replyText(t, "六十四卦加密"+plain)
		if enc == "" || enc == "加密失败!" {
			t.Fatalf("加密%q 失败: %q", plain, enc)
		}
		if got := replyText(t, "六十四卦解密"+enc); got != plain {
			t.Fatalf("解密%s = %q, want %q", enc, got, plain)
		}
	}
}

func TestGuaRoundTripAllLengths(t *testing.T) {
	for n := 1; n <= 64; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*31 + n)
		}
		dec, ok := decodeGua(encodeGuaString(src))
		if !ok || !bytes.Equal(dec, src) {
			t.Fatalf("卦符往返失败 n=%d: %x, %v", n, dec, ok)
		}
	}
}

func TestKeyRoundTrip(t *testing.T) {
	enc := replyText(t, "六十四卦用secret加密hello world")
	if enc == "" || enc == "加密失败!" {
		t.Fatalf("加密失败: %q", enc)
	}
	if got := replyText(t, "六十四卦用secret解密"+enc); got != "hello world" {
		t.Fatalf("解密 = %q", got)
	}

	// 超过 16 字节的密钥按上游规则截断，中文同样可用。
	plain := "带密钥的中文消息"
	enc = replyText(t, "六十四卦用0123456789abcdefXYZ加密"+plain)
	if enc == "" || enc == "加密失败!" {
		t.Fatalf("加密失败: %q", enc)
	}
	if got := replyText(t, "六十四卦用0123456789abcdefXYZ解密"+enc); got != plain {
		t.Fatalf("解密 = %q, want %q", got, plain)
	}
}

func TestTEAPaddingBoundaries(t *testing.T) {
	for n := 1; n <= 20; n++ {
		src := bytes.Repeat([]byte{byte(n)}, n)
		enc := teaEncrypt("key", src)
		got, ok := teaDecrypt("key", enc)
		if !ok || !bytes.Equal(got, src) {
			t.Fatalf("TEA 往返失败 n=%d: %x, %v", n, got, ok)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	cases := map[string]string{
		"六十四卦解密䷀":      "解密失败!",
		"六十四卦解密䷀䷀":     "解密失败!",
		"六十四卦用k解密䷀䷀䷀䷀": "解密失败!",
		"六十四卦解密abc":    "",
		"六十四卦用k解密☰":    "",
		"六十四卦解密":       "",
		"六十四卦加密":       "",
		"随便聊聊":         "",
	}
	for msg, want := range cases {
		if got := replyText(t, msg); got != want {
			t.Fatalf("%q = %q, want %q", msg, got, want)
		}
	}
}

// TestLongInput 确认与上游一致，不对输入长度设限。
func TestLongInput(t *testing.T) {
	plain := strings.Repeat("长文本", 2048)
	enc := replyText(t, "六十四卦加密"+plain)
	if enc == "" || enc == "加密失败!" {
		t.Fatalf("长文本加密失败")
	}
	if got := replyText(t, "六十四卦解密"+enc); got != plain {
		t.Fatalf("长文本往返失败: len=%d", len([]rune(got)))
	}
}

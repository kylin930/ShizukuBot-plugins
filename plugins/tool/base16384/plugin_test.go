package base16384

import (
	"bytes"
	"context"
	"encoding/hex"
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
	assertCommands(t, Definition(), "base16384")
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

// 上游 base14_test.go 的已知向量，用于确认编码结果逐字符一致。
func TestEncodeVectors(t *testing.T) {
	cases := map[string]string{
		"1":        "婀㴁",
		"12":       "婌渀㴂",
		"123":      "婌焰㴃",
		"1234":     "婌焳帀㴄",
		"12345":    "婌焳廔㴅",
		"123456":   "婌焳廔萀㴆",
		"1234567":  "婌焳廔萷",
		"12345678": "婌焳廔萷尀㴁",
	}
	for plain, want := range cases {
		if got := replyText(t, "加密"+plain); got != want {
			t.Fatalf("加密%s = %q, want %q", plain, got, want)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, plain := range []string{"hello", "一个测试293", "带空格 and English", "😀 emoji"} {
		enc := replyText(t, "加密"+plain)
		if enc == "" || enc == "加密失败!" {
			t.Fatalf("加密%q 失败: %q", plain, enc)
		}
		if got := replyText(t, "解密"+enc); got != plain {
			t.Fatalf("解密%s = %q, want %q", enc, got, plain)
		}
	}
}

func TestKeyRoundTrip(t *testing.T) {
	enc := replyText(t, "用secret加密hello world")
	if enc == "" || enc == "加密失败!" {
		t.Fatalf("加密失败: %q", enc)
	}
	if got := replyText(t, "用secret解密"+enc); got != "hello world" {
		t.Fatalf("解密 = %q", got)
	}
	if got := replyText(t, "用other解密"+enc); got != "解密失败!" {
		t.Fatalf("错误密钥应解密失败，got %q", got)
	}
}

func TestInvalidInput(t *testing.T) {
	if got := replyText(t, "解密一一一"); got != "解密失败!" {
		t.Fatalf("解密一一一 = %q", got)
	}
	if got := replyText(t, "用k解密一"); got != "解密失败!" {
		t.Fatalf("用k解密一 = %q", got)
	}
	// 不匹配任何指令时静默忽略。
	if got := replyText(t, "加密"); got != "" {
		t.Fatalf("加密 = %q", got)
	}
	if got := replyText(t, "随便聊聊"); got != "" {
		t.Fatalf("随便聊聊 = %q", got)
	}
}

// TestLongInput 确认与上游一致，不对输入长度设限。
func TestLongInput(t *testing.T) {
	plain := strings.Repeat("长文本", 2048)
	enc := replyText(t, "加密"+plain)
	if enc == "" || enc == "加密失败!" {
		t.Fatalf("长文本加密失败")
	}
	if got := replyText(t, "解密"+enc); got != plain {
		t.Fatalf("长文本往返失败: len=%d", len([]rune(got)))
	}
}

// TestUpstreamTEAVector 使用上游 floatbox/crypto.GetTEA + gofastTEA 实际生成的
// 密文做跨实现校验：固定随机头后，本实现的加密必须逐字节复现该密文，解密必须还原明文。
func TestUpstreamTEAVector(t *testing.T) {
	const key = "secret"
	plain := []byte("hello world")
	want, err := hex.DecodeString("813a47ad3297f60188fa6cdc63ed5663cea24dfdea60a867")
	if err != nil {
		t.Fatal(err)
	}

	if got := getTEA(key).decrypt(want); !bytes.Equal(got, plain) {
		t.Fatalf("decrypt = %x, want %x", got, plain)
	}

	old := randUint32
	header := []uint32{0xc34a6600, 0x656856b8, 0x206f6c6c}
	calls := 0
	randUint32 = func() uint32 {
		v := header[calls%len(header)]
		calls++
		return v
	}
	t.Cleanup(func() { randUint32 = old })

	if got := getTEA(key).encrypt(plain); !bytes.Equal(got, want) {
		t.Fatalf("encrypt = %x, want %x", got, want)
	}
}

// TestGetTEAKeyPadding 验证密钥截断与循环补齐规则。
func TestGetTEAKeyPadding(t *testing.T) {
	if got := getTEA("secret"); got != getTEA("secretsecretsecr") {
		t.Fatalf("短密钥补齐不一致: %v", got)
	}
	if got := getTEA("0123456789abcdefEXTRA"); got != getTEA("0123456789abcdef") {
		t.Fatalf("长密钥截断不一致: %v", got)
	}
	if got := getTEA(""); got != (teaCipher{}) {
		t.Fatalf("空密钥 = %v", got)
	}
}

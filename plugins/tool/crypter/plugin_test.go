package crypter

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	assertCommands(t, Definition(), "crypter")
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

// 与上游 encodeHou 的已知向量一致：0x68 → 嗯～，0x69 → 嗯哈。
func TestHouVector(t *testing.T) {
	if got := replyText(t, "齁语加密hi"); got != "嗯～嗯哈" {
		t.Fatalf("齁语加密hi = %q", got)
	}
	if got := replyText(t, "h加密hi"); got != "嗯～嗯哈" {
		t.Fatalf("h加密hi = %q", got)
	}
	if got := replyText(t, "齁语解密嗯～嗯哈"); got != "hi" {
		t.Fatalf("齁语解密 = %q", got)
	}
	if got := replyText(t, "h解密嗯～嗯哈"); got != "hi" {
		t.Fatalf("h解密 = %q", got)
	}
}

func TestHouRoundTrip(t *testing.T) {
	for _, plain := range []string{"hello", "中文测试", "😀 emoji", "a b c", "~齁"} {
		enc := replyText(t, "齁语加密"+plain)
		if enc == "" {
			t.Fatalf("齁语加密%q 没有回复", plain)
		}
		if got := replyText(t, "h解密"+enc); got != plain {
			t.Fatalf("齁语往返失败：%q → %q → %q", plain, enc, got)
		}
	}
}

// 与上游 encryptFumo 的已知向量一致："hello" 的 Base64 为 aGVsbG8=。
func TestFumoVector(t *testing.T) {
	want := "FUMo.FuMo-FuMo.FUMO,FUmO.FuMo-fumo|="
	if got := replyText(t, "fumo加密hello"); got != want {
		t.Fatalf("fumo加密hello = %q, want %q", got, want)
	}
	if got := replyText(t, "fumo解密"+want); got != "hello" {
		t.Fatalf("fumo解密 = %q", got)
	}
}

func TestFumoRoundTrip(t *testing.T) {
	for _, plain := range []string{"hello world", "一个测试293", "😀", "a"} {
		enc := replyText(t, "fumo加密"+plain)
		if enc == "" {
			t.Fatalf("fumo加密%q 没有回复", plain)
		}
		if got := replyText(t, "fumo解密"+enc); got != plain {
			t.Fatalf("Fumo 往返失败：%q → %q → %q", plain, enc, got)
		}
	}
}

func TestHouEncryptPrefersVoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("text"); got != "嗯～嗯哈" {
			t.Errorf("text = %q", got)
		}
		w.Write([]byte(`{"file_url":"https://voice.test/1.mp3"}`))
	}))
	defer srv.Close()

	var replies []sdk.Reply
	c := sdk.Call{Config: map[string]any{configVoiceEndpoint: srv.URL}, Event: sdk.Event{
		Message: "齁语加密hi",
		Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].Kind != "voice" || replies[0].URL != "https://voice.test/1.mp3" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestHouEncryptVoiceFallbackToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var replies []sdk.Reply
	c := sdk.Call{Config: map[string]any{configVoiceEndpoint: srv.URL}, Event: sdk.Event{
		Message: "齁语加密hi",
		Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].Kind != "text" || replies[0].Text != "嗯～嗯哈" {
		t.Fatalf("replies = %+v", replies)
	}
}

func TestUsage(t *testing.T) {
	for _, msg := range []string{"齁语加密", "h加密", "齁语解密", "h解密", "fumo加密", "fumo解密"} {
		if got := replyText(t, msg); !strings.Contains(got, "用法") {
			t.Fatalf("%q 的提示 = %q", msg, got)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	cases := map[string]string{
		"齁语解密齁":          "齁语密文长度错误，无法解密",
		"fumo解密abc":      "包含无效的Fumo字符或格式错误",
		"fumo解密fumo-abc": "包含无效的Fumo字符",
	}
	for msg, want := range cases {
		if got := replyText(t, msg); !strings.Contains(got, want) {
			t.Fatalf("%q = %q, want contains %q", msg, got, want)
		}
	}
	if got := replyText(t, "普通聊天"); got != "" {
		t.Fatalf("无关消息不应回复，实际 = %q", got)
	}
}

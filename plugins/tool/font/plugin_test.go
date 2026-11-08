package font

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
	assertCommands(t, Definition(), "font")
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "font" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFontStacks(t *testing.T) {
	names := []string{
		"用终末体", "用终末变体", "用紫罗兰体", "用樱酥体", "用Consolas体", "用粗苹方体",
		"用未来荧黑体", "用Gugi体", "用八丸体", "用Impact体", "用猫啃体", "用苹方体",
	}
	if len(names) != 12 {
		t.Fatalf("字体数量 = %d", len(names))
	}
	for _, name := range names {
		if stack, ok := fontStacks[name]; !ok || stack == "" {
			t.Fatalf("字体 %q 未映射", name)
		}
	}
	if stack := fontStack("用不存在体"); stack != defaultFontStack {
		t.Fatalf("未知字体应回退默认字体栈，得到 %q", stack)
	}
	if fontStacks["用终末体"] == fontStacks["用粗苹方体"] ||
		fontStacks["用粗苹方体"] == fontStacks["用苹方体"] ||
		fontStacks["用终末体"] == fontStacks["用苹方体"] {
		t.Fatalf("终末体/粗苹方体/苹方体不应映射到同一字体栈")
	}
}

func TestParseTrigger(t *testing.T) {
	cases := []struct {
		message    string
		wantStack  string
		wantJitter bool
		wantText   string
	}{
		{"用苹方体渲染文字你好", defaultFontStack, false, "你好"},
		{"用终末体渲染抖动文字你好", syumatuFontStack, true, "你好"},
		{"用粗苹方体渲染文字你好", boldPingFangFontStack, false, "你好"},
		{"用Consolas体渲染文字OK", `Consolas, monospace`, false, "OK"},
		{"用Impact体渲染抖动文字BANG", `Impact, Haettenschweiler, sans-serif`, true, "BANG"},
		{"渲染文字默认字体", defaultFontStack, false, "默认字体"},
		{"渲染抖动文字无字体", defaultFontStack, true, "无字体"},
		{"用未知体渲染文字兜底", defaultFontStack, false, "兜底"},
		{"渲染文字第一行\n第二行", defaultFontStack, false, "第一行\n第二行"},
	}
	for _, tc := range cases {
		request, ok := parseTrigger(tc.message)
		if !ok {
			t.Fatalf("未命中 %q", tc.message)
		}
		if request.fontStack != tc.wantStack {
			t.Fatalf("%q 字体栈 = %q，期望 %q", tc.message, request.fontStack, tc.wantStack)
		}
		if request.jitter != tc.wantJitter {
			t.Fatalf("%q 抖动 = %v，期望 %v", tc.message, request.jitter, tc.wantJitter)
		}
		if request.text != tc.wantText {
			t.Fatalf("%q 文本 = %q，期望 %q", tc.message, request.text, tc.wantText)
		}
	}
}

func TestParseTriggerMiss(t *testing.T) {
	for _, message := range []string{"你好", "渲染文字", "用苹方体文字内容"} {
		if _, ok := parseTrigger(message); ok {
			t.Fatalf("不应命中 %q", message)
		}
	}
}

func TestBuildHTML(t *testing.T) {
	got := buildHTML(`Consolas, monospace`, "<b>&\n第二行", false)
	for _, want := range []string{
		"width:400px",
		"font-family:Consolas, monospace",
		"&lt;b&gt;&amp;",
		"第二行",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("HTML 缺少 %q：\n%s", want, got)
		}
	}
	if strings.Contains(got, "<b>&") {
		t.Fatalf("HTML 未转义正文：\n%s", got)
	}
	if strings.Contains(got, "translate(") {
		t.Fatalf("非抖动模式不应包含位移：\n%s", got)
	}

	jitter := buildHTML(`Consolas, monospace`, "你好\n世界", true)
	if strings.Count(jitter, "transform:translate(") != 4 {
		t.Fatalf("抖动模式应为每个字符独立偏移：\n%s", jitter)
	}
	if !strings.Contains(jitter, "<br>") || !strings.Contains(jitter, "<span") {
		t.Fatalf("抖动模式应保留换行与字符 span：\n%s", jitter)
	}
}

// TestHandleIgnoresOtherMessages 用 fake Call 确认非触发消息不产生回复，
// 同时避免测试环境启动浏览器。
func TestHandleIgnoresOtherMessages(t *testing.T) {
	var replies []sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "你好", Reply: func(_ context.Context, r sdk.Reply) error {
		replies = append(replies, r)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 0 {
		t.Fatalf("unexpected replies: %+v", replies)
	}
}

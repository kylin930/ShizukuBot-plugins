package qrcode

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
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
	assertCommands(t, Definition(), "qrcode")
}

func (e *testEnv) run(t *testing.T, args ...string) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		e.replies = append(e.replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{
		Event: ev, Args: args,
		Config: map[string]any{"enablecommand": true},
	}); err != nil {
		t.Fatalf("Handle(%v) error: %v", args, err)
	}
}

func TestCommandDisabled(t *testing.T) {
	var replies []sdk.Reply
	ev := sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		replies = append(replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Args: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 0 {
		t.Fatalf("disabled command replied: %+v", replies)
	}
}

func (e *testEnv) image(t *testing.T) image.Image {
	t.Helper()
	if len(e.replies) == 0 || e.replies[len(e.replies)-1].Kind != "image" {
		t.Fatalf("expected image reply, got %+v", e.replies)
	}
	url := e.replies[len(e.replies)-1].URL
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("unexpected image url prefix: %s", url[:min(len(url), 32)])
	}
	data, err := base64.StdEncoding.DecodeString(url[len(prefix):])
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

func (e *testEnv) text() string {
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	return ""
}

func TestDefaultSize(t *testing.T) {
	e := &testEnv{}
	e.run(t, "hello")
	img := e.image(t)
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("image not square: %v", b)
	}
	// 21 模块(版本1) + 4*2 边界 = 29，默认缩放 4 => 116。
	if b.Dx() != 116 {
		t.Fatalf("default size = %d, want 116", b.Dx())
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a == 0 {
		t.Fatalf("background should be opaque")
	}
}

func TestWidthAndColors(t *testing.T) {
	e := &testEnv{}
	e.run(t, "-w", "200", "-d", "#ff0000", "-l", "#00ff00", "hello")
	img := e.image(t)
	if got := img.Bounds().Dx(); got != 200 {
		t.Fatalf("width = %d, want 200", got)
	}
	if r, g, b, _ := img.At(3, 3).RGBA(); r>>8 != 0 || g>>8 != 0xff || b>>8 != 0 {
		t.Fatalf("corner color = %d,%d,%d, want light green", r>>8, g>>8, b>>8)
	}
	foundRed := false
	for y := 0; y < 200 && !foundRed; y++ {
		for x := 0; x < 200; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 == 0xff && g>>8 == 0 && b>>8 == 0 {
				foundRed = true
				break
			}
		}
	}
	if !foundRed {
		t.Fatalf("dark red modules not found")
	}
}

func TestMarginAndScale(t *testing.T) {
	e := &testEnv{}
	e.run(t, "-m", "2", "-s", "2", "hello")
	img := e.image(t)
	// 21 + 4 = 25 单元 * 2 = 50。
	if got := img.Bounds().Dx(); got != 50 {
		t.Fatalf("size = %d, want 50", got)
	}
}

func TestErrors(t *testing.T) {
	e := &testEnv{}
	e.run(t)
	if got := e.text(); got != "请输入源文本。" {
		t.Fatalf("empty text reply = %q", got)
	}

	e.run(t, "[CQ:image,file=abc]")
	if got := e.text(); got != "禁止输入纯文本以外的内容。" {
		t.Fatalf("cq reply = %q", got)
	}

	e.run(t, "-m")
	if !strings.Contains(e.text(), "缺少参数值") {
		t.Fatalf("missing value reply = %q", e.text())
	}

	e.run(t, "-w", "abc", "hi")
	if !strings.Contains(e.text(), "需要非负整数") {
		t.Fatalf("invalid number reply = %q", e.text())
	}

	e.run(t, "-d", "no-such-color", "hi")
	if !strings.Contains(e.text(), "颜色无法识别") {
		t.Fatalf("invalid color reply = %q", e.text())
	}
}

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want color.RGBA
	}{
		{"#fff", color.RGBA{255, 255, 255, 255}},
		{"#ff0000", color.RGBA{255, 0, 0, 255}},
		{"#00ff0080", color.RGBA{0, 255, 0, 128}},
		{"rgb(1,2,3)", color.RGBA{1, 2, 3, 255}},
		{"rgba(1,2,3,0.5)", color.RGBA{1, 2, 3, 128}},
		{"blue", color.RGBA{0, 0, 255, 255}},
	}
	for _, tt := range tests {
		got, err := parseColor(tt.in)
		if err != nil {
			t.Fatalf("parseColor(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseColor(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
	if _, err := parseColor("nope"); err == nil {
		t.Fatalf("expected error for unknown color")
	}
}

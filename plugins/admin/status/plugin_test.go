package status

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// tinyPNG 是 1x1 PNG，满足 ReplyImageBytes 的真实图片类型校验。
var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

type testEnv struct {
	replies []sdk.Reply
}

func (e *testEnv) run(t *testing.T, text string) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{Message: text, Reply: func(_ context.Context, r sdk.Reply) error {
		e.replies = append(e.replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: ev}); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) text() string {
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	return ""
}

func fixtureInfo() info {
	return info{
		Day: true,
		Basic: []statusItem{
			{Percent: 45, Name: "CPU", Text: []string{"8C16T", "最大 3.20Ghz"}},
			{Percent: 80, Name: "RAM", Text: []string{"总共 16.00GB", "已用 12.80GB", "剩余 3.20GB"}},
			{Percent: 95, Name: "SWAP", Text: []string{"总共 4.00GB", "已用 3.80GB", "剩余 0.20GB"}},
		},
		Disks: []statusItem{
			{Percent: 60, Name: `C:\`, Text: []string{"60.00GB / 100.00GB"}},
		},
		More: []statusItem{
			{Name: "OS", Text: []string{"windows"}},
			{Name: "CPU", Text: []string{"Test CPU"}},
			{Name: "Version", Text: []string{"10.0.19045"}},
			{Name: "Plugin", Text: []string{"共 3 个"}},
			{Name: "Memory", Text: []string{"已用 20.00MB"}},
		},
		BotRuntime: "ShizukuBot 已运行 1 天 01:03:00 | 系统运行 2 天 03:04:05",
		BotStatus:  "2026-11-08 12:00:00 | Compiled by go1.26.1 | Windows amd64",
	}
}

// stubDeps 替换采集与渲染，测试结束后自动还原。
func stubDeps(t *testing.T, in info, collectErr error, img []byte) *string {
	t.Helper()
	var got string
	oldCollect, oldRender := collect, renderHTML
	collect = func(sdk.Call) (info, error) { return in, collectErr }
	renderHTML = func(_ context.Context, page []byte, _ sdk.RenderOptions) ([]byte, error) {
		got = string(page)
		return img, nil
	}
	t.Cleanup(func() {
		collect = oldCollect
		renderHTML = oldRender
	})
	return &got
}

func TestTriggers(t *testing.T) {
	for _, trigger := range []string{"检查身体", "自检", "启动自检", "系统状态"} {
		t.Run(trigger, func(t *testing.T) {
			stubDeps(t, fixtureInfo(), nil, tinyPNG)
			e := &testEnv{}
			e.run(t, trigger)
			if len(e.replies) != 1 {
				t.Fatalf("expected 1 reply, got %+v", e.replies)
			}
			if e.replies[0].Kind != "image" {
				t.Fatalf("expected image reply, got %+v", e.replies[0])
			}
		})
	}
}

func TestIgnoresOtherMessages(t *testing.T) {
	stubDeps(t, fixtureInfo(), nil, tinyPNG)
	e := &testEnv{}
	e.run(t, "你好")
	if len(e.replies) != 0 {
		t.Fatalf("expected no reply, got %+v", e.replies)
	}
}

func TestCardContent(t *testing.T) {
	got := stubDeps(t, fixtureInfo(), nil, tinyPNG)
	e := &testEnv{}
	e.run(t, "自检")
	page := *got

	for _, want := range []string{
		`class="day"`,
		"8C16T",
		"最大 3.20Ghz",
		"总共 16.00GB",
		"已用 12.80GB",
		"剩余 3.20GB",
		`C:\`,
		"60.00GB / 100.00GB",
		"Test CPU",
		"10.0.19045",
		"共 3 个",
		"已用 20.00MB",
		"ShizukuBot 已运行 1 天 01:03:00 | 系统运行 2 天 03:04:05",
		"| Windows amd64",
		"45%",
		"80%",
		"95%",
		"60%",
		"--c:rgb(255,70,0)",
		"--c:rgb(255,165,0)",
		"--c:rgb(145,240,145)",
		"Created By ShizukuBot-Plugin " + pluginVersion,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("card missing %q", want)
		}
	}
}

func TestCardNightColors(t *testing.T) {
	in := fixtureInfo()
	in.Day = false
	got := stubDeps(t, in, nil, tinyPNG)
	e := &testEnv{}
	e.run(t, "系统状态")
	page := *got
	if !strings.Contains(page, `class="night"`) {
		t.Fatalf("expected night class, got %q", page)
	}
	for _, want := range []string{"--c:rgb(215,50,0)", "--c:rgb(205,135,0)", "--c:rgb(115,200,115)"} {
		if !strings.Contains(page, want) {
			t.Fatalf("night card missing %q", want)
		}
	}
}

func TestAvatarAndBotName(t *testing.T) {
	got := stubDeps(t, fixtureInfo(), nil, tinyPNG)
	e := &testEnv{}
	e.run(t, "自检")
	if !strings.Contains(*got, `<div class="avatar">S</div>`) || !strings.Contains(*got, "<div class=\"name\">ShizukuBot</div>") {
		t.Fatalf("default bot name/avatar missing: %s", *got)
	}

	ev := sdk.Event{
		BotID:   "shizuku",
		Message: "自检",
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev}); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if !strings.Contains(*got, `<div class="avatar">S</div>`) || !strings.Contains(*got, "shizuku") {
		t.Fatalf("bot name/avatar missing: %s", *got)
	}
}

func TestCollectError(t *testing.T) {
	stubDeps(t, info{}, errors.New("boom"), tinyPNG)
	e := &testEnv{}
	e.run(t, "自检")
	if got := e.text(); got != "ERROR: boom" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestRenderError(t *testing.T) {
	oldRender := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, errors.New("render failed")
	}
	t.Cleanup(func() { renderHTML = oldRender })
	oldCollect := collect
	collect = func(sdk.Call) (info, error) { return fixtureInfo(), nil }
	t.Cleanup(func() { collect = oldCollect })

	e := &testEnv{}
	e.run(t, "自检")
	if got := e.text(); got != "ERROR: render failed" {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestStoragefmt(t *testing.T) {
	cases := map[float64]string{
		512:                       "512.00B",
		1024:                      "1.00KB",
		1536:                      "1.50KB",
		1024 * 1024:               "1.00MB",
		1024 * 1024 * 1024:        "1.00GB",
		2 * 1024 * 1024 * 1024:    "2.00GB",
		1024 * 1024 * 1024 * 1024: "1.00TB",
	}
	for in, want := range cases {
		if got := storagefmt(in); got != want {
			t.Fatalf("storagefmt(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBotRuntimeAndStatus(t *testing.T) {
	nowTime := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	boot := nowTime.Add(-time.Duration(25*3600+3*60) * time.Second)
	uptime := uint64(2*86400 + 3*3600 + 4*60 + 5)
	want := "ShizukuBot 已运行 1 天 01:03:00 | 系统运行 2 天 03:04:05"
	if got := botRuntime(nowTime, boot, uptime); got != want {
		t.Fatalf("botRuntime = %q, want %q", got, want)
	}

	status := botStatus(nowTime, "windows")
	for _, want := range []string{"2026-11-08 12:00:00", "| Compiled by ", "| Windows "} {
		if !strings.Contains(status, want) {
			t.Fatalf("botStatus = %q missing %q", status, want)
		}
	}
}

func TestIsDay(t *testing.T) {
	for hour, want := range map[int]bool{0: false, 7: false, 8: true, 12: true, 18: true, 19: false, 23: false} {
		if got := isDay(hour); got != want {
			t.Fatalf("isDay(%d) = %v, want %v", hour, got, want)
		}
	}
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatalf("invalid definition: %v", err)
	}
}

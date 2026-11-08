package huozi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type testEnv struct {
	replies []sdk.Reply
}

func (e *testEnv) run(t *testing.T, text string, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{Message: text, Reply: func(_ context.Context, r sdk.Reply) error {
		e.replies = append(e.replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: cfg}); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) last(t *testing.T) sdk.Reply {
	t.Helper()
	if len(e.replies) == 0 {
		t.Fatalf("no replies")
	}
	return e.replies[len(e.replies)-1]
}

func useEndpoint(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	old := apiEndpoint
	apiEndpoint = ts.URL + "/api/make"
	t.Cleanup(func() {
		apiEndpoint = old
		ts.Close()
	})
}

func TestGenerateWithDefaults(t *testing.T) {
	var gotQuery url.Values
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/a.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "活字乱刷 你好世界", nil)

	reply := e.last(t)
	if reply.Kind != "voice" || reply.URL != "http://example.com/a.mp3" {
		t.Fatalf("reply = %+v", reply)
	}
	if got := gotQuery.Get("text"); got != "你好世界" {
		t.Fatalf("text = %q", got)
	}
	if got := gotQuery.Get("inYsddMode"); got != "false" {
		t.Fatalf("inYsddMode = %q", got)
	}
	if got := gotQuery.Get("speedMult"); got != "1" {
		t.Fatalf("speedMult = %q", got)
	}
	if got := gotQuery.Get("pitchMult"); got != "1" {
		t.Fatalf("pitchMult = %q", got)
	}
}

func TestGenerateWithFlags(t *testing.T) {
	var gotQuery url.Values
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/b.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "huozi-generator -y -n -r -s 1.5 -p 0.5 哈哈", nil)

	if got := gotQuery.Get("inYsddMode"); got != "true" {
		t.Fatalf("inYsddMode = %q", got)
	}
	if got := gotQuery.Get("norm"); got != "true" {
		t.Fatalf("norm = %q", got)
	}
	if got := gotQuery.Get("reverse"); got != "true" {
		t.Fatalf("reverse = %q", got)
	}
	if got := gotQuery.Get("speedMult"); got != "1.5" {
		t.Fatalf("speedMult = %q", got)
	}
	if got := gotQuery.Get("pitchMult"); got != "0.5" {
		t.Fatalf("pitchMult = %q", got)
	}
	if got := gotQuery.Get("text"); got != "哈哈" {
		t.Fatalf("text = %q", got)
	}
}

func TestInlineOptionValue(t *testing.T) {
	var gotQuery url.Values
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/c.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "活字印刷 -s=1.2 测试", nil)
	if got := gotQuery.Get("speedMult"); got != "1.2" {
		t.Fatalf("speedMult = %q", got)
	}
	if got := gotQuery.Get("text"); got != "测试" {
		t.Fatalf("text = %q", got)
	}
}

func TestNewlineEscaped(t *testing.T) {
	var gotQuery url.Values
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/d.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "活字乱刷 第一行\n第二行", nil)
	if got := gotQuery.Get("text"); got != `第一行\n第二行` {
		t.Fatalf("text = %q, want escaped newline", got)
	}
}

func TestWaitTip(t *testing.T) {
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/e.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "活字乱刷 你好", map[string]any{"waitTip_Switch": "正在生成，请稍候…"})
	if len(e.replies) != 2 {
		t.Fatalf("replies = %+v, want tip + voice", e.replies)
	}
	if e.replies[0].Kind != "text" || e.replies[0].Text != "正在生成，请稍候…" {
		t.Fatalf("first reply = %+v", e.replies[0])
	}
	if e.replies[1].Kind != "voice" {
		t.Fatalf("second reply = %+v", e.replies[1])
	}
}

func TestEmptyContent(t *testing.T) {
	e := &testEnv{}
	e.run(t, "活字乱刷", nil)
	if got := e.last(t); got.Kind != "text" || got.Text != "请输入要生成音频的文本。" {
		t.Fatalf("reply = %+v", got)
	}
}

func TestAPIFailure(t *testing.T) {
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":500}`))
	})
	e := &testEnv{}
	e.run(t, "活字乱刷 你好", nil)
	if got := e.last(t); got.Kind != "text" || got.Text != "音频生成失败，请稍后再试。" {
		t.Fatalf("reply = %+v", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	var gotQuery url.Values
	useEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{"code":200,"file_url":"http://example.com/f.mp3"}`))
	})
	e := &testEnv{}
	e.run(t, "活字乱刷 你好", map[string]any{
		"default_inYsddMode": true,
		"default_speedMult":  1.3,
		"default_pitchMult":  0.8,
	})
	if got := gotQuery.Get("inYsddMode"); got != "true" {
		t.Fatalf("inYsddMode = %q", got)
	}
	if got := gotQuery.Get("speedMult"); got != "1.3" {
		t.Fatalf("speedMult = %q", got)
	}
	if got := gotQuery.Get("pitchMult"); got != "0.8" {
		t.Fatalf("pitchMult = %q", got)
	}
}

func TestNoTrigger(t *testing.T) {
	e := &testEnv{}
	e.run(t, "普通聊天", nil)
	if len(e.replies) != 0 {
		t.Fatalf("expected no replies, got %+v", e.replies)
	}
}

func TestParseArgsPreservesText(t *testing.T) {
	_, content := parseArgs(" -y  你好  世界 ")
	if !strings.Contains(content, "你好") || !strings.Contains(content, "世界") {
		t.Fatalf("content = %q", content)
	}
}

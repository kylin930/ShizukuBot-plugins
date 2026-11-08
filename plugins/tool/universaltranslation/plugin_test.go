package universaltranslation

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	assertCommands(t, Definition(), "universal-translation")
}

func (e *testEnv) run(t *testing.T, args []string, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		e.replies = append(e.replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Args: args, Config: cfg}); err != nil {
		t.Fatalf("Handle(%v) error: %v", args, err)
	}
}

func (e *testEnv) text(t *testing.T) string {
	t.Helper()
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	t.Fatalf("no text reply in %+v", e.replies)
	return ""
}

func useServers(t *testing.T, tencent, cloudflare http.Handler) {
	t.Helper()
	ts := httptest.NewServer(tencent)
	cs := httptest.NewServer(cloudflare)
	oldTencent, oldCloudflare := tencentURL, cloudflareURL
	tencentURL, cloudflareURL = ts.URL, cs.URL
	t.Cleanup(func() {
		tencentURL, cloudflareURL = oldTencent, oldCloudflare
		ts.Close()
		cs.Close()
	})
}

func TestTencentSuccess(t *testing.T) {
	var gotText, gotTarget string
	useServers(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotText = r.URL.Query().Get("SourceText")
			gotTarget = r.URL.Query().Get("Target")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"Response":{"TargetText":"hello"}}}`))
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("cloudflare should not be called")
		}),
	)
	e := &testEnv{}
	e.run(t, []string{"你好"}, nil)
	if got := e.text(t); got != "hello" {
		t.Fatalf("reply = %q, want hello", got)
	}
	if gotText != "你好" {
		t.Fatalf("SourceText = %q", gotText)
	}
	if gotTarget != "en" {
		t.Fatalf("Target = %q, want default en", gotTarget)
	}
}

func TestFallbackToCloudflare(t *testing.T) {
	useServers(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("target_lang"); got != "ja-JP" {
				t.Errorf("target_lang = %q, want ja-JP", got)
			}
			if got := r.URL.Query().Get("source_lang"); got != "zh" {
				t.Errorf("source_lang = %q, want zh", got)
			}
			w.Write([]byte(`{"response":{"translated_text":"こんにちは"}}`))
		}),
	)
	e := &testEnv{}
	e.run(t, []string{"-t", "日语", "你好"}, nil)
	if got := e.text(t); got != "こんにちは" {
		t.Fatalf("reply = %q, want japanese", got)
	}
}

func TestLanguageCodeWithEquals(t *testing.T) {
	var gotTarget string
	useServers(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTarget = r.URL.Query().Get("Target")
			w.Write([]byte(`{"data":{"Response":{"TargetText":"ok"}}}`))
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
	e := &testEnv{}
	e.run(t, []string{"--to=中文", "hello"}, nil)
	if gotTarget != "zh" {
		t.Fatalf("Target = %q, want zh", gotTarget)
	}
}

func TestAllProvidersFailSilently(t *testing.T) {
	useServers(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }),
	)
	e := &testEnv{}
	e.run(t, []string{"你好"}, nil)
	if len(e.replies) != 0 {
		t.Fatalf("unexpected replies: %+v", e.replies)
	}
}

func TestConfigDefaultTarget(t *testing.T) {
	var gotTarget string
	useServers(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTarget = r.URL.Query().Get("Target")
			w.Write([]byte(`{"data":{"Response":{"TargetText":"ok"}}}`))
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
	e := &testEnv{}
	e.run(t, []string{"你好"}, map[string]any{"defaultTargetLang": "ko"})
	if gotTarget != "ko" {
		t.Fatalf("Target = %q, want ko", gotTarget)
	}
}

func TestLanguageTable(t *testing.T) {
	e := &testEnv{}
	e.run(t, []string{"语言代码对照表"}, nil)
	if got := e.text(t); got != tableNotice {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmptyText(t *testing.T) {
	e := &testEnv{}
	e.run(t, nil, nil)
	if got := e.text(t); got != "请输入要翻译的文本..." {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmbeddedLanguageMap(t *testing.T) {
	if got := resolveLanguage("英文"); got != "en" {
		t.Fatalf("英文 = %q, want en", got)
	}
	if got := resolveLanguage("中文(繁体)"); got != "zh-TW" {
		t.Fatalf("中文(繁体) = %q", got)
	}
	// 上游 JSON 中「南非语」重复出现，后者覆盖前者。
	if got := resolveLanguage("南非语"); got != "af-ZA" {
		t.Fatalf("南非语 = %q, want af-ZA", got)
	}
	if got := resolveLanguage("xx-YY"); got != "xx-YY" {
		t.Fatalf("unknown code = %q", got)
	}
}

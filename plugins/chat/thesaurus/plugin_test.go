package thesaurus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func mentionedEvent(message string) sdk.Event {
	return sdk.Event{
		GroupID: "g",
		Message: message,
		Raw:     json.RawMessage(`{"mentions":[{"id":"100","is_you":true}]}`),
	}
}

func call(t *testing.T, event sdk.Event) (string, bool) {
	t.Helper()
	got, replied := "", false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesUntilConfident(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if calls < 3 {
			_, _ = w.Write([]byte(`{"reply":"模糊","confidence":0.1}`))
			return
		}
		_, _ = w.Write([]byte(`{"reply":"你好呀","confidence":0.9}`))
	}))
	defer server.Close()
	old := apiURL
	apiURL = server.URL
	defer func() { apiURL = old }()

	got, replied := call(t, mentionedEvent("<@100> 你好"))
	if !replied || got != "你好呀" || calls != 3 {
		t.Fatalf("reply = %q, replied = %v, calls = %d", got, replied, calls)
	}
}

func TestLowConfidenceIgnored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"reply":"模糊","confidence":0.05}`))
	}))
	defer server.Close()
	old := apiURL
	apiURL = server.URL
	defer func() { apiURL = old }()

	if _, replied := call(t, mentionedEvent("<@100> 随便说说")); replied {
		t.Fatal("low-confidence match replied")
	}
}

func TestNotMentionedIgnored(t *testing.T) {
	old := apiURL
	apiURL = "http://127.0.0.1:1"
	defer func() { apiURL = old }()

	if _, replied := call(t, sdk.Event{GroupID: "g", Message: "你好"}); replied {
		t.Fatal("message without mention replied")
	}
}

func TestAPICodeFromConfigOrEnv(t *testing.T) {
	if got := apiCode(map[string]any{"api_code": " from-config "}); got != "from-config" {
		t.Fatalf("config code = %q", got)
	}
	t.Setenv("SHIZUKU_THESAURUS_CODE", "from-env")
	if got := apiCode(nil); got != "from-env" {
		t.Fatalf("env code = %q", got)
	}
}

func TestHTTPerrorIgnored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()
	old := apiURL
	apiURL = server.URL
	defer func() { apiURL = old }()

	if got, replied := call(t, mentionedEvent("<@100> 你好")); replied {
		t.Fatalf("unexpected reply %q", got)
	}
	if _, err := query(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

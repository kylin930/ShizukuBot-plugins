package nbnhhsh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func reply(t *testing.T, message string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`[{"trans":["永远的神","永不言败"]}]`))
	}))
	defer server.Close()
	old := guessURL
	guessURL = server.URL
	defer func() { guessURL = old }()

	got := reply(t, "?? yyds")
	if got != "yyds: 永远的神, 永不言败" {
		t.Fatalf("reply = %q", got)
	}
}

func TestInputtingFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"inputting":["暂无释义"]}]`))
	}))
	defer server.Close()
	old := guessURL
	guessURL = server.URL
	defer func() { guessURL = old }()

	got := reply(t, "？yyds")
	if got != "yyds: 暂无释义" {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	old := guessURL
	guessURL = server.URL
	defer func() { guessURL = old }()

	got := reply(t, "?? yyds")
	if !strings.Contains(got, "没有找到释义") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := reply(t, "yyds"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

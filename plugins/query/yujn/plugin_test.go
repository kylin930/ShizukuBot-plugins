package yujn

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

func TestTextCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("  今天也要开心  "))
	}))
	defer server.Close()
	old := textEndpoints["土味情话"]
	textEndpoints["土味情话"] = endpoint{url: server.URL}
	defer func() { textEndpoints["土味情话"] = old }()

	if got := reply(t, "土味情话"); got != "今天也要开心" {
		t.Fatalf("reply = %q", got)
	}
}

func TestCleanParagraph(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>山有木兮木有枝</p><p>心悦君兮君不知</p>"))
	}))
	defer server.Close()
	old := textEndpoints["随机美句"]
	textEndpoints["随机美句"] = endpoint{url: server.URL, cleanParagraph: true}
	defer func() { textEndpoints["随机美句"] = old }()

	if got := reply(t, "随机美句"); got != "山有木兮木有枝心悦君兮君不知" {
		t.Fatalf("reply = %q", got)
	}
}

func TestErrorReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	old := textEndpoints["随机骚话"]
	textEndpoints["随机骚话"] = endpoint{url: server.URL}
	defer func() { textEndpoints["随机骚话"] = old }()

	if got := reply(t, "随机骚话"); !strings.Contains(got, "获取失败") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := reply(t, "随机美文"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

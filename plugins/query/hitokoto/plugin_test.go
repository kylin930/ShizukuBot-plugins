package hitokoto

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

func TestHitokoto(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hitokoto":"人生如逆旅","from":"苏轼","from_who":"苏轼"}`))
	}))
	defer server.Close()
	old := apiURL
	apiURL = server.URL
	defer func() { apiURL = old }()

	got := reply(t, "一言")
	if !strings.Contains(got, "人生如逆旅") || !strings.Contains(got, "苏轼《苏轼》") {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmptyMessage(t *testing.T) {
	if got := reply(t, "今天天气不错"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

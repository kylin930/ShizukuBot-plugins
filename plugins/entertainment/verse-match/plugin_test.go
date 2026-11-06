package versematch

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

func TestCouplet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>下联：秋水共长天一色</p>"))
	}))
	defer server.Close()
	old := poetURL
	poetURL = server.URL
	defer func() { poetURL = old }()

	got := reply(t, "对对联 落霞与孤鹜齐飞")
	if !strings.Contains(got, "▼下联:秋水共长天一色") {
		t.Fatalf("reply = %q", got)
	}
}

func TestRejectNonChinese(t *testing.T) {
	if got := reply(t, "对对联 hello"); !strings.Contains(got, "英文") {
		t.Fatalf("reply = %q", got)
	}
}

func TestUsage(t *testing.T) {
	if got := reply(t, "对对联"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
}

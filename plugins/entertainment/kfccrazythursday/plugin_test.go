package kfccrazythursday

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestHandle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("  v我50  "))
	}))
	defer server.Close()
	old := crazyURL
	crazyURL = server.URL
	defer func() { crazyURL = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "疯狂星期四", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != "v我50" {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "星期四", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestErrorReply(t *testing.T) {
	old := crazyURL
	crazyURL = "http://127.0.0.1:1/"
	defer func() { crazyURL = old }()
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "疯狂星期四", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "失败") {
		t.Fatalf("reply = %q", got)
	}
}

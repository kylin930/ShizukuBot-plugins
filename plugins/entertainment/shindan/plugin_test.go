package shindan

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

const resultHTML = `<div id="shindanResult"><p>今天你是 <strong>魔法少女</strong></p><img src="x"><p>a &amp; b</p></div>`

func TestShindanWithName(t *testing.T) {
	var postedName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("Set-Cookie", "_session=token123; Path=/; HttpOnly")
			_, _ = w.Write([]byte(`<form id="shindanForm"><input type="hidden" name="_token" value="tok"></form>`))
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
			return
		}
		if got := r.FormValue("_token"); got != "tok" {
			t.Errorf("token = %q", got)
		}
		postedName = r.FormValue("user_input_value_1")
		_, _ = w.Write([]byte(resultHTML))
	}))
	defer server.Close()
	old := baseURL
	baseURL = server.URL
	defer func() { baseURL = old }()

	got := reply(t, "卖萌 小明")
	if got != "今天你是 魔法少女\na & b" {
		t.Fatalf("reply = %q", got)
	}
	if !strings.HasPrefix(postedName, "小明") || len(postedName) <= len("小明") {
		t.Fatalf("posted name = %q", postedName)
	}
}

func TestShindanFallsBackToUserID(t *testing.T) {
	var postedName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("Set-Cookie", "_session=token123; Path=/")
			_, _ = w.Write([]byte(`<input type="hidden" name="_token" value="tok">`))
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		postedName = r.FormValue("user_input_value_1")
		_, _ = w.Write([]byte(resultHTML))
	}))
	defer server.Close()
	old := baseURL
	baseURL = server.URL
	defer func() { baseURL = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "今日老婆", UserID: "u42", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(postedName, "u42") {
		t.Fatalf("posted name = %q", postedName)
	}
	if got == "" {
		t.Fatal("empty reply")
	}
}

func TestMissingResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("Set-Cookie", "_session=token123; Path=/")
			_, _ = w.Write([]byte(`<input type="hidden" name="_token" value="tok">`))
			return
		}
		_, _ = w.Write([]byte(`<html><body>nothing</body></html>`))
	}))
	defer server.Close()
	old := baseURL
	baseURL = server.URL
	defer func() { baseURL = old }()

	if got := reply(t, "卖萌 小明"); !strings.Contains(got, "无法查找到结果") {
		t.Fatalf("reply = %q", got)
	}
}

func TestMissingCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<input type="hidden" name="_token" value="tok">`))
	}))
	defer server.Close()
	old := baseURL
	baseURL = server.URL
	defer func() { baseURL = old }()

	if got := reply(t, "卖萌 小明"); !strings.Contains(got, "cookie") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := reply(t, "今天天气不错"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

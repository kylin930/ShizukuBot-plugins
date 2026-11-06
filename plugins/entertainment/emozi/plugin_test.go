package emozi

import (
	"context"
	"encoding/json"
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

func TestEncodePicksFirstChoice(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/encode" {
			http.NotFound(w, r)
			return
		}
		calls++
		var body encodeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		if len(body.Choice) == 0 {
			_, _ = w.Write([]byte(`{"code":0,"message":"","result":{"text":"初步结果","choice":[2]}}`))
			return
		}
		for _, c := range body.Choice {
			if c != 0 {
				t.Errorf("choice = %v", body.Choice)
			}
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"","result":{"text":"你好(・ω・)ノ"}}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	if got := reply(t, "抽象转写 你好"); got != "你好(・ω・)ノ" {
		t.Fatalf("reply = %q", got)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestDecode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/decode" {
			http.NotFound(w, r)
			return
		}
		var body decodeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"code":0,"message":"","result":"你好世界"}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	if got := reply(t, "抽象还原 (・ω・)"); got != "你好世界" {
		t.Fatalf("reply = %q", got)
	}
}

func TestUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":5,"message":"请求过于频繁"}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	if got := reply(t, "抽象转写 你好"); !strings.Contains(got, "请求过于频繁") {
		t.Fatalf("reply = %q", got)
	}
}

func TestUsageAndIgnoresOtherText(t *testing.T) {
	if got := reply(t, "抽象转写"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := reply(t, "抽象还原"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := reply(t, "抽象 你好"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

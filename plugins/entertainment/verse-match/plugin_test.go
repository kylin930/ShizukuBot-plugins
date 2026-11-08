package versematch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func replies(t *testing.T, message string) []string {
	t.Helper()
	var got []string
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = append(got, r.Text)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	old := poetURL
	poetURL = server.URL
	t.Cleanup(func() { poetURL = old })
}

func TestCouplet(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>下联：秋水共长天一色</p>"))
	})

	got := replies(t, "对对联 落霞与孤鹜齐飞")
	if len(got) != 2 || got[0] != "正在对联中..." {
		t.Fatalf("replies = %v", got)
	}
	if !strings.Contains(got[1], "▼下联:秋水共长天一色") {
		t.Fatalf("reply = %q", got[1])
	}
}

func TestMainCommand(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>下联：秋水共长天一色</p>"))
	})

	got := replies(t, "verse-match 落霞与孤鹜齐飞")
	if len(got) != 2 || !strings.Contains(got[1], "▼下联:秋水共长天一色") {
		t.Fatalf("replies = %v", got)
	}
}

func TestNoCouplet(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>没有结果</p>"))
	})

	got := replies(t, "对对联 落霞与孤鹜齐飞")
	if len(got) != 2 || got[1] != "未能找到合适的下联。" {
		t.Fatalf("replies = %v", got)
	}
}

func TestRequestFailure(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	got := replies(t, "对对联 落霞与孤鹜齐飞")
	if len(got) != 2 || got[1] != "请求过程中发生错误，请稍后再试。" {
		t.Fatalf("replies = %v", got)
	}
}

func TestRejectNonChinese(t *testing.T) {
	got := replies(t, "对对联 hello")
	if len(got) != 1 || !strings.Contains(got[0], "英文") {
		t.Fatalf("replies = %v", got)
	}
}

func TestUsage(t *testing.T) {
	got := replies(t, "对对联")
	if len(got) != 1 || !strings.Contains(got[0], "用法") {
		t.Fatalf("replies = %v", got)
	}
	if got := replies(t, "verse-match"); len(got) != 1 || !strings.Contains(got[0], "用法") {
		t.Fatalf("replies = %v", got)
	}
}

func TestIgnoreOtherText(t *testing.T) {
	if got := replies(t, "今天天气不错"); len(got) != 0 {
		t.Fatalf("unexpected replies %v", got)
	}
}

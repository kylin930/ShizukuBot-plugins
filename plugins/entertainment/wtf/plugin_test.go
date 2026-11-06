package wtf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	old := apiBase
	apiBase = server.URL + "/"
	t.Cleanup(func() { apiBase = old })
}

func reply(t *testing.T, event sdk.Event) (string, bool) {
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

func TestList(t *testing.T) {
	got, replied := reply(t, sdk.Event{Message: "鬼东西列表"})
	if !replied {
		t.Fatal("list did not reply")
	}
	for _, want := range []string{"00. 你的意义是什么?", "87. 你的蘿莉控程度全國排名"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list missing %q", want)
		}
	}
	if lines := strings.Count(got, "\n") + 1; lines != len(table) {
		t.Fatalf("lines = %d, table = %d", lines, len(table))
	}
}

func TestQuery(t *testing.T) {
	var escaped string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		escaped = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"text":"你的结果是魔法少女","ok":true,"msg":""}`)
	})
	got, replied := reply(t, sdk.Event{GroupID: "g", UserID: "用户1", Message: "查询鬼东西0"})
	if !replied {
		t.Fatal("query did not reply")
	}
	if escaped != "/mRIFuS/%E7%94%A8%E6%88%B71" {
		t.Fatalf("request path = %q", escaped)
	}
	if got != "> 你的意义是什么?\n你的结果是魔法少女" {
		t.Fatalf("reply = %q", got)
	}
}

func TestQueryUsesMentionedUser(t *testing.T) {
	var escaped string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		escaped = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"text":"结果","ok":true,"msg":""}`)
	})
	_, replied := reply(t, sdk.Event{
		GroupID: "g",
		UserID:  "sender",
		Message: `查询鬼东西0 <qqbot-at-user id="TARGET" />`,
	})
	if !replied {
		t.Fatal("query did not reply")
	}
	if escaped != "/mRIFuS/TARGET" {
		t.Fatalf("request path = %q", escaped)
	}
}

func TestQueryUpstreamFailure(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"text":"","ok":false,"msg":"参数错误"}`)
	})
	got, replied := reply(t, sdk.Event{GroupID: "g", UserID: "u1", Message: "查询鬼东西1"})
	if !replied || !strings.Contains(got, "参数错误") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestQueryBadStatus(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	got, replied := reply(t, sdk.Event{GroupID: "g", UserID: "u1", Message: "查询鬼东西1"})
	if !replied || !strings.Contains(got, "上游返回状态") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestQueryUnknownIndex(t *testing.T) {
	if got, replied := reply(t, sdk.Event{GroupID: "g", UserID: "u1", Message: "查询鬼东西99"}); !replied || got != "没有这项内容！" {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestQueryMissingNumber(t *testing.T) {
	if got, replied := reply(t, sdk.Event{GroupID: "g", UserID: "u1", Message: "查询鬼东西"}); !replied || !strings.Contains(got, "序号") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestPrivateQueryIgnored(t *testing.T) {
	if _, replied := reply(t, sdk.Event{UserID: "u1", Message: "查询鬼东西0"}); replied {
		t.Fatal("private query replied")
	}
}

func TestNonMatch(t *testing.T) {
	if _, replied := reply(t, sdk.Event{GroupID: "g", Message: "鬼东西"}); replied {
		t.Fatal("unexpected reply")
	}
}

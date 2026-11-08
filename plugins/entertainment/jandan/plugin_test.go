package jandan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func replyAll(t *testing.T, userID, message string, st sdk.State, config map[string]any) []sdk.Reply {
	t.Helper()
	var replies []sdk.Reply
	c := sdk.Call{State: st, Config: config, Event: sdk.Event{UserID: userID, Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		replies = append(replies, r)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return replies
}

// serveAPI 启动假的评论接口，page 返回 code、链接列表与总页数。
func serveAPI(t *testing.T, pages map[int][]string, total int, blocked map[int]bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fmt.Sprintf("/api/comment/post/%d", picPostID) {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if blocked[page] {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1000, "msg": "较老的数据仅对登陆用户开放访问", "data": nil})
			return
		}
		links, ok := pages[page]
		if !ok {
			http.Error(w, "missing page", http.StatusInternalServerError)
			return
		}
		list := make([]map[string]string, 0, len(links))
		for _, link := range links {
			list = append(list, map[string]string{"content": `<img src="` + link + `" />`})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "success",
			"data": map[string]any{"total_pages": total, "current_page": total, "list": list},
		})
	}))
	t.Cleanup(server.Close)
	old := apiBase
	apiBase = server.URL
	t.Cleanup(func() { apiBase = old })
}

func fixedRand(t *testing.T, value int) {
	t.Helper()
	old := randIntn
	randIntn = func(int) int { return value }
	t.Cleanup(func() { randIntn = old })
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestImageLinks(t *testing.T) {
	page := `<p>hi</p><img src="//img.example.com/a.jpg"><img data-src="https://img.example.com/b.jpg"/><img src="data:image/png;base64,xxx"><img src="/img/c.jpg">`
	got := imageLinks(page)
	want := []string{"https://img.example.com/a.jpg", "https://img.example.com/b.jpg", "https://jandan.net/img/c.jpg"}
	if len(got) != len(want) {
		t.Fatalf("links = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("links[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRandomUsesLibrary(t *testing.T) {
	fixedRand(t, 0)
	st := memState{}
	if err := sdk.SaveJSON(context.Background(), st, libraryKey, pictureLibrary{URLs: []string{"https://img.example.com/lib.jpg"}}); err != nil {
		t.Fatal(err)
	}
	got := replyAll(t, "u1", "来份屌图", st, nil)
	if len(got) != 2 || got[0].Kind != "keyboard" || got[1].Kind != "image" || got[1].URL != "https://img.example.com/lib.jpg" {
		t.Fatalf("replies = %+v", got)
	}
}

func TestRandomFallsBackToLatestPage(t *testing.T) {
	fixedRand(t, 0)
	serveAPI(t, map[int][]string{0: {"https://img.example.com/latest.jpg"}}, 1, nil)
	st := memState{}
	got := replyAll(t, "u1", "来份弔图", st, nil)
	if len(got) != 2 || got[1].Kind != "image" || got[1].URL != "https://img.example.com/latest.jpg" {
		t.Fatalf("replies = %+v", got)
	}
	var lib pictureLibrary
	if err := sdk.LoadJSON(context.Background(), st, libraryKey, &lib); err != nil || len(lib.URLs) != 1 {
		t.Fatalf("library = %+v, err = %v", lib, err)
	}
}

func TestUpdateCrawlsAllAccessiblePages(t *testing.T) {
	serveAPI(t, map[int][]string{
		0: {"https://img.example.com/p0.jpg"},
		2: {"https://img.example.com/p2.jpg"},
		1: {"https://img.example.com/p1.jpg"},
	}, 3, nil)
	admin := map[string]any{"admin_users": []string{"admin"}}
	st := memState{}
	got := replyAll(t, "admin", "更新吊图", st, admin)
	if len(got) != 2 || got[0].Text != "少女更新中..." || got[1].Text != "更新完成!" {
		t.Fatalf("replies = %+v", got)
	}
	var lib pictureLibrary
	if err := sdk.LoadJSON(context.Background(), st, libraryKey, &lib); err != nil {
		t.Fatal(err)
	}
	if len(lib.URLs) != 3 || lib.URLs[0] != "https://img.example.com/p0.jpg" {
		t.Fatalf("library = %+v", lib)
	}
}

func TestUpdateStopsAtLoginRequired(t *testing.T) {
	serveAPI(t, map[int][]string{0: {"https://img.example.com/p0.jpg"}}, 3, map[int]bool{2: true})
	admin := map[string]any{"admin_users": []string{"admin"}}
	st := memState{}
	got := replyAll(t, "admin", "更新屌图", st, admin)
	if len(got) != 2 || got[1].Text != "更新完成!" {
		t.Fatalf("replies = %+v", got)
	}
	var lib pictureLibrary
	_ = sdk.LoadJSON(context.Background(), st, libraryKey, &lib)
	if len(lib.URLs) != 1 {
		t.Fatalf("library = %+v", lib)
	}
}

func TestUpdateRequiresAdmin(t *testing.T) {
	got := replyAll(t, "u1", "更新屌图", memState{}, nil)
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有权限") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestUpdateReportsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL
	defer func() { apiBase = old }()

	admin := map[string]any{"admin_users": []string{"admin"}}
	got := replyAll(t, "admin", "更新屌图", memState{}, admin)
	if len(got) != 2 || !strings.Contains(got[1].Text, "ERROR:") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestNonMatch(t *testing.T) {
	got := replyAll(t, "u1", "来份屌图看看", memState{}, nil)
	if len(got) != 0 {
		t.Fatalf("unexpected replies %+v", got)
	}
}

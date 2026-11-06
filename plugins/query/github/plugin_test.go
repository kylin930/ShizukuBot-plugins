package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, message string) capture {
	t.Helper()
	var got capture
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		switch r.Kind {
		case "image":
			got.images = append(got.images, r.URL)
		default:
			got.texts = append(got.texts, r.Text)
		}
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func stub(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/repositories" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if q := r.URL.Query().Get("q"); q != "shizuku" {
			t.Errorf("q = %q", q)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		_, _ = w.Write([]byte(`{"total_count":1,"items":[{"full_name":"octocat/shizuku","description":"a bot","watchers":12,"forks":3,"open_issues":1,"language":"Go","license":{"key":"mit"},"pushed_at":"2024-01-01T00:00:00Z","html_url":"https://github.com/octocat/shizuku"}]}`))
	}))
	t.Cleanup(server.Close)
	oldURL, oldBase := searchURL, previewBase
	searchURL = server.URL + "/search/repositories"
	previewBase = "https://preview.example/"
	t.Cleanup(func() { searchURL, previewBase = oldURL, oldBase })
}

func TestSearchTextAndImage(t *testing.T) {
	stub(t)
	got := run(t, ">github shizuku")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "octocat/shizuku") ||
		!strings.Contains(got.texts[0], "Star/Fork/Issue: 12/3/1") {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 1 || got.images[0] != "https://preview.example/octocat/shizuku" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestImageOnly(t *testing.T) {
	stub(t)
	got := run(t, ">github -p shizuku")
	if len(got.texts) != 0 || len(got.images) != 1 {
		t.Fatalf("texts = %q, images = %q", got.texts, got.images)
	}
}

func TestTextOnly(t *testing.T) {
	stub(t)
	got := run(t, ">github -t shizuku")
	if len(got.texts) != 1 || len(got.images) != 0 {
		t.Fatalf("texts = %q, images = %q", got.texts, got.images)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "github shizuku"); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

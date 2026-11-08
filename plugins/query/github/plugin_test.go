package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"github"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

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
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		switch r.URL.Path {
		case "/search/repositories":
			if q := r.URL.Query().Get("q"); q != "shizuku" {
				t.Errorf("q = %q", q)
			}
			_, _ = w.Write([]byte(`{"total_count":1,"items":[{"full_name":"octocat/shizuku","description":"a bot","watchers":12,"forks":3,"open_issues":1,"language":"Go","license":{"key":"mit"},"pushed_at":"2024-01-01T00:00:00Z","html_url":"https://github.com/octocat/shizuku"}]}`))
		case "/repos/octocat/shizuku":
			_, _ = w.Write([]byte(`{"full_name":"octocat/shizuku","description":"a bot","stargazers_count":34,"forks":3,"open_issues":1,"language":"Go","license":{"key":"mit"},"pushed_at":"2024-01-01T00:00:00Z","html_url":"https://github.com/octocat/shizuku"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	oldURL, oldRepo, oldBase := searchURL, repoURL, previewBase
	searchURL = server.URL + "/search/repositories"
	repoURL = server.URL + "/repos/"
	previewBase = "https://preview.example/"
	t.Cleanup(func() { searchURL, repoURL, previewBase = oldURL, oldRepo, oldBase })
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

func TestRepoLink(t *testing.T) {
	stub(t)
	got := run(t, "推荐 https://github.com/octocat/shizuku 这个仓库")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "[octocat/shizuku](https://github.com/octocat/shizuku)") ||
		!strings.Contains(got.texts[0], "Star/Fork/Issue: 34/3/1") {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 1 || got.images[0] != "https://preview.example/octocat/shizuku" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestRepoLinkWithSuffix(t *testing.T) {
	stub(t)
	for _, message := range []string{
		"https://github.com/octocat/shizuku.git",
		"https://github.com/octocat/shizuku/issues/12",
		"看这个 https://www.github.com/octocat/shizuku。",
	} {
		if got := run(t, message); len(got.texts) != 1 || !strings.Contains(got.texts[0], "octocat/shizuku") {
			t.Fatalf("%q texts = %q", message, got.texts)
		}
	}
}

func TestIgnoresNonRepoLinks(t *testing.T) {
	stub(t)
	for _, message := range []string{
		"https://github.com/octocat",
		"https://github.com/topics/go",
		"https://example.com/octocat/shizuku",
	} {
		if got := run(t, message); len(got.texts) != 0 || len(got.images) != 0 {
			t.Fatalf("%q unexpected reply %q %q", message, got.texts, got.images)
		}
	}
}

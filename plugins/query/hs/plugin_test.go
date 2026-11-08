package hs

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
	for _, name := range []string{"hs"} {
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

func stub(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<script>var hash = "deadbeef";</script>`))
		case "/ajax.php":
			if r.URL.Query().Get("hash") != "deadbeef" {
				t.Errorf("hash = %q", r.URL.Query().Get("hash"))
			}
			if r.URL.Query().Get("search") == "" {
				t.Error("missing search")
			}
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	oldHome, oldAjax, oldBase := homeURL, ajaxURL, imageBase
	homeURL = server.URL + "/"
	ajaxURL = server.URL + "/ajax.php"
	imageBase = "https://img.example/hs/"
	t.Cleanup(func() { homeURL, ajaxURL, imageBase = oldHome, oldAjax, oldBase })
}

func TestSearchCards(t *testing.T) {
	stub(t, `{"list":[{"CardID":"CS2_029","auth_key":"k1"},{"CardID":"CS2_030","auth_key":"k2"}]}`)
	got := run(t, "搜卡火球")
	if len(got.images) != 2 ||
		got.images[0] != "https://img.example/hs/CS2_029.png?auth_key=k1" ||
		got.images[1] != "https://img.example/hs/CS2_030.png?auth_key=k2" {
		t.Fatalf("images = %q", got.images)
	}
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "https://fbigame.com/card?search=") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestDeckCodeImage(t *testing.T) {
	code := "AAE" + strings.Repeat("A", 70)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<script>var hash = "deadbeef";</script>`))
		case "/ajax.php":
			mods := r.URL.Query()["mod"]
			if len(mods) == 0 || mods[len(mods)-1] != "general_deck_image" {
				t.Errorf("mod = %q", mods)
			}
			if r.URL.Query().Get("deck_code") != code {
				t.Errorf("deck_code = %q", r.URL.Query().Get("deck_code"))
			}
			if r.URL.Query().Get("hash") != "deadbeef" {
				t.Errorf("hash = %q", r.URL.Query().Get("hash"))
			}
			_, _ = w.Write([]byte(`{"img":"iVBORw0KGgo="}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	oldHome, oldAjax := homeURL, ajaxURL
	homeURL = server.URL + "/"
	ajaxURL = server.URL + "/ajax.php"
	t.Cleanup(func() { homeURL, ajaxURL = oldHome, oldAjax })

	got := run(t, "卡组 "+code)
	if len(got.texts) != 0 {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 1 || !strings.HasPrefix(got.images[0], "data:image/png;base64,") {
		t.Fatalf("images = %q", got.images)
	}
}

func TestEmptyResult(t *testing.T) {
	stub(t, `{"list":[]}`)
	got := run(t, "搜卡不存在")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "查询为空") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "今天打炉石"); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

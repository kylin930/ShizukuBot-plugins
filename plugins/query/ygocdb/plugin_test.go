package ygocdb

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

func stub(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "青眼" {
			t.Errorf("search = %q", r.URL.Query().Get("search"))
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	oldURL, oldBase := searchURL, imageBase
	searchURL = server.URL + "/api/v0/?search="
	imageBase = "https://img.example/"
	t.Cleanup(func() { searchURL, imageBase = oldURL, oldBase })
}

const cardJSON = `{"result":[{"id":89631139,"cn_name":"青眼白龙","jp_name":"","en_name":"Blue-Eyes White Dragon","text":{"types":"龙族/通常","pdesc":"","desc":"以高攻击力著称的传说之龙。"}}]}`

func TestSearchText(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/yds 青眼")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "青眼白龙") ||
		!strings.Contains(got.texts[0], "卡片密码：89631139") ||
		!strings.Contains(got.texts[0], "Blue-Eyes White Dragon") {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 0 {
		t.Fatalf("images = %q", got.images)
	}
}

func TestSearchImage(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/ydp青眼")
	if len(got.images) != 1 || got.images[0] != "https://img.example/89631139.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestSearchBoth(t *testing.T) {
	stub(t, cardJSON)
	got := run(t, "/ydb 青眼")
	if len(got.images) != 1 || len(got.texts) != 1 {
		t.Fatalf("images = %q, texts = %q", got.images, got.texts)
	}
}

func TestEmptyKeyword(t *testing.T) {
	got := run(t, "/ydp")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "空手假象") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestNoResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	t.Cleanup(server.Close)
	old := searchURL
	searchURL = server.URL + "/api/v0/?search="
	t.Cleanup(func() { searchURL = old })

	got := run(t, "/yds 青眼")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "没有找到") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "青眼白龙"); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

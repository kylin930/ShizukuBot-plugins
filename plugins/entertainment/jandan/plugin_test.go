package jandan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"shizukubot/sdk"
)

const pageHTML = `<div class="comments">
<a class="view_img_link" href="//img.example.com/a.jpg">a</a>
<a href="//img.example.com/b.jpg" class="view_img_link other">b</a>
<a href="//img.example.com/c.jpg" class="other">c</a>
<a class="view_img_link" href="/img/d.jpg">d</a>
</div>`

func serve(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	old := pageURL
	pageURL = server.URL
	t.Cleanup(func() { pageURL = old })
}

func reply(t *testing.T, message string) (sdk.Reply, bool) {
	t.Helper()
	var got sdk.Reply
	replied := false
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestImageLinks(t *testing.T) {
	got := imageLinks(pageHTML)
	want := []string{
		"https://img.example.com/a.jpg",
		"https://img.example.com/b.jpg",
		"https://jandan.net/img/d.jpg",
	}
	if len(got) != len(want) {
		t.Fatalf("links = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("links[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRandomPicture(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(pageHTML))
	})
	got, replied := reply(t, "来份屌图")
	if !replied || got.Kind != "image" {
		t.Fatalf("reply = %+v, replied = %v", got, replied)
	}
	switch got.URL {
	case "https://img.example.com/a.jpg", "https://img.example.com/b.jpg", "https://jandan.net/img/d.jpg":
	default:
		t.Fatalf("unexpected image url %q", got.URL)
	}
	for _, trigger := range []string{"来份弔图", "来份吊图"} {
		if _, replied := reply(t, trigger); !replied {
			t.Fatalf("%s did not reply", trigger)
		}
	}
}

func TestUpstreamError(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if got, replied := reply(t, "来份屌图"); !replied || got.Kind != "text" || got.Text == "" {
		t.Fatalf("reply = %+v, replied = %v", got, replied)
	}
}

func TestNoPicture(t *testing.T) {
	serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>nothing</body></html>`))
	})
	if got, replied := reply(t, "来份屌图"); !replied || got.Kind != "text" || got.Text == "" {
		t.Fatalf("reply = %+v, replied = %v", got, replied)
	}
}

func TestNonMatch(t *testing.T) {
	if _, replied := reply(t, "来份屌图看看"); replied {
		t.Fatal("unexpected reply")
	}
}

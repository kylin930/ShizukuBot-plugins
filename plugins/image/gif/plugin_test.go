package gif

import (
	stdctx "context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type memState struct {
	data map[string]json.RawMessage
}

func (m *memState) Get(_ stdctx.Context, key string) (json.RawMessage, error) {
	return m.data[key], nil
}

func (m *memState) Set(_ stdctx.Context, key string, value json.RawMessage) error {
	m.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf strings.Builder
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return []byte(buf.String())
}

func setupEnv(t *testing.T) *httptest.Server {
	t.Helper()
	pic := pngBytes(t, 240)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pic)
	}))
	oldPath, oldBase, oldAvatar, oldClient := datapath, materialBase, avatarURL, httpClient
	dir := t.TempDir() + string(os.PathSeparator)
	datapath = dir
	_ = os.MkdirAll(filepath.Join(dir, "materials"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "users"), 0755)
	materialBase = srv.URL + "/materials/"
	avatarURL = srv.URL + "/avatar?nk="
	httpClient = srv.Client()
	t.Cleanup(func() {
		srv.Close()
		datapath, materialBase, avatarURL, httpClient = oldPath, oldBase, oldAvatar, oldClient
	})
	return srv
}

func run(t *testing.T, message string, raw string) []sdk.Reply {
	t.Helper()
	var replies []sdk.Reply
	c := sdk.Call{
		Event: sdk.Event{
			UserID:  "10001",
			GroupID: "20002",
			Message: message,
			Raw:     []byte(raw),
			Reply: func(_ stdctx.Context, r sdk.Reply) error {
				replies = append(replies, r)
				return nil
			},
		},
		State: &memState{data: map[string]json.RawMessage{}},
	}
	if err := Handle(stdctx.Background(), c); err != nil {
		t.Fatalf("handle %q: %v", message, err)
	}
	return replies
}

func TestMatchCommandUsesLongestPrefix(t *testing.T) {
	if name, rest, ok := matchCommand("2舔@1234"); !ok || name != "2舔" || rest != "@1234" {
		t.Fatalf("unexpected match: %q %q %v", name, rest, ok)
	}
	if name, _, ok := matchCommand("舔屏@1234"); !ok || name != "舔屏" {
		t.Fatalf("expected 舔屏, got %q", name)
	}
	if _, _, ok := matchCommand("你好世界"); ok {
		t.Fatal("unrelated text should not match")
	}
}

func TestHandleByMention(t *testing.T) {
	setupEnv(t)
	replies := run(t, `摸<qqbot-at-user id="12345678" />`, "")
	if len(replies) != 1 {
		t.Fatalf("want 1 reply, got %d", len(replies))
	}
	if replies[0].Kind != "image" || !strings.HasPrefix(replies[0].URL, "data:image/gif;base64,") {
		t.Fatalf("unexpected reply: %+v", replies[0])
	}
}

func TestHandleByImageAttachment(t *testing.T) {
	srv := setupEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"attachments": []map[string]any{{"url": srv.URL + "/input.png", "content_type": "image/png"}},
	})
	replies := run(t, "拍", string(raw))
	if len(replies) != 1 || replies[0].Kind != "image" {
		t.Fatalf("unexpected replies: %+v", replies)
	}
}

func TestHandleByTrailingQQ(t *testing.T) {
	setupEnv(t)
	replies := run(t, "丢12345678", "")
	if len(replies) != 1 || replies[0].Kind != "image" {
		t.Fatalf("unexpected replies: %+v", replies)
	}
}

func TestHandleWithoutTargetIsSilent(t *testing.T) {
	setupEnv(t)
	if replies := run(t, "摸", ""); len(replies) != 0 {
		t.Fatalf("want no reply, got %+v", replies)
	}
	if replies := run(t, "随便说点什么", ""); len(replies) != 0 {
		t.Fatalf("want no reply, got %+v", replies)
	}
}

func TestSendPictureForms(t *testing.T) {
	setupEnv(t)
	pic := pngBytes(t, 20)
	cases := map[string]string{
		"base64": "base64://" + base64.StdEncoding.EncodeToString(pic),
		"data":   "data:image/png;base64," + base64.StdEncoding.EncodeToString(pic),
		"url":    "https://example.com/a.gif",
	}
	path := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(path, pic, 0644); err != nil {
		t.Fatal(err)
	}
	cases["file"] = "file:///" + path
	for name, picURL := range cases {
		var got sdk.Reply
		c := sdk.Call{Event: sdk.Event{Reply: func(_ stdctx.Context, r sdk.Reply) error {
			got = r
			return nil
		}}}
		if err := sendPicture(stdctx.Background(), c, picURL); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Kind != "image" {
			t.Fatalf("%s: unexpected reply %+v", name, got)
		}
	}
}

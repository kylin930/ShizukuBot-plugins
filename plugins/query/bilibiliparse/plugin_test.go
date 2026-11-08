package bilibiliparse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bilibiliparse"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, group, message string) capture {
	t.Helper()
	var got capture
	c := sdk.Call{Event: sdk.Event{Message: message, GroupID: group, Reply: func(_ context.Context, r sdk.Reply) error {
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

// stub 启动一个覆盖视频/专栏/直播三个接口的假上游。
func stub(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		switch r.URL.Path {
		case "/x/web-interface/view":
			if r.URL.Query().Get("bvid") != "BV1xx411c7mD" && r.URL.Query().Get("aid") != "170001" {
				t.Errorf("view params = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"bvid":"BV1xx411c7mD","aid":170001,"title":"测试视频",
				"pic":"https://i0.hdslb.com/test.jpg","desc":"这是一个测试视频的简介","duration":754,
				"owner":{"name":"测试UP"},"stat":{"view":1234,"danmaku":56,"like":78}}}`))
		case "/x/article/viewinfo":
			if r.URL.Query().Get("id") != "17134450" {
				t.Errorf("article id = %q", r.URL.Query().Get("id"))
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":17134450,"title":"测试专栏","summary":"专栏摘要",
				"banner_url":"https://i0.hdslb.com/banner.jpg","author_name":"测试作者",
				"stats":{"view":4321,"like":12}}}`))
		case "/room/v1/Room/get_info":
			if r.URL.Query().Get("room_id") != "22603245" {
				t.Errorf("room id = %q", r.URL.Query().Get("room_id"))
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"room_id":22603245,"title":"测试直播间","uname":"主播甲",
				"user_cover":"https://i0.hdslb.com/cover.jpg","keyframe":"https://i0.hdslb.com/key.jpg",
				"live_status":1,"online":987}}`))
		case "/short/abc":
			w.Header().Set("Location", "https://www.bilibili.com/video/BV1xx411c7mD")
			w.WriteHeader(http.StatusFound)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	oldView, oldArt, oldRoom := viewURL, artURL, roomURL
	viewURL, artURL, roomURL = server.URL+"/x/web-interface/view", server.URL+"/x/article/viewinfo", server.URL+"/room/v1/Room/get_info"
	oldShort := shortRe
	shortRe = regexp.MustCompile(`(?:https?://)?` + regexp.QuoteMeta(strings.TrimPrefix(server.URL, "http://")) + `/short/[0-9A-Za-z]+`)
	lastParsed = map[string]time.Time{}
	t.Cleanup(func() {
		server.Close()
		viewURL, artURL, roomURL = oldView, oldArt, oldRoom
		shortRe = oldShort
		lastParsed = map[string]time.Time{}
	})
	return server
}

func TestVideoLink(t *testing.T) {
	stub(t)
	got := run(t, "g-video", "看看这个 https://www.bilibili.com/video/BV1xx411c7mD 不错")
	if len(got.texts) != 1 {
		t.Fatalf("texts = %q", got.texts)
	}
	for _, want := range []string{"[测试视频](https://www.bilibili.com/video/BV1xx411c7mD)", "UP：测试UP", "12:34", "播放 1234", "简介"} {
		if !strings.Contains(got.texts[0], want) {
			t.Errorf("reply missing %q:\n%s", want, got.texts[0])
		}
	}
	if len(got.images) != 1 || got.images[0] != "https://i0.hdslb.com/test.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestAVLink(t *testing.T) {
	stub(t)
	got := run(t, "g-av", "https://www.bilibili.com/video/av170001")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "测试视频") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestShortLink(t *testing.T) {
	server := stub(t)
	got := run(t, "g-short", "短链 "+server.URL+"/short/abc")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "测试视频") {
		t.Fatalf("short link parsed = %q", got.texts)
	}
	if len(got.images) != 1 {
		t.Fatalf("images = %q", got.images)
	}
}

func TestArticleLink(t *testing.T) {
	stub(t)
	got := run(t, "g-article", "https://www.bilibili.com/read/cv17134450")
	if len(got.texts) != 1 {
		t.Fatalf("texts = %q", got.texts)
	}
	for _, want := range []string{"[测试专栏](https://www.bilibili.com/read/cv17134450)", "作者：测试作者", "阅读 4321", "专栏摘要"} {
		if !strings.Contains(got.texts[0], want) {
			t.Errorf("reply missing %q:\n%s", want, got.texts[0])
		}
	}
	if len(got.images) != 1 || got.images[0] != "https://i0.hdslb.com/banner.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestLiveLink(t *testing.T) {
	stub(t)
	got := run(t, "g-live", "https://live.bilibili.com/22603245")
	if len(got.texts) != 1 {
		t.Fatalf("texts = %q", got.texts)
	}
	for _, want := range []string{"[测试直播间](https://live.bilibili.com/22603245)", "主播：主播甲", "直播中", "在线人气：987"} {
		if !strings.Contains(got.texts[0], want) {
			t.Errorf("reply missing %q:\n%s", want, got.texts[0])
		}
	}
	if len(got.images) != 1 || got.images[0] != "https://i0.hdslb.com/key.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestThrottle(t *testing.T) {
	stub(t)
	first := run(t, "g-throttle", "https://www.bilibili.com/video/BV1xx411c7mD")
	second := run(t, "g-throttle", "https://www.bilibili.com/video/BV1xx411c7mD")
	if len(first.texts) != 1 {
		t.Fatalf("first texts = %q", first.texts)
	}
	if len(second.texts) != 0 || len(second.images) != 0 {
		t.Fatalf("second should be throttled: %q %q", second.texts, second.images)
	}
}

func TestIgnoresOtherLinks(t *testing.T) {
	stub(t)
	for _, msg := range []string{"随便聊聊", "https://example.com/video/BV1xx411c7mD", "https://www.bilibili.com/"} {
		if got := run(t, "g-quiet", msg); len(got.texts) != 0 || len(got.images) != 0 {
			t.Fatalf("%q unexpected reply %q %q", msg, got.texts, got.images)
		}
	}
}

func TestResolveRelativeLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/video/BV1xx411c7mD")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	t.Cleanup(server.Close)

	got, err := resolve(context.Background(), server.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	if want := server.URL + "/video/BV1xx411c7mD"; got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

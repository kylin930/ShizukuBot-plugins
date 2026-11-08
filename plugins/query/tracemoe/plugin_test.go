package tracemoe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	for _, name := range []string{"tracemoe"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, message, raw string) capture {
	t.Helper()
	var got capture
	event := sdk.Event{Message: message}
	if raw != "" {
		event.Raw = json.RawMessage(raw)
	}
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		switch r.Kind {
		case "image":
			got.images = append(got.images, r.URL)
		default:
			got.texts = append(got.texts, r.Text)
		}
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	return got
}

type stubServer struct {
	calls int
	query url.Values
}

func stub(t *testing.T, status int, body string) *stubServer {
	t.Helper()
	return stubFor(t, status, func(string) string { return body })
}

// stubFor 按查询的图片地址返回不同响应，用于多图测试。
func stubFor(t *testing.T, status int, bodyFor func(imageURL string) string) *stubServer {
	t.Helper()
	s := &stubServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		s.query = r.URL.Query()
		if r.URL.Path != "/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		imageURL := r.URL.Query().Get("url")
		if imageURL == "" {
			t.Error("missing url")
		}
		if _, ok := r.URL.Query()["anilistInfo"]; !ok {
			t.Error("missing anilistInfo")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodyFor(imageURL)))
	}))
	t.Cleanup(server.Close)
	old := searchURL
	searchURL = server.URL + "/search"
	t.Cleanup(func() { searchURL = old })
	return s
}

const imageAttachment = `{"attachments":[{"url":"https://img.example/shot.jpg","content_type":"image/jpeg"}]}`

func TestNoImage(t *testing.T) {
	s := stub(t, http.StatusOK, `{"result":[]}`)
	got := run(t, "以图搜番", "")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "用法") {
		t.Fatalf("texts = %q", got.texts)
	}
	if s.calls != 0 {
		t.Fatalf("calls = %d", s.calls)
	}
}

func TestSearchAttachment(t *testing.T) {
	s := stub(t, http.StatusOK, `{
		"frameCount": 100,
		"error": "",
		"result": [{
			"anilist": {
				"title": {"native":"進撃の巨人","romaji":"Shingeki no Kyojin","english":"Attack on Titan"},
				"synonyms": ["进击的巨人"],
				"isAdult": false
			},
			"episode": 1,
			"from": 72.5,
			"to": 75.2,
			"similarity": 0.973,
			"image": "https://api.trace.moe/preview/1.jpg",
			"filename": "Shingeki no Kyojin - 01.mkv"
		}]
	}`)
	got := run(t, "搜番", imageAttachment)
	if s.query.Get("url") != "https://img.example/shot.jpg" {
		t.Fatalf("url = %q", s.query.Get("url"))
	}
	if len(got.texts) != 2 || !strings.Contains(got.texts[0], "少女祈祷中") {
		t.Fatalf("texts = %q", got.texts)
	}
	text := got.texts[1]
	for _, want := range []string{"我有把握是这个", "进击的巨人", "進撃の巨人", "Attack on Titan", "话数：1", "01:12 - 01:15", "97.3%", "成人内容：否", "文件名：Shingeki no Kyojin - 01.mkv"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want %q", text, want)
		}
	}
	if len(got.images) != 1 || got.images[0] != "https://api.trace.moe/preview/1.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

// TestSearchMultipleImages 断言消息里的每张图片都会被识别并各自回复预览图。
func TestSearchMultipleImages(t *testing.T) {
	s := stubFor(t, http.StatusOK, func(imageURL string) string {
		switch imageURL {
		case "https://img.example/one.jpg":
			return `{"result":[{
				"anilist":{"title":{"native":"作品A","romaji":"Sakuhin A","english":"Work A"},"isAdult":true},
				"episode":2,"from":60,"to":61.5,"similarity":0.9,
				"image":"https://api.trace.moe/preview/a.jpg","filename":"a.mkv"
			}]}`
		case "https://img.example/two.jpg":
			return `{"result":[{
				"anilist":{"title":{"native":"作品B","romaji":"Sakuhin B","english":"Work B"},"isAdult":false},
				"episode":"4","from":5,"to":6,"similarity":50,
				"image":"http://cdn.example/b.jpg"
			}]}`
		default:
			return `{"result":[]}`
		}
	})
	raw := `{"attachments":[{"url":"https://img.example/one.jpg"},{"url":"https://img.example/two.jpg"}]}`
	got := run(t, "以图搜番", raw)
	if s.calls != 2 {
		t.Fatalf("calls = %d", s.calls)
	}
	if len(got.texts) != 3 || !strings.Contains(got.texts[0], "少女祈祷中") {
		t.Fatalf("texts = %q", got.texts)
	}
	first, second := got.texts[1], got.texts[2]
	for _, want := range []string{"作品A", "Sakuhin A", "Work A", "话数：2", "01:00 - 01:01", "90.0%", "成人内容：是", "文件名：a.mkv"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first = %q, want %q", first, want)
		}
	}
	for _, want := range []string{"作品B", "Sakuhin B", "Work B", "话数：4", "00:05 - 00:06", "50.0%", "成人内容：否"} {
		if !strings.Contains(second, want) {
			t.Fatalf("second = %q, want %q", second, want)
		}
	}
	if len(got.images) != 2 || got.images[0] != "https://api.trace.moe/preview/a.jpg" || got.images[1] != "http://cdn.example/b.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestSearchTextURL(t *testing.T) {
	s := stub(t, http.StatusOK, `{"result":[{"anilist":{"title":{"native":"作品"}},"episode":"3","from":5,"to":6,"similarity":60,"image":"http://cdn.example/no.jpg"}]}`)
	got := run(t, "以图搜番 https://img.example/direct.jpg", "")
	if s.query.Get("url") != "https://img.example/direct.jpg" {
		t.Fatalf("url = %q", s.query.Get("url"))
	}
	text := strings.Join(got.texts, "\n")
	if !strings.Contains(text, "大概是这个") || !strings.Contains(text, "话数：3") || !strings.Contains(text, "60.0%") {
		t.Fatalf("text = %q", text)
	}
	// 预览图不再限制 https，http 外链同样发送。
	if len(got.images) != 1 || got.images[0] != "http://cdn.example/no.jpg" {
		t.Fatalf("images = %q", got.images)
	}
}

func TestKeywordInSentence(t *testing.T) {
	s := stub(t, http.StatusOK, `{"result":[{"anilist":{"title":{"native":"作品"}},"episode":1,"from":5,"to":6,"similarity":90,"image":"https://api.trace.moe/preview/x.jpg"}]}`)
	got := run(t, "帮我看看这张截图 搜番 是哪部", imageAttachment)
	if s.calls != 1 || s.query.Get("url") != "https://img.example/shot.jpg" {
		t.Fatalf("calls = %d, url = %q", s.calls, s.query.Get("url"))
	}
	text := strings.Join(got.texts, "\n")
	if !strings.Contains(text, "我有把握是这个") || !strings.Contains(text, "话数：1") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestKeywordInSentenceURL(t *testing.T) {
	s := stub(t, http.StatusOK, `{"result":[{"anilist":{"title":{"native":"作品"}},"episode":"2","from":1,"to":2,"similarity":90}]}`)
	got := run(t, "用 以图搜番 看看 https://img.example/inline.jpg 这张", "")
	if s.query.Get("url") != "https://img.example/inline.jpg" {
		t.Fatalf("url = %q", s.query.Get("url"))
	}
	if len(got.texts) != 2 {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestEmptyResult(t *testing.T) {
	stub(t, http.StatusOK, `{"error":"","result":[]}`)
	got := run(t, "搜索番剧", imageAttachment)
	if len(got.texts) != 2 || !strings.Contains(got.texts[1], "没有找到") {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 0 {
		t.Fatalf("images = %q", got.images)
	}
}

func TestUpstreamError(t *testing.T) {
	stub(t, http.StatusInternalServerError, "boom")
	got := run(t, "以图搜番", imageAttachment)
	if len(got.texts) != 2 || !strings.Contains(got.texts[1], "搜番失败") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestAPIErrorField(t *testing.T) {
	stub(t, http.StatusOK, `{"error":"rate limit exceeded","result":[]}`)
	got := run(t, "以图搜番", imageAttachment)
	if len(got.texts) != 2 || !strings.Contains(got.texts[1], "rate limit exceeded") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	s := stub(t, http.StatusOK, `{"result":[]}`)
	if got := run(t, "今天搜番真开心", ""); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
	if s.calls != 0 {
		t.Fatalf("calls = %d", s.calls)
	}
}

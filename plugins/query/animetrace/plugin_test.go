package animetrace

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
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
	for _, name := range []string{"animetrace"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

// testPNG 是一张真实可解码的 100×100 纯色 PNG。
var testPNG = mustSolidPNG(100, 100)

func mustSolidPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 60, B: 60, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

const okJSON = `{"code":0,"zh_message":"","data":[{"box":[0.1,0.1,0.5,0.5],
	"character":[{"work":"测试作品","character":"测试角色","confidence":0.95,"time":"12:34"}]}]}`

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, message string, raw json.RawMessage) capture {
	t.Helper()
	var got capture
	c := sdk.Call{Event: sdk.Event{Message: message, Raw: raw, Reply: func(_ context.Context, r sdk.Reply) error {
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

// stub 启动一个同时提供图片下载与识别接口的假上游。
func stub(t *testing.T, apiStatus int, apiBody, wantModel string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG)
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.FormValue("model"); got != wantModel {
			t.Errorf("model = %q, want %q", got, wantModel)
		}
		if got := r.FormValue("is_multi"); got != "0" {
			t.Errorf("is_multi = %q", got)
		}
		if got := r.FormValue("ai_detect"); got != "0" {
			t.Errorf("ai_detect = %q", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file field: %v", err)
		} else {
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if len(data) == 0 {
				t.Error("empty image upload")
			}
		}
		w.WriteHeader(apiStatus)
		_, _ = w.Write([]byte(apiBody))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	old := searchURL
	searchURL = server.URL + "/v1/search"
	t.Cleanup(func() { searchURL = old })
	return server
}

func TestUsageWithoutImage(t *testing.T) {
	got := run(t, "动漫识图", nil)
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "用法") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestRecognizeAttachment(t *testing.T) {
	server := stub(t, http.StatusOK, okJSON, "anime_model_lovelive")
	raw := json.RawMessage(`{"attachments":[{"url":"` + server.URL + `/image","content_type":"image/png"}]}`)
	got := run(t, "动漫识图", raw)
	if len(got.texts) != 2 {
		t.Fatalf("texts = %q", got.texts)
	}
	if !strings.Contains(got.texts[0], "少女祈祷中") || !strings.Contains(got.texts[0], "共识别到 1 个角色") {
		t.Fatalf("header = %q", got.texts[0])
	}
	text := got.texts[1]
	for _, want := range []string{"《测试作品》", "测试角色", "相似度 95.0%", "时间点 12:34"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reply %q missing %q", text, want)
		}
	}
}

// TestCroppedImageReply 断言按 box 裁剪后会收到图片回复，而不只是文本。
func TestCroppedImageReply(t *testing.T) {
	server := stub(t, http.StatusOK, okJSON, "anime_model_lovelive")
	raw := json.RawMessage(`{"attachments":[{"url":"` + server.URL + `/image","content_type":"image/png"}]}`)
	got := run(t, "动漫识图", raw)
	if len(got.images) != 1 {
		t.Fatalf("images = %q", got.images)
	}
	if !strings.HasPrefix(got.images[0], "data:image/jpeg;base64,") {
		t.Fatalf("image = %.40q", got.images[0])
	}
}

// TestMissingBoxFallsBackToOriginal 断言 box 缺失时退回原图而不整体失败。
func TestMissingBoxFallsBackToOriginal(t *testing.T) {
	body := `{"code":0,"data":[{"character":[{"work":"作品A","character":"角色A","confidence":0.8}]}]}`
	server := stub(t, http.StatusOK, body, "anime_model_lovelive")
	raw := json.RawMessage(`{"attachments":[{"url":"` + server.URL + `/image","content_type":"image/png"}]}`)
	got := run(t, "动漫识图", raw)
	if len(got.images) != 1 || !strings.HasPrefix(got.images[0], "data:image/png;base64,") {
		t.Fatalf("images = %q", got.images)
	}
	if len(got.texts) != 2 || !strings.Contains(got.texts[1], "角色A") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestRecognizeTextURLAndModel(t *testing.T) {
	server := stub(t, http.StatusOK, okJSON, "pre_stable")
	got := run(t, "动漫识图 2 "+server.URL+"/image", nil)
	if len(got.texts) != 2 || !strings.Contains(strings.Join(got.texts, "\n"), "《测试作品》") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestRecognizeGalModel(t *testing.T) {
	server := stub(t, http.StatusOK, okJSON, "full_game_model_kira")
	got := run(t, "Gal识图 "+server.URL+"/image", nil)
	if len(got.texts) != 2 || !strings.Contains(strings.Join(got.texts, "\n"), "测试角色") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestUpstreamError(t *testing.T) {
	server := stub(t, http.StatusOK, `{"code":1001,"zh_message":"模型不存在"}`, "custom_model")
	got := run(t, "动漫识图 custom_model "+server.URL+"/image", nil)
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "模型不存在") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestUpstreamStatusError(t *testing.T) {
	server := stub(t, http.StatusInternalServerError, "boom", "anime_model_lovelive")
	got := run(t, "动漫识图 "+server.URL+"/image", nil)
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "识别请求失败") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestImageDownloadError(t *testing.T) {
	server := stub(t, http.StatusOK, okJSON, "anime_model_lovelive")
	got := run(t, "动漫识图 "+server.URL+"/missing", nil)
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "下载图片失败") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "今天天气不错", nil); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

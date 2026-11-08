package realcugan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

// makePNG 生成一张指定尺寸的 PNG，用于图片下载与推理响应。
func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// stubServer 同时提供图片下载与推理接口。
type stubServer struct {
	requests []predictRequest
	image    []byte
}

// setup 启动测试服务器并把 predictURL 指向它。
func setup(t *testing.T) (*stubServer, string) {
	t.Helper()
	s := &stubServer{image: makePNG(t, 4, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("/image.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(s.image)
	})
	mux.HandleFunc("/run/predict", func(w http.ResponseWriter, r *http.Request) {
		var req predictRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		s.requests = append(s.requests, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(s.image)},
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	old := predictURL
	predictURL = ts.URL + "/run/predict"
	t.Cleanup(func() { predictURL = old })
	return s, ts.URL + "/image.png"
}

// run 执行一条带附件的消息并收集回复。
func run(t *testing.T, message string, imageURLs ...string) []sdk.Reply {
	t.Helper()
	attachments := make([]map[string]string, 0, len(imageURLs))
	for _, u := range imageURLs {
		attachments = append(attachments, map[string]string{"url": u, "content_type": "image/png"})
	}
	raw, err := json.Marshal(map[string]any{"attachments": attachments})
	if err != nil {
		t.Fatalf("marshal attachments: %v", err)
	}
	var got []sdk.Reply
	c := sdk.Call{Event: sdk.Event{
		Message: message,
		GroupID: "g1",
		UserID:  "u1",
		Raw:     raw,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("handle %q: %v", message, err)
	}
	return got
}

func TestChooseParams(t *testing.T) {
	cases := []struct {
		name      string
		fashu     string
		width     int
		height    int
		wantScale int
		wantCon   string
		wantModel string
	}{
		{"default", "清晰术", 1000, 1000, 2, "conservative", "up2x-latest-conservative.pth"},
		{"double", "清晰术双重吟唱", 10, 10, 2, "conservative", "up2x-latest-conservative.pth"},
		{"triple small", "清晰术三重吟唱", 100, 100, 3, "conservative", "up3x-latest-conservative.pth"},
		{"quad small", "清晰术四重吟唱", 100, 100, 4, "conservative", "up4x-latest-conservative.pth"},
		{"triple too big", "清晰术三重吟唱", 1000, 1000, 2, "conservative", "up2x-latest-conservative.pth"},
		{"quad too big", "清晰术四重吟唱", 1000, 1000, 2, "conservative", "up2x-latest-conservative.pth"},
		{"boundary", "清晰术四重吟唱", 632, 633, 2, "conservative", "up2x-latest-conservative.pth"},
		{"strong", "清晰术四重吟唱强力术式", 100, 100, 4, "denoise3x", "up4x-latest-denoise3x.pth"},
		{"medium", "清晰术中等术式", 100, 100, 2, "denoise2x", "up2x-latest-denoise2x.pth"},
		{"medium scaled", "清晰术三重吟唱中等术式", 100, 100, 3, "no-denoise", "up3x-latest-no-denoise.pth"},
		{"weak", "清晰术弱术式", 100, 100, 2, "denoise1x", "up2x-latest-denoise1x.pth"},
		{"weak scaled", "清晰术四重吟唱弱术式", 100, 100, 4, "no-denoise", "up4x-latest-no-denoise.pth"},
		{"invariant", "清晰术不变式", 100, 100, 2, "no-denoise", "up2x-latest-no-denoise.pth"},
		{"original", "清晰术原式", 100, 100, 2, "conservative", "up2x-latest-conservative.pth"},
		{"invariant wins", "清晰术不变式原式", 100, 100, 2, "no-denoise", "up2x-latest-no-denoise.pth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scale, con := chooseParams(tc.fashu, tc.width, tc.height)
			if scale != tc.wantScale || con != tc.wantCon {
				t.Fatalf("chooseParams = (%d, %q), want (%d, %q)", scale, con, tc.wantScale, tc.wantCon)
			}
			if got := modelName(scale, con); got != tc.wantModel {
				t.Fatalf("modelName = %q, want %q", got, tc.wantModel)
			}
		})
	}
}

func TestPredictSuccess(t *testing.T) {
	s, imageURL := setup(t)
	got := run(t, "清晰术双重吟唱强力术式", imageURL)
	if len(got) != 3 {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "text" || got[0].Text != "少女祈祷中..." {
		t.Fatalf("first reply = %+v", got[0])
	}
	if got[1].Kind != "text" || got[1].Text != "2重唱denoise3x分支大清晰术!" {
		t.Fatalf("caption reply = %+v", got[1])
	}
	if got[2].Kind != "image" || !strings.HasPrefix(got[2].URL, "data:image/png;base64,") {
		t.Fatalf("image reply = %+v", got[2])
	}

	if len(s.requests) != 1 {
		t.Fatalf("requests = %d", len(s.requests))
	}
	req := s.requests[0]
	if len(req.Data) != 3 {
		t.Fatalf("data = %+v", req.Data)
	}
	encoded, ok := req.Data[0].(string)
	if !ok || !strings.HasPrefix(encoded, "data:image/jpeg;base64,") {
		t.Fatalf("image payload = %v", req.Data[0])
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "data:image/jpeg;base64,")); err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if req.Data[1] != "up2x-latest-denoise3x.pth" {
		t.Fatalf("model = %v", req.Data[1])
	}
	if req.Data[2] != float64(2) {
		t.Fatalf("scale arg = %v", req.Data[2])
	}
}

func TestPredictMultipleImages(t *testing.T) {
	s, imageURL := setup(t)
	got := run(t, "清晰术", imageURL, imageURL)
	if len(got) != 5 {
		t.Fatalf("replies = %+v", got)
	}
	if len(s.requests) != 2 || len(s.requests[0].Data) != 3 {
		t.Fatalf("requests = %+v", s.requests)
	}
	for _, i := range []int{1, 3} {
		if got[i].Kind != "text" || !strings.Contains(got[i].Text, "分支大清晰术!") {
			t.Fatalf("caption = %+v", got[i])
		}
		if got[i+1].Kind != "image" {
			t.Fatalf("reply = %+v", got[i+1])
		}
	}
}

func TestPredictHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/image.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(makePNG(t, 4, 4))
	})
	mux.HandleFunc("/run/predict", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	old := predictURL
	predictURL = ts.URL + "/run/predict"
	t.Cleanup(func() { predictURL = old })

	got := run(t, "清晰术", ts.URL+"/image.png")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "text" || got[0].Text != "少女祈祷中..." {
		t.Fatalf("first reply = %+v", got[0])
	}
	if got[1].Kind != "text" || !strings.Contains(got[1].Text, "ERROR:") ||
		!strings.Contains(got[1].Text, "500") {
		t.Fatalf("error reply = %+v", got[1])
	}
}

func TestDownloadError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	t.Cleanup(ts.Close)
	got := run(t, "清晰术", ts.URL+"/missing.png")
	if len(got) != 2 || !strings.Contains(got[1].Text, "ERROR:") || !strings.Contains(got[1].Text, "404") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestNoImageIgnored(t *testing.T) {
	got := run(t, "清晰术")
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

func TestOtherTextIgnored(t *testing.T) {
	_, imageURL := setup(t)
	got := run(t, "今天来一发清晰术", imageURL)
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

func TestInvalidImageData(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/image.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "not an image")
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	got := run(t, "清晰术", ts.URL+"/image.png")
	if len(got) != 2 || !strings.Contains(got[1].Text, "ERROR:") {
		t.Fatalf("replies = %+v", got)
	}
}

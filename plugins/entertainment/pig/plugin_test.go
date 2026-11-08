package pig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// memState 是内存版 sdk.State。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

// resetCache 清空包级数据缓存。
func resetCache() {
	pigMu.Lock()
	pigCache = nil
	pigMap = make(map[string]*pigImage)
	lastFetch = time.Time{}
	pigMu.Unlock()
}

// useServer 启动测试服务器并指向它，返回图片字节与请求计数。
func useServer(t *testing.T, images []pigImage, handler func(w http.ResponseWriter, r *http.Request) bool) ([]byte, *int) {
	t.Helper()
	resetCache()
	pngData, err := encodePNG()
	if err != nil {
		t.Fatalf("encode png: %v", err)
	}
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if handler != nil && handler(w, r) {
			return
		}
		if r.URL.Path == "/Pig/pig_data.json" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pigResponse{Total: len(images), Images: images})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/Pig/assets/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngData)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	oldData, oldAsset := pigDataURL, pigAssetBase
	pigDataURL = ts.URL + "/Pig/pig_data.json"
	pigAssetBase = ts.URL + "/Pig/assets/"
	t.Cleanup(func() {
		pigDataURL, pigAssetBase = oldData, oldAsset
		resetCache()
	})
	return pngData, &requests
}

// encodePNG 生成一张 1x1 的 PNG。
func encodePNG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0xFF, A: 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// call 执行一次事件并收集回复。
func call(t *testing.T, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{State: memState{}, Event: sdk.Event{
		UserID:  "u1",
		GroupID: "g1",
		Message: message,
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

func samplePigs() []pigImage {
	return []pigImage{
		{ID: "1", Title: "开心猪", Filename: "1.png"},
		{ID: "2", Title: "伤心猪", Filename: "2.png"},
		{ID: "3", Title: "吃肉猪", Filename: "3.png"},
	}
}

func TestRandom(t *testing.T) {
	useServer(t, samplePigs(), nil)
	oldRand := randIntn
	randIntn = func(int) int { return 0 }
	t.Cleanup(func() { randIntn = oldRand })

	for _, cmd := range []string{"随机猪猪", "来份猪猪", "抽个猪猪"} {
		got := call(t, cmd)
		if len(got) != 2 {
			t.Fatalf("%s replies = %+v", cmd, got)
		}
		if got[0].Text != "🐷 ID: 1 | 开心猪" {
			t.Fatalf("%s text = %q", cmd, got[0].Text)
		}
		if got[1].Kind != "image" || !strings.HasPrefix(got[1].URL, "data:image/png;base64,") {
			t.Fatalf("%s image = %+v", cmd, got[1])
		}
	}
}

func TestSearchHit(t *testing.T) {
	useServer(t, samplePigs(), nil)

	got := call(t, "搜索猪猪 心")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	text := got[0].Text
	for _, want := range []string{"找到 2 只猪猪", "1: 开心猪 (ID: 1)", "2: 伤心猪 (ID: 2)", "为您返回第一个猪猪"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q missing %q", text, want)
		}
	}
	if got[1].Kind != "image" {
		t.Fatalf("image reply = %+v", got[1])
	}
}

func TestSearchTruncate(t *testing.T) {
	var images []pigImage
	for i := 1; i <= 12; i++ {
		images = append(images, pigImage{
			ID:       fmt.Sprintf("%d", i),
			Title:    fmt.Sprintf("猪猪%02d", i),
			Filename: fmt.Sprintf("%d.png", i),
		})
	}
	useServer(t, images, nil)

	got := call(t, "搜索猪猪 猪猪")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	text := got[0].Text
	if !strings.Contains(text, "找到 12 只猪猪") || !strings.Contains(text, "...等共 12 条结果") {
		t.Fatalf("text = %q", text)
	}
	if !strings.Contains(text, "10: 猪猪10 (ID: 10)") || strings.Contains(text, "11: 猪猪11") {
		t.Fatalf("text should list only first 10: %q", text)
	}
}

func TestSearchMiss(t *testing.T) {
	useServer(t, samplePigs(), nil)

	got := call(t, "搜索猪猪 狗狗")
	if len(got) != 1 || !strings.Contains(got[0].Text, "未找到包含“狗狗”的猪猪") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestIDHit(t *testing.T) {
	useServer(t, samplePigs(), nil)

	got := call(t, "猪猪id 2")
	if len(got) != 2 || got[0].Text != "🐷 ID: 2 | 伤心猪" || got[1].Kind != "image" {
		t.Fatalf("replies = %+v", got)
	}
}

func TestIDMiss(t *testing.T) {
	useServer(t, samplePigs(), nil)

	got := call(t, "猪猪id 99")
	if len(got) != 1 || !strings.Contains(got[0].Text, "未找到 ID 为 99 的猪猪") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestDataParseFailureRetry(t *testing.T) {
	first := true
	_, requests := useServer(t, samplePigs(), func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/Pig/pig_data.json" {
			return false
		}
		if first {
			first = false
			_, _ = w.Write([]byte("not json"))
			return true
		}
		return false
	})

	got := call(t, "随机猪猪")
	if len(got) != 1 || !strings.Contains(got[0].Text, "解析JSON失败") {
		t.Fatalf("first replies = %+v", got)
	}

	oldRand := randIntn
	randIntn = func(int) int { return 0 }
	t.Cleanup(func() { randIntn = oldRand })
	got = call(t, "随机猪猪")
	if len(got) != 2 || got[0].Text != "🐷 ID: 1 | 开心猪" {
		t.Fatalf("retry replies = %+v", got)
	}
	if *requests < 2 {
		t.Fatalf("requests = %d, want retry", *requests)
	}
}

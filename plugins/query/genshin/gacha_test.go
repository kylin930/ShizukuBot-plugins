package genshin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"shizukubot/sdk"
)

// memState 是测试用的内存状态存储。
type memState struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

func newMemState() *memState {
	return &memState{data: map[string]json.RawMessage{}}
}

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[key], nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

// jpegStub 具备 JPEG 文件头，可被 http.DetectContentType 识别。
var jpegStub = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00}

// stubGacha 固定随机数与渲染结果，并重置累计抽卡次数。
func stubGacha(t *testing.T, rendered *string) {
	t.Helper()
	oldRand, oldRender, oldProvider := randIntn, renderHTML, assetProvider
	oldTotal := atomic.LoadUint64(&totalPulls)
	assetProvider = func(context.Context) (*gachaAssets, error) {
		return nil, errors.New("测试环境下不下载素材")
	}
	randIntn = func(n int) int { return 0 }
	renderHTML = func(_ context.Context, html []byte, _ sdk.RenderOptions) ([]byte, error) {
		if rendered != nil {
			*rendered = string(html)
		}
		return jpegStub, nil
	}
	atomic.StoreUint64(&totalPulls, 0)
	t.Cleanup(func() {
		randIntn, renderHTML, assetProvider = oldRand, oldRender, oldProvider
		atomic.StoreUint64(&totalPulls, oldTotal)
	})
}

func gachaCall(t *testing.T, state sdk.State, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{State: state, Event: sdk.Event{
		GroupID: "g1",
		UserID:  "u1",
		Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGachaTen(t *testing.T) {
	var html string
	stubGacha(t, &html)
	got := gachaCall(t, newMemState(), "原神十连")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "十连") {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[1].Kind != "image" || !strings.HasPrefix(got[1].URL, "data:image/jpeg") {
		t.Fatalf("image reply = %+v", got[1])
	}
	if n := strings.Count(html, `class="card`); n != 10 {
		t.Fatalf("cards = %d，HTML = %s", n, html)
	}
}

func TestGachaFiveStarPool(t *testing.T) {
	var html string
	stubGacha(t, &html)
	state := newMemState()
	if got := gachaCall(t, state, "切换原神卡池"); got[0].Text != "切换到五星卡池~" {
		t.Fatalf("reply = %q", got[0].Text)
	}
	atomic.StoreUint64(&totalPulls, 1) // 避免下一抽触发累计保底打乱全五星断言
	got := gachaCall(t, state, "原神十连")
	if !strings.Contains(got[0].Text, "五星") {
		t.Fatalf("text = %q", got[0].Text)
	}
	if n := strings.Count(html, `class="card s5"`); n != 10 {
		t.Fatalf("五星卡片 = %d，HTML = %s", n, html)
	}
	if got := gachaCall(t, state, "切换原神卡池"); got[0].Text != "切换到普通卡池~" {
		t.Fatalf("reply = %q", got[0].Text)
	}
}

func TestGachaPity(t *testing.T) {
	stubGacha(t, nil)
	atomic.StoreUint64(&totalPulls, 8) // 下一次累计到 9，触发五星保底
	items := drawTen(false)
	found := false
	for _, item := range items {
		if item.Stars == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("第 9 抽未触发五星保底：%+v", items)
	}
	if len(items) != 10 {
		t.Fatalf("items = %d", len(items))
	}
}

// newTestAssets 用极小的合成图片搭一个与上游同结构的素材包。
func newTestAssets(t *testing.T) *gachaAssets {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, encode func(*bytes.Buffer) error) {
		var b bytes.Buffer
		if err := encode(&b); err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	pngImage := func(name string) {
		add(name, func(b *bytes.Buffer) error {
			return png.Encode(b, image.NewRGBA(image.Rect(0, 0, 4, 4)))
		})
	}
	jpgImage := func(name string) {
		add(name, func(b *bytes.Buffer) error {
			return jpeg.Encode(b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
		})
	}
	for _, name := range []string{"bg0.jpg", "five_bg.jpg", "four_bg.jpg", "three_bg.jpg"} {
		jpgImage("Genshin/" + name)
	}
	for _, name := range []string{"Reply.png", "anemo.png", "sword.png",
		"gacha/ThreeStar.png", "gacha/FourStar.png", "gacha/FiveStar.png",
		"five/anemo_测试五星.png", "four/anemo_测试四星.png",
		"five2/sword_测试五星武器.png", "four2/sword_测试四星武器.png",
		"Three/sword_测试三星.png"} {
		pngImage("Genshin/" + name)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "Genshin.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zr.Close() })
	assets, err := newGachaAssets(zr)
	if err != nil {
		t.Fatal(err)
	}
	return assets
}

func TestGachaAssetsRender(t *testing.T) {
	assets := newTestAssets(t)
	oldProvider, oldRand := assetProvider, randIntn
	oldTotal := atomic.LoadUint64(&totalPulls)
	assetProvider = func(context.Context) (*gachaAssets, error) { return assets, nil }
	randIntn = func(int) int { return 0 }
	atomic.StoreUint64(&totalPulls, 1) // 避开下一次累计保底
	t.Cleanup(func() {
		assetProvider, randIntn = oldProvider, oldRand
		atomic.StoreUint64(&totalPulls, oldTotal)
	})

	state := newMemState()
	if got := gachaCall(t, state, "切换原神卡池"); got[0].Text != "切换到五星卡池~" {
		t.Fatalf("reply = %q", got[0].Text)
	}
	got := gachaCall(t, state, "原神十连")
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "测试五星") || !strings.Contains(got[0].Text, "五星角色") {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[1].Kind != "image" || !strings.HasPrefix(got[1].URL, "data:image/jpeg") {
		t.Fatalf("image reply = %+v", got[1])
	}
}

func TestGachaRenderFallback(t *testing.T) {
	oldRender, oldProvider := renderHTML, assetProvider
	assetProvider = func(context.Context) (*gachaAssets, error) {
		return nil, errors.New("测试环境下不下载素材")
	}
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML, assetProvider = oldRender, oldProvider })

	got := gachaCall(t, newMemState(), "原神十连")
	if len(got) != 1 {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "生成抽卡结果失败") || strings.Count(got[0].Text, "（") != 10 {
		t.Fatalf("text = %q", got[0].Text)
	}
}

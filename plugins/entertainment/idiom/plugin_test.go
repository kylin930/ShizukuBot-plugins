package idiom

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

// tinyPNG 生成合法的 1x1 PNG，供图片回复链路使用。
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

type testEnv struct {
	replies []sdk.Reply
}

func (e *testEnv) run(t *testing.T, text string, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{
		Message: text,
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: cfg}); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) text(t *testing.T) string {
	t.Helper()
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	t.Fatalf("no text reply in %+v", e.replies)
	return ""
}

func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	old := baseURL
	baseURL = ts.URL
	t.Cleanup(func() {
		baseURL = old
		ts.Close()
	})
}

func TestExactIdiom(t *testing.T) {
	var gotPath, gotWords string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotWords = r.URL.Query().Get("words")
		w.Write([]byte(`<html><head><meta content="一帆风顺，汉语成语。" name="description"></head></html>`))
	})
	e := &testEnv{}
	e.run(t, "成语查询 一帆风顺", nil)
	if gotPath != "/chengyu/search" || gotWords != "一帆风顺" {
		t.Fatalf("request path=%q words=%q", gotPath, gotWords)
	}
	if got, want := e.text(t), "“一帆风顺”\n一帆风顺，汉语成语。"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func TestExactIdiomNotFound(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html></html>`))
	})
	e := &testEnv{}
	e.run(t, "成语查询 一帆风顺", nil)
	if got := e.text(t); got != "未找到成语释义。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestFuzzyIdiom(t *testing.T) {
	var gotWords string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotWords = r.URL.Query().Get("words")
		w.Write([]byte(`<div class="title"><h3><a href="/a" title="一帆风顺">一帆风顺</a></h3><span class="pinyin">yī fán fēng shùn</span></div>
<div class="title"><h3><a href="/b" title="一帆顺风">一帆顺风</a></h3><span class="pinyin">yī fān shùn fēng</span></div>`))
	})
	e := &testEnv{}
	e.run(t, "成语查询 ?帆风?", nil)
	if gotWords != "?帆风*" {
		t.Fatalf("words = %q, want ?帆风*", gotWords)
	}
	got := e.text(t)
	if !strings.Contains(got, "“?帆风?” 的搜索结果：") {
		t.Fatalf("reply = %q", got)
	}
	if !strings.Contains(got, "一帆风顺  yī fán fēng shùn") {
		t.Fatalf("reply missing first item: %q", got)
	}
	if !strings.Contains(got, "一帆顺风  yī fān shùn fēng") {
		t.Fatalf("reply missing second item: %q", got)
	}
}

func TestFuzzyTrailingWildcard(t *testing.T) {
	var gotWords string
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotWords = r.URL.Query().Get("words")
		w.Write([]byte(`<div class="title"><h3><a title="一帆风顺" href="/a">一帆风顺</a></h3><span class="pinyin">yī</span></div>`))
	})
	e := &testEnv{}
	e.run(t, "成语查询 一帆？？", nil)
	if gotWords != "一帆**" {
		t.Fatalf("words = %q, want 一帆**", gotWords)
	}
}

func TestFuzzyLimit(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div class="title"><h3><a href="/a" title="甲">甲</a></h3><span class="pinyin">jiǎ</span></div>
<div class="title"><h3><a href="/b" title="乙">乙</a></h3><span class="pinyin">yǐ</span></div>
<div class="title"><h3><a href="/c" title="丙">丙</a></h3><span class="pinyin">bǐng</span></div>`))
	})
	e := &testEnv{}
	e.run(t, "成语查询 ????", map[string]any{"Maximumidiom": 2})
	got := e.text(t)
	if strings.Contains(got, "丙") {
		t.Fatalf("limit not applied: %q", got)
	}
	if !strings.Contains(got, "甲") || !strings.Contains(got, "乙") {
		t.Fatalf("missing results: %q", got)
	}
}

func TestInvalidIdiomLength(t *testing.T) {
	e := &testEnv{}
	e.run(t, "成语查询 一帆", nil)
	if got := e.text(t); got != "请输入四字成语哦，未知的字可以用【？】代替。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmptyIdiomSilent(t *testing.T) {
	e := &testEnv{}
	e.run(t, "成语查询", nil)
	if len(e.replies) != 0 {
		t.Fatalf("expected no replies, got %+v", e.replies)
	}
}

func TestSynonymQuery(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jinyici/search" {
			t.Errorf("path = %q, want /jinyici/search", r.URL.Path)
		}
		w.Write([]byte(`<li><h3><a href="/a" title="顺利">顺利</a></h3><p class=pinyin>shùn lì</p><p class=summary>没有阻碍 <span class=more>查看详情</span></p></li>
<li><h3><a href="/b" title="顺畅">顺畅</a></h3><p class=pinyin>shùn chàng</p><p class=summary>流利通畅</p></li>`))
	})
	e := &testEnv{}
	e.run(t, "近义词查询 顺利", nil)
	got := e.text(t)
	if !strings.Contains(got, "近义词 [顺利] 查询结果：") {
		t.Fatalf("reply = %q", got)
	}
	if !strings.Contains(got, "顺利 (shùn lì): 没有阻碍") {
		t.Fatalf("missing summary or more span removed: %q", got)
	}
	if strings.Contains(got, "查看详情") {
		t.Fatalf("more span should be removed: %q", got)
	}
	if !strings.Contains(got, "顺畅 (shùn chàng): 流利通畅") {
		t.Fatalf("missing second item: %q", got)
	}
}

func TestAntonymQueryNotFound(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fanyici/search" {
			t.Errorf("path = %q, want /fanyici/search", r.URL.Path)
		}
		w.Write([]byte(`<html></html>`))
	})
	e := &testEnv{}
	e.run(t, "反义词查询 顺利", nil)
	if got := e.text(t); got != "未找到匹配的反义词。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestWordIncomplete(t *testing.T) {
	e := &testEnv{}
	e.run(t, "近义词查询", nil)
	if got := e.text(t); got != "输入内容不完整哦~" {
		t.Fatalf("reply = %q", got)
	}
	e.run(t, "反义词查询 ?利", nil)
	if got := e.text(t); got != "输入内容不完整哦~" {
		t.Fatalf("reply = %q", got)
	}
}

func TestFetchFailure(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	e := &testEnv{}
	e.run(t, "成语查询 一帆风顺", nil)
	if got := e.text(t); got != "查询失败，请稍后再试。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestImagifyOutput(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta content="一帆风顺，汉语成语。" name="description">`))
	})
	var gotDoc string
	var gotOpts sdk.RenderOptions
	old := renderHTML
	renderHTML = func(_ context.Context, doc []byte, opts sdk.RenderOptions) ([]byte, error) {
		gotDoc = string(doc)
		gotOpts = opts
		return tinyPNG(t), nil
	}
	t.Cleanup(func() { renderHTML = old })

	e := &testEnv{}
	e.run(t, "成语查询 一帆风顺", map[string]any{"imagifyoutput": true})
	if gotOpts.Width != 750 || !gotOpts.FullPage {
		t.Fatalf("render opts = %+v", gotOpts)
	}
	if !strings.Contains(gotDoc, "【释义】") {
		t.Fatalf("title not replaced: %s", gotDoc)
	}
	if !strings.Contains(gotDoc, "一帆风顺") {
		t.Fatalf("words missing: %s", gotDoc)
	}
	if !strings.Contains(gotDoc, "<div>一帆风顺，汉语成语。</div>") {
		t.Fatalf("paraphrase missing: %s", gotDoc)
	}
	if len(e.replies) == 0 || e.replies[len(e.replies)-1].Kind != "image" {
		t.Fatalf("expected image reply, got %+v", e.replies)
	}
}

func TestRenderCardWildcardBlank(t *testing.T) {
	doc := renderCard("?帆??", "line1\nline2", titleSynonymResults)
	if !strings.Contains(doc, "【近义词查询结果】") {
		t.Fatalf("title missing: %s", doc)
	}
	if strings.Contains(doc, ">?<") {
		t.Fatalf("wildcard should be blank: %s", doc)
	}
	if !strings.Contains(doc, "<div>line1</div><br><div>line2</div>") {
		t.Fatalf("paraphrase newline handling wrong: %s", doc)
	}
}

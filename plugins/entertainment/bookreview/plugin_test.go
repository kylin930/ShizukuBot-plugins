package bookreview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"shizukubot/sdk"
)

// testJPEG 是带 JPEG 文件头的最小字节，足以通过 ReplyImageBytes 的类型校验。
var testJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9")

type captureResult struct {
	reply   sdk.Reply
	replied bool
	html    string
}

// capture 执行一次 Handle，渲染替换为返回固定 JPEG 的桩并记录卡片 HTML。
func capture(t *testing.T, message string) captureResult {
	t.Helper()
	var res captureResult
	old := renderHTML
	renderHTML = func(_ context.Context, page []byte, _ sdk.RenderOptions) ([]byte, error) {
		res.html = string(page)
		return testJPEG, nil
	}
	t.Cleanup(func() { renderHTML = old })

	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		res.reply = r
		res.replied = true
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRandomReviewUsesCorpus(t *testing.T) {
	old := randIntn
	randIntn = func(int) int { return 3 }
	t.Cleanup(func() { randIntn = old })

	got := capture(t, "随机书评")
	if !got.replied {
		t.Fatal("随机书评 未触发回复")
	}
	if got.reply.Kind != "image" || !strings.HasPrefix(got.reply.URL, "data:image/jpeg;base64,") {
		t.Fatalf("reply = %+v", got.reply)
	}
	for _, want := range []string{"《赤心巡天》", "作者：情何以甚", "文笔在网文里属于上游"} {
		if !strings.Contains(got.html, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestRandomResultAlwaysInCorpus(t *testing.T) {
	old := randIntn
	t.Cleanup(func() { randIntn = old })

	for i := range reviews {
		idx := i
		randIntn = func(int) int { return idx }
		got := capture(t, "随机书评")
		if !got.replied {
			t.Fatalf("第 %d 条未触发回复", idx)
		}
		if got.reply.Kind != "image" {
			t.Fatalf("第 %d 条回复类型 = %q", idx, got.reply.Kind)
		}
		if want := "《" + reviews[idx].Book + "》"; !strings.Contains(got.html, want) {
			t.Fatalf("第 %d 条 html 缺少 %q", idx, want)
		}
	}
}

func TestKeywordReview(t *testing.T) {
	got := capture(t, "书评群穿明末")
	if !got.replied {
		t.Fatal("书评+关键词 未触发回复")
	}
	if got.reply.Kind != "image" {
		t.Fatalf("kind = %q", got.reply.Kind)
	}
	for _, want := range []string{"《临高启明》", "吹牛者", "群穿明末"} {
		if !strings.Contains(got.html, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

// TestKeywordOnlySearchesComment 上游只在评语字段检索，书名与作者命中不算。
func TestKeywordOnlySearchesComment(t *testing.T) {
	for _, keyword := range []string{"书评临高启明", "书评天瑞说符", "书评绝对不存在的书名"} {
		got := capture(t, keyword)
		if !got.replied {
			t.Fatalf("%q 应仍渲染空书评图", keyword)
		}
		if got.reply.Kind != "image" {
			t.Fatalf("%q kind = %q", keyword, got.reply.Kind)
		}
		if !strings.Contains(got.html, `<div class="comment"></div>`) {
			t.Fatalf("%q 未渲染空评语卡片: %q", keyword, got.html)
		}
		for _, unexpected := range []string{"临高启明", "天瑞说符", "没有找到"} {
			if strings.Contains(got.html, unexpected) {
				t.Fatalf("%q 意外渲染 %q", keyword, unexpected)
			}
		}
	}
}

// TestRenderFallback 渲染失败时回退为原有文本格式。
func TestRenderFallback(t *testing.T) {
	oldRand := randIntn
	randIntn = func(int) int { return 3 }
	t.Cleanup(func() { randIntn = oldRand })

	oldRender := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, errors.New("browser unavailable")
	}
	t.Cleanup(func() { renderHTML = oldRender })

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "随机书评", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "text" || got.Text != reviews[3].format() {
		t.Fatalf("reply = %+v", got)
	}
}

// TestImageSendFailureFallsBack 图片发送失败时同样回退文本。
func TestImageSendFailureFallsBack(t *testing.T) {
	oldRand := randIntn
	randIntn = func(int) int { return 1 }
	t.Cleanup(func() { randIntn = oldRand })

	oldRender := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return testJPEG, nil
	}
	t.Cleanup(func() { renderHTML = oldRender })

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "随机书评", Reply: func(_ context.Context, r sdk.Reply) error {
		if r.Kind == "image" {
			return errors.New("upload failed")
		}
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "text" || got.Text != reviews[1].format() {
		t.Fatalf("reply = %+v", got)
	}
}

func TestIgnoresOtherMessages(t *testing.T) {
	messages := []string{
		"",
		"你好",
		"书评",
		"书评   ",
		"书评诡秘@之主",
		"随机书评啊",
		"书评" + strings.Repeat("很", 26),
	}
	for _, message := range messages {
		if got := capture(t, message); got.replied {
			t.Fatalf("%q 不应触发回复", message)
		}
	}
}

func TestCorpusIsComplete(t *testing.T) {
	if len(reviews) < 30 || len(reviews) > 50 {
		t.Fatalf("语料条数 = %d", len(reviews))
	}
	for i, r := range reviews {
		if strings.TrimSpace(r.Book) == "" || strings.TrimSpace(r.Author) == "" || strings.TrimSpace(r.Comment) == "" {
			t.Fatalf("第 %d 条语料字段为空", i)
		}
	}
}

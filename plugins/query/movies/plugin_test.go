package movies

import (
	"context"
	"errors"
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
	for _, name := range []string{"movies"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

const (
	todayJSON = `{"movieList":[
	  {"id":1200486,"nm":"哪吒之魔童降世","img":"https://img.example/nezha.jpg","cat":"动画,喜剧,奇幻","rt":"2019-07-26","sc":9.4,"star":"吕艳婷,囧森瑟夫,瀚墨","watched":123456},
	  {"id":1217236,"nm":"八佰","img":"https://img.example/bai.jpg","cat":"剧情,历史,战争","rt":"2020-08-21","sc":8.7,"star":"王千源,张译,姜武","watched":98000}
	]}`

	comingJSON = `{"coming":[
	  {"id":900001,"nm":"预售片","cat":"剧情","rt":"2026-12-01","sc":0,"star":"张三","wish":25000,"comingTitle":"12月1日 18:00"}
	]}`

	detailJSON = `{"detailMovie":{
	  "id":1200486,"nm":"哪吒之魔童降世","dir":"饺子","star":"吕艳婷,囧森瑟夫,瀚墨",
	  "cat":"动画,喜剧,奇幻","oriLang":"中国大陆","dur":110,"rt":"2019-07-26","sc":9.4,
	  "dra":"天地灵气孕育出一颗能量巨大的混元珠，元始天尊将混元珠提炼成灵珠和魔丸。"
	}}`

	baiDetailJSON = `{"detailMovie":{
	  "id":1217236,"nm":"八佰","dir":"管虎","star":"王千源,张译,姜武",
	  "cat":"剧情,历史,战争","oriLang":"中国大陆","dur":147,"rt":"2020-08-21","sc":8.7
	}}`

	presaleDetailJSON = `{"detailMovie":{
	  "id":900001,"nm":"预售片","dir":"李四","star":"张三","cat":"剧情",
	  "oriLang":"中国大陆","version":"2D","rt":"2026-12-01","sc":0
	}}`

	searchJSON = `{"movies":{"list":[
	  {"id":1200486,"nm":"哪吒之魔童降世","img":"https://img.example/nezha.jpg","dir":"饺子",
	   "star":"吕艳婷,囧森瑟夫,瀚墨","cat":"动画,喜剧,奇幻","rt":"2019-07-26","sc":9.4}
	]}}`

	testPoster = "data:image/jpeg;base64,cG9zdGVy"
)

// testJPEG 是带 JPEG 文件头的最小字节，足以通过 ReplyImageBytes 的类型校验。
var testJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9")

type capture struct {
	texts  []string
	images []string
}

func run(t *testing.T, message string) capture {
	t.Helper()
	var got capture
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		if r.Kind == "image" {
			got.images = append(got.images, r.URL)
		} else {
			got.texts = append(got.texts, r.Text)
		}
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// stubRender 把渲染替换为返回固定 JPEG 的桩，并返回收集到的 HTML。
func stubRender(t *testing.T) *string {
	t.Helper()
	var page string
	old := renderHTML
	renderHTML = func(_ context.Context, html []byte, _ sdk.RenderOptions) ([]byte, error) {
		page = string(html)
		return testJPEG, nil
	}
	t.Cleanup(func() { renderHTML = old })
	return &page
}

// stubPoster 把海报下载替换为固定 data URI，避免测试联网。
func stubPoster(t *testing.T) {
	t.Helper()
	old := fetchImage
	fetchImage = func(_ context.Context, _ string) string { return testPoster }
	t.Cleanup(func() { fetchImage = old })
}

// useServer 把四个接口地址指向本地测试服务器，并在测试结束后还原。
func useServer(t *testing.T, base string) {
	t.Helper()
	oldOn, oldComing, oldDetail, oldSearch := movieOnURL, comingURL, detailURL, searchURL
	movieOnURL = base + "/ajax/movieOnInfoList"
	comingURL = base + "/ajax/comingList?token="
	detailURL = base + "/ajax/detailmovie?movieId="
	searchURL = base + "/ajax/search?kw="
	t.Cleanup(func() {
		movieOnURL, comingURL, detailURL, searchURL = oldOn, oldComing, oldDetail, oldSearch
	})
}

func stub(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Mozilla") {
			t.Errorf("User-Agent = %q", ua)
		}
		if r.Header.Get("Referer") == "" {
			t.Errorf("缺少 Referer")
		}
		switch r.URL.Path {
		case "/ajax/movieOnInfoList":
			_, _ = w.Write([]byte(todayJSON))
		case "/ajax/comingList":
			_, _ = w.Write([]byte(comingJSON))
		case "/ajax/detailmovie":
			switch r.URL.Query().Get("movieId") {
			case "1200486":
				_, _ = w.Write([]byte(detailJSON))
			case "1217236":
				_, _ = w.Write([]byte(baiDetailJSON))
			case "900001":
				_, _ = w.Write([]byte(presaleDetailJSON))
			default:
				t.Errorf("unexpected movieId %q", r.URL.Query().Get("movieId"))
				_, _ = w.Write([]byte(`{"detailMovie":{}}`))
			}
		case "/ajax/search":
			if r.URL.Query().Get("kw") != "哪吒" {
				_, _ = w.Write([]byte(`{"movies":{"list":[]}}`))
				return
			}
			_, _ = w.Write([]byte(searchJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	useServer(t, server.URL)
}

func TestHelp(t *testing.T) {
	got := run(t, "电影帮助")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "今日电影") ||
		!strings.Contains(got.texts[0], "电影搜索") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestToday(t *testing.T) {
	stub(t)
	page := stubRender(t)
	stubPoster(t)

	got := run(t, "今日电影")
	if len(got.texts) != 0 {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 1 || !strings.HasPrefix(got.images[0], "data:image/jpeg;base64,") {
		t.Fatalf("images = %q", got.images)
	}
	for _, want := range []string{
		"今日电影", "哪吒之魔童降世（1200486）", "导演：饺子", "演员：吕艳婷",
		"标签：动画,喜剧,奇幻", "语言：中国大陆", "上映时间：2019-07-26",
		"9.4", "12.35万 人已看", testPoster,
	} {
		if !strings.Contains(*page, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestComing(t *testing.T) {
	stub(t)
	page := stubRender(t)
	stubPoster(t)

	got := run(t, "预售电影")
	if len(got.texts) != 0 {
		t.Fatalf("texts = %q", got.texts)
	}
	if len(got.images) != 1 || !strings.HasPrefix(got.images[0], "data:image/jpeg;base64,") {
		t.Fatalf("images = %q", got.images)
	}
	for _, want := range []string{
		"预售电影", "预售片（900001）", "播放时间：12月1日 18:00", "暂无", "2.50万 人期待",
	} {
		if !strings.Contains(*page, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

// TestRenderFallback 渲染失败时回退到原有纯文本榜单。
func TestRenderFallback(t *testing.T) {
	stub(t)
	stubPoster(t)
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, errors.New("browser unavailable")
	}
	t.Cleanup(func() { renderHTML = old })

	got := run(t, "今日电影")
	if len(got.images) != 0 {
		t.Fatalf("images = %q", got.images)
	}
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "今日电影（共 2 条）") ||
		!strings.Contains(got.texts[0], "1. 哪吒之魔童降世") ||
		!strings.Contains(got.texts[0], "已看：12.35万") {
		t.Fatalf("texts = %q", got.texts)
	}
}

// TestImageSendFailureFallsBack 图片发送失败时同样回退文本。
func TestImageSendFailureFallsBack(t *testing.T) {
	stub(t)
	stubRender(t)
	stubPoster(t)

	var texts []string
	c := sdk.Call{Event: sdk.Event{Message: "预售电影", Reply: func(_ context.Context, r sdk.Reply) error {
		if r.Kind == "image" {
			return errors.New("upload failed")
		}
		texts = append(texts, r.Text)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "预售电影（共 1 条）") {
		t.Fatalf("texts = %q", texts)
	}
}

func TestSearchHit(t *testing.T) {
	stub(t)
	for _, command := range []string{"电影搜索 哪吒", "电影详情 哪吒"} {
		got := run(t, command)
		if len(got.texts) != 1 {
			t.Fatalf("%s texts = %q", command, got.texts)
		}
		for _, want := range []string{"《哪吒之魔童降世》", "导演：饺子", "类型：动画,喜剧,奇幻",
			"地区：中国大陆", "时长：110 分钟", "上映：2019-07-26", "评分：9.4 分", "简介：天地灵气"} {
			if !strings.Contains(got.texts[0], want) {
				t.Fatalf("%s text %q missing %q", command, got.texts[0], want)
			}
		}
	}
}

func TestSearchMiss(t *testing.T) {
	stub(t)
	got := run(t, "电影搜索 不存在的片")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "没有找到电影") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestSearchUsage(t *testing.T) {
	got := run(t, "电影搜索")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "用法") {
		t.Fatalf("texts = %q", got.texts)
	}
}

// TestSearchDetailFallback 详情接口失败时仍用搜索结果回复，只是缺少时长与简介。
func TestSearchDetailFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ajax/search" {
			_, _ = w.Write([]byte(searchJSON))
			return
		}
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	useServer(t, server.URL)

	got := run(t, "电影搜索 哪吒")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "《哪吒之魔童降世》") ||
		!strings.Contains(got.texts[0], "时长：暂无") {
		t.Fatalf("texts = %q", got.texts)
	}
	if strings.Contains(got.texts[0], "简介：") {
		t.Fatalf("不应包含简介: %q", got.texts[0])
	}
}

func TestUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	useServer(t, server.URL)

	got := run(t, "今日电影")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "今日电影查询失败") ||
		!strings.Contains(got.texts[0], "500") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestSecureImageURL(t *testing.T) {
	cases := map[string]string{
		"http://p0.meituan.net/a.jpg":  "https://p0.meituan.net/a.jpg",
		"https://p0.meituan.net/a.jpg": "https://p0.meituan.net/a.jpg",
		"":                             "",
	}
	for input, want := range cases {
		if got := secureImageURL(input); got != want {
			t.Fatalf("secureImageURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "今天看电影吗"); len(got.texts) != 0 || len(got.images) != 0 {
		t.Fatalf("unexpected reply %q %q", got.texts, got.images)
	}
}

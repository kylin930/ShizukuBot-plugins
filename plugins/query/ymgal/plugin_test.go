package ymgal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	for _, name := range []string{"gal"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

var testConfig = map[string]any{
	"client_id":     "id",
	"client_secret": "secret",
}

func run(t *testing.T, text string, config map[string]any) []sdk.Reply {
	t.Helper()
	return runAs(t, text, config, "")
}

func runAs(t *testing.T, text string, config map[string]any, userID string) []sdk.Reply {
	t.Helper()
	var replies []sdk.Reply
	c := sdk.Call{
		Event: sdk.Event{Message: text, UserID: userID, Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r)
			return nil
		}},
		Config: config,
	}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return replies
}

func firstText(replies []sdk.Reply) string {
	for _, r := range replies {
		if r.Kind == "text" {
			return r.Text
		}
	}
	return ""
}

func firstImage(replies []sdk.Reply) string {
	for _, r := range replies {
		if r.Kind == "image" {
			return r.URL
		}
	}
	return ""
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("编码响应失败: %v", err)
	}
}

func apiOK(data any) map[string]any {
	return map[string]any{"success": true, "code": 0, "data": data}
}

// useTestServer 把包级 API 地址指向本地测试服务器，测试结束后还原。
func useTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	oldToken, oldSearch, oldArchive, oldRandom := tokenURL, searchURL, archiveURL, randomURL
	oldCG, oldEmoticon, oldDetail := cgListURL, emoticonListURL, picsetDetailURL
	tokenURL = srv.URL + "/oauth/token"
	searchURL = srv.URL + "/open/archive/search-game"
	archiveURL = srv.URL + "/open/archive"
	randomURL = srv.URL + "/open/archive/random-game"
	cgListURL = srv.URL + "/search?type=picset&sort=default&category=Gal%20CG&page="
	emoticonListURL = srv.URL + "/search?type=picset&sort=default&category=%E5%85%B6%E4%BB%96&page="
	picsetDetailURL = srv.URL + "/co/picset/"
	t.Cleanup(func() {
		srv.Close()
		tokenURL, searchURL, archiveURL, randomURL = oldToken, oldSearch, oldArchive, oldRandom
		cgListURL, emoticonListURL, picsetDetailURL = oldCG, oldEmoticon, oldDetail
	})
	return srv
}

func okToken(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("grant_type") != "client_credentials" || q.Get("client_id") != "id" ||
			q.Get("client_secret") != "secret" || q.Get("scope") != "public" {
			t.Errorf("令牌请求参数异常: %s", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{
			"access_token": "test-token",
			"token_type":   "bearer",
			"expires_in":   3599,
			"scope":        "public",
		})
	}
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Manifest.ID != "ymgal" {
		t.Fatalf("ID = %q", d.Manifest.ID)
	}
}

func TestSearchHit(t *testing.T) {
	const cover = "https://example.com/cover.jpg"
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", okToken(t))
	mux.HandleFunc("/open/archive/search-game", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("version"); got != "1" {
			t.Errorf("version = %q", got)
		}
		q := r.URL.Query()
		if q.Get("mode") != "list" || q.Get("keyword") != "近月" ||
			q.Get("pageNum") != "1" || q.Get("pageSize") != "5" {
			t.Errorf("搜索参数异常: %s", r.URL.RawQuery)
		}
		writeJSON(t, w, apiOK(map[string]any{
			"result": []map[string]any{{
				"id":          31147,
				"name":        "月に寄りそう乙女の作法",
				"chineseName": "近月少女的礼仪",
				"orgName":     "Navel",
				"releaseDate": "2012-10-26",
				"mainImg":     cover,
			}},
			"total": 1, "hasNext": false, "pageNum": 1, "pageSize": 5,
		}))
	})
	mux.HandleFunc("/open/archive", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("gid"); got != "31147" {
			t.Errorf("gid = %q", got)
		}
		writeJSON(t, w, apiOK(map[string]any{"game": map[string]any{
			"gid":          "31147",
			"name":         "月に寄りそう乙女の作法",
			"chineseName":  "近月少女的礼仪",
			"introduction": strings.Repeat("简介", 150),
			"mainImg":      cover,
			"releaseDate":  "2012-10-26",
		}}))
	})
	useTestServer(t, mux)

	for _, text := range []string{"gal 近月", "gal查询 近月"} {
		replies := run(t, text, testConfig)
		got := firstText(replies)
		for _, want := range []string{
			"名称：近月少女的礼仪", "原名：月に寄りそう乙女の作法", "会社：Navel",
			"发售日：2012-10-26", "简介：", "…", "封面：" + cover, detailBase + "31147",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%q 回复缺少 %q:\n%s", text, want, got)
			}
		}
		if strings.Contains(got, strings.Repeat("简介", 150)) {
			t.Fatalf("%q 简介没有截断", text)
		}
		if image := firstImage(replies); image != cover {
			t.Fatalf("%q 封面 = %q", text, image)
		}
	}
}

func TestSearchMiss(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", okToken(t))
	mux.HandleFunc("/open/archive/search-game", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, apiOK(map[string]any{
			"result": []map[string]any{}, "total": 0, "hasNext": false, "pageNum": 1, "pageSize": 5,
		}))
	})
	useTestServer(t, mux)

	replies := run(t, "gal 不存在", testConfig)
	if got := firstText(replies); !strings.Contains(got, "没有找到") {
		t.Fatalf("reply = %q", got)
	}
	if image := firstImage(replies); image != "" {
		t.Fatalf("未命中时不应发送图片: %q", image)
	}
}

func TestSearchUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", okToken(t))
	mux.HandleFunc("/open/archive/search-game", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": false, "code": 614, "msg": "根据关键字找不到对应档案",
		})
	})
	useTestServer(t, mux)

	replies := run(t, "gal 近月", testConfig)
	got := firstText(replies)
	if !strings.Contains(got, "查询失败") || !strings.Contains(got, "根据关键字找不到对应档案") {
		t.Fatalf("reply = %q", got)
	}
}

func TestTokenError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(t, w, map[string]any{
			"error": "invalid_client", "error_description": "Bad client credentials",
		})
	})
	useTestServer(t, mux)

	replies := run(t, "gal 近月", testConfig)
	got := firstText(replies)
	if !strings.Contains(got, "获取月幕访问令牌失败") || !strings.Contains(got, "Bad client credentials") {
		t.Fatalf("reply = %q", got)
	}
}

func TestMissingCredential(t *testing.T) {
	srv := useTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("未配置密钥时不应请求上游: %s", r.URL.Path)
	}))

	replies := run(t, "gal 近月", nil)
	got := firstText(replies)
	if !strings.Contains(got, "client_id") || !strings.Contains(got, "client_secret") {
		t.Fatalf("reply = %q", got)
	}
	if srv.URL == "" {
		t.Fatal("测试服务器地址为空")
	}
}

func TestRandom(t *testing.T) {
	const cover = "https://example.com/random.jpg"
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", okToken(t))
	mux.HandleFunc("/open/archive/random-game", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("num"); got != "1" {
			t.Errorf("num = %q", got)
		}
		writeJSON(t, w, apiOK([]map[string]any{{
			"gid": 42, "name": "サクラノ詩", "chineseName": "樱之诗", "mainImg": cover,
		}}))
	})
	mux.HandleFunc("/open/archive", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("gid"); got != "42" {
			t.Errorf("gid = %q", got)
		}
		writeJSON(t, w, apiOK(map[string]any{"game": map[string]any{
			"gid": "42", "name": "サクラノ詩", "chineseName": "樱之诗",
			"introduction": "随机作品简介", "mainImg": cover, "releaseDate": "2015-10-23",
		}}))
	})
	useTestServer(t, mux)

	replies := run(t, "随机gal", testConfig)
	got := firstText(replies)
	for _, want := range []string{"名称：樱之诗", "原名：サクラノ詩", "发售日：2015-10-23", "简介：随机作品简介"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply 缺少 %q:\n%s", want, got)
		}
	}
	if image := firstImage(replies); image != cover {
		t.Fatalf("封面 = %q", image)
	}
}

func writeHTML(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("写入响应失败: %v", err)
	}
}

// listHTML 模拟图包搜索列表页。
func listHTML(ids []int, maxPage int) string {
	var b strings.Builder
	b.WriteString(`<html><body><div id="picset-result-list"><ul>`)
	for _, id := range ids {
		fmt.Fprintf(&b, `<div><div><a href="/co/picset/%d"><img src="https://example.com/thumb/%d.jpg"></a></div></div>`, id, id)
	}
	b.WriteString(`</ul></div><div id="pager-box"><div>`)
	for page := 1; page <= maxPage; page++ {
		fmt.Fprintf(&b, `<a class="item" href="?type=picset&page=%d">%d</a>`, page, page)
	}
	b.WriteString(`<a class="icon item pager-next" href="?page=` + strconv.Itoa(maxPage) + `">下一页</a></div></div></body></html>`)
	return b.String()
}

// detailHTML 模拟图包详情页，swiper 为 true 时走 CG 结构，否则走表情包 img 结构。
func detailHTML(title, desc string, images []string, swiper bool) string {
	var b strings.Builder
	b.WriteString(`<html><head>`)
	fmt.Fprintf(&b, `<meta name="name" content="%s">`, title)
	fmt.Fprintf(&b, `<meta name="description" content="%s">`, desc)
	b.WriteString(`</head><body><div id="main-picset-warp"><div>`)
	if swiper {
		b.WriteString(`<div class="swiper-wrapper">`)
		for _, image := range images {
			fmt.Fprintf(&b, `<div class="swiper-slide" data-background="%s"></div>`, image)
		}
		b.WriteString(`</div>`)
	} else {
		b.WriteString(`<div class="stream-list">`)
		for _, image := range images {
			fmt.Fprintf(&b, `<div><img data-src="%s"></div>`, image)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

func packImages(replies []sdk.Reply) []string {
	var images []string
	for _, r := range replies {
		if r.Kind == "image" {
			images = append(images, r.URL)
		}
	}
	return images
}

func TestRandomPicPack(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("category"); got != "Gal CG" && got != "其他" {
			t.Errorf("category = %q", got)
		}
		ids := []int{11, 12}
		if r.URL.Query().Get("page") == "2" {
			ids = []int{13}
		}
		writeHTML(t, w, listHTML(ids, 2))
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/co/picset/")
		writeHTML(t, w, detailHTML("CG "+id, "简介 "+id, []string{
			"https://example.com/" + id + "-1.jpg",
			"https://example.com/" + id + "-2.webp",
		}, true))
	})
	useTestServer(t, mux)

	oldRand := randIntn
	randIntn = func(int) int { return 0 }
	t.Cleanup(func() { randIntn = oldRand })

	for _, text := range []string{"随机galCG", "galCG", "随机gal表情包", "gal表情包"} {
		replies := run(t, text, nil)
		got := firstText(replies)
		for _, want := range []string{"图包：CG 11", "简介：简介 11", "链接：", "/co/picset/11"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%q 回复缺少 %q:\n%s", text, want, got)
			}
		}
		images := packImages(replies)
		if len(images) != 2 || images[0] != "https://example.com/11-1.jpg" {
			t.Fatalf("%q 图片 = %v", text, images)
		}
	}
}

func TestKeywordPicPackHit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("category"); got != "其他" {
			t.Errorf("category = %q", got)
		}
		writeHTML(t, w, listHTML([]int{21, 22}, 1))
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/co/picset/") {
		case "21":
			writeHTML(t, w, detailHTML("兔兔表情包", "兔兔", []string{"https://example.com/21-1.png"}, false))
		case "22":
			writeHTML(t, w, detailHTML("猫娘表情包", "可爱的猫娘表情", []string{"https://example.com/22-1.jpg"}, false))
		default:
			http.NotFound(w, r)
		}
	})
	useTestServer(t, mux)

	replies := run(t, "gal表情包 猫娘", nil)
	got := firstText(replies)
	if !strings.Contains(got, "图包：猫娘表情包") || !strings.Contains(got, "简介：可爱的猫娘表情") {
		t.Fatalf("reply = %q", got)
	}
	if images := packImages(replies); len(images) != 1 || images[0] != "https://example.com/22-1.jpg" {
		t.Fatalf("images = %v", images)
	}
}

func TestKeywordPicPackMiss(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(t, w, listHTML([]int{31, 32}, 1))
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/co/picset/")
		writeHTML(t, w, detailHTML("CG "+id, "普通场景", []string{"https://example.com/" + id + ".jpg"}, true))
	})
	useTestServer(t, mux)

	replies := run(t, "galCG 不存在", nil)
	if got := firstText(replies); !strings.Contains(got, "没有找到匹配「不存在」的CG") {
		t.Fatalf("reply = %q", got)
	}
	if images := packImages(replies); len(images) != 0 {
		t.Fatalf("未命中时不应发送图片: %v", images)
	}
}

func TestUpdateGalStats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		category, page := r.URL.Query().Get("category"), r.URL.Query().Get("page")
		switch category {
		case "Gal CG":
			if page == "2" {
				writeHTML(t, w, listHTML([]int{43}, 2))
				return
			}
			writeHTML(t, w, listHTML([]int{41, 42}, 2))
		case "其他":
			writeHTML(t, w, listHTML([]int{51}, 1))
		default:
			t.Errorf("category = %q", category)
			http.NotFound(w, r)
		}
	})
	useTestServer(t, mux)

	config := map[string]any{"admin_users": []any{"admin-openid"}}
	replies := runAs(t, "更新gal", config, "admin-openid")
	got := firstText(replies)
	for _, want := range []string{"共可抓取 4 个图包", "Gal CG：3 个图包（2 页）", "其他：1 个图包（1 页）"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply 缺少 %q:\n%s", want, got)
		}
	}
}

// memState 是测试用的内存状态。
type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func runWithState(t *testing.T, text string, config map[string]any, userID string, st sdk.State) []sdk.Reply {
	t.Helper()
	var replies []sdk.Reply
	c := sdk.Call{
		Event: sdk.Event{Message: text, UserID: userID, Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r)
			return nil
		}},
		Config: config,
		State:  st,
	}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return replies
}

func TestUpdateGalCachesIndex(t *testing.T) {
	searchCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		category, page := r.URL.Query().Get("category"), r.URL.Query().Get("page")
		switch {
		case category == "Gal CG" && page == "2":
			writeHTML(t, w, listHTML([]int{43}, 2))
		case category == "Gal CG":
			writeHTML(t, w, listHTML([]int{41, 42}, 2))
		default:
			writeHTML(t, w, listHTML([]int{51}, 1))
		}
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/co/picset/")
		writeHTML(t, w, detailHTML("CG "+id, "简介 "+id, []string{"https://example.com/" + id + ".jpg"}, true))
	})
	useTestServer(t, mux)

	st := memState{}
	config := map[string]any{"admin_users": []any{"admin"}}
	replies := runWithState(t, "更新gal", config, "admin", st)
	if got := firstText(replies); !strings.Contains(got, "共可抓取 4 个图包") {
		t.Fatalf("reply = %q", got)
	}
	var index picsetIndex
	if err := sdk.LoadJSON(context.Background(), st, picsetCacheKey, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.CG) != 3 || len(index.Emoticon) != 1 || index.UpdatedAt == 0 {
		t.Fatalf("index = %+v", index)
	}

	// 缓存命中后随机与关键词查询都不再请求列表页。
	searchCalls = 0
	oldRand := randIntn
	randIntn = func(int) int { return 0 }
	t.Cleanup(func() { randIntn = oldRand })

	replies = runWithState(t, "随机galCG", nil, "", st)
	if got := firstText(replies); !strings.Contains(got, "图包：CG 41") {
		t.Fatalf("random reply = %q", got)
	}
	replies = runWithState(t, "galCG 43", nil, "", st)
	if got := firstText(replies); !strings.Contains(got, "图包：CG 43") {
		t.Fatalf("keyword reply = %q", got)
	}
	if searchCalls != 0 {
		t.Fatalf("search calls = %d, want 0", searchCalls)
	}
}

func TestUpdateGalNoPermission(t *testing.T) {
	srv := useTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("无权限时不应请求图源: %s", r.URL.Path)
	}))
	replies := runAs(t, "更新gal", nil, "someone")
	if got := firstText(replies); !strings.Contains(got, "没有权限") {
		t.Fatalf("reply = %q", got)
	}
	if srv.URL == "" {
		t.Fatal("测试服务器地址为空")
	}
}

func TestUpdateGalSourceError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	})
	useTestServer(t, mux)

	config := map[string]any{"admin_users": []string{"admin"}}
	replies := runAs(t, "更新gal", config, "admin")
	got := firstText(replies)
	if !strings.Contains(got, "更新失败") || !strings.Contains(got, "图源不可访问") {
		t.Fatalf("reply = %q", got)
	}
}

func TestRandomPicPackListError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	})
	useTestServer(t, mux)

	replies := run(t, "随机galCG", nil)
	got := firstText(replies)
	if !strings.Contains(got, "发送图包失败") || !strings.Contains(got, "抓取图包列表失败") {
		t.Fatalf("reply = %q", got)
	}
}

func TestRandomPicPackNoImages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(t, w, listHTML([]int{61}, 1))
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(t, w, detailHTML("空图包", "没有图片", nil, true))
	})
	useTestServer(t, mux)

	replies := run(t, "galCG", nil)
	if got := firstText(replies); !strings.Contains(got, "详情页没有解析到图片") {
		t.Fatalf("reply = %q", got)
	}
}

func TestPicPackImageLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(t, w, listHTML([]int{71}, 1))
	})
	mux.HandleFunc("/co/picset/", func(w http.ResponseWriter, r *http.Request) {
		images := make([]string, 0, 60)
		for i := 1; i <= 60; i++ {
			images = append(images, fmt.Sprintf("https://example.com/%d.jpg", i))
		}
		writeHTML(t, w, detailHTML("大图包", "很多图", images, true))
	})
	useTestServer(t, mux)

	replies := run(t, "随机galCG", nil)
	if images := packImages(replies); len(images) != maxPicsetImages {
		t.Fatalf("图片数量 = %d，期望 %d", len(images), maxPicsetImages)
	}
}

func TestHelp(t *testing.T) {
	for _, text := range []string{"gal", "gal帮助", "gal查询", "月幕gal"} {
		replies := run(t, text, nil)
		got := firstText(replies)
		for _, want := range []string{"随机galCG", "gal表情包", "更新gal"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%q 帮助缺少 %q:\n%s", text, want, got)
			}
		}
	}
}

package rsshub

import (
	"context"
	"encoding/json"
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
	for _, name := range []string{"rss"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
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

// rssXML 生成一条 RSS 2.0 文档。
func rssXML(title string, items ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>` + title + `</title>`)
	for _, item := range items {
		b.WriteString(`<item><title>` + item[0] + `</title><link>` + item[1] + `</link><guid>` + item[1] + `</guid><pubDate>Fri, 07 Nov 2025 10:00:00 +0800</pubDate></item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// useBases 把 RSSHub 实例替换为测试服务器。
func useBases(t *testing.T, bases ...string) {
	t.Helper()
	old := rssBases
	rssBases = bases
	t.Cleanup(func() { rssBases = old })
}

// call 执行一次事件并收集回复。
func call(t *testing.T, c sdk.Call, message string) ([]sdk.Reply, error) {
	t.Helper()
	var got []sdk.Reply
	c.Event.Message = message
	c.Event.Reply = func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}
	err := Handle(context.Background(), c)
	return got, err
}

func adminConfig() map[string]any {
	return map[string]any{"admin_users": []any{"u1"}}
}

func groupCall(state memState, config map[string]any) sdk.Call {
	return sdk.Call{State: state, Config: config, Event: sdk.Event{GroupID: "g1", UserID: "u1"}}
}

func TestQueryRSS(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssXML("少数派", [2]string{"本周值得读", "https://sspai.com/post/1"})))
	}))
	defer ts.Close()
	useBases(t, ts.URL)

	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "rss /bookfere/weekly")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "【RSS】少数派") || !strings.Contains(got[0].Text, "本周值得读") {
		t.Fatalf("replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "https://sspai.com/post/1") {
		t.Fatalf("missing link: %q", got[0].Text)
	}
}

func TestQueryAtom(t *testing.T) {
	atom := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>示例源</title>
<entry><title>Atom 条目</title><id>urn:1</id>
<link rel="alternate" href="https://example.com/a"/><updated>2025-11-07T10:00:00Z</updated></entry></feed>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(atom))
	}))
	defer ts.Close()
	useBases(t, ts.URL)

	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "rss /atom")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "示例源") || !strings.Contains(got[0].Text, "https://example.com/a") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestMirrorFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssXML("备用源", [2]string{"条目", "https://example.com/x"})))
	}))
	defer ts.Close()
	useBases(t, "http://127.0.0.1:1", ts.URL)

	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "rss /feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "备用源") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestAddRequiresAdmin(t *testing.T) {
	got, err := call(t, groupCall(memState{}, nil), "添加rss订阅 /feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有权限") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestAddListSyncRemove(t *testing.T) {
	body := rssXML("测试源", [2]string{"旧条目", "https://example.com/old"})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	useBases(t, ts.URL)

	st := memState{}
	c := groupCall(st, adminConfig())

	got, _ := call(t, c, "添加rss订阅 /feed")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅 测试源") {
		t.Fatalf("add replies = %+v", got)
	}
	got, _ = call(t, c, "rss订阅列表")
	if len(got) != 1 || !strings.Contains(got[0].Text, "测试源") {
		t.Fatalf("list replies = %+v", got)
	}

	body = rssXML("测试源",
		[2]string{"新条目", "https://example.com/new"},
		[2]string{"旧条目", "https://example.com/old"},
	)
	got, _ = call(t, c, "rss同步")
	if len(got) != 1 || !strings.Contains(got[0].Text, "新条目") {
		t.Fatalf("sync replies = %+v", got)
	}
	if strings.Contains(got[0].Text, "旧条目") {
		t.Fatalf("old entry repeated: %q", got[0].Text)
	}

	got, _ = call(t, c, "rss同步")
	if len(got) != 1 || !strings.Contains(got[0].Text, "太频繁") {
		t.Fatalf("cooldown replies = %+v", got)
	}

	got, _ = call(t, c, "删除rss订阅 /feed")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已删除") {
		t.Fatalf("remove replies = %+v", got)
	}
}

func TestInvalidRoute(t *testing.T) {
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "rss /中文路由")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "用法") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestIgnoreChat(t *testing.T) {
	got, err := call(t, sdk.Call{Event: sdk.Event{}}, "今天天气不错")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

func TestUpstreamAliases(t *testing.T) {
	body := rssXML("测试源", [2]string{"旧条目", "https://example.com/old"})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	useBases(t, ts.URL)

	st := memState{}
	c := groupCall(st, adminConfig())

	got, _ := call(t, c, "添加rsshub订阅-/feed")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已订阅 测试源") {
		t.Fatalf("add replies = %+v", got)
	}
	got, _ = call(t, c, "查看rsshub订阅列表")
	if len(got) != 1 || !strings.Contains(got[0].Text, "测试源") {
		t.Fatalf("list replies = %+v", got)
	}
	got, _ = call(t, c, "rsshub同步")
	if len(got) != 1 || !strings.Contains(got[0].Text, "本次没有新条目") {
		t.Fatalf("sync replies = %+v", got)
	}
	got, _ = call(t, c, "删除rsshub订阅-/feed")
	if len(got) != 1 || !strings.Contains(got[0].Text, "已删除") {
		t.Fatalf("delete replies = %+v", got)
	}
}

func TestParseFeedSummaryAndImage(t *testing.T) {
	doc := `<?xml version="1.0"?><rss version="2.0"><channel><title>源</title><item>` +
		`<title>标题</title><link>https://example.com/a</link><guid>a</guid>` +
		`<description><![CDATA[<p>摘要 <b>内容</b></p><img src="https://img.example/inline.jpg">]]></description>` +
		`<enclosure url="https://img.example/cover.png" type="image/png"/>` +
		`</item></channel></rss>`
	feed, err := parseFeed([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Entries) != 1 {
		t.Fatalf("entries = %+v", feed.Entries)
	}
	entry := feed.Entries[0]
	if entry.Summary != "摘要 内容" {
		t.Fatalf("summary = %q", entry.Summary)
	}
	if entry.Image != "https://img.example/cover.png" {
		t.Fatalf("image = %q", entry.Image)
	}
}

func TestCleanSummaryTruncate(t *testing.T) {
	raw := "<p>" + strings.Repeat("字", summaryLimit+10) + "</p>"
	got := cleanSummary(raw)
	if len([]rune(got)) != summaryLimit+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("summary length = %d, %q", len([]rune(got)), got)
	}
}

func TestSyncPushSummaryAndImage(t *testing.T) {
	old := rssXML("测试源", [2]string{"旧条目", "https://example.com/old"})
	updated := `<?xml version="1.0"?><rss version="2.0"><channel><title>测试源</title>` +
		`<item><title>新条目</title><link>https://example.com/new</link><guid>new</guid>` +
		`<pubDate>Fri, 07 Nov 2025 10:00:00 +0800</pubDate>` +
		`<description><![CDATA[<p>这是新的摘要。</p>]]></description>` +
		`<enclosure url="https://img.example/new.png" type="image/png"/></item>` +
		`<item><title>旧条目</title><link>https://example.com/old</link><guid>https://example.com/old</guid>` +
		`<pubDate>Fri, 07 Nov 2025 10:00:00 +0800</pubDate></item>` +
		`</channel></rss>`
	body := old
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	useBases(t, ts.URL)

	st := memState{}
	c := groupCall(st, adminConfig())
	if got, _ := call(t, c, "添加rsshub订阅 /feed"); len(got) != 1 {
		t.Fatalf("add replies = %+v", got)
	}

	body = updated
	got, _ := call(t, c, "rsshub同步")
	if len(got) != 2 {
		t.Fatalf("sync replies = %+v", got)
	}
	if !strings.Contains(got[0].Text, "新条目") || !strings.Contains(got[0].Text, "这是新的摘要。") {
		t.Fatalf("sync text = %q", got[0].Text)
	}
	if got[1].Kind != "image" || got[1].URL != "https://img.example/new.png" {
		t.Fatalf("sync image = %+v", got[1])
	}
}

func TestPushBlockWithoutSummary(t *testing.T) {
	entry := feedEntry{Title: "标题", Link: "https://example.com/x"}
	got := pushBlock("源", entry)
	if got != "· 源：标题\n  https://example.com/x" {
		t.Fatalf("block = %q", got)
	}
}

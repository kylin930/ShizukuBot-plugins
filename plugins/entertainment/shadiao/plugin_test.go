package shadiao

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func reply(t *testing.T, message string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func overrideURLs(t *testing.T, base string) {
	t.Helper()
	old := []string{chpURL, duURL, pyqURL, chayiURL, ganhaiURL, yduanziURL, ergofabulousURL}
	chpURL = base + "/chp"
	duURL = base + "/du"
	pyqURL = base + "/pyq"
	chayiURL = base + "/sweet0"
	ganhaiURL = base + "/sweet1"
	yduanziURL = base + "/duanzi"
	ergofabulousURL = base + "/luther"
	t.Cleanup(func() {
		chpURL, duURL, pyqURL, chayiURL, ganhaiURL, yduanziURL, ergofabulousURL =
			old[0], old[1], old[2], old[3], old[4], old[5], old[6]
	})
}

func TestAllTriggers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chp":
			_, _ = w.Write([]byte(`{"data":{"text":"彩虹屁文案"}}`))
		case "/du":
			_, _ = w.Write([]byte(`{"data":{"text":"毒鸡汤文案"}}`))
		case "/pyq":
			_, _ = w.Write([]byte(`{"data":{"text":"朋友圈文案"}}`))
		case "/sweet0":
			_, _ = w.Write([]byte(`{"returnObj":{"content":"绿茶文案"}}`))
		case "/sweet1":
			_, _ = w.Write([]byte(`{"returnObj":{"content":"渣男文案"}}`))
		case "/duanzi":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"duanzi":"第一行<br>第二行"}`))
		case "/luther":
			_, _ = w.Write([]byte(`<main role="main"><p class="larger">你个<u>木头</u>！</p></main>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	overrideURLs(t, server.URL)

	cases := map[string]string{
		"哄我":     "彩虹屁文案",
		"来碗毒鸡汤":  "毒鸡汤文案",
		"发个朋友圈":  "朋友圈文案",
		"来碗绿茶":   "绿茶文案",
		"渣我":     "渣男文案",
		"讲个段子":   "第一行\n第二行",
		"马丁路德骂我": "你个木头！",
	}
	for keyword, want := range cases {
		if got := reply(t, keyword); got != want {
			t.Fatalf("%q -> %q, want %q", keyword, got, want)
		}
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := reply(t, "哄"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
	if got := reply(t, "今天天气不错"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestUpstreamErrorReply(t *testing.T) {
	old := chpURL
	chpURL = "http://127.0.0.1:1/"
	defer func() { chpURL = old }()

	if got := reply(t, "哄我"); !strings.Contains(got, "获取失败") {
		t.Fatalf("reply = %q", got)
	}
}

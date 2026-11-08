package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

// replyOf 收集一次处理中发出的所有回复。
func replyOf(t *testing.T, message string) ([]sdk.Reply, error) {
	t.Helper()
	var got []sdk.Reply
	e := sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}}
	err := Handle(context.Background(), sdk.Call{Event: e})
	return got, err
}

// useServer 把搜索与歌曲地址指向测试服务器。
func useServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	oldSearch, oldSong := searchURL, songURL
	searchURL = ts.URL + "/search?s=%s"
	songURL = ts.URL + "/song/%s.mp3"
	t.Cleanup(func() { searchURL, songURL = oldSearch, oldSong })
}

const oneSong = `{"result":{"songs":[{"id":123,"name":"晴天","duration":269000,
"artists":[{"name":"周杰伦"}],"album":{"name":"叶惠美"}}]}}`

func TestPointSong(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") == "" {
			t.Errorf("empty keyword")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oneSong))
	})

	got, err := replyOf(t, "点歌 晴天")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "voice" || !strings.HasSuffix(got[0].URL, "/123.mp3") {
		t.Fatalf("voice = %+v", got[0])
	}
	if !strings.Contains(got[1].Text, "晴天 - 周杰伦") {
		t.Fatalf("text = %q", got[1].Text)
	}
}

func TestNeteasePlatform(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(oneSong))
	})
	got, err := replyOf(t, "网易点歌晴天")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "voice" {
		t.Fatalf("replies = %+v", got)
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	got, err := replyOf(t, "酷我点歌 晴天")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "暂不支持") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestNoResult(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"songs":[]}}`))
	})
	got, err := replyOf(t, "点歌 不存在的歌")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "没有找到") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestSearchError(t *testing.T) {
	useServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	got, err := replyOf(t, "点歌 晴天")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "点歌失败") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestUsage(t *testing.T) {
	got, err := replyOf(t, "点歌")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "用法") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestIgnored(t *testing.T) {
	got, err := replyOf(t, "今天天气不错")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
}

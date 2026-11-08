package yujn

import (
	"context"
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
	for _, name := range []string{"yujn"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

func reply(t *testing.T, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTextCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("  今天也要开心  "))
	}))
	defer server.Close()
	old := textEndpoints["土味情话"]
	textEndpoints["土味情话"] = endpoint{url: server.URL}
	defer func() { textEndpoints["土味情话"] = old }()

	got := reply(t, "土味情话")
	if len(got) != 1 || got[0].Text != "今天也要开心" {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "keyboard" {
		t.Fatalf("kind = %q, want keyboard", got[0].Kind)
	}
	if !strings.Contains(got[0].Keyboard, `"data":"土味情话"`) {
		t.Fatalf("keyboard %q missing command 土味情话", got[0].Keyboard)
	}
}

func TestCleanParagraph(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<p>山有木兮木有枝</p><p>心悦君兮君不知</p>"))
	}))
	defer server.Close()
	old := textEndpoints["随机美句"]
	textEndpoints["随机美句"] = endpoint{url: server.URL, cleanParagraph: true}
	defer func() { textEndpoints["随机美句"] = old }()

	got := reply(t, "随机美句")
	if len(got) != 1 || got[0].Text != "山有木兮木有枝心悦君兮君不知" {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "keyboard" || !strings.Contains(got[0].Keyboard, `"data":"随机美句"`) {
		t.Fatalf("reply = %+v", got[0])
	}
}

func TestErrorReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	old := textEndpoints["随机骚话"]
	textEndpoints["随机骚话"] = endpoint{url: server.URL}
	defer func() { textEndpoints["随机骚话"] = old }()

	got := reply(t, "随机骚话")
	if len(got) != 1 || !strings.Contains(got[0].Text, "获取失败") {
		t.Fatalf("replies = %+v", got)
	}
	if got[0].Kind != "text" {
		t.Fatalf("kind = %q, want text", got[0].Kind)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := reply(t, "随机美文"); len(got) != 0 {
		t.Fatalf("unexpected reply %+v", got)
	}
}

func TestVoiceCommand(t *testing.T) {
	old := voiceEndpoints["御姐撒娇"]
	voiceEndpoints["御姐撒娇"] = "https://audio.example/yujie.mp3"
	defer func() { voiceEndpoints["御姐撒娇"] = old }()

	got := reply(t, "御姐撒娇")
	if len(got) != 1 || got[0].Kind != "voice" || got[0].URL != "https://audio.example/yujie.mp3" {
		t.Fatalf("replies = %+v", got)
	}
}

func TestLuluSays(t *testing.T) {
	var gotFrom, gotMsg string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFrom = r.URL.Query().Get("from")
		gotMsg = r.URL.Query().Get("msg")
		_, _ = w.Write([]byte(`{"url":"https://audio.example/lulu.mp3"}`))
	}))
	defer server.Close()
	old := yuyinURL
	yuyinURL = server.URL + "?type=json&from=%s&msg=%s"
	defer func() { yuyinURL = old }()

	got := reply(t, "让lulu说你好呀")
	if len(got) != 1 || got[0].Kind != "voice" || got[0].URL != "https://audio.example/lulu.mp3" {
		t.Fatalf("replies = %+v", got)
	}
	if gotFrom != "lulu" || gotMsg != "你好呀" {
		t.Fatalf("from = %q, msg = %q", gotFrom, gotMsg)
	}
}

func TestLuluSaysEmptyURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	old := yuyinURL
	yuyinURL = server.URL + "?type=json&from=%s&msg=%s"
	defer func() { yuyinURL = old }()

	got := reply(t, "让lulu说你好")
	if len(got) != 1 || !strings.Contains(got[0].Text, "语音生成失败") {
		t.Fatalf("replies = %+v", got)
	}
}

func TestLuluSaysHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	old := yuyinURL
	yuyinURL = server.URL + "?type=json&from=%s&msg=%s"
	defer func() { yuyinURL = old }()

	got := reply(t, "让lulu说你好")
	if len(got) != 1 || !strings.Contains(got[0].Text, "ERROR") {
		t.Fatalf("replies = %+v", got)
	}
}

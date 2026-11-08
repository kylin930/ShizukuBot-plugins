package genshin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"genshin"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

// playerBody 是构造的 Enka 响应：两个角色，第一个带 6 命座与好感度。
const playerBody = `{
  "playerInfo": {
    "nickname": "旅行者",
    "level": 60,
    "signature": "今天也要好好肝",
    "worldLevel": 8,
    "finishAchievementNum": 512
  },
  "avatarInfoList": [
    {
      "avatarId": 10000002,
      "talentIdList": [1, 2, 3, 4, 5, 6],
      "propMap": {"4001": {"ival": "90"}},
      "fetterInfo": {"expLevel": 10},
      "equipList": [
        {"itemId": 21503, "flat": {"nameTextMapHash": 3001}},
        {"itemId": 15503, "weapon": {"level": 90}, "flat": {"nameTextMapHash": 2001}}
      ]
    },
    {
      "avatarId": 10000003,
      "propMap": {"4001": {"ival": "80"}},
      "equipList": [{"itemId": 15504, "weapon": {"level": 80}, "flat": {"nameTextMapHash": 2001}}]
    }
  ]
}`

// useMock 把全部上游地址指向测试服务器，并清空静态数据缓存。
func useMock(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	oldAPI, oldCharacter, oldLocalization := apiURL, characterURL, localizationURL
	apiURL = ts.URL + "/api/uid/"
	characterURL = ts.URL + "/characters.json"
	localizationURL = ts.URL + "/loc.json"

	storeMu.Lock()
	oldStore, oldErrAt := store, storeErrAt
	store, storeErrAt = nil, time.Time{}
	storeMu.Unlock()

	t.Cleanup(func() {
		apiURL, characterURL, localizationURL = oldAPI, oldCharacter, oldLocalization
		storeMu.Lock()
		store, storeErrAt = oldStore, oldErrAt
		storeMu.Unlock()
	})
}

// mockHandler 同时提供 UID 接口与静态数据。
func mockHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/characters.json":
			io.WriteString(w, `{"10000002":{"NameTextMapHash":1002},"10000003":{"NameTextMapHash":1003}}`)
		case "/loc.json":
			io.WriteString(w, `{"CHS":{"1002":"神里绫华","1003":"胡桃","2001":"雾切之回光"},"EN":{}}`)
		default:
			w.WriteHeader(status)
			io.WriteString(w, body)
		}
	}
}

// call 执行一次事件并返回文本回复。
func call(t *testing.T, message string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{
		Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUsage(t *testing.T) {
	for _, message := range []string{"原神", "原神角色", "原神 12345678", "原神 abc", "原神 12345678901"} {
		if got := call(t, message); !strings.Contains(got, "用法") {
			t.Fatalf("%q reply = %q", message, got)
		}
	}
	if got := call(t, "你好"); got != "" {
		t.Fatalf("无关消息不应回复，got = %q", got)
	}
}

func TestQueryPlayer(t *testing.T) {
	useMock(t, mockHandler(http.StatusOK, playerBody))
	got := call(t, "原神角色 100000001")
	for _, want := range []string{
		"原神 UID 100000001", "昵称：旅行者", "等级：60", "世界等级：8",
		"成就数：512", "签名：今天也要好好肝", "角色橱窗（2）",
		"神里绫华", "Lv.90", "6命", "好感10", "武器：雾切之回光",
		"胡桃", "Lv.80",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("回复缺少 %q：%s", want, got)
		}
	}
}

func TestQueryNotFound(t *testing.T) {
	useMock(t, mockHandler(http.StatusNotFound, `{"error":"player not found"}`))
	if got := call(t, "原神 100000001"); !strings.Contains(got, "不存在") {
		t.Fatalf("reply = %q", got)
	}
}

func TestQueryUpstreamError(t *testing.T) {
	useMock(t, mockHandler(http.StatusInternalServerError, "boom"))
	got := call(t, "原神 100000001")
	if !strings.Contains(got, "查询失败") || !strings.Contains(got, "500") {
		t.Fatalf("reply = %q", got)
	}
}

func TestQueryNamesFallback(t *testing.T) {
	useMock(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/characters.json" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, playerBody)
	})
	got := call(t, "原神 100000001")
	for _, want := range []string{"角色10000002", "武器15503"} {
		if !strings.Contains(got, want) {
			t.Fatalf("回复缺少 %q：%s", want, got)
		}
	}
}

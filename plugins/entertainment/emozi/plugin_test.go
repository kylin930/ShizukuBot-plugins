package emozi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func reply(t *testing.T, message string) string {
	t.Helper()
	return replyCall(t, message, nil, nil)
}

func replyCall(t *testing.T, message string, st sdk.State, config map[string]any) string {
	t.Helper()
	var got string
	c := sdk.Call{State: st, Config: config, Event: sdk.Event{UserID: "u1", Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEncodeChoosesEachReading(t *testing.T) {
	var calls int
	var lastChoice []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/encode" {
			http.NotFound(w, r)
			return
		}
		calls++
		var body encodeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		if len(body.Choice) == 0 {
			_, _ = w.Write([]byte(`{"code":0,"message":"","result":{"text":"初步结果","choice":[2,2]}}`))
			return
		}
		lastChoice = body.Choice
		_, _ = w.Write([]byte(`{"code":0,"message":"","result":{"text":"你好(・ω・)ノ"}}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	st := memState{}
	if got := replyCall(t, "抽象转写 你好", st, nil); !strings.Contains(got, "请选择第0个多音字(1~2)") {
		t.Fatalf("first prompt = %q", got)
	}
	if got := replyCall(t, "1", st, nil); !strings.Contains(got, "请选择第1个多音字(1~2)") {
		t.Fatalf("second prompt = %q", got)
	}
	if got := replyCall(t, "2", st, nil); !strings.Contains(got, "你好(・ω・)ノ") || !strings.Contains(got, "https://emozi.seku.su/") {
		t.Fatalf("reply = %q", got)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	if len(lastChoice) != 2 || lastChoice[0] != 0 || lastChoice[1] != 1 {
		t.Fatalf("choice = %v", lastChoice)
	}
}

func TestEncodeOutOfRangeChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"message":"","result":{"text":"初步结果","choice":[2]}}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	st := memState{}
	_ = replyCall(t, "抽象转写 你好", st, nil)
	if got := replyCall(t, "3", st, nil); !strings.Contains(got, "输入越界") {
		t.Fatalf("reply = %q", got)
	}
}

func TestDecode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/decode" {
			http.NotFound(w, r)
			return
		}
		var body decodeRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"code":0,"message":"","result":"你好世界"}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	if got := reply(t, "抽象还原 (・ω・)"); !strings.Contains(got, "你好世界") {
		t.Fatalf("reply = %q", got)
	}
}

func TestUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":5,"message":"请求过于频繁"}`))
	}))
	defer server.Close()
	old := apiBase
	apiBase = server.URL + "/"
	defer func() { apiBase = old }()

	if got := reply(t, "抽象转写 你好"); !strings.Contains(got, "请求过于频繁") {
		t.Fatalf("reply = %q", got)
	}
}

func TestUsageAndIgnoresOtherText(t *testing.T) {
	if got := reply(t, "抽象转写"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := reply(t, "抽象还原"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := reply(t, "抽象 你好"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestLoginRenewsTokenFromState(t *testing.T) {
	var logins int
	var authOnEncode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getLoginSalt":
			_, _ = w.Write([]byte(`{"code":0,"result":{"salt":"abc"}}`))
		case "/login":
			logins++
			var body loginRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			sum := md5.Sum([]byte("pwabc"))
			if body.Password != hex.EncodeToString(sum[:]) || body.Username != "user" || body.Salt != "abc" {
				t.Errorf("login body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"code":0,"result":{"token":"tok"}}`))
		case "/encode":
			authOnEncode = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"code":0,"result":{"text":"你好(・ω・)ノ"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldBase, oldSalt, oldLogin := apiBase, saltURL, loginURL
	apiBase, saltURL, loginURL = server.URL+"/", server.URL+"/getLoginSalt", server.URL+"/login"
	defer func() { apiBase, saltURL, loginURL = oldBase, oldSalt, oldLogin }()

	st := memState{}
	config := map[string]any{"username": "user", "password": "pw"}
	if got := replyCall(t, "抽象转写 你好", st, config); !strings.Contains(got, "你好(・ω・)ノ") {
		t.Fatalf("reply = %q", got)
	}
	if authOnEncode != "tok" {
		t.Fatalf("Authorization = %q", authOnEncode)
	}
	// 第二次请求应复用 State 中的 token，不再登录。
	if got := replyCall(t, "抽象转写 你好", st, config); !strings.Contains(got, "你好(・ω・)ノ") {
		t.Fatalf("reply = %q", got)
	}
	if logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
}

func TestLoginFailureFallsBackAnonymous(t *testing.T) {
	var authOnEncode *string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getLoginSalt":
			_, _ = w.Write([]byte(`{"code":1,"message":"账号或密码错误"}`))
		case "/encode":
			value := r.Header.Get("Authorization")
			authOnEncode = &value
			_, _ = w.Write([]byte(`{"code":0,"result":{"text":"匿名结果"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldBase, oldSalt := apiBase, saltURL
	apiBase, saltURL = server.URL+"/", server.URL+"/getLoginSalt"
	defer func() { apiBase, saltURL = oldBase, oldSalt }()

	config := map[string]any{"username": "user", "password": "bad"}
	if got := replyCall(t, "抽象转写 你好", memState{}, config); !strings.Contains(got, "匿名结果") {
		t.Fatalf("reply = %q", got)
	}
	if authOnEncode == nil || *authOnEncode != "" {
		t.Fatalf("Authorization = %v", authOnEncode)
	}
}

func TestLoginCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getLoginSalt":
			_, _ = w.Write([]byte(`{"code":0,"result":{"salt":"s"}}`))
		case "/login":
			_, _ = w.Write([]byte(`{"code":0,"result":{"token":"tok"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldSalt, oldLogin := saltURL, loginURL
	saltURL, loginURL = server.URL+"/getLoginSalt", server.URL+"/login"
	defer func() { saltURL, loginURL = oldSalt, oldLogin }()

	st := memState{}
	if got := replyCall(t, "抽象登录 user pw", st, nil); got != "成功" {
		t.Fatalf("login reply = %q", got)
	}
	var state authState
	if err := json.Unmarshal(st[authKey], &state); err != nil || state.Token != "tok" {
		t.Fatalf("auth state = %s, err = %v", st[authKey], err)
	}
	if st[credentialsKey] == nil {
		t.Fatal("credentials not persisted")
	}
}

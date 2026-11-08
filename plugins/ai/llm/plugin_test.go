package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shizukubot/events"
	"shizukubot/sdk"
)

type memState struct {
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (s *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return s.data[key], nil
}

func (s *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	s.data[key] = value
	return nil
}

type testEnv struct {
	t       *testing.T
	state   *memState
	cfg     map[string]any
	replies []sdk.Reply
}

func newEnv(t *testing.T, cfg map[string]any) *testEnv {
	t.Helper()
	t.Cleanup(closeCachedDBs)
	return &testEnv{t: t, state: newMemState(), cfg: cfg}
}

func (e *testEnv) send(userID, nickname, text string) {
	e.t.Helper()
	e.replies = nil
	event := sdk.Event{
		BotID:     "bot",
		Source:    events.Group,
		GroupID:   "group-1",
		UserID:    userID,
		Nickname:  nickname,
		MessageID: "msg",
		Message:   text,
		Reply: func(_ context.Context, reply sdk.Reply) error {
			e.replies = append(e.replies, reply)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, Config: e.cfg, State: e.state}); err != nil {
		e.t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (e *testEnv) texts() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "text" {
			out = append(out, reply.Text)
		}
	}
	return out
}

func (e *testEnv) lastText() string {
	texts := e.texts()
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

type llmStub struct {
	mu        sync.Mutex
	requests  []chatRequest
	responses []string
}

func (s *llmStub) handler(w http.ResponseWriter, r *http.Request) {
	var request chatRequest
	_ = json.NewDecoder(r.Body).Decode(&request)
	s.mu.Lock()
	s.requests = append(s.requests, request)
	index := len(s.requests) - 1
	response := "默认回答"
	if index < len(s.responses) {
		response = s.responses[index]
	}
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": response}}},
	})
}

func (s *llmStub) last() chatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return chatRequest{}
	}
	return s.requests[len(s.requests)-1]
}

func testConfig(t *testing.T, serverURL string) map[string]any {
	t.Helper()
	return map[string]any{
		"apiUrl":          serverURL,
		"apiKey":          "test-key",
		"model":           "test-model",
		"databasePath":    filepath.Join(t.TempDir(), "llm.db"),
		"adminUsers":      []any{"admin"},
		"contextSize":     float64(50),
		"maxSummaryChars": float64(20000),
	}
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "llm" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, key := range []string{"apiUrl", "apiKey", "contextSize", "retentionHours", "adminUsers"} {
		if _, ok := def.Manifest.ConfigSchema.Properties[key]; !ok {
			t.Fatalf("missing config %q", key)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := decodeConfig(nil)
	if cfg.ContextSize != 50 || cfg.RetentionHours != 24 || cfg.SummaryDefaultLimit != 200 || cfg.SummaryMaxLimit != 1000 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !strings.Contains(cfg.SummaryPrompt, "极简总结") {
		t.Fatalf("default summary prompt = %q", cfg.SummaryPrompt)
	}
	override := decodeConfig(map[string]any{
		"contextSize":    float64(20),
		"retentionHours": "12",
		"temperature":    1.2,
		"adminUsers":     "u1,u2",
	})
	if override.ContextSize != 20 || override.RetentionHours != 12 || override.Temperature != 1.2 {
		t.Fatalf("override = %+v", override)
	}
	if !override.isAdmin("u2") || override.isAdmin("u3") {
		t.Fatal("admin list mismatch")
	}
}

func TestRecordAndPurge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc((&llmStub{}).handler))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	env := newEnv(t, cfg)
	env.send("u1", "甲", "第一条")
	env.send("u1", "甲", "第二条")

	db, err := openDB(decodeConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := recentMessages(context.Background(), db, "group-1", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
	if rows[0].Content != "第一条" || rows[1].Nickname != "甲" {
		t.Fatalf("rows = %+v", rows)
	}
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	if err := insertMessage(context.Background(), db, storedMessage{GID: "group-1", UID: "u1", Nickname: "甲", Content: "过期消息", TS: old}); err != nil {
		t.Fatal(err)
	}
	if err := purge(context.Background(), db, retentionCutoff(decodeConfig(cfg), time.Now())); err != nil {
		t.Fatal(err)
	}
	rows, _ = recentMessages(context.Background(), db, "group-1", 10)
	if len(rows) != 2 {
		t.Fatalf("purged rows = %+v", rows)
	}
	for _, row := range rows {
		if row.Content == "过期消息" {
			t.Fatal("old message was not purged")
		}
	}
}

func TestGPTWithContextLimit(t *testing.T) {
	stub := &llmStub{responses: []string{"第一次回答", "第二次回答"}}
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	env := newEnv(t, cfg)
	db, err := openDB(decodeConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	conv := "bot|group|group|group-1"
	now := time.Now().UnixMilli()
	for i := 0; i < 60; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := insertContext(context.Background(), db, conv, role, fmt.Sprintf("历史%d", i), now-int64(60-i)); err != nil {
			t.Fatal(err)
		}
	}
	env.send("u1", "甲", "/gpt 你好")
	request := stub.last()
	// 50 条历史 + 当前提问
	historyCount := 0
	for _, message := range request.Messages {
		if message.Role != "system" {
			historyCount++
		}
	}
	if historyCount != 51 {
		t.Fatalf("history count = %d", historyCount)
	}
	if request.Messages[len(request.Messages)-1].Content != "你好" {
		t.Fatalf("last message = %+v", request.Messages[len(request.Messages)-1])
	}
	if !strings.Contains(env.lastText(), "第一次回答") {
		t.Fatalf("answer = %q", env.lastText())
	}
	env.send("u1", "甲", "/gpt 继续")
	if !strings.Contains(env.lastText(), "第二次回答") {
		t.Fatalf("second answer = %q", env.lastText())
	}
	contextRows, err := recentContext(context.Background(), db, conv, 100, 0)
	if err != nil || len(contextRows) != 64 {
		t.Fatalf("context rows = %d err=%v", len(contextRows), err)
	}
}

func TestGPTUsageAndErrors(t *testing.T) {
	env := newEnv(t, map[string]any{"apiKey": "", "databasePath": filepath.Join(t.TempDir(), "llm.db")})
	env.send("u1", "甲", "/gpt 你好")
	if !strings.Contains(env.lastText(), "未配置") {
		t.Fatalf("missing key = %q", env.lastText())
	}

	server := httptest.NewServer(http.HandlerFunc((&llmStub{}).handler))
	defer server.Close()
	env = newEnv(t, testConfig(t, server.URL))
	env.send("u1", "甲", "/gpt")
	if !strings.Contains(env.lastText(), "请在 /gpt 后输入内容") {
		t.Fatalf("usage = %q", env.lastText())
	}
}

func TestSummary(t *testing.T) {
	stub := &llmStub{responses: []string{"总结结果"}}
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	env := newEnv(t, cfg)
	env.send("u1", "甲", "今天讨论终末地")
	env.send("u2", "乙", "明天开新活动")
	env.send("admin", "管理员", "群聊总结 10")

	texts := env.texts()
	if len(texts) < 2 || texts[0] != "少女思考中..." {
		t.Fatalf("texts = %v", texts)
	}
	if !strings.Contains(texts[len(texts)-1], "条消息总结") || !strings.Contains(texts[len(texts)-1], "总结结果") {
		t.Fatalf("summary = %q", texts[len(texts)-1])
	}
	request := stub.last()
	if !strings.Contains(request.Messages[1].Content, "今天讨论终末地") {
		t.Fatalf("prompt = %q", request.Messages[1].Content)
	}
	// 30 秒限频
	env.send("admin", "管理员", "群聊总结")
	if !strings.Contains(env.lastText(), "总结太频繁") {
		t.Fatalf("cooldown = %q", env.lastText())
	}
}

func TestSummaryPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc((&llmStub{}).handler))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	env := newEnv(t, cfg)
	env.send("u1", "甲", "消息")
	env.send("u1", "甲", "群聊总结")
	if !strings.Contains(env.lastText(), "仅管理员可用") {
		t.Fatalf("permission = %q", env.lastText())
	}
}

func TestSummaryNoRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc((&llmStub{}).handler))
	defer server.Close()
	env := newEnv(t, testConfig(t, server.URL))
	env.send("admin", "管理员", "群聊总结")
	if !strings.Contains(env.lastText(), "暂无可总结的群聊记录") {
		t.Fatalf("empty summary = %q", env.lastText())
	}
}

func TestSummaryMaxLimit(t *testing.T) {
	stub := &llmStub{}
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	cfg["summaryDefaultLimit"] = float64(200)
	cfg["summaryMaxLimit"] = float64(50)
	env := newEnv(t, cfg)
	env.send("u1", "甲", "消息")
	env.send("admin", "管理员", "群聊总结 9999")
	request := stub.last()
	if !strings.Contains(request.Messages[1].Content, "消息") {
		t.Fatalf("prompt = %q", request.Messages[1].Content)
	}
}

func TestSplitMessage(t *testing.T) {
	parts := splitMessage(strings.Repeat("测", 5000), splitLimit)
	if len(parts) < 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, part := range parts {
		if len([]rune(part)) > splitLimit {
			t.Fatalf("part too long: %d", len([]rune(part)))
		}
	}
}

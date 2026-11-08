package tarot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

// run 用 fake 的 Reply 能力执行一次 Handle，返回全部回复文本。
func run(t *testing.T, text, nickname string) []string {
	t.Helper()
	return runCall(t, text, nickname, nil)
}

func runCall(t *testing.T, text, nickname string, config map[string]any) []string {
	t.Helper()
	var got []string
	c := sdk.Call{Config: config, Event: sdk.Event{
		Message:  text,
		Nickname: nickname,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r.Text)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
	return got
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

// cardName 从「『正位』的『牌名』」中取出牌名。
func cardName(t *testing.T, reply string) string {
	t.Helper()
	marker := "的『"
	start := strings.Index(reply, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.Index(reply[start:], "』")
	if end < 0 {
		return ""
	}
	return reply[start : start+end]
}

func TestDeckSize(t *testing.T) {
	if len(deck) != 78 {
		t.Fatalf("牌库数量 = %d, want 78", len(deck))
	}
	if deck[0].Name != "愚者" || deck[0].Description != "新的开始、冒险、自信、乐观、好的时机" {
		t.Fatalf("未使用上游 tarots.json: %#v", deck[0])
	}
	if deck[0].ImgURL != "MajorArcana/0.png" {
		t.Fatalf("牌面缺少上游 imgUrl: %#v", deck[0])
	}
	seen := map[string]bool{}
	for _, crd := range deck {
		if crd.Name == "" || crd.Description == "" || crd.Reverse == "" || crd.ImgURL == "" {
			t.Fatalf("牌面数据不完整: %#v", crd)
		}
		if seen[crd.Name] {
			t.Fatalf("重复的牌名: %s", crd.Name)
		}
		seen[crd.Name] = true
	}
}

func TestDrawRange(t *testing.T) {
	cases := []struct {
		cardType   string
		start, end int
	}{
		{"塔罗牌", 0, 22},
		{"大阿卡纳", 0, 22},
		{"大阿尔卡纳", 0, 22},
		{"小阿卡纳", 22, 78},
		{"小阿尔卡纳", 22, 78},
		{"混合", 0, 78},
	}
	for _, tc := range cases {
		start, end := drawRange(tc.cardType)
		if start != tc.start || end != tc.end {
			t.Fatalf("drawRange(%q) = (%d, %d), want (%d, %d)", tc.cardType, start, end, tc.start, tc.end)
		}
	}
}

func TestDrawSingle(t *testing.T) {
	cases := []struct {
		trigger string
		start   int
		end     int
	}{
		{"抽塔罗牌", 0, 22},
		{"抽大阿卡纳", 0, 22},
		{"抽小阿卡纳", 22, 78},
	}
	valid := map[string]bool{}
	for _, tc := range cases {
		valid = map[string]bool{}
		for _, crd := range deck[tc.start:tc.end] {
			valid[crd.Name] = true
		}
		for i := 0; i < 20; i++ {
			got := run(t, tc.trigger, "")
			if len(got) != 1 {
				t.Fatalf("%q 回复条数 = %d, want 1", tc.trigger, len(got))
			}
			if !strings.Contains(got[0], "『正位』") && !strings.Contains(got[0], "『逆位』") {
				t.Fatalf("%q 回复缺少正逆位: %q", tc.trigger, got[0])
			}
			if !strings.Contains(got[0], "其释义为: ") {
				t.Fatalf("%q 回复缺少释义: %q", tc.trigger, got[0])
			}
			name := cardName(t, got[0])
			if !valid[name] {
				t.Fatalf("%q 抽到了范围外的牌 %q", tc.trigger, name)
			}
		}
	}
}

func TestDrawMultiple(t *testing.T) {
	got := run(t, "抽3张小阿卡纳", "")
	if len(got) != 1 {
		t.Fatalf("回复条数 = %d, want 1", len(got))
	}
	if n := strings.Count(got[0], "其释义为: "); n != 3 {
		t.Fatalf("抽到 %d 张牌, want 3: %q", n, got[0])
	}
	if !strings.Contains(got[0], "3 张塔罗牌") {
		t.Fatalf("回复缺少张数提示: %q", got[0])
	}
}

func TestDrawInvalidCount(t *testing.T) {
	if got := run(t, "抽0张塔罗牌", ""); len(got) != 1 || !strings.Contains(got[0], "张数必须为正") {
		t.Fatalf("抽0张回复 = %v", got)
	}
	if got := run(t, "抽99张塔罗牌", ""); len(got) != 1 || !strings.Contains(got[0], "抽取张数过多") {
		t.Fatalf("抽99张回复 = %v", got)
	}
}

func TestExplainKnownCard(t *testing.T) {
	got := run(t, "解塔罗牌愚者", "")
	if len(got) != 1 {
		t.Fatalf("回复条数 = %d, want 1", len(got))
	}
	for _, want := range []string{"愚者", "『正位』", "『逆位』"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("解牌回复缺少 %q: %q", want, got[0])
		}
	}
}

func TestExplainUnknownCard(t *testing.T) {
	got := run(t, "解塔罗牌不存在的牌", "")
	if len(got) != 1 {
		t.Fatalf("回复条数 = %d, want 1", len(got))
	}
	for _, want := range []string{"没有找到不存在的牌噢~", "塔罗牌列表", "大阿尔卡纳", "小阿尔卡纳"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("牌库列表回复缺少 %q: %q", want, got[0])
		}
	}
}

func TestFormations(t *testing.T) {
	types := []string{"塔罗", "大阿卡纳", "大阿尔卡纳", "小阿卡纳", "小阿尔卡纳", "混合"}
	for _, f := range formations {
		for _, cardType := range types {
			got := run(t, cardType+"牌阵"+f.Name, "测试者")
			if len(got) != 2 {
				t.Fatalf("%s牌阵%s 回复条数 = %d, want 2", cardType, f.Name, len(got))
			}
			if !strings.Contains(got[0], "少女祈祷中...") {
				t.Fatalf("缺少开场白: %q", got[0])
			}
			result := got[1]
			if !strings.Contains(result, "测试者---"+f.Name) {
				t.Fatalf("牌阵标题错误: %q", result)
			}
			if n := strings.Count(result, "其释义为: "); n != f.Cards {
				t.Fatalf("%s 抽到 %d 张牌, want %d", f.Name, n, f.Cards)
			}
			for _, pos := range f.Positions {
				if !strings.Contains(result, pos+":『") {
					t.Fatalf("%s 缺少牌位 %q: %q", f.Name, pos, result)
				}
			}
		}
	}
}

func TestFormationUnknown(t *testing.T) {
	got := run(t, "塔罗牌阵不存在的牌阵", "")
	if len(got) != 1 {
		t.Fatalf("回复条数 = %d, want 1", len(got))
	}
	for _, want := range []string{"没有找到", "现有牌阵列表", "圣三角", "六芒星"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("牌阵列表回复缺少 %q: %q", want, got[0])
		}
	}
}

func TestIgnoreOtherText(t *testing.T) {
	if got := run(t, "今天天气不错", ""); len(got) != 0 {
		t.Fatalf("非触发文本不应回复: %v", got)
	}
}

func TestQuestionIsKept(t *testing.T) {
	got := run(t, "抽塔罗牌 我该不该换工作", "")
	if len(got) != 1 {
		t.Fatalf("回复条数 = %d, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], "问：我该不该换工作") {
		t.Fatalf("回复缺少问题: %q", got[0])
	}

	got = run(t, "塔罗牌阵圣三角 我该不该换工作", "测试者")
	if len(got) != 2 {
		t.Fatalf("牌阵回复条数 = %d, want 2: %v", len(got), got)
	}
	if !strings.Contains(got[1], "问：我该不该换工作") {
		t.Fatalf("牌阵回复缺少问题: %q", got[1])
	}
}

func TestAIInterpretation(t *testing.T) {
	var path, auth, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		var request chatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		for _, message := range request.Messages {
			body += message.Content
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"牌面显示你正在犹豫，可以先收集信息再决定。"}}]}`))
	}))
	defer server.Close()

	old := aiURL
	aiURL = func(string) string { return server.URL + "/chat/completions" }
	defer func() { aiURL = old }()

	config := map[string]any{"ai_base_url": server.URL, "ai_api_key": "secret", "ai_model": "test-model"}
	got := runCall(t, "抽塔罗牌 我该不该换工作", "", config)
	if len(got) != 2 {
		t.Fatalf("回复条数 = %d, want 2: %v", len(got), got)
	}
	if !strings.Contains(got[1], "塔罗解读：") || !strings.Contains(got[1], "牌面显示你正在犹豫") {
		t.Fatalf("解读回复 = %q", got[1])
	}
	if path != "/chat/completions" || auth != "Bearer secret" {
		t.Fatalf("path = %q auth = %q", path, auth)
	}
	if !strings.Contains(body, "我该不该换工作") || !strings.Contains(body, "其释义为: ") {
		t.Fatalf("请求未包含问题或牌面: %q", body)
	}
}

func TestAIFailureFallsBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	old := aiURL
	aiURL = func(string) string { return server.URL }
	defer func() { aiURL = old }()

	config := map[string]any{"ai_base_url": server.URL, "ai_api_key": "secret", "ai_model": "test-model"}
	got := runCall(t, "抽塔罗牌 我该不该换工作", "", config)
	if len(got) != 1 {
		t.Fatalf("AI 失败时应只返回牌面: %v", got)
	}
	if !strings.Contains(got[0], "问：我该不该换工作") {
		t.Fatalf("牌面回复缺少问题: %q", got[0])
	}
}

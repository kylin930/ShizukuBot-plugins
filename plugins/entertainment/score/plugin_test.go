package score

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"shizukubot/sdk"
)

type memState struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func newMemState() *memState {
	return &memState{values: map[string]json.RawMessage{}}
}

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw := m.values[key]
	if raw == nil {
		return nil, nil
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = append(json.RawMessage(nil), value...)
	return nil
}

type testEnv struct {
	st         *memState
	now        time.Time
	replies    []sdk.Reply
	htmlCalls  int
	lastHTML   string
	renderOpts sdk.RenderOptions
	renderErr  error
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := &testEnv{
		st:  newMemState(),
		now: time.Date(2026, 3, 10, 9, 0, 0, 0, time.Local),
	}
	oldNow, oldRand, oldRender := nowFunc, randIntn, renderHTML
	nowFunc = func() time.Time { return e.now }
	randIntn = func(int) int { return 0 }
	renderHTML = func(_ context.Context, doc []byte, opts sdk.RenderOptions) ([]byte, error) {
		e.htmlCalls++
		e.lastHTML = string(doc)
		e.renderOpts = opts
		if e.renderErr != nil {
			return nil, e.renderErr
		}
		return makePNG(t), nil
	}
	t.Cleanup(func() {
		nowFunc, randIntn, renderHTML = oldNow, oldRand, oldRender
	})
	return e
}

func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 64, G: 128, B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func (e *testEnv) runEvent(t *testing.T, ev sdk.Event, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev.Reply = func(_ context.Context, reply sdk.Reply) error {
		e.replies = append(e.replies, reply)
		return nil
	}
	c := sdk.Call{Event: ev, Config: cfg, State: e.st}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("Handle(%q) error: %v", ev.Message, err)
	}
}

func (e *testEnv) runAs(t *testing.T, uid, gid, nick, text string, cfg map[string]any) {
	t.Helper()
	e.runEvent(t, sdk.Event{Message: text, UserID: uid, GroupID: gid, Nickname: nick}, cfg)
}

func (e *testEnv) run(t *testing.T, text string) {
	t.Helper()
	e.runAs(t, "u1", "g1", "测试用户", text, nil)
}

func (e *testEnv) lastText() string {
	for i := len(e.replies) - 1; i >= 0; i-- {
		if e.replies[i].Kind == "text" {
			return e.replies[i].Text
		}
	}
	return ""
}

func (e *testEnv) textReplies() []string {
	var out []string
	for _, r := range e.replies {
		if r.Kind == "text" {
			out = append(out, r.Text)
		}
	}
	return out
}

func (e *testEnv) imageCount() int {
	n := 0
	for _, r := range e.replies {
		if r.Kind == "image" {
			n++
		}
	}
	return n
}

func (e *testEnv) call() sdk.Call { return sdk.Call{State: e.st} }

func (e *testEnv) getUser(t *testing.T, uid string) userState {
	t.Helper()
	u, err := loadUser(context.Background(), e.call(), uid)
	if err != nil {
		t.Fatalf("load user %s: %v", uid, err)
	}
	return u
}

func (e *testEnv) putUser(t *testing.T, u userState) {
	t.Helper()
	u.Level = getrank(u.Exp)
	if err := sdk.SaveJSON(context.Background(), e.st, userKey(u.UID), u); err != nil {
		t.Fatalf("save user %s: %v", u.UID, err)
	}
}

func (e *testEnv) putIndex(t *testing.T, key string, ids ...string) {
	t.Helper()
	if err := sdk.SaveJSON(context.Background(), e.st, key, ids); err != nil {
		t.Fatalf("save index %s: %v", key, err)
	}
}

func (e *testEnv) getStrings(t *testing.T, key string) []string {
	t.Helper()
	var list []string
	if err := sdk.LoadJSON(context.Background(), e.st, key, &list); err != nil {
		t.Fatalf("load %s: %v", key, err)
	}
	return list
}

func (e *testEnv) getInt(t *testing.T, key string) int {
	t.Helper()
	var v int
	if err := sdk.LoadJSON(context.Background(), e.st, key, &v); err != nil {
		t.Fatalf("load %s: %v", key, err)
	}
	return v
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func TestFirstSign(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "签到")

	if e.htmlCalls != 1 {
		t.Fatalf("render calls = %d, want 1", e.htmlCalls)
	}
	if e.renderOpts.Width != 640 || !e.renderOpts.FullPage {
		t.Fatalf("render options = %+v, want width 640 full page", e.renderOpts)
	}
	if e.imageCount() != 1 {
		t.Fatalf("image replies = %d, want 1", e.imageCount())
	}
	last := e.replies[len(e.replies)-1]
	if !strings.HasPrefix(last.URL, "data:image/png;base64,") {
		t.Fatalf("last reply URL = %q, want png data url", last.URL)
	}
	contains(t, e.lastHTML, "q1.qlogo.cn")
	contains(t, e.lastHTML, "测试用户")

	u := e.getUser(t, "u1")
	if u.Coins != 1 || u.Exp != 1 || u.Level != 0 || u.LastReward != 1 {
		t.Fatalf("user = %+v, want coins 1 exp 1 level 0 last reward 1", u)
	}
	if u.LastSign != "20260310" {
		t.Fatalf("last sign = %q, want 20260310", u.LastSign)
	}
	if !containsString(e.getStrings(t, usersIndexKey), "u1") {
		t.Fatalf("users index = %v, want u1", e.getStrings(t, usersIndexKey))
	}
	if !containsString(e.getStrings(t, "group:g1:signers"), "u1") {
		t.Fatalf("group signers = %v, want u1", e.getStrings(t, "group:g1:signers"))
	}
}

func TestSameDaySignIsRejected(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "签到")
	calls := e.htmlCalls

	e.run(t, "签到")
	if !strings.Contains(e.lastText(), "今天你已经签到过了") {
		t.Fatalf("last text = %q, want duplicate notice", e.lastText())
	}
	// 对齐上游：重复签到时重新发送今日签到图。
	if e.htmlCalls != calls+1 {
		t.Fatalf("duplicate sign render calls = %d, want %d", e.htmlCalls, calls+1)
	}
	if e.imageCount() != 1 {
		t.Fatalf("duplicate sign image = %d, want 1", e.imageCount())
	}
	contains(t, e.lastHTML, "今日已签到")
	u := e.getUser(t, "u1")
	if u.Coins != 1 || u.LastReward != 1 {
		t.Fatalf("duplicate sign changed state: %+v", u)
	}
}

func TestSignPresetSelection(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "签到 2")
	contains(t, e.lastHTML, `class="p2`)

	e.runAs(t, "u2", "g1", "另一个用户", "签到 4", nil)
	contains(t, e.lastText(), "未找到签到设定")
	if u := e.getUser(t, "u2"); u.LastSign != "" {
		t.Fatalf("invalid preset should not sign in: %+v", u)
	}
}

func TestLevelBoundary(t *testing.T) {
	tests := []struct {
		exp  int
		want int
	}{
		{0, 0}, {9, 0}, {10, 1}, {19, 1}, {20, 2}, {50, 3}, {1200, 10},
	}
	for _, tt := range tests {
		if got := getrank(tt.exp); got != tt.want {
			t.Fatalf("getrank(%d) = %d, want %d", tt.exp, got, tt.want)
		}
	}
	if got := nextRankScore(0); got != 10 {
		t.Fatalf("nextRankScore(0) = %d, want 10", got)
	}
	if got := nextRankScore(10); got != scoreMax {
		t.Fatalf("nextRankScore(10) = %d, want %d", got, scoreMax)
	}

	t.Run("level up", func(t *testing.T) {
		e := newTestEnv(t)
		e.putUser(t, userState{UID: "u1", Nickname: "测试用户", Exp: 9, LastSign: "20260308"})
		e.run(t, "签到")
		u := e.getUser(t, "u1")
		if u.Exp != 10 || u.Level != 1 || u.Coins != 6 {
			t.Fatalf("user = %+v, want exp 10 level 1 coins 6", u)
		}
	})

	t.Run("exp cap", func(t *testing.T) {
		e := newTestEnv(t)
		e.putUser(t, userState{UID: "u1", Nickname: "测试用户", Exp: 1199, LastSign: "20260308"})
		e.run(t, "签到")
		u := e.getUser(t, "u1")
		if u.Exp != 1200 || u.Level != 10 || u.Coins != 51 {
			t.Fatalf("user = %+v, want exp 1200 level 10 coins 51", u)
		}

		e.now = e.now.AddDate(0, 0, 1)
		e.run(t, "签到")
		u = e.getUser(t, "u1")
		if u.Exp != 1200 || u.Level != 10 || u.Coins != 102 {
			t.Fatalf("capped user = %+v, want exp 1200 level 10 coins 102", u)
		}
		if !containsAny(e.textReplies(), "你的等级已经达到上限") {
			t.Fatalf("text replies = %v, want cap notice", e.textReplies())
		}
	})
}

func TestWallet(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "签到")
	e.run(t, "查看我的钱包")
	if got, want := e.lastText(), "QQ号：u1，的钱包有 1ATRI币"; got != want {
		t.Fatalf("wallet text = %q, want %q", got, want)
	}

	e.run(t, "查看钱包余额 abc")
	contains(t, e.lastText(), "QQ号处理失败")

	e.runAs(t, "u1", "g1", "测试用户", "查看钱包余额 10002", nil)
	if got, want := e.lastText(), "QQ号：10002，的钱包有 0ATRI币"; got != want {
		t.Fatalf("wallet target text = %q, want %q", got, want)
	}

	e.runEvent(t, sdk.Event{
		Message: "查看我的钱包", UserID: "u1", GroupID: "g1", Nickname: "测试用户",
		Raw: json.RawMessage(`{"mentions":[{"id":"u3","is_you":false}]}`),
	}, nil)
	contains(t, e.lastText(), "QQ号：u3")
}

func TestWalletCommands(t *testing.T) {
	e := newTestEnv(t)
	cfg := map[string]any{"admin_users": []any{"admin"}}

	e.runAs(t, "u1", "g1", "测试用户", "设置硬币名称金币", cfg)
	contains(t, e.lastText(), "没有权限")

	e.runAs(t, "admin", "g1", "管理员", "设置硬币名称金币", cfg)
	if got := e.lastText(); got != "记住啦~" {
		t.Fatalf("set coin name reply = %q", got)
	}

	e.runAs(t, "admin", "g1", "管理员", "签到", cfg)
	contains(t, e.lastHTML, "金币")

	e.runAs(t, "admin", "g1", "管理员", "管理钱包余额 +100", cfg)
	contains(t, e.lastText(), "修改金额为：100")
	if got := e.getUser(t, "admin").Coins; got != 101 {
		t.Fatalf("admin coins = %d, want 101", got)
	}

	e.runEvent(t, sdk.Event{
		Message: `管理钱包余额 +50 <qqbot-at-user id="u2" />`, UserID: "admin", GroupID: "g1", Nickname: "管理员",
	}, cfg)
	contains(t, e.lastText(), "已修改用户:u2")
	if got := e.getUser(t, "u2").Coins; got != 50 {
		t.Fatalf("u2 coins = %d, want 50", got)
	}

	e.runEvent(t, sdk.Event{
		Message: `钱包转账 30 <qqbot-at-user id="u2" />`, UserID: "admin", GroupID: "g1", Nickname: "管理员",
	}, cfg)
	contains(t, e.lastText(), "转账:30金币")
	if got := e.getUser(t, "admin").Coins; got != 71 {
		t.Fatalf("admin coins after transfer = %d, want 71", got)
	}
	if got := e.getUser(t, "u2").Coins; got != 80 {
		t.Fatalf("u2 coins after transfer = %d, want 80", got)
	}

	e.runEvent(t, sdk.Event{
		Message: `钱包转账 999 <qqbot-at-user id="u2" />`, UserID: "admin", GroupID: "g1", Nickname: "管理员",
	}, cfg)
	contains(t, e.lastText(), "钱包余额不足")

	e.runAs(t, "admin", "g1", "管理员", "钱包转账 10", cfg)
	contains(t, e.lastText(), "获取被转方信息失败")

	e.runAs(t, "admin", "g1", "管理员", "钱包转账 abc", cfg)
	contains(t, e.lastText(), "输入额异常")

	e.runAs(t, "admin", "", "管理员", "钱包转账 10", cfg)
	contains(t, e.lastText(), "仅限群聊")

	e.runAs(t, "admin", "g1", "管理员", "管理钱包余额 -999", cfg)
	contains(t, e.lastText(), "扣款失败")

	e.runAs(t, "admin", "g1", "管理员", "管理钱包余额 abc", cfg)
	contains(t, e.lastText(), "输入的金额异常")
}

func TestLevelRankTopN(t *testing.T) {
	e := newTestEnv(t)
	var ids []string
	for i := 1; i <= 12; i++ {
		uid := fmt.Sprintf("u%02d", i)
		e.putUser(t, userState{UID: uid, Nickname: fmt.Sprintf("用户%02d", i), Exp: i})
		ids = append(ids, uid)
	}
	e.putIndex(t, usersIndexKey, ids...)

	e.run(t, "查看等级排名")
	if e.htmlCalls != 1 || e.imageCount() != 1 {
		t.Fatalf("rank render/image = %d/%d, want 1/1", e.htmlCalls, e.imageCount())
	}
	if e.renderOpts.Width != 720 || !e.renderOpts.FullPage {
		t.Fatalf("render options = %+v", e.renderOpts)
	}
	lastPos := -1
	for i := 12; i >= 3; i-- {
		name := fmt.Sprintf("用户%02d", i)
		pos := strings.Index(e.lastHTML, name)
		if pos < 0 {
			t.Fatalf("rank html missing %s", name)
		}
		if pos <= lastPos {
			t.Fatalf("rank html order wrong at %s", name)
		}
		lastPos = pos
	}
	if strings.Contains(e.lastHTML, "用户02") || strings.Contains(e.lastHTML, "用户01") {
		t.Fatalf("rank html should be top 10")
	}

	// 同一天再次查看应复用缓存，不重新渲染。
	e.run(t, "查看等级排名")
	if e.htmlCalls != 1 || e.imageCount() != 1 {
		t.Fatalf("cached rank render/image = %d/%d, want 1/1", e.htmlCalls, e.imageCount())
	}
	if !strings.HasPrefix(e.replies[len(e.replies)-1].URL, "data:image/png;base64,") {
		t.Fatalf("cached rank reply = %+v", e.replies[len(e.replies)-1])
	}

	// 跨天后缓存失效；渲染失败时回退文本。
	e.now = e.now.AddDate(0, 0, 1)
	e.renderErr = errors.New("boom")
	e.run(t, "查看等级排名")
	if e.htmlCalls != 2 {
		t.Fatalf("next day render calls = %d, want 2", e.htmlCalls)
	}
	text := e.lastText()
	contains(t, text, "等级排名")
	contains(t, text, "用户12")
	contains(t, text, "生成排名图失败")
	if e.imageCount() != 0 {
		t.Fatalf("fallback rank should not send image")
	}
}

func TestWalletRankOnlyCurrentGroup(t *testing.T) {
	e := newTestEnv(t)
	e.putUser(t, userState{UID: "u1", Nickname: "甲", Coins: 100, Exp: 1})
	e.putUser(t, userState{UID: "u2", Nickname: "乙", Coins: 50, Exp: 1})
	e.putUser(t, userState{UID: "u3", Nickname: "丙", Coins: 999, Exp: 1})
	e.putIndex(t, usersIndexKey, "u1", "u2", "u3")
	e.putIndex(t, "group:g1:signers", "u1", "u2")
	e.putIndex(t, "group:g2:signers", "u3")

	e.runAs(t, "u1", "g1", "甲", "查看钱包排名", nil)
	if e.imageCount() != 1 {
		t.Fatalf("wallet rank image = %d, want 1", e.imageCount())
	}
	if strings.Index(e.lastHTML, "甲") >= strings.Index(e.lastHTML, "乙") {
		t.Fatalf("wallet rank order wrong")
	}
	if strings.Contains(e.lastHTML, "丙") {
		t.Fatalf("wallet rank must not include other groups")
	}

	e.runAs(t, "u3", "g2", "丙", "查看钱包排名", nil)
	contains(t, e.lastHTML, "丙")
	if strings.Contains(e.lastHTML, "甲") {
		t.Fatalf("g2 rank leaked g1 user")
	}

	e.runAs(t, "u1", "", "甲", "查看钱包排名", nil)
	contains(t, e.lastText(), "仅限群聊")
}

func TestPresetPermissionAndPersistence(t *testing.T) {
	t.Run("no admin config denies everyone", func(t *testing.T) {
		e := newTestEnv(t)
		e.runAs(t, "u1", "g1", "测试用户", "设置签到预设 2", nil)
		contains(t, e.lastText(), "没有权限")
		if got := e.getInt(t, "group:g1:preset"); got != 0 {
			t.Fatalf("group preset = %d, want 0", got)
		}
	})

	t.Run("admin list denies others", func(t *testing.T) {
		e := newTestEnv(t)
		cfg := map[string]any{"admin_users": []any{"admin"}}
		e.runAs(t, "u1", "g1", "测试用户", "设置签到预设 3", cfg)
		contains(t, e.lastText(), "没有权限")
		if got := e.getInt(t, "group:g1:preset"); got != 0 {
			t.Fatalf("group preset = %d, want 0", got)
		}

		e.runAs(t, "admin", "g1", "管理员", "设置签到预设 3", cfg)
		contains(t, e.lastText(), "设置成功")
		if got := e.getInt(t, "group:g1:preset"); got != 3 {
			t.Fatalf("group preset = %d, want 3", got)
		}
	})

	t.Run("private preset persists on user", func(t *testing.T) {
		e := newTestEnv(t)
		cfg := map[string]any{"admin_users": []any{"u1"}}
		e.runAs(t, "u1", "", "测试用户", "设置签到预设 1", cfg)
		contains(t, e.lastText(), "设置成功")
		if got := e.getUser(t, "u1").Preset; got != 1 {
			t.Fatalf("user preset = %d, want 1", got)
		}
		e.runAs(t, "u1", "", "测试用户", "签到", cfg)
		contains(t, e.lastHTML, `class="p1`)
	})

	t.Run("invalid preset", func(t *testing.T) {
		e := newTestEnv(t)
		e.runAs(t, "u1", "g1", "测试用户", "设置签到预设 4", nil)
		contains(t, e.lastText(), "未找到签到设定")
	})
}

func TestRenderFailureFallsBackToText(t *testing.T) {
	e := newTestEnv(t)
	e.renderErr = errors.New("render boom")
	e.run(t, "签到")

	if e.htmlCalls != 1 {
		t.Fatalf("render calls = %d, want 1", e.htmlCalls)
	}
	if e.imageCount() != 0 {
		t.Fatalf("image replies = %d, want 0", e.imageCount())
	}
	text := e.lastText()
	contains(t, text, "签到成功")
	contains(t, text, "签到图生成失败")
	contains(t, text, "render boom")

	u := e.getUser(t, "u1")
	if u.Coins != 1 || u.Exp != 1 || u.LastReward != 1 {
		t.Fatalf("state not persisted on render failure: %+v", u)
	}
}

func TestBackgroundCommand(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "签到")

	e.run(t, "获得签到背景")
	if e.imageCount() != 1 {
		t.Fatalf("background image = %d, want 1", e.imageCount())
	}
	contains(t, e.lastHTML, "今日签到背景")
	contains(t, e.lastHTML, "测试用户")

	e.runEvent(t, sdk.Event{
		Message: "获得签到背景", UserID: "u1", GroupID: "g1", Nickname: "测试用户",
		Raw: json.RawMessage(`{"mentions":[{"id":"u2","is_you":false}]}`),
	}, nil)
	contains(t, e.lastText(), "签到背景加载失败")

	e.runAs(t, "u1", "", "测试用户", "获得签到背景", nil)
	contains(t, e.lastText(), "仅限群聊")
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func containsAny(list []string, want string) bool {
	for _, v := range list {
		if strings.Contains(v, want) {
			return true
		}
	}
	return false
}

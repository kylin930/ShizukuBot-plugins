package driftbottle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func resetPickLimits(t *testing.T) {
	t.Helper()
	pickMu.Lock()
	pickHits = map[string][]time.Time{}
	pickMu.Unlock()
	t.Cleanup(func() {
		pickMu.Lock()
		pickHits = map[string][]time.Time{}
		pickMu.Unlock()
	})
}

func call(t *testing.T, st sdk.State, event sdk.Event) (sdk.Reply, bool) {
	t.Helper()
	var got sdk.Reply
	replied := false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, State: st}); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.Manifest.Allows("state.write") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestThrowTooShort(t *testing.T) {
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", UserID: "u1", Message: "扔漂流瓶 太短了"})
	if !replied || !strings.Contains(got.Text, "过少") || got.Kind != "text" {
		t.Fatalf("reply = %q, kind = %q, replied = %v", got.Text, got.Kind, replied)
	}
}

func TestThrowAndPick(t *testing.T) {
	resetPickLimits(t)
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { nowFunc = old })

	st := memState{}
	const msg = "这是一条足够长的漂流瓶消息"
	got, replied := call(t, st, sdk.Event{GroupID: "g1", UserID: "u1", Nickname: "小明", Message: "扔漂流瓶 " + msg})
	if !replied || !strings.Contains(got.Text, "丢出去") {
		t.Fatalf("throw reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "捞漂流瓶") {
		t.Fatalf("throw kind = %q, keyboard = %q", got.Kind, got.Keyboard)
	}

	got, replied = call(t, st, sdk.Event{GroupID: "g2", UserID: "u2", Message: "捞漂流瓶"})
	if !replied {
		t.Fatal("pick did not reply")
	}
	for _, want := range []string{msg, "投递人: 小明(u1)", "群号: g1", "2026-11-06 12:00:00"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("pick reply missing %q: %q", want, got.Text)
		}
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "捞漂流瓶") {
		t.Fatalf("pick kind = %q, keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestThrowAlias(t *testing.T) {
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", UserID: "u1", Message: "throw 这是一条足够长的漂流瓶内容"})
	if !replied || !strings.Contains(got.Text, "丢出去") {
		t.Fatalf("reply = %q, replied = %v", got.Text, replied)
	}
}

func TestPickEmptySea(t *testing.T) {
	resetPickLimits(t)
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", UserID: "u1", Message: "pick"})
	if !replied || !strings.Contains(got.Text, "还没有") || got.Kind != "text" {
		t.Fatalf("reply = %q, kind = %q, replied = %v", got.Text, got.Kind, replied)
	}
}

// TestPickRateLimit 捞瓶按群限频 5 次/10 秒，超出静默。
func TestPickRateLimit(t *testing.T) {
	resetPickLimits(t)

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	raw, err := json.Marshal([]bottle{{ID: "1", UserID: "u1", Name: "小明", GroupID: "g1", Time: "2026-11-08 12:00:00", Msg: "这是一条足够长的漂流瓶消息"}})
	if err != nil {
		t.Fatal(err)
	}
	st := memState{bottlesKey: raw}

	for i := 0; i < pickBurst; i++ {
		if _, replied := call(t, st, sdk.Event{GroupID: "g1", UserID: "u1", Message: "捞漂流瓶"}); !replied {
			t.Fatalf("第 %d 次被错误限频", i+1)
		}
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g1", UserID: "u1", Message: "捞漂流瓶"}); replied {
		t.Fatal("超出限频仍回复")
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g2", UserID: "u2", Message: "捞漂流瓶"}); !replied {
		t.Fatal("限频不应影响其他群")
	}
	now = now.Add(pickWindow)
	if _, replied := call(t, st, sdk.Event{GroupID: "g1", UserID: "u1", Message: "捞漂流瓶"}); !replied {
		t.Fatal("等待窗口后应恢复")
	}
}

func TestSenderLabel(t *testing.T) {
	if got := senderLabel(bottle{UserID: "u1", Name: "小明"}); got != "小明(u1)" {
		t.Fatalf("got %q", got)
	}
	if got := senderLabel(bottle{UserID: "u2"}); got != "u2" {
		t.Fatalf("旧瓶子昵称缺失时 got %q", got)
	}
}

func TestPrivateIgnored(t *testing.T) {
	if _, replied := call(t, memState{}, sdk.Event{UserID: "u1", Message: "捞漂流瓶"}); replied {
		t.Fatal("private pick replied")
	}
	if _, replied := call(t, memState{}, sdk.Event{UserID: "u1", Message: "扔漂流瓶 这是一条私聊里的长消息"}); replied {
		t.Fatal("private throw replied")
	}
}

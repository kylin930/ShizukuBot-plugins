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

func call(t *testing.T, st sdk.State, event sdk.Event) (string, bool) {
	t.Helper()
	got, replied := "", false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r.Text
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
	if !replied || !strings.Contains(got, "过少") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestThrowAndPick(t *testing.T) {
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { nowFunc = old })

	st := memState{}
	const msg = "这是一条足够长的漂流瓶消息"
	got, replied := call(t, st, sdk.Event{GroupID: "g1", UserID: "u1", Message: "扔漂流瓶 " + msg})
	if !replied || !strings.Contains(got, "丢出去") {
		t.Fatalf("throw reply = %q", got)
	}

	got, replied = call(t, st, sdk.Event{GroupID: "g2", UserID: "u2", Message: "捞漂流瓶"})
	if !replied {
		t.Fatal("pick did not reply")
	}
	for _, want := range []string{msg, "投递人: u1", "群号: g1", "2026-11-06 12:00:00"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pick reply missing %q: %q", want, got)
		}
	}
}

func TestThrowAlias(t *testing.T) {
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", UserID: "u1", Message: "throw 这是一条足够长的漂流瓶内容"})
	if !replied || !strings.Contains(got, "丢出去") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestPickEmptySea(t *testing.T) {
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", UserID: "u1", Message: "pick"})
	if !replied || !strings.Contains(got, "还没有") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
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

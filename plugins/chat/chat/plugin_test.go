package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return m[key], nil
}

func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func call(t *testing.T, st sdk.State, event sdk.Event) (string, bool) {
	t.Helper()
	replied := false
	got := ""
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
	if !d.Manifest.Allows("state.write") || !d.Manifest.Allows("state.read") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestMentionOnlyReplies(t *testing.T) {
	old := pick
	pick = func(int) int { return 0 }
	defer func() { pick = old }()

	got, replied := call(t, memState{}, sdk.Event{
		GroupID: "g",
		Message: "<@100>",
		Raw:     json.RawMessage(`{"mentions":[{"id":"100","is_you":true}]}`),
	})
	if !replied || got != replies[0] {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestAirConditioner(t *testing.T) {
	st := memState{}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "空调开"}); got != "❄️哔~" {
		t.Fatalf("开 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "群温度"}); got != "❄️风速中\n群温度 26℃" {
		t.Fatalf("温度 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "设置温度30"}); got != "❄️风速中\n群温度 30℃" {
		t.Fatalf("设置 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "空调关"}); got != "💤哔~" {
		t.Fatalf("关 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "设置温度30"}); got != "💤\n群温度 26℃" {
		t.Fatalf("关闭时设置 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "other", Message: "群温度"}); got != "💤\n群温度 26℃" {
		t.Fatalf("其他群 = %q", got)
	}
}

func TestUnmatchedMessageIgnored(t *testing.T) {
	if _, replied := call(t, memState{}, sdk.Event{GroupID: "g", Message: "今天天气不错"}); replied {
		t.Fatal("unexpected reply")
	}
}

func TestStateErrorStillReplies(t *testing.T) {
	st := failingState{}
	if got, replied := call(t, st, sdk.Event{GroupID: "g", Message: "群温度"}); !replied || got != "💤\n群温度 26℃" {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

type failingState struct{}

func (failingState) Get(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("state unavailable")
}

func (failingState) Set(context.Context, string, json.RawMessage) error {
	return errors.New("state unavailable")
}

package diana

import (
	"context"
	"encoding/json"
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

func call(t *testing.T, c sdk.Call, event sdk.Event) (sdk.Reply, bool) {
	t.Helper()
	var got sdk.Reply
	replied := false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r
		return nil
	}
	c.Event = event
	if err := Handle(context.Background(), c); err != nil {
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

func TestEssayAndSick(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	got, replied := call(t, sdk.Call{State: memState{}}, sdk.Event{Message: "小作文"})
	if !replied || got.Text != essays[0] || got.Kind != "keyboard" {
		t.Fatalf("reply = %q kind = %q", got.Text, got.Kind)
	}
	if !strings.Contains(got.Keyboard, "小作文") {
		t.Fatalf("keyboard = %q", got.Keyboard)
	}

	got, _ = call(t, sdk.Call{State: memState{}}, sdk.Event{Message: "发大病"})
	if !strings.Contains(got.Text, "宝") {
		t.Fatalf("reply = %q", got.Text)
	}
}

func TestAddRequiresAdmin(t *testing.T) {
	got, _ := call(t, sdk.Call{State: memState{}}, sdk.Event{Message: addPrefix + " 这是一段自定义的小作文内容"})
	if !strings.Contains(got.Text, "没有权限") {
		t.Fatalf("reply = %q", got.Text)
	}
}

func TestAddAndPick(t *testing.T) {
	st := memState{}
	cfg := map[string]any{"admin_users": []any{"admin"}}
	got, _ := call(t, sdk.Call{State: st, Config: cfg},
		sdk.Event{UserID: "admin", Message: addPrefix + " 这是一段自定义的小作文内容"})
	if got.Text != "记住啦!" {
		t.Fatalf("reply = %q", got.Text)
	}

	old := pickIndex
	pickIndex = func(n int) int { return n - 1 }
	defer func() { pickIndex = old }()
	got, _ = call(t, sdk.Call{State: st}, sdk.Event{Message: "小作文"})
	if !strings.Contains(got.Text, "自定义的小作文") {
		t.Fatalf("reply = %q", got.Text)
	}
}

func TestAddTooShort(t *testing.T) {
	cfg := map[string]any{"admin_users": []string{"admin"}}
	got, _ := call(t, sdk.Call{State: memState{}, Config: cfg},
		sdk.Event{UserID: "admin", Message: addPrefix + " 太短"})
	if !strings.Contains(got.Text, "8 到 500") {
		t.Fatalf("reply = %q", got.Text)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	got, replied := call(t, sdk.Call{State: memState{}}, sdk.Event{Message: "作文"})
	if replied {
		t.Fatalf("unexpected reply %q", got.Text)
	}
}

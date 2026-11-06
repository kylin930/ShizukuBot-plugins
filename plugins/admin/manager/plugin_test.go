package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

type result struct {
	reply  string
	action sdk.Action
	acted  bool
}

func call(t *testing.T, config map[string]any, event sdk.Event) result {
	t.Helper()
	var r result
	event.Reply = func(_ context.Context, msg sdk.Reply) error {
		r.reply = msg.Text
		return nil
	}
	event.Act = func(_ context.Context, a sdk.Action) error {
		r.action, r.acted = a, true
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, Config: config}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"message.recall", "group.manage", "config.read"} {
		if !d.Manifest.Allows(p) {
			t.Fatalf("missing permission %s", p)
		}
	}
}

func TestMyID(t *testing.T) {
	r := call(t, nil, sdk.Event{GroupID: "g", UserID: "member", Message: "我的ID"})
	if !strings.Contains(r.reply, "member") {
		t.Fatalf("reply = %q", r.reply)
	}
}

func TestRecallCurrentMessage(t *testing.T) {
	r := call(t, nil, sdk.Event{GroupID: "g", UserID: "member", MessageID: "msg-1", Message: "撤回本条"})
	if !r.acted || r.action.Kind != sdk.ActionRecall || r.action.MessageID != "msg-1" {
		t.Fatalf("action = %+v, acted = %v", r.action, r.acted)
	}
}

func TestSelfMute(t *testing.T) {
	r := call(t, nil, sdk.Event{GroupID: "g", UserID: "member", Message: "我要自闭 10分钟"})
	if !r.acted || r.action.Kind != sdk.ActionMute || r.action.UserID != "member" || r.action.Seconds != 600 {
		t.Fatalf("action = %+v", r.action)
	}
}

func TestAdminMuteMentionedUser(t *testing.T) {
	config := map[string]any{"admin_users": []any{"admin"}}
	r := call(t, config, sdk.Event{
		GroupID: "g",
		UserID:  "admin",
		Message: `禁言 <qqbot-at-user id="target" /> 1分钟`,
	})
	if !r.acted || r.action.Kind != sdk.ActionMute || r.action.UserID != "target" || r.action.Seconds != 60 {
		t.Fatalf("action = %+v", r.action)
	}
}

func TestNonAdminDenied(t *testing.T) {
	config := map[string]any{"admin_users": []any{"admin"}}
	r := call(t, config, sdk.Event{
		GroupID: "g",
		UserID:  "member",
		Message: `禁言 <qqbot-at-user id="target" /> 1分钟`,
	})
	if r.acted {
		t.Fatalf("non-admin action executed: %+v", r.action)
	}
	if !strings.Contains(r.reply, "没有权限") {
		t.Fatalf("reply = %q", r.reply)
	}
}

func TestKickWithBlacklist(t *testing.T) {
	config := map[string]any{"admin_users": []string{"admin"}}
	r := call(t, config, sdk.Event{
		GroupID: "g",
		UserID:  "admin",
		Message: `踢出 <qqbot-at-user id="target" /> 并拉黑`,
	})
	if !r.acted || r.action.Kind != sdk.ActionKick || r.action.UserID != "target" || !r.action.Blacklist {
		t.Fatalf("action = %+v", r.action)
	}
}

func TestParseDuration(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"10分钟": 10 * time.Minute,
		"2小时":  2 * time.Hour,
		"3天":   3 * 24 * time.Hour,
		"99天":  30 * 24 * time.Hour,
	} {
		got, ok := parseDuration(input)
		if !ok || got != want {
			t.Fatalf("%q = %v, %v", input, got, ok)
		}
	}
	if _, ok := parseDuration("很久"); ok {
		t.Fatal("invalid duration accepted")
	}
}

package antiabuse

import (
	"context"
	"encoding/json"
	"errors"
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
	for _, p := range []string{"state.read", "state.write", "message.recall", "group.manage"} {
		if !d.Manifest.Allows(p) {
			t.Fatalf("missing permission %s: %v", p, d.Manifest.Permissions)
		}
	}
}

func TestDefaultWordDetected(t *testing.T) {
	got, replied := call(t, memState{}, sdk.Event{GroupID: "g", Message: "你这个傻逼"})
	if !replied || !strings.Contains(got, "傻逼") || !strings.Contains(got, "违禁词") {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func TestDefaultWordRecallsAndMutes(t *testing.T) {
	var actions []sdk.Action
	event := sdk.Event{
		GroupID:   "g",
		UserID:    "member",
		MessageID: "msg",
		Message:   "你这个傻逼",
		Act: func(_ context.Context, a sdk.Action) error {
			actions = append(actions, a)
			return nil
		},
	}
	reply, _ := call(t, memState{}, event)
	if len(actions) != 2 {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].Kind != sdk.ActionRecall || actions[0].MessageID != "msg" {
		t.Fatalf("recall = %+v", actions[0])
	}
	if actions[1].Kind != sdk.ActionMute || actions[1].UserID != "member" || actions[1].Seconds != 120 {
		t.Fatalf("mute = %+v", actions[1])
	}
	if !strings.Contains(reply, "已撤回并禁言") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestPunishFallsBackWhenHostDenies(t *testing.T) {
	event := sdk.Event{
		GroupID: "g",
		UserID:  "member",
		Message: "你这个傻逼",
		Act: func(context.Context, sdk.Action) error {
			return errors.New("denied")
		},
	}
	reply, _ := call(t, memState{}, event)
	if !strings.Contains(reply, "请注意言行") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestWordListCommands(t *testing.T) {
	st := memState{}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "查看违禁词"}); got != "本群违禁词有\n[]" {
		t.Fatalf("空列表 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "添加违禁词坏蛋"}); got != "成功" {
		t.Fatalf("添加 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "添加违禁词坏蛋"}); got != "成功" {
		t.Fatalf("重复添加 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "查看违禁词"}); got != "本群违禁词有\n[坏蛋]" {
		t.Fatalf("列表 = %q", got)
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: "你是坏蛋吧"}); !replied {
		t.Fatal("custom word not detected")
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "删除违禁词坏蛋"}); got != "成功" {
		t.Fatalf("删除 = %q", got)
	}
	if got, _ := call(t, st, sdk.Event{GroupID: "g", Message: "查看违禁词"}); got != "本群违禁词有\n[]" {
		t.Fatalf("删除后列表 = %q", got)
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: "你是坏蛋吧"}); replied {
		t.Fatal("deleted word still detected")
	}
}

func TestCommandsAreNotScanned(t *testing.T) {
	got, _ := call(t, memState{}, sdk.Event{GroupID: "g", Message: "添加违禁词傻逼"})
	if got != "成功" {
		t.Fatalf("reply = %q", got)
	}
}

func TestDeleteWithoutWords(t *testing.T) {
	if got, _ := call(t, memState{}, sdk.Event{GroupID: "g", Message: "删除违禁词坏蛋"}); !strings.Contains(got, "还没有") {
		t.Fatalf("reply = %q", got)
	}
}

func TestNormalizationAndPrivate(t *testing.T) {
	if _, replied := call(t, memState{}, sdk.Event{GroupID: "g", Message: "傻\n逼"}); !replied {
		t.Fatal("normalization should strip newlines before matching")
	}
	if _, replied := call(t, memState{}, sdk.Event{Message: "傻逼"}); replied {
		t.Fatal("private message replied")
	}
	if got, _ := call(t, memState{}, sdk.Event{GroupID: "g", Message: "添加违禁词"}); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
}

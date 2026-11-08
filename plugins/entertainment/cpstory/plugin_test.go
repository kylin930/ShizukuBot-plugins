package cpstory

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestCPStory(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "组cp 小明 小红", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got.Text, "{") || !strings.Contains(got.Text, "小明") || !strings.Contains(got.Text, "小红") {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "小明") {
		t.Fatalf("kind = %q keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestSeparators(t *testing.T) {
	for _, in := range []string{"磕cp，阿强、阿珍", "磕CP 阿强和 阿珍"} {
		if names := splitNames(strings.TrimPrefix(strings.TrimPrefix(in, "磕cp"), "磕CP")); len(names) != 2 {
			t.Fatalf("%q -> %v", in, names)
		}
	}
}

func TestMissingName(t *testing.T) {
	for _, message := range []string{"组cp 小明", "磕cp 小明"} {
		var got string
		c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		}}}
		if err := Handle(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if got != "请用空格分开两个人名" {
			t.Fatalf("%q reply = %q", message, got)
		}
	}
}

// TestGroupCPFromMentions 组cp 从 @ 消息里提取两个 QQ 号作为攻受。
func TestGroupCPFromMentions(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "组cp <@10001> <@10002>", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got.Text, "{") || !strings.Contains(got.Text, "10001") || !strings.Contains(got.Text, "10002") {
		t.Fatalf("reply = %q", got.Text)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "cpdd", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

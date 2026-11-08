package funny

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestJoke(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{
		Message:  "讲个笑话",
		Nickname: "小明",
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "%name") || !strings.Contains(got.Text, "小明") {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "讲个笑话") {
		t.Fatalf("kind = %q keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestPraiseWithoutNickname(t *testing.T) {
	old := pickIndex
	// 共用 jokes 表，最后几条是夸夸语料，固定取最后一条以便断言。
	pickIndex = func(int) int { return len(jokes) - 1 }
	defer func() { pickIndex = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "夸夸", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "你") || strings.Contains(got, "%name") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "讲个故事", Reply: func(_ context.Context, r sdk.Reply) error {
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

func TestPrefixIgnoresArguments(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	for _, message := range []string{"讲个笑话 @某人", "讲个笑话@123456", "讲个笑话 小明"} {
		var got sdk.Reply
		c := sdk.Call{Event: sdk.Event{Message: message, Nickname: "小红", Reply: func(_ context.Context, r sdk.Reply) error {
			got = r
			return nil
		}}}
		if err := Handle(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if got.Text == "" || !strings.Contains(got.Text, "小红") {
			t.Fatalf("%q reply = %q", message, got.Text)
		}
	}

	for _, message := range []string{"夸夸 @某人", "夸夸 小明"} {
		var got string
		c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		}}}
		if err := Handle(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if got == "" || strings.Contains(got, "%name") {
			t.Fatalf("%q reply = %q", message, got)
		}
	}
}

package tiangou

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestDiary(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "舔狗日记", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Text != diaries[0] {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "舔狗日记") {
		t.Fatalf("kind = %q keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestShort(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 1 }
	defer func() { pickIndex = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "舔狗语录", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != shorts[1] {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "舔狗", Reply: func(_ context.Context, r sdk.Reply) error {
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

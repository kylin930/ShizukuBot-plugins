package thesaurus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func atEvent(message string) sdk.Event {
	raw, _ := json.Marshal(map[string]any{"mentions": []map[string]any{{"is_you": true}}})
	return sdk.Event{Message: message, Raw: raw}
}

func TestMatchedReply(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got string
	e := atEvent("你好")
	e.Reply = func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: e}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "你好") {
		t.Fatalf("reply = %q", got)
	}
}

func TestFallback(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got string
	e := atEvent("今天路过一家店")
	e.Reply = func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: e}); err != nil {
		t.Fatal(err)
	}
	if got != fallbacks[0] {
		t.Fatalf("reply = %q", got)
	}
}

func TestEmptyMentionIgnored(t *testing.T) {
	var got string
	e := atEvent("")
	e.Reply = func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: e}); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("empty mention replied %q", got)
	}
}

func TestRequiresMention(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "你好", Reply: func(_ context.Context, r sdk.Reply) error {
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

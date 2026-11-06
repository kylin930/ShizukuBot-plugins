package chrev

import (
	"context"
	"testing"

	"shizukubot/sdk"
)

func reply(t *testing.T, message string) string {
	t.Helper()
	var got string
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReverse(t *testing.T) {
	// "abc" 倒序后逐字符翻转: c → ɔ, b → q, a → ɐ
	if got := reply(t, "翻转 abc"); got != "ɔqɐ" {
		t.Fatalf("reply = %q", got)
	}
}

func TestNonMatch(t *testing.T) {
	if got := reply(t, "翻转 你好"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestUsage(t *testing.T) {
	if got := reply(t, "翻转   "); got == "" {
		t.Fatal("expected usage reply")
	}
}

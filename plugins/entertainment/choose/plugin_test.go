package choose

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func run(t *testing.T, message string) string {
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

func TestChoose(t *testing.T) {
	got := run(t, "选择 可口可乐 还是 百事可乐")
	for _, want := range []string{"1, 可口可乐", "2, 百事可乐", "你最终会选"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply missing %q: %q", want, got)
		}
	}
}

func TestChooseIgnoresOtherMessages(t *testing.T) {
	if got := run(t, "今天吃什么"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestChooseSingleOption(t *testing.T) {
	if got := run(t, "选择 只有可乐"); !strings.Contains(got, "选项太少") {
		t.Fatalf("reply = %q", got)
	}
}

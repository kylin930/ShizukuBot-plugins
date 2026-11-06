package bmi

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func replyText(t *testing.T, args ...string) string {
	t.Helper()
	var got string
	c := sdk.Call{Args: args, Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBMI(t *testing.T) {
	got := replyText(t, "175", "145")
	if !strings.Contains(got, "BMI指数为") {
		t.Fatalf("reply = %q", got)
	}
}

func TestBMIUsage(t *testing.T) {
	if got := replyText(t, "175"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyText(t, "abc", "145"); !strings.Contains(got, "数字") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyText(t, "0", "145"); !strings.Contains(got, "不能为0") {
		t.Fatalf("reply = %q", got)
	}
}

package curse

import (
	"context"
	"strings"
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

func TestExplicitTriggers(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	if got := reply(t, "骂我"); got != minLevel[0] {
		t.Fatalf("reply = %q", got)
	}
	if got := reply(t, "大力骂我"); got != maxLevel[0] {
		t.Fatalf("reply = %q", got)
	}
}

func TestMentionedKeyword(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	if got := reply(t, "<@10000> 你妈"); got != maxLevel[0] {
		t.Fatalf("reply = %q", got)
	}
}

func TestKeywordWithoutMentionIgnored(t *testing.T) {
	if got := reply(t, "你妈"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	for _, message := range []string{"你好", "骂", "大力", "今天天气不错"} {
		if got := reply(t, message); got != "" {
			t.Fatalf("%q -> unexpected reply %q", message, got)
		}
	}
}

func TestListsArePopulated(t *testing.T) {
	if len(minLevel) == 0 || len(maxLevel) == 0 {
		t.Fatal("empty curse lists")
	}
	for _, line := range append(append([]string{}, minLevel...), maxLevel...) {
		if strings.TrimSpace(line) == "" {
			t.Fatal("blank curse line")
		}
	}
}

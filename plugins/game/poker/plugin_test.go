package poker

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

func TestPoker(t *testing.T) {
	for _, trigger := range []string{"抽扑克", "poker"} {
		got := reply(t, trigger)
		if !strings.HasPrefix(got, "你抽到了 ") {
			t.Fatalf("%s reply = %q", trigger, got)
		}
	}
}

func TestNonMatch(t *testing.T) {
	if got := reply(t, "抽扑克牌"); got != "" {
		t.Fatalf("unexpected reply %q", got)
	}
}

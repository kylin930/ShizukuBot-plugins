package omikuji

import (
	"context"
	"fmt"
	"testing"
	"time"

	"shizukubot/sdk"
)

func freeze(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func draw(t *testing.T, event sdk.Event) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDefinition(t *testing.T) {
	if err := Definition().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDrawSign(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	n := signNumber("u1", at)
	got := draw(t, sdk.Event{UserID: "u1", Message: "求签"})
	if len(got) != 2 {
		t.Fatalf("reply count = %d", len(got))
	}
	for i, r := range got {
		if r.Kind != "image" {
			t.Fatalf("reply %d kind = %q", i, r.Kind)
		}
		want := fmt.Sprintf("%s%d_%d.jpg", imageBase, n, i)
		if r.URL != want {
			t.Fatalf("reply %d url = %q, want %q", i, r.URL, want)
		}
	}
}

func TestZhanbuIsStablePerDay(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	first := draw(t, sdk.Event{UserID: "u1", Message: "占卜"})
	second := draw(t, sdk.Event{UserID: "u1", Message: "占卜"})
	if len(first) != 2 || len(second) != 2 || first[0].URL != second[0].URL {
		t.Fatalf("first = %v, second = %v", first, second)
	}
}

func TestNonMatch(t *testing.T) {
	if got := draw(t, sdk.Event{UserID: "u1", Message: "求签一下"}); len(got) != 0 {
		t.Fatalf("unexpected reply %v", got)
	}
}

func TestSignNumberBounds(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	for _, user := range []string{"", "u1", "123456789"} {
		if n := signNumber(user, at); n < 1 || n > maxNumber {
			t.Fatalf("signNumber(%q) = %d", user, n)
		}
	}
}

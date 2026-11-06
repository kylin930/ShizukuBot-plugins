package breakrepeat

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"shizukubot/sdk"
)

type memState map[string]json.RawMessage

func (m memState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m memState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

func call(t *testing.T, st sdk.State, event sdk.Event) (string, bool) {
	t.Helper()
	got, replied := "", false
	event.Reply = func(_ context.Context, r sdk.Reply) error {
		replied = true
		got = r.Text
		return nil
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, State: st}); err != nil {
		t.Fatal(err)
	}
	return got, replied
}

func TestDefinition(t *testing.T) {
	d := Definition()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.Manifest.Allows("state.write") {
		t.Fatal(d.Manifest.Permissions)
	}
}

func TestBreakRepeat(t *testing.T) {
	st := memState{}
	message := "复读一下"
	for i := 1; i <= 4; i++ {
		if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: message}); replied {
			t.Fatalf("replied too early at message %d", i)
		}
	}
	got, replied := call(t, st, sdk.Event{GroupID: "g", Message: message})
	if !replied {
		t.Fatal("repeat not broken")
	}
	if !sameRunes(got, message) {
		t.Fatalf("broken text = %q", got)
	}
	if got == message {
		t.Fatalf("text should be shuffled, got %q", got)
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: "新话题"}); replied {
		t.Fatal("fresh message replied")
	}
}

func TestDifferentMessageResetsCounter(t *testing.T) {
	st := memState{}
	for _, msg := range []string{"A", "A", "B", "A", "A", "A", "A"} {
		if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: msg}); replied {
			t.Fatalf("unexpected reply for %q", msg)
		}
	}
	if _, replied := call(t, st, sdk.Event{GroupID: "g", Message: "A"}); !replied {
		t.Fatal("expected break after five consecutive repeats")
	}
}

func TestPrivateMessageIgnored(t *testing.T) {
	if _, replied := call(t, memState{}, sdk.Event{Message: "复读"}); replied {
		t.Fatal("private message replied")
	}
}

func TestShortTextNotShuffled(t *testing.T) {
	st := memState{}
	for i := 0; i < 4; i++ {
		call(t, st, sdk.Event{GroupID: "g", Message: "嗯"})
	}
	got, replied := call(t, st, sdk.Event{GroupID: "g", Message: "嗯"})
	if !replied || got != "嗯" {
		t.Fatalf("reply = %q, replied = %v", got, replied)
	}
}

func sameRunes(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	ra, rb := []rune(a), []rune(b)
	sort.Slice(ra, func(i, j int) bool { return ra[i] < ra[j] })
	sort.Slice(rb, func(i, j int) bool { return rb[i] < rb[j] })
	return string(ra) == string(rb)
}

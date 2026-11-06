package ping

import (
	"context"
	"testing"

	"shizukubot/sdk"
)

func TestPong(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != "pong" {
		t.Fatalf("reply = %q", got)
	}
}

func TestInvalidHost(t *testing.T) {
	var got string
	c := sdk.Call{Args: []string{"http://bad host"}, Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got == "" || got == "pong" {
		t.Fatalf("reply = %q", got)
	}
}

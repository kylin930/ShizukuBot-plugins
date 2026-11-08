package xhstext

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestCorpusScale(t *testing.T) {
	if len(pengchang) != 384 || len(yougeng) != 248 {
		t.Fatalf("语料数量 = %d/%d, want 384/248", len(pengchang), len(yougeng))
	}
}

func TestPengchang(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "捧场", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Text != pengchang[0] {
		t.Fatalf("reply = %q", got.Text)
	}
	if got.Kind != "keyboard" || !strings.Contains(got.Keyboard, "捧场") {
		t.Fatalf("kind = %q keyboard = %q", got.Kind, got.Keyboard)
	}
}

func TestYougeng(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 2 }
	defer func() { pickIndex = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "有梗", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got != yougeng[2] {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "捧个场", Reply: func(_ context.Context, r sdk.Reply) error {
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

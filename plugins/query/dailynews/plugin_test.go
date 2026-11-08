package dailynews

import (
	"context"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dailynews"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

func TestHandle(t *testing.T) {
	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "今日早报", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "image" || got.URL != imageURL {
		t.Fatalf("reply = %+v", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "早报", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "" {
		t.Fatalf("unexpected reply %+v", got)
	}
}

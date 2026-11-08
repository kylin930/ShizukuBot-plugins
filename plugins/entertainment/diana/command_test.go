package diana

import (
	"context"
	"testing"

	"shizukubot/sdk"
)

type commandRegistrar struct{ commands map[string]sdk.CommandHandler }

func (r *commandRegistrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

func (r *commandRegistrar) Command(name string, handler sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = handler
	return nil
}

type commandHost struct{ *commandRegistrar }

func (h *commandHost) Context() context.Context { return context.Background() }
func (h *commandHost) BotID() string            { return "bot" }
func (h *commandHost) Config() map[string]any   { return nil }
func (h *commandHost) State() sdk.State         { return nil }
func (h *commandHost) Log(string)               {}

func TestRegistersCommand(t *testing.T) {
	definition := Definition()
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	registrar := &commandRegistrar{}
	if err := definition.New().Load(context.Background(), &commandHost{registrar}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registrar.commands["diana"]; !ok {
		t.Fatalf("commands = %v", registrar.commands)
	}
}

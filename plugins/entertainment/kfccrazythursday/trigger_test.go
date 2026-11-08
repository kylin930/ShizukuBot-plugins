package kfccrazythursday

import (
	"context"
	"testing"

	"shizukubot/sdk"
)

type cmdRegistrar struct {
	commands map[string]sdk.CommandHandler
}

func (r *cmdRegistrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

func (r *cmdRegistrar) Command(name string, h sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = h
	return nil
}

type cmdHost struct{ *cmdRegistrar }

func (h *cmdHost) Context() context.Context { return context.Background() }
func (h *cmdHost) BotID() string            { return "bot" }
func (h *cmdHost) Config() map[string]any   { return nil }
func (h *cmdHost) State() sdk.State         { return nil }
func (h *cmdHost) Log(string)               {}

func TestDefinitionRegistersCommand(t *testing.T) {
	def := Definition()
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	reg := &cmdRegistrar{}
	if err := def.New().Load(context.Background(), &cmdHost{cmdRegistrar: reg}); err != nil {
		t.Fatal(err)
	}
	if reg.commands["kfc-crazy-thursday"] == nil {
		t.Fatalf("commands = %v", reg.commands)
	}
}

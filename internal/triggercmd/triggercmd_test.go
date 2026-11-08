package triggercmd

import (
	"context"
	"errors"
	"testing"

	"shizukubot/sdk"
)

type fakeRegistrar struct {
	events   []sdk.EventType
	commands map[string]sdk.CommandHandler
	failOn   string
}

func (f *fakeRegistrar) On(typ sdk.EventType, h sdk.EventHandler) error {
	f.events = append(f.events, typ)
	return nil
}

func (f *fakeRegistrar) Command(name string, h sdk.CommandHandler) error {
	if name == f.failOn {
		return errors.New("register failed")
	}
	if f.commands == nil {
		f.commands = map[string]sdk.CommandHandler{}
	}
	f.commands[name] = h
	return nil
}

func load(t *testing.T, def sdk.Definition, f *fakeRegistrar) {
	t.Helper()
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	ext := def.New()
	host := &fakeHost{fakeRegistrar: f}
	if err := ext.Load(context.Background(), host); err != nil {
		t.Fatal(err)
	}
}

type fakeHost struct{ *fakeRegistrar }

func (h *fakeHost) Context() context.Context { return context.Background() }
func (h *fakeHost) BotID() string            { return "bot" }
func (h *fakeHost) Config() map[string]any   { return nil }
func (h *fakeHost) State() sdk.State         { return nil }
func (h *fakeHost) Log(string)               {}

func meta() sdk.Meta {
	return sdk.Meta{ID: "demo", Name: "演示", Version: "1.0.0"}
}

func TestEventPluginRegistersCommandsAndDedupesEvents(t *testing.T) {
	var got string
	handler := func(_ context.Context, c sdk.Call) error {
		got = c.Event.Message
		return nil
	}
	f := &fakeRegistrar{}
	load(t, EventPlugin(meta(), []sdk.EventType{sdk.GroupMessage, sdk.GroupMessage, sdk.C2CMessage}, handler,
		[]Command{{Name: "demo", Trigger: "演示"}}), f)

	if len(f.events) != 2 {
		t.Fatalf("events = %v", f.events)
	}
	if len(f.commands) != 1 {
		t.Fatalf("commands = %v", f.commands)
	}
	if err := f.commands["demo"](context.Background(), sdk.Call{
		Event: sdk.Event{Message: "/demo 参数"},
		Args:  []string{"参数"},
	}); err != nil {
		t.Fatal(err)
	}
	if got != "演示 参数" {
		t.Fatalf("handler message = %q", got)
	}
}

func TestWrapDoesNotMutateOriginalEvent(t *testing.T) {
	handler := func(_ context.Context, c sdk.Call) error {
		if c.Event.Message != "演示" {
			t.Fatalf("message = %q", c.Event.Message)
		}
		return nil
	}
	c := sdk.Call{Event: sdk.Event{Message: "/demo"}}
	if err := Wrap(handler, Command{Name: "demo", Trigger: "演示"})(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.Event.Message != "/demo" {
		t.Fatalf("original event mutated: %q", c.Event.Message)
	}
}

func TestUsageCommandDoesNotForward(t *testing.T) {
	called := false
	handler := func(context.Context, sdk.Call) error { called = true; return nil }
	var reply string
	c := sdk.Call{
		Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
			reply = r.Text
			return nil
		}},
		Args: []string{"任意"},
	}
	if err := Wrap(handler, Command{Name: "demo", Usage: "自动触发，无需指令"})(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("usage command should not forward to handler")
	}
	if reply != "自动触发，无需指令" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestDefineWithoutEvents(t *testing.T) {
	f := &fakeRegistrar{}
	load(t, Define(meta(), []Command{{Name: "demo", Trigger: "演示"}}), f)
	if len(f.events) != 0 || len(f.commands) != 1 {
		t.Fatalf("events=%v commands=%v", f.events, f.commands)
	}
}

func TestRegistrationErrorPropagates(t *testing.T) {
	f := &fakeRegistrar{failOn: "demo"}
	def := EventPlugin(meta(), []sdk.EventType{sdk.GroupMessage}, func(context.Context, sdk.Call) error { return nil },
		[]Command{{Name: "demo", Trigger: "演示"}})
	ext := def.New()
	if err := ext.Load(context.Background(), &fakeHost{fakeRegistrar: f}); err == nil {
		t.Fatal("expected registration error")
	}
}

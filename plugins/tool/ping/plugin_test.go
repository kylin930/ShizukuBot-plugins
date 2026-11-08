package ping

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

type stubRegistrar struct {
	commands map[string]sdk.CommandHandler
}

func (r *stubRegistrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

func (r *stubRegistrar) Command(name string, handler sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = handler
	return nil
}

type stubHost struct{ *stubRegistrar }

func (h *stubHost) Context() context.Context { return context.Background() }
func (h *stubHost) BotID() string            { return "bot" }
func (h *stubHost) Config() map[string]any   { return nil }
func (h *stubHost) State() sdk.State         { return nil }
func (h *stubHost) Log(string)               {}

func assertCommands(t *testing.T, def sdk.Definition, names ...string) {
	t.Helper()
	if err := def.Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	registrar := &stubRegistrar{}
	if err := def.New().Load(context.Background(), &stubHost{stubRegistrar: registrar}); err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range names {
		if _, ok := registrar.commands[name]; !ok {
			t.Fatalf("missing command %q in %v", name, registrar.commands)
		}
	}
}

func TestTriggerCommands(t *testing.T) {
	assertCommands(t, Definition(), "ping")
}

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

func TestIPv6LiteralAccepted(t *testing.T) {
	for _, host := range []string{"::1", "[::1]", "[2001:db8::1]:443"} {
		var got string
		c := sdk.Call{Args: []string{host}, Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
			got = r.Text
			return nil
		}}}
		if err := Handle(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "请输入正确") {
			t.Fatalf("IPv6 %q 被判为非法: %q", host, got)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"example.com", "example.com", true},
		{"example.com:8080", "example.com:8080", true},
		{"1.2.3.4", "1.2.3.4", true},
		{"::1", "::1", true},
		{"[::1]", "::1", true},
		{"[::1]:80", "[::1]:80", true},
		{"http://bad host", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeHost(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("normalizeHost(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
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

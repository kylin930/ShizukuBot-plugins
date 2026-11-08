package bmi

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
	assertCommands(t, Definition(), "bmi", "bmi-calculator")
}

func replyText(t *testing.T, args ...string) string {
	t.Helper()
	var got string
	c := sdk.Call{Args: args, Event: sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBMI(t *testing.T) {
	got := replyText(t, "175", "145")
	if !strings.Contains(got, "BMI指数为") {
		t.Fatalf("reply = %q", got)
	}
}

func TestBMIUsage(t *testing.T) {
	if got := replyText(t, "175"); !strings.Contains(got, "用法") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyText(t, "abc", "145"); !strings.Contains(got, "数字") {
		t.Fatalf("reply = %q", got)
	}
	if got := replyText(t, "0", "145"); !strings.Contains(got, "不能为0") {
		t.Fatalf("reply = %q", got)
	}
}

func TestDefinitionConfigKeys(t *testing.T) {
	def := Definition()
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, key := range adviceKeys {
		if _, ok := def.Manifest.ConfigSchema.Properties[key]; !ok {
			t.Fatalf("missing config key %q", key)
		}
	}
}

func TestAdviceConfigurable(t *testing.T) {
	got := adviceFromConfig(map[string]any{"normal": []any{"自定义正常提示"}})
	if len(got["normal"]) != 1 || got["normal"][0] != "自定义正常提示" {
		t.Fatalf("normal = %v", got["normal"])
	}
	if len(got["underweight"]) == 0 {
		t.Fatal("missing default advice fallback")
	}
}

func TestAdviceBoundaries(t *testing.T) {
	cases := map[float64]string{
		18.4: "underweight",
		18.5: "normal",
		24.8: "normal",
		24.9: "overweight",
		29.9: "moderately_obese",
		34.9: "severely_obese",
		39.9: "very_severely_obese",
	}
	for bmi, want := range cases {
		got := adviceFor(bmi, defaultAdvice)
		found := false
		for _, item := range defaultAdvice[want] {
			if item == got {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("BMI %.1f -> %q, want one of %v", bmi, got, defaultAdvice[want])
		}
	}
}

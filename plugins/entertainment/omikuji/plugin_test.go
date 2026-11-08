package omikuji

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

// buttonCommands 解出键盘里每个按钮的指令，用于校验点击后能触发对应逻辑。
func buttonCommands(t *testing.T, payload string) []string {
	t.Helper()
	var kb sdk.Keyboard
	if err := json.Unmarshal([]byte(payload), &kb); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, row := range kb.Content.Rows {
		for _, button := range row.Buttons {
			cmds = append(cmds, button.Action.Data)
		}
	}
	return cmds
}

func hasCommand(cmds []string, want string) bool {
	for _, cmd := range cmds {
		if cmd == want {
			return true
		}
	}
	return false
}

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
	if len(got) != 1 {
		t.Fatalf("reply count = %d", len(got))
	}
	tail := got[0]
	if tail.Kind != "keyboard" {
		t.Fatalf("reply kind = %q", tail.Kind)
	}
	if tail.Mention != "u1" {
		t.Fatalf("mention = %q", tail.Mention)
	}
	for i := 0; i < 2; i++ {
		want := fmt.Sprintf("%s%d_%d.jpg", imageBase, n, i)
		if !strings.Contains(tail.Text, want) {
			t.Fatalf("reply text missing %q: %q", want, tail.Text)
		}
	}
	if !strings.Contains(tail.Text, "解签") {
		t.Fatalf("reply text = %q", tail.Text)
	}
	if cmds := buttonCommands(t, tail.Keyboard); !hasCommand(cmds, "解签") {
		t.Fatalf("tail commands = %v", cmds)
	}
}

func TestDrawSignFallsBack(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	n := signNumber("u1", at)
	var got []sdk.Reply
	event := sdk.Event{UserID: "u1", Message: "求签", Reply: func(_ context.Context, r sdk.Reply) error {
		if r.Kind == "keyboard" {
			return fmt.Errorf("keyboard unsupported")
		}
		got = append(got, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != "text" || got[0].Mention != "u1" {
		t.Fatalf("fallback replies = %+v", got)
	}
	for i := 1; i < 3; i++ {
		want := fmt.Sprintf("%s%d_%d.jpg", imageBase, n, i-1)
		if got[i].Kind != "image" || got[i].URL != want {
			t.Fatalf("fallback reply %d = %+v, want %q", i, got[i], want)
		}
	}
}

func TestZhanbuIsStablePerDay(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	first := draw(t, sdk.Event{UserID: "u1", Message: "占卜"})
	second := draw(t, sdk.Event{UserID: "u1", Message: "占卜"})
	if len(first) != 1 || len(second) != 1 || first[0].Text != second[0].Text {
		t.Fatalf("first = %v, second = %v", first, second)
	}
}

func TestJieqian(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	freeze(t, at)
	n := signNumber("u1", at)
	got := draw(t, sdk.Event{UserID: "u1", Message: "解签"})
	if len(got) != 1 || got[0].Kind != "text" || got[0].Mention != "u1" {
		t.Fatalf("reply = %+v", got)
	}
	if !strings.Contains(got[0].Text, "日本东京浅草寺观音灵签") {
		t.Fatalf("unexpected sign text: %q", got[0].Text)
	}
	if _, ok := kujiTexts[strconv.Itoa(n)]; !ok {
		t.Fatalf("missing kuji text for %d", n)
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

package poker

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func reply(t *testing.T, message string) sdk.Reply {
	t.Helper()
	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPoker(t *testing.T) {
	for _, trigger := range []string{"抽扑克", "poker"} {
		got := reply(t, trigger)
		const prefix = "data:image/jpeg;base64,"
		if got.Kind != "image" || !strings.HasPrefix(got.URL, prefix) {
			t.Fatalf("%s reply = %+v", trigger, got)
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got.URL, prefix))
		if err != nil || len(data) == 0 {
			t.Fatalf("%s image data invalid: %v", trigger, err)
		}
	}
}

// TestSpecialCards 校验上游卡池中的 Lynette/Lyney 也能被等概率抽到。
func TestSpecialCards(t *testing.T) {
	if len(cardDeck) != 54 {
		t.Fatalf("deck size = %d, want 54", len(cardDeck))
	}
	for _, target := range []string{"Lynette", "Lyney"} {
		index := -1
		for i, c := range cardDeck {
			if c.file == target {
				index = i
				break
			}
		}
		if index < 0 {
			t.Fatalf("special card %q missing", target)
		}
		old := pick
		pick = func(int) int { return index }
		var replies []sdk.Reply
		c := sdk.Call{Event: sdk.Event{Message: "抽扑克", Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r)
			return nil
		}}}
		err := Handle(context.Background(), c)
		pick = old
		if err != nil {
			t.Fatal(err)
		}
		if len(replies) != 2 || !strings.Contains(replies[0].Text, target) || replies[1].Kind != "image" {
			t.Fatalf("%s replies = %+v", target, replies)
		}
	}
}

func TestNonMatch(t *testing.T) {
	if got := reply(t, "抽扑克牌"); got.Kind != "" {
		t.Fatalf("unexpected reply %+v", got)
	}
}

func TestKeyboardBeforeImage(t *testing.T) {
	var replies []sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: "抽扑克", Reply: func(_ context.Context, r sdk.Reply) error {
		replies = append(replies, r)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || replies[0].Kind != "keyboard" ||
		!strings.Contains(replies[0].Keyboard, "再抽一张") || replies[1].Kind != "image" {
		t.Fatalf("replies = %+v", replies)
	}
}

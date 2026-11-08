package hyaku

import (
	"context"
	"strings"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hyaku"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

func collector() (sdk.Event, *[]sdk.Reply) {
	replies := new([]sdk.Reply)
	return sdk.Event{Reply: func(_ context.Context, r sdk.Reply) error {
		*replies = append(*replies, r)
		return nil
	}}, replies
}

func TestRandomPoem(t *testing.T) {
	oldPick, oldBase := pickIndex, imageBase
	pickIndex = func(int) int { return 16 }
	imageBase = "http://img/"
	defer func() { pickIndex, imageBase = oldPick, oldBase }()

	event, replies := collector()
	event.Message = "百人一首"
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	if len(*replies) != 3 {
		t.Fatalf("replies = %d", len(*replies))
	}
	first := (*replies)[0]
	if first.Kind != "keyboard" || !strings.Contains(first.Text, "在原業平朝臣") {
		t.Fatalf("first = %+v", first)
	}
	if (*replies)[1].URL != "http://img/img/017.jpg" || (*replies)[2].URL != "http://img/img/017.png" {
		t.Fatalf("images = %q %q", (*replies)[1].URL, (*replies)[2].URL)
	}
}

func TestByNumber(t *testing.T) {
	oldBase := imageBase
	imageBase = "http://img/"
	defer func() { imageBase = oldBase }()

	event, replies := collector()
	event.Message = "百人一首之 1"
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	if len(*replies) != 3 || !strings.Contains((*replies)[0].Text, "天智天皇") {
		t.Fatalf("replies = %+v", *replies)
	}
	text := (*replies)[0].Text
	wantOrder := []string{
		"●番号：1", "◉歌人：天智天皇",
		"○上の句：秋の田のかりほの庵の苫をあらみ",
		"○下の句：わが衣手は露にぬれつつ",
		"◎上の句ひらがな：あきのたのかりほのいほのとまをあらみ",
		"◎下の句ひらがな：わがころもではつゆにぬれつつ",
	}
	last := -1
	for _, want := range wantOrder {
		idx := strings.Index(text, want)
		if idx < 0 || idx < last {
			t.Fatalf("poem text %q missing or out of order %q", text, want)
		}
		last = idx
	}
}

func TestOutOfRange(t *testing.T) {
	event, replies := collector()
	event.Message = "百人一首之 101"
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0].Text, "1 到 100") {
		t.Fatalf("replies = %+v", *replies)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	event, replies := collector()
	event.Message = "百人一首?"
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		t.Fatal(err)
	}
	if len(*replies) != 0 {
		t.Fatalf("unexpected replies %+v", *replies)
	}
}

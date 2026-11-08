package dish

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func TestFindRecipe(t *testing.T) {
	var got sdk.Reply
	c := sdk.Call{Event: sdk.Event{
		Message:  "怎么做可乐鸡翅",
		Nickname: "小明",
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = r
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"客官小明", "可乐鸡翅", "做法辣！"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("reply missing %q: %q", want, got.Text)
		}
	}
	if got.Kind != "keyboard" {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestRandomRecipe(t *testing.T) {
	old := pickIndex
	pickIndex = func(int) int { return 0 }
	defer func() { pickIndex = old }()

	var got string
	c := sdk.Call{Event: sdk.Event{Message: "随机菜谱", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, recipes[0].Name) || !strings.Contains(got, "已为客官送上") {
		t.Fatalf("reply = %q", got)
	}
	if strings.Contains(got, "辣！") {
		t.Fatalf("随机菜谱不应带辣标志: %q", got)
	}
}

func TestRecipeNotFound(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "烹饪 佛跳墙", Reply: func(_ context.Context, r sdk.Reply) error {
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "没有佛跳墙") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	var got string
	c := sdk.Call{Event: sdk.Event{Message: "今天吃什么", Reply: func(_ context.Context, r sdk.Reply) error {
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

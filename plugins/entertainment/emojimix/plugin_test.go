package emojimix

import (
	"context"
	"testing"

	"shizukubot/sdk"
)

const (
	firstURL  = "https://www.gstatic.com/android/keyboard/emojikitchen/20201001/u1f604/u1f604_u1f600.png"
	secondURL = "https://www.gstatic.com/android/keyboard/emojikitchen/20201001/u1f600/u1f600_u1f604.png"
)

// call 执行一次事件并收集回复。
func call(t *testing.T, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{Event: sdk.Event{
		Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			got = append(got, r)
			return nil
		},
	}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("handle %q: %v", message, err)
	}
	return got
}

// stubHead 替换 HEAD 探测并记录访问过的地址。
func stubHead(t *testing.T, ok func(string) bool) *[]string {
	t.Helper()
	old := headOK
	var seen []string
	headOK = func(rawURL string) bool {
		seen = append(seen, rawURL)
		return ok(rawURL)
	}
	t.Cleanup(func() { headOK = old })
	return &seen
}

func TestMixFirstURL(t *testing.T) {
	seen := stubHead(t, func(string) bool { return true })
	got := call(t, "😄😀")
	if len(got) != 1 || got[0].Kind != "image" || got[0].URL != firstURL {
		t.Fatalf("replies = %+v", got)
	}
	if len(*seen) != 1 || (*seen)[0] != firstURL {
		t.Fatalf("head calls = %v", *seen)
	}
}

func TestMixIgnoresSpaces(t *testing.T) {
	stubHead(t, func(string) bool { return true })
	got := call(t, " 😄 😀 ")
	if len(got) != 1 || got[0].URL != firstURL {
		t.Fatalf("replies = %+v", got)
	}
}

func TestMixFallbackSecondURL(t *testing.T) {
	seen := stubHead(t, func(rawURL string) bool { return rawURL != firstURL })
	got := call(t, "😄😀")
	if len(got) != 1 || got[0].Kind != "image" || got[0].URL != secondURL {
		t.Fatalf("replies = %+v", got)
	}
	if len(*seen) != 2 || (*seen)[0] != firstURL || (*seen)[1] != secondURL {
		t.Fatalf("head calls = %v", *seen)
	}
}

func TestMixBothMissing(t *testing.T) {
	seen := stubHead(t, func(string) bool { return false })
	if got := call(t, "😄😀"); len(got) != 0 {
		t.Fatalf("replies = %+v", got)
	}
	if len(*seen) != 2 {
		t.Fatalf("head calls = %v", *seen)
	}
}

func TestMixNonMatch(t *testing.T) {
	seen := stubHead(t, func(string) bool { return true })
	for _, msg := range []string{"😄", "😄😀😊", "ab", "😄🦊", "你好"} {
		if got := call(t, msg); len(got) != 0 {
			t.Fatalf("%q replies = %+v", msg, got)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("head calls = %v", *seen)
	}
}

package alipayvoice

import (
	"context"
	"strings"
	"testing"

	"shizukubot/sdk"
)

func replyOf(message string) (sdk.Reply, error) {
	var got sdk.Reply
	e := sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = r
		return nil
	}}
	err := Handle(context.Background(), sdk.Call{Event: e})
	return got, err
}

func TestVoice(t *testing.T) {
	got, err := replyOf("支付宝到账 100")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "voice" {
		t.Fatalf("kind = %q", got.Kind)
	}
	if !strings.HasSuffix(got.URL, "/100.mp3") {
		t.Fatalf("url = %q", got.URL)
	}
}

func TestDecimal(t *testing.T) {
	got, err := replyOf("支付宝到账88.88")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.URL, "/88.88.mp3") {
		t.Fatalf("url = %q", got.URL)
	}
}

func TestInvalidAmount(t *testing.T) {
	got, err := replyOf("支付宝到账 ../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind == "voice" || !strings.Contains(got.Text, "格式") {
		t.Fatalf("reply = %+v", got)
	}
}

func TestEmptyAmount(t *testing.T) {
	got, err := replyOf("支付宝到账")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "用法") {
		t.Fatalf("reply = %+v", got)
	}
}

func TestIgnored(t *testing.T) {
	got, err := replyOf("今天吃什么")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "" || got.Text != "" {
		t.Fatalf("unexpected reply %+v", got)
	}
}

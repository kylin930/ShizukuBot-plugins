package wife

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"shizukubot/events"
	"shizukubot/sdk"
)

type testEnv struct {
	t       *testing.T
	userID  string
	groupID string
	replies []sdk.Reply
}

func newEnv(t *testing.T) *testEnv {
	return &testEnv{t: t, userID: "10001", groupID: "20002"}
}

func (e *testEnv) send(text string) {
	e.t.Helper()
	e.replies = nil
	event := sdk.Event{
		BotID:     "bot",
		Source:    events.Group,
		GroupID:   e.groupID,
		UserID:    e.userID,
		MessageID: "msg",
		Message:   text,
		Reply: func(_ context.Context, reply sdk.Reply) error {
			e.replies = append(e.replies, reply)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: event}); err != nil {
		e.t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (e *testEnv) texts() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "text" {
			out = append(out, reply.Text)
		}
	}
	return out
}

func (e *testEnv) lastText() string {
	texts := e.texts()
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func (e *testEnv) images() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "image" {
			out = append(out, reply.URL)
		}
	}
	return out
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for x := 0; x < 24; x++ {
		for y := 0; y < 24; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func stubImages(t *testing.T) {
	t.Helper()
	oldDir := wifeCacheDir
	wifeCacheDir = t.TempDir()
	oldDownload := downloadWifeImage
	data := testPNG(t)
	downloadWifeImage = func(context.Context, string) ([]byte, error) { return data, nil }
	oldTimer := scheduleTimerFunc
	scheduleTimerFunc = func(sdk.Call, *wifeGame, config) {}
	t.Cleanup(func() {
		wifeCacheDir = oldDir
		downloadWifeImage = oldDownload
		scheduleTimerFunc = oldTimer
		games.Range(func(key, _ any) bool {
			games.Delete(key)
			return true
		})
	})
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "wife" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestCards(t *testing.T) {
	cards, err := loadCards()
	if err != nil || len(cards) == 0 {
		t.Fatalf("cards: %v", err)
	}
	work, name := cardName("[86-不存在的战区-]芙拉蒂蕾娜·米利杰.jpeg")
	if work != "86-不存在的战区-" || name != "芙拉蒂蕾娜·米利杰" {
		t.Fatalf("cardName = %q %q", work, name)
	}
	if work, name := cardName("plain.jpeg"); work != "" || name != "" {
		t.Fatalf("plain cardName = %q %q", work, name)
	}
}

func TestDailyIndex(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	first := dailyIndex("10001", 100, now)
	if again := dailyIndex("10001", 100, now); again != first {
		t.Fatalf("same day changed: %d -> %d", first, again)
	}
	if next := dailyIndex("10001", 100, now.Add(24*time.Hour)); next == first {
		t.Fatalf("next day should change (got %d)", next)
	}
	if other := dailyIndex("10002", 100, now); other == first {
		// 允许极小概率碰撞，但 100 张里同日不同用户撞上时换一组
		if dailyIndex("10003", 100, now) == first && dailyIndex("10004", 100, now) == first {
			t.Fatal("different users all got the same index")
		}
	}
}

func TestDrawWife(t *testing.T) {
	stubImages(t)
	env := newEnv(t)
	env.send("抽老婆")
	texts := env.texts()
	if len(texts) == 0 || !strings.Contains(texts[0], "今天的二次元老婆是") {
		t.Fatalf("texts = %v", texts)
	}
	if len(env.images()) == 0 {
		t.Fatalf("images missing: %v", texts)
	}
}

func TestGuessGameCorrect(t *testing.T) {
	stubImages(t)
	env := newEnv(t)
	env.send("猜老婆")
	if len(env.images()) == 0 {
		t.Fatal("guess should send blurred image")
	}
	if !strings.Contains(env.lastText(), "请回答该二次元角色名字") {
		t.Fatalf("instructions = %q", env.lastText())
	}
	key := conversationKey(sdk.Call{Event: sdk.Event{BotID: "bot", Source: events.Group, GroupID: "20002"}})
	value, ok := games.Load(key)
	if !ok {
		t.Fatal("game not stored")
	}
	game := value.(*wifeGame)
	env.send(game.name + "酱")
	if !strings.Contains(env.lastText(), "太棒了,你猜对了") {
		t.Fatalf("correct reply = %q", env.lastText())
	}
	if _, exists := games.Load(key); exists {
		t.Fatal("game should be removed after finish")
	}
}

func TestGuessGameWrongThenSkip(t *testing.T) {
	stubImages(t)
	env := newEnv(t)
	env.send("猜老婆")
	key := conversationKey(sdk.Call{Event: sdk.Event{BotID: "bot", Source: events.Group, GroupID: "20002"}})
	value, _ := games.Load(key)
	game := value.(*wifeGame)
	env.send("完全不存在的角色酱")
	if !strings.Contains(env.lastText(), "回答错误") {
		t.Fatalf("wrong reply = %q", env.lastText())
	}
	if len(env.images()) == 0 {
		t.Fatal("wrong answer should send harder image")
	}
	game.mu.Lock()
	level := game.level
	game.mu.Unlock()
	if level != 2 {
		t.Fatalf("level = %d", level)
	}
	env.send("跳过")
	if !strings.Contains(env.lastText(), "已跳过猜题") {
		t.Fatalf("skip reply = %q", env.lastText())
	}
}

func TestGuessGameSingle(t *testing.T) {
	stubImages(t)
	env := newEnv(t)
	env.send("猜老婆")
	env.send("猜老婆")
	if !strings.Contains(env.lastText(), "已经有正在进行的游戏") {
		t.Fatalf("second game reply = %q", env.lastText())
	}
}

func TestGuessGameIgnoresOtherMessages(t *testing.T) {
	stubImages(t)
	env := newEnv(t)
	env.send("猜老婆")
	env.send("抽老婆")
	if !strings.Contains(env.lastText(), "今天的二次元老婆是") {
		t.Fatalf("normal command blocked: %q", env.lastText())
	}
}

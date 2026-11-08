package wife

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"shizukubot/sdk"
)

const (
	gameMaxTries = 3
)

var (
	answerPattern = regexp.MustCompile(`^(·)?[^酱]+酱|^跳过$`)
	blurSizes     = []int{0, 3, 5, 8}
	games         sync.Map
)

type wifeGame struct {
	mu       sync.Mutex
	key      string
	card     string
	work     string
	name     string
	answer   string
	image    []byte
	imageURL string
	level    int
	deadline time.Time
	done     bool
}

func conversationKey(c sdk.Call) string {
	parts := []string{c.Event.BotID, string(c.Event.Source)}
	if c.Event.GroupID != "" {
		parts = append(parts, "group", c.Event.GroupID)
	} else if c.Event.ChannelID != "" {
		parts = append(parts, "channel", c.Event.ChannelID)
	} else {
		parts = append(parts, "user", c.Event.UserID)
	}
	return strings.Join(parts, "|")
}

func startGame(ctx context.Context, c sdk.Call, cfg config) error {
	key := conversationKey(c)
	if value, ok := games.Load(key); ok {
		game := value.(*wifeGame)
		game.mu.Lock()
		active := !game.done && time.Now().Before(game.deadline)
		game.mu.Unlock()
		if active {
			return c.Reply(ctx, "已经有正在进行的游戏...")
		}
		games.Delete(key)
	}
	cards, err := loadCards()
	if err != nil || len(cards) == 0 {
		return c.Reply(ctx, "ERROR: 无法获取老婆库数据: "+errText(err))
	}
	card := cards[rand.Intn(len(cards))]
	work, name := cardName(card)
	if name == "" {
		return c.Reply(ctx, "ERROR: 老婆库数据格式异常")
	}
	data, imageURL, err := wifeImage(ctx, card)
	if err != nil {
		return c.Reply(ctx, "[猜老婆] 远程下载及本地读取图片均失败:\n"+err.Error())
	}
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return c.Reply(ctx, "[猜老婆] 图片解码失败:\n"+err.Error())
	}
	blurred, err := blurImage(img, gameMaxTries)
	if err != nil {
		return c.Reply(ctx, "[猜老婆] 图片生成失败:\n"+err.Error())
	}
	game := &wifeGame{
		key:      key,
		card:     card,
		work:     work,
		name:     name,
		answer:   strings.ToLower(name),
		image:    data,
		imageURL: imageURL,
		level:    gameMaxTries,
		deadline: time.Now().Add(time.Duration(cfg.GameTimeoutSeconds) * time.Second),
	}
	games.Store(key, game)
	if err := c.ReplyImageBytes(ctx, blurred); err != nil {
		_ = c.ReplyImage(ctx, imageURL)
	}
	if err := c.Reply(ctx, "请回答该二次元角色名字\n以“xxx酱”格式回答\n发送“跳过”结束猜题"); err != nil {
		return err
	}
	scheduleTimerFunc(c, game, cfg)
	return nil
}

// scheduleTimerFunc 便于单测关闭真实计时器。
var scheduleTimerFunc = scheduleGameTimers

// scheduleGameTimers 复刻上游的 105 秒提醒与 120 秒超时。
func scheduleGameTimers(c sdk.Call, game *wifeGame, cfg config) {
	timeout := time.Duration(cfg.GameTimeoutSeconds) * time.Second
	hintAfter := timeout - time.Duration(cfg.HintLeadSeconds)*time.Second
	time.AfterFunc(hintAfter, func() {
		game.mu.Lock()
		active := !game.done && time.Now().Before(game.deadline)
		game.mu.Unlock()
		if !active {
			return
		}
		replyGame(context.Background(), c, fmt.Sprintf("[猜老婆]你还有%ds作答时间", cfg.HintLeadSeconds))
	})
	time.AfterFunc(timeout, func() {
		finishGame(context.Background(), c, game, "timeout")
	})
}

func replyGame(ctx context.Context, c sdk.Call, text string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = c.Reply(ctx, text)
}

// handleAnswer 处理进行中的猜老婆答题；未命中返回 false。
func handleAnswer(ctx context.Context, c sdk.Call, cfg config, text string) bool {
	key := conversationKey(c)
	value, ok := games.Load(key)
	if !ok {
		return false
	}
	game := value.(*wifeGame)
	game.mu.Lock()
	expired := game.done || !time.Now().Before(game.deadline)
	game.mu.Unlock()
	if expired {
		finishGame(ctx, c, game, "timeout")
		games.Delete(key)
		return false
	}
	if !answerPattern.MatchString(text) {
		return false
	}
	answer := strings.TrimSpace(strings.ReplaceAll(text, "酱", ""))
	if answer == "" && text != "跳过" {
		return true
	}
	if text == "跳过" {
		finishGame(ctx, c, game, "skip")
		return true
	}
	game.mu.Lock()
	if strings.Contains(game.answer, strings.ToLower(answer)) {
		game.mu.Unlock()
		finishGame(ctx, c, game, "correct")
		return true
	}
	game.level--
	level := game.level
	work := game.work
	game.mu.Unlock()
	if level < 1 {
		finishGame(ctx, c, game, "failed")
		return true
	}
	img, err := blurImageBytes(game.image, level)
	if err != nil {
		replyGame(ctx, c, fmt.Sprintf("回答错误,你还有%d次机会\n请继续作答\n(提示：%s)", level, work))
		return true
	}
	hint := ""
	if level == 2 {
		hint = "(提示：" + work + ")\n"
	}
	_ = c.Reply(ctx, fmt.Sprintf("回答错误,你还有%d次机会\n%s请继续作答(难度降低)\n", level, hint))
	_ = c.ReplyImageBytes(ctx, img)
	return true
}

func finishGame(ctx context.Context, c sdk.Call, game *wifeGame, reason string) {
	game.mu.Lock()
	if game.done {
		game.mu.Unlock()
		return
	}
	game.done = true
	game.mu.Unlock()
	games.Delete(game.key)

	var text string
	switch reason {
	case "correct":
		text = "太棒了,你猜对了!\n角色是:\n" + game.name + "\n出自《" + game.work + "》\n"
	case "skip":
		text = "已跳过猜题\n角色是:\n" + game.name + "\n出自《" + game.work + "》\n"
	case "failed":
		text = "很遗憾,次数到了,游戏结束!\n角色是:\n" + game.name + "\n出自《" + game.work + "》\n"
	default:
		text = "[猜老婆]倒计时结束，游戏结束...\n角色是:\n" + game.name + "\n出自《" + game.work + "》\n"
	}
	_ = c.Reply(ctx, text)
	if len(game.image) > 0 && len(game.image) <= 5<<20 {
		_ = c.ReplyImageBytes(ctx, game.image)
	} else if game.imageURL != "" {
		_ = c.ReplyImage(ctx, game.imageURL)
	}
}

func blurImage(img image.Image, level int) ([]byte, error) {
	img = blur(img, level)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func blurImageBytes(data []byte, level int) ([]byte, error) {
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return blurImage(img, level)
}

func blur(img image.Image, level int) image.Image {
	if level < 0 {
		level = 0
	}
	if level >= len(blurSizes) {
		level = len(blurSizes) - 1
	}
	sigma := float64(blurSizes[level] * 3)
	if sigma <= 0 {
		return img
	}
	return imaging.Blur(img, sigma)
}

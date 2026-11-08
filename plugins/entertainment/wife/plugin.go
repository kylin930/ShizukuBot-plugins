// Package wife 移植 ZeroBot 老婆库：每日抽老婆与猜老婆小游戏。
package wife

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const pluginVersion = "1.0.0"

var cardPattern = regexp.MustCompile(`^\[(.*?)\](.*)\..*$`)

// config 保留可调项：猜老婆的提示与超时时间。
type config struct {
	GameTimeoutSeconds int
	HintLeadSeconds    int
}

func defaultConfig() config {
	return config{GameTimeoutSeconds: 120, HintLeadSeconds: 15}
}

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	readInt := func(key string, dst *int) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case float64:
				*dst = int(v)
			case int:
				*dst = v
			}
		}
	}
	readInt("gameTimeoutSeconds", &cfg.GameTimeoutSeconds)
	readInt("hintLeadSeconds", &cfg.HintLeadSeconds)
	if cfg.GameTimeoutSeconds < 30 {
		cfg.GameTimeoutSeconds = 30
	}
	if cfg.HintLeadSeconds < 1 || cfg.HintLeadSeconds >= cfg.GameTimeoutSeconds {
		cfg.HintLeadSeconds = 15
	}
	return cfg
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"gameTimeoutSeconds": {Type: "integer", Description: "猜老婆每题作答时间（秒）", Default: json.RawMessage(`120`)},
			"hintLeadSeconds":    {Type: "integer", Description: "结束前多少秒提醒剩余时间", Default: json.RawMessage(`15`)},
		},
	}
}

// Definition 订阅「抽老婆」「猜老婆」。
func Definition() sdk.Definition {
	events := append([]sdk.EventType{}, sdk.MessageEvents...)
	events = append(events, sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage)
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "wife",
		Name:        "老婆库",
		Version:     pluginVersion,
		Description: "从二次元角色库每日抽老婆，并支持猜老婆小游戏",
	}, events, Handle, []triggercmd.Command{
		{Name: "wife", Trigger: "抽老婆"},
	}, sdk.WithPermissions("state.read", "state.write", "config.read"), sdk.WithConfig(configSchema()))
}

// Handle 处理抽老婆、猜老婆与猜老婆答题。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	cfg := decodeConfig(c.Config)
	switch text {
	case "抽老婆":
		return drawWife(ctx, c)
	case "猜老婆":
		return startGame(ctx, c, cfg)
	}
	if handleAnswer(ctx, c, cfg, text) {
		return nil
	}
	return nil
}

var (
	cardsOnce sync.Once
	cardList  []string
	cardsErr  error
)

func loadCards() ([]string, error) {
	cardsOnce.Do(func() {
		data, err := asset("wife.json")
		if err != nil {
			cardsErr = err
			return
		}
		cardsErr = json.Unmarshal(data, &cardList)
	})
	return cardList, cardsErr
}

// cardName 解析形如「[作品]角色.jpeg」的卡片名。
func cardName(card string) (work, name string) {
	match := cardPattern.FindStringSubmatch(card)
	if len(match) >= 3 {
		return match[1], match[2]
	}
	return "", ""
}

// dailyIndex 复刻上游 RandSenderPerDayN：CRC64(ISO) 覆盖日期与用户 ID。
func dailyIndex(userID string, n int, now time.Time) int {
	if n <= 0 {
		return 0
	}
	sum := crc64.New(crc64.MakeTable(crc64.ISO))
	sum.Write([]byte(now.Format("20060102")))
	sum.Write([]byte(userID))
	r := rand.New(rand.NewSource(int64(sum.Sum64())))
	return r.Intn(n)
}

func drawWife(ctx context.Context, c sdk.Call) error {
	cards, err := loadCards()
	if err != nil || len(cards) == 0 {
		return c.Reply(ctx, "ERROR: 无法获取老婆库数据: "+errText(err))
	}
	card := cards[dailyIndex(c.Event.UserID, len(cards), time.Now())]
	work, name := cardName(card)
	message := "今天的二次元老婆是~【" + card + "】哒"
	if work != "" && name != "" {
		message = fmt.Sprintf("今天的二次元老婆是~来自【%s】的【%s】哒", work, name)
	}
	data, imageURL, err := wifeImage(ctx, card)
	if err != nil {
		_ = c.ReplyAt(ctx, message+"\n【图片下载失败: "+err.Error()+"】", c.Event.UserID)
		return nil
	}
	if len(data) > 0 && len(data) <= 5<<20 {
		if err := c.ReplyImageBytes(ctx, data); err != nil {
			_ = c.ReplyImage(ctx, imageURL)
		}
	} else if imageURL != "" {
		_ = c.ReplyImage(ctx, imageURL)
	}
	if err := c.ReplyAt(ctx, message, c.Event.UserID); err != nil {
		return err
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return "未知错误"
	}
	return err.Error()
}

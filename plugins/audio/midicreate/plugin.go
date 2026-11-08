package midicreate

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const defaultTimbre = 40

var (
	practiceRegexp     = regexp.MustCompile(`^(个人|团队)听音练习$`)
	practiceNoteRegexp = regexp.MustCompile(`^[A-G][b|#]?\d{0,2}$`)
)

const helpText = `midi音乐制作
- midi制作 <音符串>：合成并发送 WAV 语音
  音名 C~B，可加 # 或 b（如 C#6、Bb5），R 表示休止符
  默认时值四分音符，<n 加倍（如 <1），<-n 减半（如 <-1）
  音符之间用空格或逗号分隔
  示例：midi制作 CCGGAAGR FFEEDDCR GGFFEEDR
- 设置音色 <0~127>：切换 GM 音色（按群保存，私聊按用户保存）
- 个人听音练习 / 团队听音练习（仅群聊）`

// Definition 注册 midicreate 编译期插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(
		sdk.Meta{
			ID:          "midicreate",
			Name:        "midi音乐制作",
			Version:     "1.0.0",
			Description: "用音符串合成 midi 风格音乐，支持音色切换与听音练习",
		},
		sdk.MessageEvents,
		Handle,
		[]triggercmd.Command{{Name: "midicreate", Trigger: "midi制作"}},
		sdk.WithPermissions("state.read", "state.write"),
	)
}

// Handle 处理消息事件。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}

	if handled, err := handlePracticeAnswer(ctx, c, text); handled {
		return err
	}
	if sdk.MatchRegexp(text, practiceRegexp) != nil {
		return startPractice(ctx, c, text)
	}
	if arg, ok := sdk.MatchPrefix(text, "midi制作"); ok {
		return handleCreate(ctx, c, arg)
	}
	if arg, ok := sdk.MatchPrefix(text, "设置音色"); ok {
		return handleSetTimbre(ctx, c, arg)
	}
	return nil
}

func handleCreate(ctx context.Context, c sdk.Call, arg string) error {
	if arg == "" {
		return c.Reply(ctx, helpText)
	}
	events, err := parseMusic(arg)
	if err != nil {
		return c.Reply(ctx, "ERROR: 无法转换midi文件, "+err.Error())
	}
	if len(events) == 0 {
		return c.Reply(ctx, "没有可播放的音符")
	}
	return c.ReplyVoiceBytes(ctx, synthesize(events, getTimbre(ctx, c)), "audio/wav")
}

func handleSetTimbre(ctx context.Context, c sdk.Call, arg string) error {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return c.Reply(ctx, "ERROR: 音色应该是 0~127 的整数")
	}
	if n < 0 || n > 127 {
		return c.Reply(ctx, "音色应该在0~127之间")
	}
	if err := sdk.SaveJSON(ctx, c.State, timbreKey(c), n); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return c.Reply(ctx, "成功")
}

// timbreKey 对齐上游作用域：群聊按群保存，私聊按用户保存。
func timbreKey(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return "midicreate:timbre:g:" + c.Event.GroupID
	}
	return "midicreate:timbre:u:" + c.Event.UserID
}

// getTimbre 读取持久化的 GM 音色，默认 40（小提琴）。
func getTimbre(ctx context.Context, c sdk.Call) int {
	timbre := defaultTimbre
	_ = sdk.LoadJSON(ctx, c.State, timbreKey(c), &timbre)
	if timbre < 0 || timbre > 127 {
		return defaultTimbre
	}
	return timbre
}

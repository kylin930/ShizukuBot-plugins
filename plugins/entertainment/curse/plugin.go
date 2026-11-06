// Package curse 骂人反击：被骂时用内置语录回击。
package curse

import (
	"context"
	"math/rand/v2"
	"strings"

	"shizukubot/sdk"
)

var (
	// minLevel 对应「骂我」的轻度回击。
	minLevel = []string{
		"你是不是傻？这么明显都没看出来。",
		"就这？我还以为你能说出点什么。",
		"别闹了，你的话我连标点符号都不信。",
		"你这脑子是租来的吧，到期了记得还。",
		"说话前先过过脑子，虽然可能不太够用。",
		"你也就这点本事了，继续加油吧。",
		"建议你先去补补课，再来跟我抬杠。",
		"你的自信是从哪批发的？给我也来一份。",
		"别急着反驳，先想想自己说得对不对。",
		"你这话说得，跟没说一样。",
		"行吧，你开心就好。",
		"淡定，别把自己气坏了。",
		"你这理解能力，真是让人着急。",
		"你先冷静一下，我们再来聊。",
		"这话有点离谱，不过我已经习惯了。",
	}
	// maxLevel 对应「大力骂我」以及被 @ 辱骂时的回击。
	maxLevel = []string{
		"闭嘴吧，听你说话简直是在浪费我的时间。",
		"你这种人也就只能在键盘后面逞能了。",
		"别吠了，没人想听。",
		"脑子是个好东西，希望你也有一个。",
		"滚一边去，别在这儿丢人现眼。",
		"就你这水平，也配来跟我叫板？",
		"你除了会喷人，还会干什么？",
		"省省吧，你那点小伎俩早就被人看穿了。",
		"你活着就是为了给人添堵吗？",
		"要点脸吧，别把自己的无知当个性。",
		"跟你说话真费劲，回去练练再来。",
		"别再秀下限了，已经很下限了。",
		"你这嘴是借来的吧，急着还？",
		"差不多得了，你这丑态已经够看了。",
		"回你的窝里待着去，别出来乱咬。",
	}
	// keywords 与上游一致的辱骂触发词。
	keywords = []string{
		"他妈", "公交车", "你妈", "操", "屎", "去死", "快死", "我日", "逼",
		"尼玛", "艾滋", "癌症", "有病", "烦你", "你爹", "屮", "cnm",
	}
	// pickIndex 可在测试中替换。
	pickIndex = rand.IntN
)

// Definition subscribes to message events for the curse triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "curse",
		Name:        "骂人反击",
		Version:     "1.0.0",
		Description: "发送「骂我」「大力骂我」，或 @机器人辱骂，返回内置回击语录",
	}, sdk.MessageEvents, Handle)
}

// Handle 处理「骂我」「大力骂我」，以及 @机器人并带辱骂关键词时的回击。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "大力骂我"):
		return c.Reply(ctx, pick(maxLevel))
	case sdk.MatchFull(text, "骂我"):
		return c.Reply(ctx, pick(minLevel))
	}
	if sdk.Mentioned(c.Event) && containsAny(text, keywords) {
		return c.Reply(ctx, pick(maxLevel))
	}
	return nil
}

func pick(lines []string) string { return lines[pickIndex(len(lines))] }

func containsAny(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

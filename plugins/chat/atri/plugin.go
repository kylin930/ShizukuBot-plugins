// Package atri 亚托莉人格文本回复，语料来自 https://github.com/Kyomotoi/ATRI。
package atri

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// nowFunc 便于测试固定当前时间。
var nowFunc = time.Now

// pickIndex 与 chanceIndex 可在测试中替换；前者选语料，后者判定概率。
var (
	pickIndex   = rand.IntN
	chanceIndex = rand.IntN
)

var (
	morningWords = []string{"早哇", "早上好", "ohayo", "哦哈哟", "お早う", "早好", "早", "早早早"}
	noonWords    = []string{"中午好", "午安", "午好"}
	nightWords   = []string{"晚安", "oyasuminasai", "おやすみなさい", "晚好", "晚上好"}
	loveWords    = []string{"喜欢", "爱你", "爱", "suki", "daisuki", "すき", "好き", "贴贴", "老婆", "亲一个", "mua"}
	insultWords  = []string{"草你妈", "操你妈", "脑瘫", "废柴", "fw", "five", "废物", "战斗", "爬", "爪巴", "sb", "SB", "傻B"}
	praiseWords  = []string{"高性能", "太棒了", "すごい", "sugoi", "斯国一", "よかった"}
	reliefWords  = []string{"没事", "没关系", "大丈夫", "还好", "不要紧", "没出大问题", "没伤到哪"}
	askWords     = []string{"好吗", "是吗", "行不行", "能不能", "可不可以"}
)

// Definition subscribes to the ATRI personality triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "atri",
		Name:        "亚托莉人格",
		Version:     "1.0.0",
		Description: "基于 ATRI 语料的人格回复：问候、夸赞、撒娇与吐槽（纯文本）",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "atri", Trigger: "萝卜子"}})
}

// Handle 按触发词返回对应语料。上游在 1:00–5:59 让 ATRI 睡觉，不回应任何请求。
func Handle(ctx context.Context, c sdk.Call) error {
	if hour := nowFunc().Hour(); hour >= 1 && hour < 6 {
		return nil
	}
	text := strings.TrimSpace(sdk.Text(c.Event))
	switch {
	case sdk.MatchFull(text, "萝卜子"):
		return c.Reply(ctx, pick("萝卜子是对机器人的蔑称！", "是亚托莉......萝卜子可是对机器人的蔑称"))
	case inAny(text, morningWords):
		return c.Reply(ctx, morningReply())
	case inAny(text, noonWords):
		return c.Reply(ctx, noonReply())
	case inAny(text, nightWords):
		return c.Reply(ctx, nightReply())
	case sdk.MatchFull(text, "我好了"):
		return c.Reply(ctx, pick("不许好！", "憋回去！"))
	case sdk.MatchFull(text, "？") || sdk.MatchFull(text, "?") || sdk.MatchFull(text, "¿"):
		return c.Reply(ctx, pick("?", "？", "嗯？", "(。´・ω・)ん?", "ん？"))
	}

	mentioned := sdk.Mentioned(c.Event)
	if mentioned {
		switch {
		case containsAny(text, loveWords):
			return c.Reply(ctx, pick("我也最喜欢你了！", "欸嘿嘿……被发现了。", "贴贴！"))
		case containsAny(text, insultWords):
			return c.Reply(ctx, pick("哼！才不理你！", "呜……好过分。", "我、我可是高性能的，才不生气！"))
		case containsAny(text, praiseWords):
			return c.Reply(ctx, praiseReply())
		case containsAny(text, reliefWords):
			return c.Reply(ctx, reliefReply())
		case strings.Contains(text, "答应我"):
			return c.Reply(ctx, "我无法回应你的请求")
		}
	}
	if containsAny(text, askWords) {
		return c.Reply(ctx, pick("当然可以！", "嗯……可以哦。", "不可以！"))
	}
	// 上游「啊这」1/2、「离谱」3/5 概率回复，未命中时保持沉默。
	if strings.Contains(text, "啊这") {
		if chanceIndex(2) != 0 {
			return nil
		}
		return c.Reply(ctx, pick("啊这", "这……这不太好吧", "嗯……"))
	}
	if strings.Contains(text, "离谱") {
		if chanceIndex(5) >= 3 {
			return nil
		}
		return c.Reply(ctx, pick("?", "？", "嗯？", "(。´・ω・)ん?", "ん？", "确实离谱"))
	}
	return nil
}

// morningReply 按当前时段选择早安回复。
func morningReply() string {
	switch now := nowFunc().Hour(); {
	case now < 6:
		return pick("zzzz......", "zzzzzzzz......", "别...不要..zzz..那..zzz..", "...zzz....哧溜哧溜....")
	case now < 9:
		return pick("啊......早上好...(哈欠)", "唔......吧唧...早上...哈啊啊~~~\n早上好......",
			"早上好......", "早上好呜......呼啊啊~~~~", "吧唧吧唧......怎么了...已经早上了么...",
			"早上好！", "早上好......欸~~~脸好近呢")
	case now < 18:
		return pick("哼！这个点还早啥，昨晚干啥去了！？", "熬夜了对吧熬夜了对吧熬夜了对吧？？？！",
			"是不是熬夜是不是熬夜是不是熬夜？！")
	default:
		return pick("早个啥？哼唧！我都准备洗洗睡了！", "不是...你看看几点了，哼！", "晚上好哇")
	}
}

// noonReply 中午时段才回应午安。
func noonReply() string {
	if now := nowFunc().Hour(); now > 11 && now < 15 {
		return pick("午安w", "午觉要好好睡哦，ATRI会陪伴在你身旁的w",
			"嗯哼哼~睡吧，就像平常一样安眠吧~o(≧▽≦)o", "睡你午觉去！哼唧！！")
	}
	return "现在还不是午休时间哦"
}

// nightReply 按当前时段选择晚安回复。
func nightReply() string {
	switch now := nowFunc().Hour(); {
	case now < 6:
		return pick("zzzz......", "zzzzzzzz......", "别...不要..zzz..那..zzz..", "...zzz....哧溜哧溜....")
	case now < 11:
		return pick("你可猝死算了吧！", "？啊这", "亲，这边建议赶快去睡觉呢~~~", "不可忍不可忍不可忍！！为何这还不猝死！！")
	case now < 15:
		return pick("午安w", "午觉要好好睡哦，ATRI会陪伴在你身旁的w",
			"嗯哼哼~睡吧，就像平常一样安眠吧~o(≧▽≦)o", "睡你午觉去！哼唧！！")
	case now < 19:
		return pick("难不成？？晚上不想睡觉？？现在休息", "就......挺离谱的...现在睡觉", "现在还是白天哦，睡觉还太早了")
	default:
		return pick("嗯哼哼~睡吧，就像平常一样安眠吧~o(≧▽≦)o", "......(打瞌睡)",
			"呼...呼...已经睡着了哦~...呼......", "......我、我会在这守着你的，请务必好好睡着")
	}
}

// praiseReply 高性能夸赞的固定语料。
func praiseReply() string {
	return pick("当然，我是高性能的嘛~！", "小事一桩，我是高性能的嘛", "怎么样？还是我比较高性能吧？",
		"哼哼！我果然是高性能的呢！", "因为我是高性能的嘛！嗯哼！", "因为我是高性能的呢！",
		"哎呀~，我可真是太高性能了", "正是，因为我是高性能的", "是的。我是高性能的嘛♪",
		"毕竟我可是高性能的！", "嘿嘿，我的高性能发挥出来啦♪", "我果然是很高性能的机器人吧！",
		"是吧！谁叫我这么高性能呢！哼哼！", "交给我吧，有高性能的我陪着呢",
		"呣......我的高性能，毫无遗憾地施展出来了......")
}

// reliefReply 安慰语料。
func reliefReply() string {
	return pick("当然，我是高性能的嘛~！", "没事没事，因为我是高性能的嘛！嗯哼！", "没事的，因为我是高性能的呢！",
		"正是，因为我是高性能的", "是的。我是高性能的嘛♪", "毕竟我可是高性能的！",
		"那种程度的事不算什么的。\n别看我这样，我可是高性能的", "没问题的，我可是高性能的")
}

// pick 随机取一条语料。
func pick(list ...string) string { return list[pickIndex(len(list))] }

// inAny 判断文本是否与列表中的某一项完全相等。
func inAny(text string, list []string) bool {
	for _, w := range list {
		if text == w {
			return true
		}
	}
	return false
}

// containsAny 判断文本是否包含列表中的任意关键词。
func containsAny(text string, list []string) bool {
	for _, w := range list {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

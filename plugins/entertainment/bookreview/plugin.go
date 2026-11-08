// Package bookreview 随机书评：发送「随机书评」随机返回一条书评，发送「书评+关键词」检索书评。
package bookreview

import (
	"context"
	"fmt"
	"html"
	"math/rand"
	"strings"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// review 一条书评，包含书名、作者与评语，对应上游 bookreview 表中的一条记录。
type review struct {
	Book    string
	Author  string
	Comment string
}

// reviews 书评语料，沿用上游「哀伤雪刃推书」的风格。
var reviews = []review{
	{"诡秘之主", "爱潜水的乌贼", "克苏鲁神话与维多利亚时代碰撞出的厚重设定，序列途径让每一次升级都带着疯狂的代价。前期节奏偏慢，熬过第一卷就再也停不下来。"},
	{"凡人修仙传", "忘语", "没有金手指大开的爽感，只有韩立步步为营的算计与谨慎。凡人的底色贯穿始终，修真界的残酷被写得很实在，适合喜欢长线养成的读者。"},
	{"道诡异仙", "狐尾的笔", "把克苏鲁、修仙和精神病院缝合得恰到好处，主角的疯与世界的疯互为镜像。阅读体验压抑又上头，胆子小的建议白天看。"},
	{"赤心巡天", "情何以甚", "文笔在网文里属于上游，人物立得住，朝堂与江湖两条线互相咬合。缺点是节奏偏慢，追更需要耐心。"},
	{"大奉打更人", "卖报小郎君", "探案与修仙结合得轻巧，配角鲜活、笑点密集。爽点给得痛快，适合下班后放松，别太较真逻辑。"},
	{"长夜余火", "爱潜水的乌贼", "废土公路片式的末日旅行，乌贼把希望写得克制又动人。群像扎实，结局见仁见智，但过程值得。"},
	{"黎明之剑", "远瞳", "种田、工业化与魔法文明的推演细致到令人发指，喜欢看文明如何一步步点科技的读者会非常满足。"},
	{"深海余烬", "远瞳", "幽灵船与末日海洋的设定浪漫又阴森，日常与冒险的比例拿捏得不错，是远瞳式幽默的又一次发挥。"},
	{"修真四万年", "卧牛真人", "把修真和科幻焊在一起的大胆尝试，热血、中二、理想主义，主角的信念感能感染人。"},
	{"牧神记", "宅猪", "宅猪的想象力一如既往地放飞，牧神宇宙的格局越铺越大。前期惊艳，中后期略显散，但名场面不少。"},
	{"一世之尊", "爱潜水的乌贼", "以武道求超脱，轮回与彼岸的设定很有味道。乌贼早期作品，完成度不如后期，但灵气十足。"},
	{"奥术神座", "爱潜水的乌贼", "把科学方法带进魔法世界，论文式升级别具一格。设定控的狂欢，感情线稍弱。"},
	{"灭运图录", "爱潜水的乌贼", "乌贼的处女作，笔力稚嫩但构思认真，运气流与算计流的结合已经初具雏形，适合考据党。"},
	{"琥珀之剑", "绯炎", "西幻琥珀流的天花板之一，剑与魔法的世界恢弘，主角的成长线扎实，就是更新曾经让人望眼欲穿。"},
	{"赘婿", "愤怒的香蕉", "权谋、商战、家国天下，文字老练，人物有血有肉。慢热且断更史悠久，入坑前请做好心理准备。"},
	{"临高启明", "吹牛者", "群穿明末搞工业化的开山之作，考据密度惊人。与其说是小说，不如说是一场思想实验。"},
	{"神秘复苏", "佛前献花", "灵异与规则的结合，恐怖氛围营造到位，前期压迫感极强。后期战力膨胀，但前中期的体验很难被替代。"},
	{"我在精神病院学斩神", "三九音域", "都市异能加神话背景，热血与煽情都拉满。文风偏燃，适合喜欢主角一路硬刚的读者。"},
	{"夜的命名术", "会说话的肘子", "赛博朋克与超凡力量的混搭，肘子的想象力依旧在线，人物塑造有笑有泪，收尾稍显仓促。"},
	{"第一序列", "会说话的肘子", "废土求生，主角野哥的成长线很完整，配角的牺牲写得克制有力。比前作更成熟的一部。"},
	{"从红月开始", "黑山老鬼", "红月降临后的诡异世界，设定带感，悬疑感强，前期追更时真的会后背发凉。"},
	{"灵境行者", "卖报小郎君", "灵境副本设计花样百出，节奏明快，爽感稳定，适合想无脑放松的时候看。"},
	{"我们生活在南京", "天瑞说符", "硬核科幻与南京城的烟火气结合，细节扎实，情感克制，读完会想再去秦淮河边走一走。"},
	{"死在火星上", "天瑞说符", "火星生存的硬科幻小品，技术细节扎实，幽默感在线，篇幅不长，一口气能读完。"},
	{"泰坦无人声", "天瑞说符", "太空惊悚加推理，封闭空间里的压迫感写得极好，反转有惊喜，适合喜欢悬疑的读者。"},
	{"无限恐怖", "zhttty", "无限流的启蒙之作，副本设计影响了后来一大批作品。以今天的眼光看文笔略糙，但开创性无可替代。"},
	{"诡秘地海", "狐尾的笔", "狐尾的早期作品，克苏鲁式海洋冒险，脑洞清奇，氛围到位，能看出道诡异仙的雏形。"},
	{"玄鉴仙族", "季越人", "家族修仙的群像写法，代际传承的厚重感很新鲜。人物多而不乱，适合喜欢慢节奏种族的读者。"},
	{"苟在妖武乱世修仙", "文抄公", "苟道流的舒适区，主角稳健发育，日常与危机交替，看着不累，适合通勤路上读。"},
	{"光阴之外", "耳根", "耳根式的苍凉与浪漫，光阴长河的意象很美。中后期争议较大，但前期的意境值得一看。"},
	{"宇宙职业选手", "我吃西红柿", "把职业选手和宇宙竞技结合起来，热血竞技的爽感纯粹，比赛描写有画面感。"},
	{"诸界末日在线", "烟火成城", "末日游戏化的老配方，副本与主线并行，节奏快、爽点密，适合解压。"},
	{"站在食物链顶端的男人", "熊狼狗", "熊狼狗式的极端理性与黑暗幽默，主角的行事逻辑冷得发抖，喜欢反套路的会看得很爽。"},
	{"寂静杀戮", "熊狼狗", "杀戮都市风格的无限流，战斗描写凌厉，节奏紧凑，篇幅适中，适合一口气补完。"},
	{"旧日之箓", "熊狼狗", "旧日支配者与修真体系的又一次碰撞，设定有趣，风格阴郁，粉丝向明显。"},
	{"我的治愈系游戏", "我会修空调", "恐怖游戏与治愈日常的反差感拿捏得很好，副本设计有创意，情绪价值给得很足。"},
	{"这游戏也太真实了", "晨星LL", "第四天灾流的代表，玩家与NPC的互动写得鲜活，工业化种田加战争推演，越看越上头。"},
	{"学霸的黑科技系统", "晨星LL", "学霸流加黑科技，升级路径清晰，爽点稳定，学术细节不必深究，看着解压就好。"},
	{"超神机械师", "齐佩甲", "星际背景下的机械师成长史，设定扎实，节奏轻快，是齐佩甲最成熟的一本。"},
	{"惊悚乐园", "三天两觉", "无限流加游戏副本，三渣的脑洞与吐槽依旧犀利，解谜与战斗并重，老书但不过时。"},
}

// randIntn 抽成包级变量，便于测试替换随机源。
var randIntn = rand.Intn

const (
	// cardWidth 书评卡片宽度，与上游 400px 画布同量级，长评也能排下。
	cardWidth = 640
	// renderTimeout 单张卡片的渲染超时。
	renderTimeout = 30 * time.Second
)

// renderHTML 抽成包级变量，测试时替换为返回固定 JPEG 的桩。
var renderHTML = sdk.RenderHTML

const reviewHTML = `<!doctype html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;padding:0;background:#efe7d8;}
body{width:640px;box-sizing:border-box;padding:28px;font-family:"PingFang SC","Microsoft YaHei",sans-serif;color:#2b2b2b;}
.card{background:#fffdf7;border-radius:20px;padding:34px 38px 30px;box-shadow:0 10px 28px rgba(90,70,40,.12);}
.brand{font-size:14px;letter-spacing:5px;color:#b08968;margin-bottom:20px;}
.title{font-size:32px;font-weight:700;line-height:1.35;margin:0 0 10px;}
.author{font-size:17px;color:#7a6a58;margin-bottom:24px;}
.divider{height:1px;background:#e8ddca;margin-bottom:24px;}
.comment{font-size:19px;line-height:1.9;white-space:pre-wrap;word-break:break-word;text-align:justify;}
.mark{margin-top:22px;font-size:30px;color:#d9c7a8;text-align:right;line-height:1;}
</style></head><body><div class="card">
<div class="brand">哀伤雪刃推书</div>
<div class="title">《%s》</div>
<div class="author">作者：%s</div>
<div class="divider"></div>
<div class="comment">%s</div>
<div class="mark">」</div>
</div></body></html>`

// reviewCardHTML 把一条书评排成卡片页面。
func reviewCardHTML(r review) string {
	return fmt.Sprintf(reviewHTML, html.EscapeString(r.Book), html.EscapeString(r.Author), html.EscapeString(r.Comment))
}

// replyReview 优先发送图片卡片；渲染或发送失败时回退纯文本。
func replyReview(ctx context.Context, c sdk.Call, r review) error {
	if img, err := renderHTML(ctx, []byte(reviewCardHTML(r)), sdk.RenderOptions{
		Width:    cardWidth,
		FullPage: true,
		Timeout:  renderTimeout,
	}); err == nil {
		if err = c.ReplyImageBytes(ctx, img); err == nil {
			return nil
		}
	}
	return c.Reply(ctx, r.format())
}

// Definition 订阅「随机书评」和「书评+关键词」两类消息。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "bookreview",
		Name:        "哀伤雪刃推书书评",
		Version:     "1.1.0",
		Description: "发送「随机书评」随机推荐一条书评，发送「书评+关键词」检索书评",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "bookreview", Trigger: "随机书评"}})
}

// Handle 处理随机书评与关键词书评检索；未命中时与上游一致，仍渲染一张空书评图。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if sdk.MatchFull(text, "随机书评") {
		return replyReview(ctx, c, pickRandom())
	}
	keyword, ok := sdk.MatchPrefix(text, "书评")
	if !ok || !validKeyword(keyword) {
		return nil
	}
	return replyReview(ctx, c, findByKeyword(keyword))
}

// format 按「书名、作者、评语」的固定格式输出一条书评。
func (r review) format() string {
	return fmt.Sprintf("《%s》 作者：%s\n%s", r.Book, r.Author, r.Comment)
}

// pickRandom 从语料中随机取一条书评。
func pickRandom() review {
	return reviews[randIntn(len(reviews))]
}

// findByKeyword 只按评语字段检索，返回第一条命中的书评；未命中时返回零值。
func findByKeyword(keyword string) review {
	kw := strings.ToLower(keyword)
	for _, r := range reviews {
		if strings.Contains(strings.ToLower(r.Comment), kw) {
			return r
		}
	}
	return review{}
}

// validKeyword 校验关键词为 1 到 25 个汉字、英文字母或数字，与上游正则保持一致。
func validKeyword(keyword string) bool {
	runes := []rune(keyword)
	if len(runes) < 1 || len(runes) > 25 {
		return false
	}
	for _, r := range runes {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', unicode.Is(unicode.Han, r):
		default:
			return false
		}
	}
	return true
}

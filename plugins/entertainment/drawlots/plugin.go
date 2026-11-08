// Package drawlots 多功能抽签：按签名从内置签池中随机抽取一条签文。
package drawlots

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// noPermission 是缺少管理员身份时的统一提示。
const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

// lotEntry 是一条签，包含等级、签文与解签。
type lotEntry struct {
	Level   string
	Verse   string
	Explain string
}

// lot 是一种签，对应原插件的「XX签」图包。
type lot struct {
	Name    string
	Entries []lotEntry
}

// lots 是内置签池，按固定顺序输出，保证「抽签列表」结果稳定。
var lots = []lot{
	{
		Name: "每日",
		Entries: []lotEntry{
			{Level: "大吉", Verse: "晨光入户，诸事顺遂", Explain: "今天适合把拖着的小事一次做完，越早动手越顺。"},
			{Level: "中吉", Verse: "风来水面，自有涟漪", Explain: "不必强求结果，按部就班，午后会有小惊喜。"},
			{Level: "小吉", Verse: "细水长流，静待花开", Explain: "慢一点也没关系，稳住的节奏才是今天的好运。"},
		},
	},
	{
		Name: "事业",
		Entries: []lotEntry{
			{Level: "上吉", Verse: "舟行顺水，帆借东风", Explain: "手上的项目会迎来推进机会，主动汇报比埋头苦干更有用。"},
			{Level: "中吉", Verse: "积土成山，风雨兴焉", Explain: "基础工作别嫌琐碎，今天的积累会在关键时候托住你。"},
			{Level: "小吉", Verse: "路远且长，行则将至", Explain: "遇到卡点先放一放，换个角度再谈，合作方会松口。"},
		},
	},
	{
		Name: "学业",
		Entries: []lotEntry{
			{Level: "上吉", Verse: "读书破卷，下笔有神", Explain: "记忆状态在线，适合攻最难的那一章。"},
			{Level: "中吉", Verse: "温故知新，可以为师", Explain: "把错题重做一遍，比刷新题更划算。"},
			{Level: "小吉", Verse: "不积跬步，无以至千里", Explain: "今天进度慢没关系，别中断就是赢。"},
		},
	},
	{
		Name: "缘分",
		Entries: []lotEntry{
			{Level: "上吉", Verse: "有缘千里，自会相逢", Explain: "主动打个招呼，你等的那句话可能就在今天。"},
			{Level: "中吉", Verse: "月上柳梢，人约黄昏", Explain: "别急着下结论，多聊几句会发现对方也在意。"},
			{Level: "小吉", Verse: "花开有时，人遇有期", Explain: "缘分不必催，先把自己过得舒展。"},
		},
	},
	{
		Name: "财运",
		Entries: []lotEntry{
			{Level: "大吉", Verse: "财源滚滚，水到渠成", Explain: "适合整理账目，会翻出一笔被忘掉的收入。"},
			{Level: "中吉", Verse: "君子爱财，取之有道", Explain: "小额尝试可行，别把生活费押上去。"},
			{Level: "小吉", Verse: "积少成多，聚沙成塔", Explain: "今天省下的每一笔，都是月底的底气。"},
		},
	},
	{
		Name: "健康",
		Entries: []lotEntry{
			{Level: "上吉", Verse: "身轻如燕，气顺神清", Explain: "早睡一小时，比任何补品都管用。"},
			{Level: "中吉", Verse: "动静相宜，百脉调和", Explain: "久坐之后起来走两圈，肩颈会谢谢你。"},
		},
	},
	{
		Name: "欧气",
		Entries: []lotEntry{
			{Level: "上吉", Verse: "欧气东来，紫气盈门", Explain: "今天抽卡、开箱、抢券都有戏，冲就完了。"},
			{Level: "小吉", Verse: "福星照拂，小喜临门", Explain: "非到极致便是欧，下一发一定是好东西。"},
		},
	},
}

// nowFunc 便于测试冻结日期。
var nowFunc = time.Now

// Definition subscribes to the 抽签列表 / 抽XX签 / 看XX签 / 加XX签 / 删XX签 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "drawlots",
		Name:        "多功能抽签",
		Version:     "1.0.0",
		Description: "发送「抽事业签」随机抽签，发送「抽签列表」查看全部签池，发送「看事业签」查看签文",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "drawlots", Trigger: "抽签列表"}},
		sdk.WithPermissions("config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许使用加签/删签入口的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle 处理抽签相关指令：列表、抽签、查看，以及原插件的添加/删除入口。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if sdk.MatchFull(text, "抽签列表") || sdk.MatchFull(text, "刷新抽签列表") {
		return c.Reply(ctx, listText())
	}

	if name, ok := cutTail(text, "抽", "签"); ok && name != "" {
		l, found := findLot(name)
		if !found {
			return c.Reply(ctx, "才...才没有"+name+"签这种东西啦")
		}
		e := drawEntry(l, c.Event.UserID)
		return c.Reply(ctx, fmt.Sprintf("【%s签·%s】\n签文：%s\n解签：%s", l.Name, e.Level, e.Verse, e.Explain))
	}

	if name, ok := cutTail(text, "看", "签"); ok && name != "" {
		// 上游「看XX签」需要用户或群管理员权限，这里统一按 admin_users 校验。
		if !isAdmin(c) {
			return c.Reply(ctx, noPermission)
		}
		l, found := findLot(name)
		if !found {
			return c.Reply(ctx, "才...才没有"+name+"签这种东西啦")
		}
		return c.Reply(ctx, viewText(l))
	}

	if name, ok := cutTail(text, "加", "签"); ok {
		if !isAdmin(c) {
			return c.Reply(ctx, noPermission)
		}
		if name == "" {
			return c.Reply(ctx, "请使用正确的指令形式哦~")
		}
		return c.Reply(ctx, "ERROR: 当前使用内置文本签，暂不支持添加自定义签文")
	}

	if name, ok := cutTail(text, "删", "签"); ok {
		if !isAdmin(c) {
			return c.Reply(ctx, noPermission)
		}
		if _, found := findLot(name); !found {
			return c.Reply(ctx, "才...才没有"+name+"签这种东西啦")
		}
		return c.Reply(ctx, "ERROR: 当前使用内置文本签，暂不支持删除签文")
	}

	return nil
}

// isAdmin 判断发送者是否在 admin_users 中。
func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" || c.Config == nil {
		return false
	}
	switch v := c.Config["admin_users"].(type) {
	case []string:
		for _, id := range v {
			if id == c.Event.UserID {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if id, ok := item.(string); ok && id == c.Event.UserID {
				return true
			}
		}
	}
	return false
}

// cutTail 匹配「前缀 + 名字 + 后缀」的指令，返回中间的名字。
func cutTail(text, prefix, suffix string) (string, bool) {
	rest, ok := sdk.MatchPrefix(text, prefix)
	if !ok {
		return "", false
	}
	return sdk.MatchSuffix(rest, suffix)
}

// findLot 按签名查找签池。
func findLot(name string) (lot, bool) {
	for _, l := range lots {
		if l.Name == name {
			return l, true
		}
	}
	return lot{}, false
}

// drawEntry 按「用户 + 日期」固定抽取一条签，与上游 RandSenderPerDayN 一致。
func drawEntry(l lot, userID string) lotEntry {
	return l.Entries[dailyIndex(userID, len(l.Entries), nowFunc())]
}

// dailyIndex 用日期与用户 ID 构造稳定随机源，同一用户同一天结果固定。
func dailyIndex(userID string, n int, now time.Time) int {
	sum := crc64.New(crc64.MakeTable(crc64.ISO))
	_, _ = sum.Write([]byte(now.Format("20060102")))
	_, _ = sum.Write([]byte(userID))
	r := rand.New(rand.NewSource(int64(sum.Sum64())))
	return r.Intn(n)
}

// listText 输出签池列表，沿用原插件的排版。
func listText() string {
	var b strings.Builder
	b.WriteString(" 签 名 [ 类 型 ]----签数\n")
	b.WriteString("———————————\n")
	for _, l := range lots {
		b.WriteString(l.Name + "[文本]----" + strconv.Itoa(len(l.Entries)) + "\n")
		b.WriteString("----------\n")
	}
	return b.String()
}

// viewText 列出某个签池的全部签文。
func viewText(l lot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【%s签】共 %d 签\n", l.Name, len(l.Entries))
	for i, e := range l.Entries {
		fmt.Fprintf(&b, "%d. [%s] %s\n%s\n", i+1, e.Level, e.Verse, e.Explain)
	}
	return strings.TrimRight(b.String(), "\n")
}

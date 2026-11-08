package genshin

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"shizukubot/sdk"
)

// 抽卡随机源与渲染函数均为包级变量，测试可替换。
var (
	randIntn   = rand.Intn
	renderHTML = sdk.RenderHTML

	// totalPulls 是进程内累计抽卡次数，上游每累计 9 次保底一个五星。
	totalPulls uint64
)

// gachaModeKey 保存各群/各用户的卡池模式。
const gachaModeKey = "gacha_mode"

// gachaItem 是抽卡结果中的一项。
type gachaItem struct {
	Name  string
	Stars int
	Kind  string // 角色 / 武器
}

// 以下卡池是素材包不可用时的回退名单，与上游 Genshin.zip 的分组一致：
// 正常情况下优先用素材包合成立绘卡面，这里仅用于 HTML 名称卡片兜底。
var (
	threeStarWeapons = []string{
		"冷刃", "铁影阔剑", "沐浴龙血的剑", "飞天御剑", "黎明神剑", "暗铁剑", "吃虎鱼刀",
		"白铁大剑", "以理服人", "鸦羽弓", "神射手之誓", "弹弓", "信使", "魔导绪论",
		"讨龙英杰谭", "翡玉法球", "甲级宝珏", "黑缨枪", "白缨枪",
	}
	fourStarCharacters = []string{
		"安柏", "凯亚", "丽莎", "芭芭拉", "雷泽", "香菱", "班尼特", "诺艾尔", "菲谢尔",
		"砂糖", "凝光", "行秋", "北斗", "重云", "迪奥娜", "辛焱", "罗莎莉亚", "烟绯",
		"早柚", "九条裟罗", "托马", "云堇", "五郎", "久岐忍", "鹿野院平藏", "多莉",
		"柯莱", "坎蒂丝", "莱依拉", "珐露珊", "瑶瑶", "卡维", "米卡", "绮良良",
		"琳妮特", "菲米尼", "夏洛蒂", "夏沃蕾", "嘉明", "赛索斯", "卡齐娜", "欧洛伦",
		"蓝砚", "伊安珊", "塔利雅", "伊法", "爱诺",
	}
	fourStarWeapons = []string{
		"祭礼剑", "笛剑", "匣里龙吟", "西风剑", "暗巷闪光", "斩岩", "祭礼大剑",
		"西风大剑", "雨裁", "千岩古剑", "弓藏", "祭礼弓", "绝弦", "西风猎弓",
		"暗巷猎手", "幽夜华尔兹", "钢轮弓", "试作澹月", "破魔之弓", "流浪乐章",
		"祭礼残章", "匣里日月", "西风秘典", "暗巷的酒与诗", "昭心", "忍冬之果",
		"试作金珀", "西风长枪", "黑岩刺枪", "决斗之枪", "千岩长枪", "宗室猎枪",
		"试作星镰", "龙脊长枪", "喜多院十文字", "断浪长鳍", "「渔获」",
	}
	fiveStarCharacters = []string{
		"迪卢克", "琴", "莫娜", "七七", "刻晴", "达达利亚", "钟离", "阿贝多", "甘雨",
		"魈", "胡桃", "优菈", "枫原万叶", "神里绫华", "宵宫", "雷电将军", "珊瑚宫心海",
		"荒泷一斗", "申鹤", "八重神子", "神里绫人", "夜兰", "提纳里", "赛诺", "妮露",
		"纳西妲", "流浪者", "艾尔海森", "迪希雅", "白术", "林尼", "那维莱特",
		"莱欧斯利", "芙宁娜", "娜维娅", "闲云", "千织", "阿蕾奇诺", "克洛琳德",
		"希格雯", "艾梅莉埃", "玛拉妮", "基尼奇", "希诺宁", "恰斯卡", "玛薇卡",
		"茜特菈莉", "梦见月瑞希", "瓦蕾莎", "爱可菲", "丝柯克", "伊涅芙",
	}
	fiveStarWeapons = []string{
		"天空之刃", "天空之傲", "天空之翼", "天空之卷", "天空之脊", "风鹰剑",
		"阿莫斯之弓", "四风原典", "狼的末路", "和璞鸢", "尘世之锁", "磐岩结绿",
		"无工之剑", "贯虹之槊", "斫峰之刃", "护摩之杖", "终末嗟叹之诗", "雾切之回光",
		"松籁响起之时", "苍古自由之誓", "不灭月华", "薙草之稻光", "赤角石溃杵",
		"神乐之真意", "波乱月白经津", "若水", "猎人之径", "圣显之钥", "赤沙之杖",
		"图莱杜拉的回忆", "苇海信标", "静水流涌之辉", "万世流涌大典", "金流监督",
		"苍纹角杯", "白雨心弦", "赦罪", "山王长牙", "岩峰巡歌", "冲浪时光",
		"千夜浮梦", "寝正月初晴", "祭星者之望", "香韵奏者", "息灾",
	}
)

// gachaStore 按群记录卡池模式。
type gachaStore map[string]bool

// drawTen 按上游规则抽十连：普通池 3% 五星、17% 四星、80% 三星，
// 没有四星及以上时把最后一项换成四星；每累计 9 抽额外保底一个五星。
// 五星卡池中每一抽都是五星。
func drawTen(fiveStarMode bool) []gachaItem {
	items := make([]gachaItem, 0, 10)
	if atomic.AddUint64(&totalPulls, 1)%9 == 0 {
		items = append(items, randomFive())
	}
	for len(items) < 10 {
		if fiveStarMode {
			items = append(items, randomFive())
			continue
		}
		switch a := randIntn(1000); {
		case a <= 800:
			items = append(items, gachaItem{Name: pick(threeStarWeapons), Stars: 3, Kind: "武器"})
		case a <= 885:
			items = append(items, gachaItem{Name: pick(fourStarCharacters), Stars: 4, Kind: "角色"})
		case a <= 970:
			items = append(items, gachaItem{Name: pick(fourStarWeapons), Stars: 4, Kind: "武器"})
		case a <= 985:
			items = append(items, randomFive())
		default:
			items = append(items, gachaItem{Name: pick(fiveStarWeapons), Stars: 5, Kind: "武器"})
		}
	}
	if !fiveStarMode {
		if !hasFourStarOrAbove(items) {
			if randIntn(2) == 0 {
				items[len(items)-1] = gachaItem{Name: pick(fourStarCharacters), Stars: 4, Kind: "角色"}
			} else {
				items[len(items)-1] = gachaItem{Name: pick(fourStarWeapons), Stars: 4, Kind: "武器"}
			}
		}
	}
	return items[:10]
}

// randomFive 在五星角色与五星武器中随机取一个。
func randomFive() gachaItem {
	if randIntn(2) == 0 {
		return gachaItem{Name: pick(fiveStarCharacters), Stars: 5, Kind: "角色"}
	}
	return gachaItem{Name: pick(fiveStarWeapons), Stars: 5, Kind: "武器"}
}

func hasFourStarOrAbove(items []gachaItem) bool {
	for _, item := range items {
		if item.Stars >= 4 {
			return true
		}
	}
	return false
}

func pick(pool []string) string {
	if len(pool) == 0 {
		return "未知"
	}
	return pool[randIntn(len(pool))]
}

// gachaText 汇总五星结果；没有五星时返回空字符串。
func gachaText(items []gachaItem) string {
	var chars, weapons []string
	for _, item := range items {
		if item.Stars != 5 {
			continue
		}
		if item.Kind == "角色" {
			chars = append(chars, item.Name)
		} else {
			weapons = append(weapons, item.Name)
		}
	}
	var b strings.Builder
	if len(chars) > 0 {
		b.WriteString("★五星角色★\n")
		b.WriteString(strings.Join(chars, " * ") + " * ")
	}
	if len(weapons) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("★五星武器★\n")
		b.WriteString(strings.Join(weapons, " * ") + " * ")
	}
	return strings.TrimSpace(b.String())
}

// gachaHTML 生成十连结果卡片。
func gachaHTML(items []gachaItem) []byte {
	var b bytes.Buffer
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;padding:24px;background:#1b1b22;font-family:"Microsoft YaHei",sans-serif;color:#fff}
h1{margin:0 0 16px;font-size:26px;font-weight:600;text-align:center;color:#e8dcc0}
.grid{display:grid;grid-template-columns:repeat(5,160px);gap:14px;justify-content:center}
.card{height:200px;border-radius:14px;display:flex;flex-direction:column;align-items:center;justify-content:center;box-shadow:0 4px 16px rgba(0,0,0,.45)}
.s3{background:linear-gradient(160deg,#4b5563,#2b3440)}
.s4{background:linear-gradient(160deg,#7c5cbf,#43307a)}
.s5{background:linear-gradient(160deg,#d9a441,#8a5b1c)}
.name{font-size:20px;font-weight:600;text-align:center;padding:0 8px;line-height:1.35}
.stars{margin-top:10px;font-size:18px;letter-spacing:2px;color:#ffe9a8}
.kind{margin-top:6px;font-size:13px;opacity:.85}
</style></head><body><h1>原神十连</h1><div class="grid">`)
	for _, item := range items {
		b.WriteString(fmt.Sprintf(`<div class="card s%d"><div class="name">%s</div><div class="stars">%s</div><div class="kind">%s</div></div>`,
			item.Stars, template.HTMLEscapeString(item.Name), strings.Repeat("★", item.Stars), template.HTMLEscapeString(item.Kind)))
	}
	b.WriteString(`</div></body></html>`)
	return b.Bytes()
}

// handleGacha 处理「原神十连」：优先用上游素材包合成立绘卡面，
// 素材不可用时回退到名称卡片。
func handleGacha(ctx context.Context, c sdk.Call) error {
	mode, err := loadGachaMode(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取卡池失败："+err.Error())
	}
	if assets, err := assetProvider(ctx); err == nil && assets != nil {
		if cards, err := drawFromAssets(assets, mode); err == nil {
			if img, err := assets.renderImage(cards); err == nil {
				if text := cardsText(cards); text != "" {
					if err := c.Reply(ctx, "恭喜你抽到了：\n"+text); err != nil {
						return err
					}
				} else if err := c.Reply(ctx, "十连成功~"); err != nil {
					return err
				}
				return c.ReplyImageBytes(ctx, img)
			}
		}
	}
	items := drawTen(mode)
	img, err := renderHTML(ctx, gachaHTML(items), sdk.RenderOptions{Width: 960, Height: 520, FullPage: true, Timeout: 15 * time.Second})
	if err != nil {
		return c.Reply(ctx, "生成抽卡结果失败："+err.Error()+"\n"+describeTen(items))
	}
	if text := gachaText(items); text != "" {
		if err := c.Reply(ctx, "恭喜你抽到了：\n"+text); err != nil {
			return err
		}
	} else {
		if err := c.Reply(ctx, "十连成功~"); err != nil {
			return err
		}
	}
	return c.ReplyImageBytes(ctx, img)
}

// describeTen 在无法出图时用文本兜底展示结果。
func describeTen(items []gachaItem) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("%s %s（%s）", strings.Repeat("★", item.Stars), item.Name, item.Kind))
	}
	return strings.Join(lines, "\n")
}

// handleSwitchPool 处理「切换原神卡池」。
func handleSwitchPool(ctx context.Context, c sdk.Call) error {
	st, err := loadGachaStore(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取卡池失败："+err.Error())
	}
	key := gachaKey(c)
	mode := !st[key]
	st[key] = mode
	if err := sdk.SaveJSON(ctx, c.State, gachaModeKey, st); err != nil {
		return c.Reply(ctx, "保存卡池失败："+err.Error())
	}
	if mode {
		return c.Reply(ctx, "切换到五星卡池~")
	}
	return c.Reply(ctx, "切换到普通卡池~")
}

func gachaKey(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return "g:" + c.Event.GroupID
	}
	return "u:" + c.Event.UserID
}

func loadGachaMode(ctx context.Context, c sdk.Call) (bool, error) {
	st, err := loadGachaStore(ctx, c)
	if err != nil {
		return false, err
	}
	return st[gachaKey(c)], nil
}

func loadGachaStore(ctx context.Context, c sdk.Call) (gachaStore, error) {
	st := gachaStore{}
	if err := sdk.LoadJSON(ctx, c.State, gachaModeKey, &st); err != nil {
		return nil, err
	}
	if st == nil {
		st = gachaStore{}
	}
	return st, nil
}

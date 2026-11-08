package mcfish

// 本文件的数据表来自上游 zerobot-plugin/plugin/mcfish 的默认值与帮助文本。
// 上游把完整的物品名称、概率、价格和耐久放在公共数据文件
// McFish/articlesInfo.json 中，本仓库没有该文件，因此这里只恢复源码中
// 可见的物品，并使用上游源码里的默认分类概率（treasure 1 / pole 4 /
// fish 35 / waste 60 的累计值，对应帮助文本“默认上钩概率 60%”）。
// 垃圾的具体名称在上游数据文件中同样不可见，这里用单一“垃圾”占位。

// 每日钓鱼上限（上游 FishLimit）。
const fishLimit = 50

// 规则版本号（上游 main.go）。
const version = "5.6.2"

// 维修次数上限（上游 pole.go）。
const maxRepair = 10

// 附魔等级上限（上游 enchantLevel 0~Ⅲ）。
const maxEnchant = 3

// 每日鱼竿交易上限（上游 checkCanSalesFor）。
const dailyRodTrade = 10

// 每日普通物品交易上限（上游 checkCanSalesFor）。
const dailyItemTrade = 30

// 物品大类。
const (
	kindFish     = "fish"
	kindWaste    = "waste"
	kindTreasure = "treasure"
	kindPole     = "pole"
)

// itemDef 是物品基础数据。
type itemDef struct {
	Name    string
	Kind    string
	Price   int
	Durable int
	// Prob 是同一掉落池内的概率（百分比）。
	Prob int
}

// itemDefs 按展示顺序排列。
var itemDefs = []itemDef{
	// 鱼竿（鱼竿池内概率来自帮助文本 0.7/0.2/0.06/0.03/0.01 的百倍）。
	{"木竿", kindPole, 100, 30, 70},
	{"铁竿", kindPole, 300, 50, 20},
	{"金竿", kindPole, 700, 70, 6},
	{"钻石竿", kindPole, 1500, 100, 3},
	{"下界合金竿", kindPole, 3100, 150, 1},
	{"三叉戟", kindPole, 4000, 300, 0},
	// 宝藏池。
	{"诱钓", kindTreasure, 1000, 0, 25},
	{"海之眷顾", kindTreasure, 2500, 0, 10},
	{"净化书", kindTreasure, 5000, 0, 19},
	{"宝藏诅咒", kindTreasure, 0, 0, 25},
	{"唱片", kindTreasure, 3000, 0, 1},
	{"美西螈", kindPole, 3000, 0, 1},
	{"海豚", kindFish, 1000, 0, 19},
	// 鱼类（池内概率来自帮助文本 0.69/0.2/0.06/0.03/0.01/0.01 的百倍）。
	{"鳕鱼", kindFish, 10, 0, 69},
	{"鲑鱼", kindFish, 50, 0, 20},
	{"热带鱼", kindFish, 100, 0, 6},
	{"河豚", kindFish, 300, 0, 3},
	{"鹦鹉螺", kindFish, 500, 0, 1},
	{"墨鱼", kindFish, 500, 0, 1},
	// 垃圾。
	{"垃圾", kindWaste, 10, 0, 100},
}

// 各掉落池按上游 fish.go 的判定顺序排列。
var (
	poleRoll     = []string{"木竿", "铁竿", "金竿", "钻石竿", "下界合金竿"}
	treasureRoll = []string{"美西螈", "唱片", "海之眷顾", "净化书", "宝藏诅咒", "海豚", "诱钓"}
	fishRoll     = []string{"鳕鱼", "鲑鱼", "热带鱼", "河豚", "鹦鹉螺", "墨鱼"}
	wasteRoll    = []string{"垃圾"}
)

// legacyPrices 保存旧版精简实现的 24 种鱼价格，用于迁移旧状态后继续出售。
var legacyPrices = map[string]int{
	"鲈鱼": 12, "鲤鱼": 15, "鲫鱼": 20, "章鱼": 40, "龙虾": 70,
	"水母": 90, "鳗鱼": 120, "剑鱼": 240, "金枪鱼": 300,
	"深海鮟鱇": 480, "魔鬼鱼": 560, "帝王蟹": 650, "鲨鱼": 800,
	"沧龙": 2000, "海皇鲸": 3200, "利维坦": 5000, "远古鱼龙": 8000,
}

var itemIndex = func() map[string]itemDef {
	m := make(map[string]itemDef, len(itemDefs))
	for _, d := range itemDefs {
		m[d.Name] = d
	}
	return m
}()

// tradeNames 为购买/出售指令提供按长度倒序的名称，避免“木竿”抢先匹配“下界合金竿”。
var tradeNames = func() []string {
	names := make([]string, 0, len(itemDefs)+1)
	for _, d := range itemDefs {
		names = append(names, d.Name)
	}
	names = append(names, "初始木竿")
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if len(names[j]) > len(names[i]) {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}()

func defOf(name string) (itemDef, bool) {
	d, ok := itemIndex[name]
	return d, ok
}

// basePrice 返回物品均价；旧版鱼名回退到旧价格表。
func basePrice(name string) int {
	if d, ok := itemIndex[name]; ok {
		return d.Price
	}
	if p, ok := legacyPrices[name]; ok {
		return p
	}
	return 10
}

func maxDurable(name string) int {
	if d, ok := itemIndex[name]; ok {
		return d.Durable
	}
	return 0
}

func isRod(name string) bool {
	d, ok := itemIndex[name]
	return ok && d.Kind == kindPole && d.Durable > 0
}

func isWaste(name string) bool {
	d, ok := itemIndex[name]
	return ok && d.Kind == kindWaste
}

// defaultStock 是每日商店初始库存，仅作纯文本移植的默认值。
func defaultStock(name string) int {
	switch name {
	case "木竿":
		return 5
	case "铁竿", "金竿", "钻石竿", "下界合金竿":
		return 1
	case "三叉戟":
		return 0 // 上游只能合成、修复和交易
	case "美西螈":
		return 1
	case "诱钓", "海之眷顾", "净化书", "唱片", "海豚":
		return 5
	case "宝藏诅咒":
		return 0
	default:
		return 20
	}
}

// shopNames 是商店展示与刷新价格的范围（上游 thingList 不含宝藏诅咒）。
var shopNames = func() []string {
	names := make([]string, 0, len(itemDefs))
	for _, d := range itemDefs {
		if d.Name != "宝藏诅咒" {
			names = append(names, d.Name)
		}
	}
	return names
}()

// weightedRoll 在给定掉落池内按 itemDef.Prob 掷一次，返回物品名。
func weightedRoll(roll []string) string {
	dice := randIntn(100)
	sum := 0
	for _, name := range roll {
		d, _ := defOf(name)
		sum += d.Prob
		if dice < sum {
			return name
		}
	}
	return roll[len(roll)-1]
}

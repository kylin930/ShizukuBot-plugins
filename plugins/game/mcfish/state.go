package mcfish

import (
	"context"
	"strings"

	"shizukubot/sdk"
)

// rodItem 对应上游 equip/article 中的一根鱼竿（含耐久、维修次数与附魔）。
type rodItem struct {
	Name        string `json:"name"`
	Durable     int    `json:"durable"`
	Maintenance int    `json:"maintenance"`
	Induce      int    `json:"induce"`
	Favor       int    `json:"favor"`
}

// player 是单个用户的状态。前面的字段为新玩法状态，末尾保留旧版精简实现
// 的字段用于迁移，迁移后会将 Pack 合并进 Fish/Waste/Treasure。
type player struct {
	Coins          int            `json:"coins"`
	Fish           map[string]int `json:"fish,omitempty"`
	Waste          map[string]int `json:"waste,omitempty"`
	Treasure       map[string]int `json:"treasure,omitempty"`
	Bait           map[string]int `json:"bait,omitempty"`
	Rods           []rodItem      `json:"rods,omitempty"`
	Equip          *rodItem       `json:"equip,omitempty"`
	FishToday      int            `json:"fish_today,omitempty"`
	FishDate       string         `json:"fish_date,omitempty"`
	Curse          int            `json:"curse,omitempty"` // 功德--(x)，每 10 点凝结为 1 层宝藏诅咒
	Bless          int            `json:"bless,omitempty"` // 功德++(x)，每 75 点凝结为 1 本净化书
	Coupons        int            `json:"coupons,omitempty"`
	BuyCount       int            `json:"buy_count,omitempty"`
	SellCount      int            `json:"sell_count,omitempty"`
	CouponProgress int            `json:"coupon_progress,omitempty"`
	RodTrade       int            `json:"rod_trade,omitempty"`
	TradeDate      string         `json:"trade_date,omitempty"`
	ShopDate       string         `json:"shop_date,omitempty"`
	Discounts      map[string]int `json:"discounts,omitempty"`
	Stock          map[string]int `json:"stock,omitempty"`

	// 旧版精简实现的状态字段。
	RodLevel int            `json:"rod_level,omitempty"`
	LastFish int64          `json:"last_fish,omitempty"`
	Pack     map[string]int `json:"pack,omitempty"`
	Weight   float64        `json:"weight,omitempty"`
}

var legacyRodNames = []string{"木竿", "铁竿", "金竿", "钻石竿", "下界合金竿"}

func stateKey(c sdk.Call) string { return "mcfish:" + c.Event.UserID }

func initMaps(p *player) {
	if p.Fish == nil {
		p.Fish = map[string]int{}
	}
	if p.Waste == nil {
		p.Waste = map[string]int{}
	}
	if p.Treasure == nil {
		p.Treasure = map[string]int{}
	}
	if p.Bait == nil {
		p.Bait = map[string]int{}
	}
	if p.Discounts == nil {
		p.Discounts = map[string]int{}
	}
	if p.Stock == nil {
		p.Stock = map[string]int{}
	}
}

// normalize 初始化容器、迁移旧状态并刷新每日限制。
func normalize(p *player) {
	initMaps(p)
	// 旧版状态至少会带有 Pack/RodLevel/LastFish/Weight 之一，用于区分
	// 新状态里“有鱼但没有鱼竿”的正常情况。
	hadLegacy := len(p.Pack) > 0 || p.RodLevel > 0 || p.LastFish > 0 || p.Weight > 0
	if len(p.Pack) > 0 {
		for name, n := range p.Pack {
			if n <= 0 {
				continue
			}
			if d, ok := defOf(name); ok {
				switch d.Kind {
				case kindWaste:
					p.Waste[name] += n
				case kindTreasure:
					p.Treasure[name] += n
				default:
					p.Fish[name] += n
				}
			} else {
				// 旧版 24 种鱼中未出现在新表的鱼，保留在鱼获里。
				p.Fish[name] += n
			}
		}
		p.Pack = nil
	}
	// 旧版 RodLevel 表示已升级的鱼竿；迁移为已装备的满耐久的同名鱼竿。
	if p.Equip == nil && len(p.Rods) == 0 && hadLegacy {
		level := p.RodLevel
		if level < 0 {
			level = 0
		}
		if level >= len(legacyRodNames) {
			level = len(legacyRodNames) - 1
		}
		name := legacyRodNames[level]
		p.Equip = &rodItem{Name: name, Durable: maxDurable(name)}
	}
	refreshDaily(p)
}

func refreshDaily(p *player) {
	today := nowFunc().Format("2006-01-02")
	if p.FishDate != today {
		p.FishToday = 0
		p.FishDate = today
	}
	if p.TradeDate != today {
		p.BuyCount = 0
		p.SellCount = 0
		p.RodTrade = 0
		p.TradeDate = today
	}
	if p.ShopDate != today || len(p.Discounts) == 0 {
		for _, name := range shopNames {
			p.Discounts[name] = 50 + randIntn(150)
			p.Stock[name] = defaultStock(name)
		}
		p.ShopDate = today
	}
}

// load 读取用户状态；状态不可用时返回零值，插件继续以临时状态运行。
func load(ctx context.Context, c sdk.Call) player {
	p := player{}
	_ = sdk.LoadJSON(ctx, c.State, stateKey(c), &p)
	normalize(&p)
	return p
}

func save(ctx context.Context, c sdk.Call, p player) error {
	return sdk.SaveJSON(ctx, c.State, stateKey(c), p)
}

func fishTotal(p *player) int {
	total := 0
	for _, n := range p.Fish {
		total += n
	}
	return total
}

func curseLayers(p *player) int { return p.Treasure["宝藏诅咒"] }

func addBless(p *player, n int) {
	p.Bless += n
	for p.Bless >= 75 {
		p.Bless -= 75
		p.Treasure["净化书"]++
	}
}

func addCurse(p *player, n int) {
	p.Curse += n
	for p.Curse >= 10 {
		p.Curse -= 10
		p.Treasure["宝藏诅咒"]++
	}
}

// addItem 把非鱼竿物品放入对应分类；美西螈按装备存入鱼竿列表。
func addItem(p *player, name string, n int) {
	if n <= 0 {
		return
	}
	if name == "美西螈" {
		for i := 0; i < n; i++ {
			p.Rods = append(p.Rods, rodItem{Name: name})
		}
		return
	}
	if d, ok := defOf(name); ok {
		switch d.Kind {
		case kindWaste:
			p.Waste[name] += n
		case kindTreasure:
			p.Treasure[name] += n
		default:
			p.Fish[name] += n
		}
		return
	}
	p.Fish[name] += n
}

// itemCount 返回非鱼竿物品数量。
func itemCount(p *player, name string) int {
	if d, ok := defOf(name); ok {
		switch d.Kind {
		case kindWaste:
			return p.Waste[name]
		case kindTreasure:
			return p.Treasure[name]
		}
	}
	if n, ok := p.Fish[name]; ok {
		return n
	}
	return p.Bait[name]
}

// removeItem 扣除非鱼竿物品，返回实际扣除数量。
func removeItem(p *player, name string, n int) int {
	have := itemCount(p, name)
	if n > have {
		n = have
	}
	if n <= 0 {
		return 0
	}
	if d, ok := defOf(name); ok {
		switch d.Kind {
		case kindWaste:
			p.Waste[name] -= n
			if p.Waste[name] <= 0 {
				delete(p.Waste, name)
			}
			return n
		case kindTreasure:
			p.Treasure[name] -= n
			if p.Treasure[name] <= 0 {
				delete(p.Treasure, name)
			}
			return n
		}
	}
	if _, ok := p.Fish[name]; ok {
		p.Fish[name] -= n
		if p.Fish[name] <= 0 {
			delete(p.Fish, name)
		}
		return n
	}
	p.Bait[name] -= n
	if p.Bait[name] <= 0 {
		delete(p.Bait, name)
	}
	return n
}

// rodCount 统计背包内（不含已装备）指定鱼竿数量。
func rodCount(p *player, name string) int {
	n := 0
	for _, r := range p.Rods {
		if r.Name == name {
			n++
		}
	}
	return n
}

// takeRods 从背包取出指定数量的鱼竿并返回。
func takeRods(p *player, name string, n int) []rodItem {
	taken := make([]rodItem, 0, n)
	keep := p.Rods[:0]
	for _, r := range p.Rods {
		if r.Name == name && len(taken) < n {
			taken = append(taken, r)
			continue
		}
		keep = append(keep, r)
	}
	p.Rods = keep
	return taken
}

func discountOf(p *player, name string) int {
	if d := p.Discounts[name]; d > 0 {
		return d
	}
	return 100
}

// parseNameCount 从“购买木竿 3”这类文本里拆出物品名与数量。
func parseNameCount(rest string) (string, int, bool) {
	rest = strings.TrimSpace(rest)
	for _, name := range tradeNames {
		if !strings.HasPrefix(rest, name) {
			continue
		}
		tail := strings.TrimSpace(rest[len(name):])
		if tail == "" {
			return name, 1, true
		}
		n := 0
		ok := true
		for _, r := range tail {
			if r < '0' || r > '9' {
				ok = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if ok && n > 0 {
			return name, n, true
		}
	}
	return "", 0, false
}

package mcfish

import (
	"context"
	"fmt"
	"strings"

	"shizukubot/sdk"
)

func buyUnitPrice(p *player, name string) int {
	return basePrice(name) * discountOf(p, name) / 100
}

// salePrice 是普通物品售价：基础均价×当日波动×80%交易税×诅咒系数。
func salePrice(p *player, name string) int {
	price := basePrice(name) * discountOf(p, name) / 100
	price = price * 8 / 10
	price = price * curseSellPercent(p) / 100
	if price < 0 {
		return 0
	}
	return price
}

// rodSalePrice 参考上游对耐久、维修次数和附魔的估值方式。
func rodSalePrice(p *player, r rodItem) int {
	if r.Name == "美西螈" {
		return salePrice(p, r.Name)
	}
	price := basePrice(r.Name)
	price -= maxDurable(r.Name) - r.Durable
	price -= r.Maintenance * 2
	price += r.Induce * 600 * discountOf(p, "诱钓") / 100
	price += r.Favor * 1800 * discountOf(p, "海之眷顾") / 100
	if price < 0 {
		price = 0
	}
	price = price * discountOf(p, r.Name) / 100
	price = price * 8 / 10
	price = price * curseSellPercent(p) / 100
	if price < 0 {
		return 0
	}
	return price
}

func curseSellPercent(p *player) int {
	v := 100 - 10*curseLayers(p)
	if v < 0 {
		return 0
	}
	return v
}

func curseBuyPercent(p *player) int { return 100 + 10*curseLayers(p) }

func handleShop(ctx context.Context, c sdk.Call) error {
	p := load(ctx, c)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("【钓鱼商店】今日价格波动（诅咒 %d 层，售价系数 %d%%，购买系数 %d%%）\n",
		curseLayers(&p), curseSellPercent(&p), curseBuyPercent(&p)))
	for _, name := range shopNames {
		discount := discountOf(&p, name)
		b.WriteString(fmt.Sprintf("%s：库存%d 均价%d 波动%+d%% 售价%d\n",
			name, p.Stock[name], basePrice(name), discount-100, buyUnitPrice(&p, name)))
	}
	b.WriteString(fmt.Sprintf("普通物品每日买卖合计上限 %d，鱼竿/三叉戟/美西螈每日交易上限 %d；出售统一扣 20%% 交易税。",
		dailyItemTrade, dailyRodTrade))
	return c.Reply(ctx, b.String())
}

func handleBuy(ctx context.Context, c sdk.Call, rest string) error {
	name, n, ok := parseNameCount(rest)
	if !ok {
		return c.Reply(ctx, "未找到该商品，请发送「钓鱼商店」查看可购买内容。")
	}
	if name == "初始木竿" {
		name = "木竿"
	}
	if name == "宝藏诅咒" {
		return c.Reply(ctx, "宝藏诅咒无法购买。")
	}
	p := load(ctx, c)
	rodTrade := isRod(name) || name == "美西螈"
	notes := make([]string, 0, 3)

	if rodTrade {
		left := dailyRodTrade - p.RodTrade
		if left <= 0 {
			return c.Reply(ctx, "今日鱼竿交易次数已用完。")
		}
		if n > left {
			n = left
			notes = append(notes, fmt.Sprintf("今日鱼竿交易仅剩 %d 次。", left))
		}
	} else {
		left := dailyItemTrade - p.BuyCount - p.SellCount
		if left <= 0 {
			return c.Reply(ctx, "今日普通物品买卖次数已用完。")
		}
		if n > left {
			n = left
			notes = append(notes, fmt.Sprintf("今日普通物品买卖仅剩 %d 次。", left))
		}
	}
	if name == "净化书" {
		available := curseLayers(&p) - p.Treasure["净化书"]
		if available <= 0 {
			return c.Reply(ctx, "你当前没有需要净化的诅咒。")
		}
		if n > available {
			n = available
		}
	}
	stock := p.Stock[name]
	if stock <= 0 {
		return c.Reply(ctx, fmt.Sprintf("商店里的%s已售罄。", name))
	}
	if n > stock {
		n = stock
		notes = append(notes, fmt.Sprintf("库存仅剩 %d 件。", stock))
	}
	if n <= 0 {
		return c.Reply(ctx, "购买数量必须大于 0。")
	}

	unit := buyUnitPrice(&p, name)
	couponUse := 0
	if p.Coupons > couponUse && couponUse < n {
		couponUse = p.Coupons
		if couponUse > n {
			couponUse = n
		}
		if couponUse > 3 {
			couponUse = 3
		}
	}
	cost := unit*n - unit*couponUse/2
	cost = cost * curseBuyPercent(&p) / 100
	if p.Coins < cost {
		return c.Reply(ctx, fmt.Sprintf("金币不足：购买 %s×%d 需要 %d 金币，你只有 %d。", name, n, cost, p.Coins))
	}

	p.Coins -= cost
	p.Stock[name] -= n
	p.Coupons -= couponUse
	if rodTrade {
		p.RodTrade += n
		// 上游只对名字含「竿」的交易累积诅咒，美西螈与三叉戟不累积。
		if strings.Contains(name, "竿") {
			addCurse(&p, n)
		}
		if isRod(name) {
			for i := 0; i < n; i++ {
				p.Rods = append(p.Rods, rodItem{Name: name, Durable: maxDurable(name)})
			}
		} else {
			addItem(&p, name, n)
		}
	} else {
		p.BuyCount += n
		if couponUse == 0 {
			p.CouponProgress += n
			for p.CouponProgress >= 20 {
				p.CouponProgress -= 20
				p.Coupons = 3
			}
		}
		addItem(&p, name, n)
	}
	if err := save(ctx, c, p); err != nil {
		return err
	}
	msg := fmt.Sprintf("购买 %s×%d 成功，花费 %d 金币（优惠券 %d 张）。", name, n, cost, couponUse)
	if len(notes) > 0 {
		msg += "\n" + strings.Join(notes, "\n")
	}
	return c.Reply(ctx, msg)
}

func handleSell(ctx context.Context, c sdk.Call, rest string) error {
	name, n, ok := parseNameCount(rest)
	if !ok {
		return c.Reply(ctx, "未找到该物品，请确认名称与数量。")
	}
	if name == "初始木竿" {
		name = "木竿"
	}
	if name == "宝藏诅咒" {
		return c.Reply(ctx, "宝藏诅咒无法出售。")
	}
	p := load(ctx, c)
	notes := make([]string, 0, 2)
	total := 0

	if name == "美西螈" {
		have := rodCount(&p, name)
		if have <= 0 {
			return c.Reply(ctx, "你的背包里没有美西螈。")
		}
		if n > have {
			n = have
		}
		left := dailyRodTrade - p.RodTrade
		if left <= 0 {
			return c.Reply(ctx, "今日鱼竿交易次数已用完。")
		}
		if n > left {
			n = left
			notes = append(notes, fmt.Sprintf("今日鱼竿交易仅剩 %d 次。", left))
		}
		takeRods(&p, name, n)
		for i := 0; i < n; i++ {
			total += salePrice(&p, name)
		}
		p.RodTrade += n
	} else if isRod(name) {
		have := rodCount(&p, name)
		if have <= 0 {
			return c.Reply(ctx, fmt.Sprintf("你的背包里没有%s（已装备的鱼竿不能出售）。", name))
		}
		if n > have {
			n = have
		}
		left := dailyRodTrade - p.RodTrade
		if left <= 0 {
			return c.Reply(ctx, "今日鱼竿交易次数已用完。")
		}
		if n > left {
			n = left
			notes = append(notes, fmt.Sprintf("今日鱼竿交易仅剩 %d 次。", left))
		}
		taken := takeRods(&p, name, n)
		for _, r := range taken {
			total += rodSalePrice(&p, r)
		}
		p.RodTrade += n
		if strings.Contains(name, "竿") {
			addCurse(&p, n)
		}
	} else {
		have := itemCount(&p, name)
		if have <= 0 {
			return c.Reply(ctx, fmt.Sprintf("你的背包里没有%s。", name))
		}
		if n > have {
			n = have
		}
		left := dailyItemTrade - p.BuyCount - p.SellCount
		if left <= 0 {
			return c.Reply(ctx, "今日普通物品买卖次数已用完。")
		}
		if n > left {
			n = left
			notes = append(notes, fmt.Sprintf("今日普通物品买卖仅剩 %d 次。", left))
		}
		removeItem(&p, name, n)
		for i := 0; i < n; i++ {
			total += salePrice(&p, name)
		}
		p.SellCount += n
		if _, ok := defOf(name); ok && name != "宝藏诅咒" {
			p.Stock[name] += n
			if p.Stock[name] > 255 {
				p.Stock[name] = 255
			}
		}
	}
	p.Coins += total
	if err := save(ctx, c, p); err != nil {
		return err
	}
	msg := fmt.Sprintf("出售 %s×%d 成功，获得 %d 金币（诅咒系数 %d%%）。", name, n, total, curseSellPercent(&p))
	if len(notes) > 0 {
		msg += "\n" + strings.Join(notes, "\n")
	}
	return c.Reply(ctx, msg)
}

func handleSellAllWaste(ctx context.Context, c sdk.Call) error {
	p := load(ctx, c)
	total, count := 0, 0
	for name, n := range p.Waste {
		if n <= 0 {
			continue
		}
		count += n
		total += salePrice(&p, name) * n
	}
	if count == 0 {
		return c.Reply(ctx, "你的背包里没有垃圾。")
	}
	p.Waste = map[string]int{}
	p.Coins += total
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("出售所有垃圾×%d，获得 %d 金币（诅咒系数 %d%%）。", count, total, curseSellPercent(&p)))
}

func handleRemoveCurse(ctx context.Context, c sdk.Call, n int) error {
	p := load(ctx, c)
	layers := curseLayers(&p)
	if layers <= 0 {
		return c.Reply(ctx, "你没有绑定任何诅咒。")
	}
	if n > layers {
		n = layers
	}
	books := p.Treasure["净化书"]
	if books < n {
		return c.Reply(ctx, fmt.Sprintf("净化书不足：消除 %d 层诅咒需要 %d 本，你只有 %d 本。", n, n, books))
	}
	removeItem(&p, "净化书", n)
	removeItem(&p, "宝藏诅咒", n)
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("消耗 %d 本净化书，消除了 %d 层诅咒。剩余诅咒 %d 层。", n, n, curseLayers(&p)))
}

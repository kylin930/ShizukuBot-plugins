package mcfish

import (
	"context"
	"fmt"
	"strings"

	"shizukubot/sdk"
)

func handleRepair(ctx context.Context, c sdk.Call) error {
	p := load(ctx, c)
	if p.Equip == nil {
		return c.Reply(ctx, "你还没有装备鱼竿。")
	}
	if p.Equip.Name == "美西螈" {
		return c.Reply(ctx, "美西螈不消耗耐久，也无法修复。")
	}
	limit := maxDurable(p.Equip.Name)
	if p.Equip.Durable >= limit {
		return c.Reply(ctx, "当前鱼竿耐久已满，无需修复。")
	}
	if p.Equip.Maintenance >= maxRepair {
		return c.Reply(ctx, fmt.Sprintf("这根鱼竿已维修 %d 次，达到上限，无法继续修复。", maxRepair))
	}
	missing := limit - p.Equip.Durable
	cost := missing * basePrice(p.Equip.Name) / limit / 2
	if cost < 1 {
		cost = 1
	}
	if p.Coins < cost {
		return c.Reply(ctx, fmt.Sprintf("金币不足：修复需要 %d 金币，你只有 %d。", cost, p.Coins))
	}
	p.Coins -= cost
	p.Equip.Durable = limit
	p.Equip.Maintenance++
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("修复 %s 成功，耐久恢复至 %d/%d，花费 %d 金币，维修次数 %d/%d。",
		p.Equip.Name, p.Equip.Durable, limit, cost, p.Equip.Maintenance, maxRepair))
}

func handleEquip(ctx context.Context, c sdk.Call, rest string) error {
	name := strings.TrimSpace(rest)
	p := load(ctx, c)
	if d, ok := defOf(name); !ok || (d.Kind != kindPole) {
		return c.Reply(ctx, "可装备的物品：木竿、铁竿、金竿、钻石竿、下界合金竿、三叉戟、美西螈。")
	}
	if p.Equip != nil && p.Equip.Name == name {
		return c.Reply(ctx, fmt.Sprintf("%s 已经装备中。", name))
	}
	best := -1
	for i, r := range p.Rods {
		if r.Name != name {
			continue
		}
		if r.Name != "美西螈" && r.Durable <= 0 {
			continue
		}
		if best < 0 || r.Durable > p.Rods[best].Durable {
			best = i
		}
	}
	if best < 0 {
		return c.Reply(ctx, fmt.Sprintf("背包里没有可装备的%s。", name))
	}
	chosen := p.Rods[best]
	p.Rods = append(p.Rods[:best], p.Rods[best+1:]...)
	if p.Equip != nil {
		p.Rods = append(p.Rods, *p.Equip)
	}
	p.Equip = &chosen
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, "已装备 "+rodLabel(chosen)+"。")
}

func handleEnchant(ctx context.Context, c sdk.Call, rest string) error {
	book := strings.TrimSpace(rest)
	if book != "诱钓" && book != "海之眷顾" {
		return c.Reply(ctx, "可附魔：诱钓、海之眷顾（等级 0~Ⅲ）。")
	}
	p := load(ctx, c)
	if p.Equip == nil {
		return c.Reply(ctx, "请先装备鱼竿再附魔。")
	}
	if p.Equip.Name == "美西螈" {
		return c.Reply(ctx, "美西螈无法附魔。")
	}
	if p.Treasure[book] <= 0 {
		return c.Reply(ctx, fmt.Sprintf("你的背包里没有%s附魔书。", book))
	}
	removeItem(&p, book, 1)
	level := p.Equip.Induce
	if book == "海之眷顾" {
		level = p.Equip.Favor
	}
	if level >= maxEnchant {
		_ = save(ctx, c, p)
		return c.Reply(ctx, fmt.Sprintf("%s已达到 %s 上限，这本附魔书被浪费了。", book, roman(maxEnchant)))
	}
	if randIntn(100) > 50 {
		_ = save(ctx, c, p)
		return c.Reply(ctx, "附魔失败，附魔书已消耗，鱼竿没有变化。")
	}
	if book == "诱钓" {
		p.Equip.Induce++
		level = p.Equip.Induce
	} else {
		p.Equip.Favor++
		level = p.Equip.Favor
	}
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("附魔成功！%s 现在拥有 %s%s。", p.Equip.Name, book, roman(level)))
}

var craftMaterial = map[string]string{
	"铁竿":    "木竿",
	"金竿":    "铁竿",
	"钻石竿":   "金竿",
	"下界合金竿": "钻石竿",
	"三叉戟":   "下界合金竿",
}

func handleCraft(ctx context.Context, c sdk.Call, rest string) error {
	target := strings.TrimSpace(rest)
	material, ok := craftMaterial[target]
	if !ok {
		return c.Reply(ctx, "可合成：铁竿、金竿、钻石竿、下界合金竿、三叉戟。")
	}
	p := load(ctx, c)
	if rodCount(&p, material) < 3 {
		return c.Reply(ctx, fmt.Sprintf("合成%s需要 3 根%s，你只有 %d 根。", target, material, rodCount(&p, material)))
	}
	taken := takeRods(&p, material, 3)
	induce, favor := 0, 0
	for _, r := range taken {
		induce += r.Induce
		favor += r.Favor
	}
	if randIntn(100) >= 90 {
		if err := save(ctx, c, p); err != nil {
			return err
		}
		return c.Reply(ctx, fmt.Sprintf("合成失败！3 根%s消失了。", material))
	}
	p.Rods = append(p.Rods, rodItem{
		Name:    target,
		Durable: maxDurable(target),
		Induce:  induce / 3,
		Favor:   favor / 3,
	})
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, fmt.Sprintf("合成成功！获得 %s（耐久%d/%d，继承 诱钓%s / 海之眷顾%s）。",
		target, maxDurable(target), maxDurable(target), roman(induce/3), roman(favor/3)))
}

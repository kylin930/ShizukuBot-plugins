package mcfish

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"shizukubot/sdk"
)

func rodLabel(r rodItem) string {
	if r.Name == "美西螈" {
		return "美西螈（不消耗耐久）"
	}
	parts := []string{fmt.Sprintf("%s 耐久%d/%d 维修%d/%d", r.Name, r.Durable, maxDurable(r.Name), r.Maintenance, maxRepair)}
	if r.Induce > 0 {
		parts = append(parts, "诱钓"+roman(r.Induce))
	}
	if r.Favor > 0 {
		parts = append(parts, "海之眷顾"+roman(r.Favor))
	}
	return strings.Join(parts, " ")
}

func roman(n int) string {
	switch n {
	case 1:
		return "Ⅰ"
	case 2:
		return "Ⅱ"
	case 3:
		return "Ⅲ"
	default:
		return fmt.Sprintf("%d", n)
	}
}

func handleBackpack(ctx context.Context, c sdk.Call) error {
	p := load(ctx, c)
	var b strings.Builder
	b.WriteString("【钓鱼背包】\n")
	b.WriteString(fmt.Sprintf("金币：%d\n", p.Coins))
	b.WriteString(fmt.Sprintf("功德：祝福 %d/75，诅咒 %d/10（已凝结 %d 层）\n", p.Bless, p.Curse, curseLayers(&p)))
	b.WriteString(fmt.Sprintf("优惠券：%d 张\n", p.Coupons))
	if p.Equip == nil {
		b.WriteString("装备：无\n")
	} else {
		b.WriteString("装备：" + rodLabel(*p.Equip) + "\n")
	}
	b.WriteString("备用鱼竿：" + mapLine(countRods(p.Rods), nil) + "\n")
	baits := map[string]int{}
	for name, n := range p.Bait {
		baits[name] += n
	}
	for _, name := range []string{"诱钓", "海之眷顾"} {
		baits[name] += p.Treasure[name]
	}
	b.WriteString("饵料/附魔书：" + mapLine(baits, nil) + "\n")
	b.WriteString(fmt.Sprintf("鱼获（共 %d 条）：%s\n", fishTotal(&p), mapLine(p.Fish, orderedNames(p.Fish))))
	b.WriteString("垃圾：" + mapLine(p.Waste, orderedNames(p.Waste)) + "\n")
	b.WriteString("宝藏/道具：" + mapLine(p.Treasure, orderedNames(p.Treasure)) + "\n")
	b.WriteString(fmt.Sprintf("今日钓鱼：%d/%d；今日购买/出售：%d/%d；今日鱼竿交易：%d/%d",
		p.FishToday, fishLimit, p.BuyCount+p.SellCount, dailyItemTrade, p.RodTrade, dailyRodTrade))
	return c.Reply(ctx, b.String())
}

func countRods(rods []rodItem) map[string]int {
	m := map[string]int{}
	for _, r := range rods {
		m[r.Name]++
	}
	return m
}

// mapLine 输出“物品×数量”，order 为空时按名称排序。
func mapLine(m map[string]int, order []string) string {
	if len(m) == 0 {
		return "无"
	}
	names := order
	if len(names) == 0 {
		names = make([]string, 0, len(m))
		for name := range m {
			if m[name] > 0 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if m[name] > 0 {
			parts = append(parts, fmt.Sprintf("%s×%d", name, m[name]))
		}
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, "、")
}

func handleProbabilities(ctx context.Context, c sdk.Call) error {
	p := load(ctx, c)
	total := fishTotal(&p)
	dolphin := p.Fish["海豚"] > 0
	z := currentZones(p.Equip, total, dolphin)
	var b strings.Builder
	b.WriteString("【当前装备概率明细】\n")
	if p.Equip == nil {
		b.WriteString("装备：无（按默认概率计算）\n")
	} else {
		b.WriteString("装备：" + rodLabel(*p.Equip) + "\n")
	}
	if total > 100 || (p.Equip != nil && p.Equip.Name == "美西螈") {
		b.WriteString("状态：鱼竿加成生效，物品概率提高至 90%\n")
	}
	if dolphin {
		b.WriteString("状态：拥有海豚，垃圾填满剩余概率\n")
	}
	if p.Equip != nil && p.Equip.Induce > 0 {
		b.WriteString(fmt.Sprintf("诱钓%s：预计咬钩等待 %d 秒\n", roman(p.Equip.Induce), 120/(p.Equip.Induce+1)))
	}
	tw, pw, fw, ww := z.treasureMax, z.poleMax-z.poleMin, z.fishMax-z.fishMin, z.wasteMax-z.wasteMin
	b.WriteString(fmt.Sprintf("区域概率：宝藏 %d%%、鱼竿 %d%%、鱼类 %d%%、垃圾 %d%%、赛博空气 %d%%\n",
		tw, pw, fw, ww, 100-z.wasteMax))
	b.WriteString("—— 宝藏 ——\n")
	writePool(&b, treasureRoll, tw)
	b.WriteString("—— 鱼竿 ——\n")
	writePool(&b, poleRoll, pw)
	b.WriteString("—— 鱼类 ——\n")
	writePool(&b, fishRoll, fw)
	b.WriteString("—— 垃圾 ——\n")
	writePool(&b, wasteRoll, ww)
	b.WriteString("概率为单次上钩的总体概率。")
	return c.Reply(ctx, b.String())
}

func writePool(b *strings.Builder, pool []string, width int) {
	for _, name := range pool {
		d, _ := defOf(name)
		b.WriteString(fmt.Sprintf("%s：%.2f%%\n", name, float64(d.Prob*width)/100))
	}
}

func handleRules(ctx context.Context, c sdk.Call) error {
	text := fmt.Sprintf(`【Minecraft 钓鱼规则 v%s】
1. 每日最多进行 %d 次钓鱼，跨日重置；「进行n次钓鱼」会受剩余次数和鱼竿耐久限制。
2. 每次钓鱼消耗 1 点耐久；三叉戟一次产出 3 份掉落；美西螈不消耗耐久，但每次需吞食 3 条鱼，并使非鱼竿掉落变为 5 倍。
3. 鱼竿耐久耗尽会损坏，可使用「修复鱼竿」消耗金币恢复，维修次数上限 %d 次。
4. 功德：每钓鱼 1 次祝福+1，满 75 得 1 本净化书；买卖鱼竿积累诅咒，满 10 层得 1 个宝藏诅咒。
5. 诅咒会让售价降低、购买价格提高；「消除绑定诅咒」或「消除宝藏诅咒」会消耗 1 本净化书和 1 层诅咒。
6. 每购买 20 件非鱼竿物品可获得 3 张半价优惠券；每日普通物品买卖合计上限 %d，鱼竿交易上限 %d。
7. 鱼竿、三叉戟、美西螈可用「装备xxx」切换；附魔「诱钓」「海之眷顾」等级 0~Ⅲ，前者加快咬钩，后者提高宝藏/鱼竿/鱼类的概率并压低垃圾。
8. 合成：3 根同名鱼竿合成下一级（木→铁→金→钻石→下界合金，下界合金竿→三叉戟），成功率 90%%，失败材料消失。
9. 出售鱼竿会按耐久、维修次数与附魔估值；出售物品统一扣除 20%% 交易税。
10. 指令：钓鱼背包 / 进行钓鱼 / 进行n次钓鱼 / 修复鱼竿 / 钓鱼商店 / 购买xxx [数量] / 出售xxx [数量] / 出售所有垃圾 / 装备xx / 附魔xx / 合成xx / 消除宝藏诅咒 [数量] / 当前装备概率明细 / 查看钓鱼规则。`,
		version, fishLimit, maxRepair, dailyItemTrade, dailyRodTrade)
	return c.Reply(ctx, text)
}

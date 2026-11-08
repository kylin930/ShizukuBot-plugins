package mcfish

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"shizukubot/sdk"
)

// zoneRange 是当前装备/附魔下四类掉落物的累计概率区间 [Min, Max)。
// 默认累计值与上游 fish.go 的 probableList（1/4/35/60）一致，
// 触发“物品概率提高至90%”时改为 2/10/45/90，剩余为赛博空气。
type zoneRange struct {
	treasureMax, poleMin, poleMax, fishMin, fishMax, wasteMin, wasteMax int
}

func currentZones(equip *rodItem, totalFish int, dolphin bool) zoneRange {
	z := zoneRange{treasureMax: 1, poleMin: 1, poleMax: 4, fishMin: 4, fishMax: 35, wasteMin: 35, wasteMax: 60}
	if totalFish > 100 || (equip != nil && equip.Name == "美西螈") {
		z = zoneRange{treasureMax: 2, poleMin: 2, poleMax: 10, fishMin: 10, fishMax: 45, wasteMin: 45, wasteMax: 90}
	}
	if dolphin {
		// 海豚让垃圾概率占满剩余区间，不再有赛博空气。
		z.wasteMax = 100
	}
	favor := 0
	if equip != nil {
		favor = equip.Favor
	}
	z.treasureMax += favor
	z.poleMin += favor
	z.poleMax += favor * 2
	z.fishMin += favor * 2
	z.fishMax += favor * 3
	z.wasteMin += favor * 3
	return z
}

// rollOne 掷一次掉落；ok=false 表示钓到赛博空气。
func rollOne(p *player) (string, bool) {
	return rollWithZones(currentZones(p.Equip, fishTotal(p), p.Fish["海豚"] > 0))
}

func rollWithZones(z zoneRange) (string, bool) {
	dice := randIntn(100)
	if dice >= z.wasteMax {
		return "", false
	}
	if dice >= z.wasteMin {
		return weightedRoll(wasteRoll), true
	}
	if dice < z.treasureMax {
		return weightedRoll(treasureRoll), true
	}
	if dice < z.poleMax {
		return weightedRoll(poleRoll), true
	}
	if dice < z.fishMax {
		return weightedRoll(fishRoll), true
	}
	return "", false
}

func newCaughtRod(name string) rodItem {
	durable := maxDurable(name)
	return rodItem{
		Name:        name,
		Durable:     1 + randIntn(durable),
		Maintenance: randIntn(10),
		Induce:      randIntn(3),
		Favor:       randIntn(2),
	}
}

// eatFish 供美西螈吞食，优先吃掉常见鱼。
func eatFish(p *player, need int) {
	for _, name := range fishRoll {
		if need <= 0 {
			return
		}
		take := p.Fish[name]
		if take > need {
			take = need
		}
		if take > 0 {
			removeItem(p, name, take)
			need -= take
		}
	}
	if need <= 0 {
		return
	}
	names := make([]string, 0, len(p.Fish))
	for name := range p.Fish {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if need <= 0 {
			return
		}
		take := p.Fish[name]
		if take > need {
			take = need
		}
		removeItem(p, name, take)
		need -= take
	}
}

func handleFish(ctx context.Context, c sdk.Call, n int) error {
	if n > fishLimit {
		return c.Reply(ctx, "请输入正确的次数")
	}
	p := load(ctx, c)
	if p.Equip == nil {
		if len(p.Rods) > 0 {
			return c.Reply(ctx, "你还没有装备鱼竿，请使用「装备木竿」等指令先装备背包里的鱼竿。")
		}
		return c.Reply(ctx, "你还没有鱼竿，请先使用「购买初始木竿」，再使用「装备木竿」。")
	}
	remaining := fishLimit - p.FishToday
	if remaining <= 0 {
		_ = save(ctx, c, p)
		return c.Reply(ctx, fmt.Sprintf("今日钓鱼次数已用完（%d/%d），明天再来吧。", p.FishToday, fishLimit))
	}
	requested := n
	if n > remaining {
		n = remaining
	}
	if n <= 0 {
		return c.Reply(ctx, "钓鱼次数必须大于 0。")
	}

	equipName := p.Equip.Name
	notes := make([]string, 0, 3)
	if requested > n {
		notes = append(notes, fmt.Sprintf("今日仅剩 %d 次，本次只进行了 %d 次。", remaining, n))
	}

	if equipName == "美西螈" {
		need := n * 3
		if fishTotal(&p) < need {
			p.Equip = nil
			_ = save(ctx, c, p)
			return c.Reply(ctx, "美西螈没有足够的鱼可以吃，已经离你而去了。")
		}
		eatFish(&p, need)
		notes = append(notes, fmt.Sprintf("美西螈吞食了 %d 条鱼。", need))
	} else {
		if p.Equip.Durable <= 0 {
			p.Equip = nil
			_ = save(ctx, c, p)
			return c.Reply(ctx, "你的鱼竿耐久已耗尽，请先修复或更换。")
		}
		if n > p.Equip.Durable {
			n = p.Equip.Durable
			notes = append(notes, fmt.Sprintf("鱼竿耐久不足，本次只进行了 %d 次。", n))
		}
	}

	drops := n
	if equipName == "三叉戟" {
		drops = n * 3
	}
	zones := currentZones(p.Equip, fishTotal(&p), p.Fish["海豚"] > 0)
	counts := map[string]int{}
	air := 0
	for i := 0; i < drops; i++ {
		name, ok := rollWithZones(zones)
		if !ok {
			air++
			continue
		}
		counts[name]++
	}
	if equipName == "美西螈" {
		// 上游：美西螈使非鱼竿、非美西螈掉落变为 5 倍。
		for name, count := range counts {
			d, _ := defOf(name)
			if d.Kind != kindPole && name != "美西螈" {
				counts[name] = count * 5
			}
		}
	}

	gainedRods := map[string][]rodItem{}
	for name, count := range counts {
		if isRod(name) {
			for i := 0; i < count; i++ {
				r := newCaughtRod(name)
				p.Rods = append(p.Rods, r)
				gainedRods[name] = append(gainedRods[name], r)
			}
			continue
		}
		addItem(&p, name, count)
	}

	destroyed := false
	if equipName != "美西螈" {
		p.Equip.Durable -= n
		if p.Equip.Durable <= 0 {
			destroyed = true
			p.Equip = nil
		}
	}
	p.FishToday += n
	addBless(&p, n)
	if err := save(ctx, c, p); err != nil {
		return err
	}
	return c.Reply(ctx, formatFishResult(&p, n, counts, air, gainedRods, notes, destroyed))
}

func formatFishResult(p *player, n int, counts map[string]int, air int, rods map[string][]rodItem, notes []string, destroyed bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("你进行了 %d 次钓鱼：\n", n))
	lines := make([]string, 0, 8)
	for _, name := range orderedNames(counts) {
		count := counts[name]
		if count <= 0 {
			continue
		}
		if isRod(name) {
			parts := make([]string, 0, len(rods[name]))
			for _, r := range rods[name] {
				parts = append(parts, fmt.Sprintf("%s 耐久%d/%d", r.Name, r.Durable, maxDurable(r.Name)))
			}
			lines = append(lines, fmt.Sprintf("鱼竿×%d（%s）", count, strings.Join(parts, "，")))
		} else {
			lines = append(lines, fmt.Sprintf("%s×%d", name, count))
		}
	}
	if air > 0 {
		lines = append(lines, fmt.Sprintf("赛博空气×%d", air))
	}
	if len(lines) == 0 {
		lines = append(lines, "什么也没有钓到。")
	}
	b.WriteString(strings.Join(lines, "\n"))
	if p.Equip != nil {
		if p.Equip.Name == "美西螈" {
			b.WriteString("\n当前装备：美西螈（不消耗耐久）")
		} else {
			b.WriteString(fmt.Sprintf("\n当前装备：%s，耐久 %d/%d", p.Equip.Name, p.Equip.Durable, maxDurable(p.Equip.Name)))
		}
	}
	if destroyed {
		b.WriteString("\n你的鱼竿因耐久耗尽损坏了！")
	}
	b.WriteString(fmt.Sprintf("\n今日钓鱼：%d/%d", p.FishToday, fishLimit))
	for _, note := range notes {
		b.WriteString("\n" + note)
	}
	return b.String()
}

// orderedNames 按鱼竿、宝藏、鱼、垃圾、旧版鱼名的顺序输出，保证结果稳定。
func orderedNames(m map[string]int) []string {
	order := make([]string, 0, len(poleRoll)+len(treasureRoll)+len(fishRoll)+len(wasteRoll))
	order = append(order, poleRoll...)
	order = append(order, treasureRoll...)
	order = append(order, fishRoll...)
	order = append(order, wasteRoll...)
	seen := map[string]bool{}
	out := make([]string, 0, len(m))
	for _, name := range order {
		if seen[name] {
			continue
		}
		seen[name] = true
		if m[name] > 0 {
			out = append(out, name)
		}
	}
	extra := make([]string, 0, len(m))
	for name, n := range m {
		if n > 0 && !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

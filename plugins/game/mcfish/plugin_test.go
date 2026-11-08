package mcfish

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

const testKey = "mcfish:u1"

type memState struct {
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (m *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return m.data[key], nil
}

func (m *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	m.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

func newEnv(t *testing.T) (*memState, time.Time) {
	t.Helper()
	oldNow, oldRand := nowFunc, randIntn
	now := time.Date(2026, 1, 8, 12, 0, 0, 0, time.Local)
	nowFunc = func() time.Time { return now }
	randIntn = func(int) int { return 0 }
	t.Cleanup(func() {
		nowFunc, randIntn = oldNow, oldRand
	})
	return newMemState(), now
}

func seed(t *testing.T, st *memState, p player, now time.Time) {
	t.Helper()
	initMaps(&p)
	today := now.Format("2006-01-02")
	if p.FishDate == "" {
		p.FishDate = today
	}
	if p.TradeDate == "" {
		p.TradeDate = today
	}
	if p.ShopDate == "" {
		p.ShopDate = today
	}
	for _, name := range shopNames {
		if p.Discounts[name] == 0 {
			p.Discounts[name] = 100
		}
		if _, ok := p.Stock[name]; !ok {
			p.Stock[name] = defaultStock(name)
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal player: %v", err)
	}
	st.data[testKey] = raw
}

func loadTest(_ *testing.T, st *memState) player {
	c := sdk.Call{Event: sdk.Event{UserID: "u1", GroupID: "g1"}, State: st}
	return load(context.Background(), c)
}

func run(t *testing.T, st *memState, msg string) string {
	t.Helper()
	reply := ""
	c := sdk.Call{
		Event: sdk.Event{
			Message: msg, UserID: "u1", GroupID: "g1",
			Reply: func(_ context.Context, r sdk.Reply) error {
				reply = r.Text
				return nil
			},
		},
		State: st,
	}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
	return reply
}

// fishRand 让一次掷点落在鱼类区间，并落在鳕鱼上。
func fishRand(n int) int {
	if n > 20 {
		return 20
	}
	return 0
}

func TestFishProduceAndDailyLimit(t *testing.T) {
	st, now := newEnv(t)
	randIntn = fishRand
	seed(t, st, player{
		Equip:     &rodItem{Name: "木竿", Durable: 30},
		FishToday: 49,
	}, now)

	got := run(t, st, "进行2次钓鱼")
	if !strings.Contains(got, "鳕鱼×1") || !strings.Contains(got, "仅剩 1 次") {
		t.Fatalf("unexpected fishing reply: %s", got)
	}
	p := loadTest(t, st)
	if p.FishToday != fishLimit || p.Fish["鳕鱼"] != 1 || p.Equip.Durable != 29 || p.Bless != 1 {
		t.Fatalf("state after fishing: %+v", p)
	}
	if again := run(t, st, "钓鱼"); !strings.Contains(again, "已用完") {
		t.Fatalf("daily limit not enforced: %s", again)
	}
}

func TestFishDurabilityAndTrident(t *testing.T) {
	st, now := newEnv(t)
	randIntn = fishRand
	seed(t, st, player{Equip: &rodItem{Name: "木竿", Durable: 1}}, now)

	got := run(t, st, "进行3次钓鱼")
	if !strings.Contains(got, "耐久不足") || !strings.Contains(got, "损坏") {
		t.Fatalf("unexpected durability reply: %s", got)
	}
	if p := loadTest(t, st); p.Equip != nil {
		t.Fatalf("broken rod should be removed: %+v", p)
	}

	st2, now2 := newEnv(t)
	randIntn = fishRand
	seed(t, st2, player{Equip: &rodItem{Name: "三叉戟", Durable: 300}}, now2)
	got = run(t, st2, "进行2次钓鱼")
	if !strings.Contains(got, "鳕鱼×6") {
		t.Fatalf("trident should produce 3x drops: %s", got)
	}
	if p := loadTest(t, st2); p.Equip.Durable != 298 || p.FishToday != 2 {
		t.Fatalf("trident state: %+v", p)
	}
}

func TestAxolotlMechanic(t *testing.T) {
	st, now := newEnv(t)
	randIntn = fishRand
	seed(t, st, player{
		Equip: &rodItem{Name: "美西螈"},
		Fish:  map[string]int{"鳕鱼": 10},
	}, now)

	got := run(t, st, "钓鱼")
	if !strings.Contains(got, "吞食了 3 条鱼") || !strings.Contains(got, "鳕鱼×5") {
		t.Fatalf("unexpected axolotl reply: %s", got)
	}
	p := loadTest(t, st)
	if p.Fish["鳕鱼"] != 12 || p.Equip == nil || p.Equip.Name != "美西螈" {
		t.Fatalf("axolotl state: %+v", p)
	}

	st2, now2 := newEnv(t)
	seed(t, st2, player{Equip: &rodItem{Name: "美西螈"}, Fish: map[string]int{"鳕鱼": 1}}, now2)
	if got := run(t, st2, "钓鱼"); !strings.Contains(got, "离你而去") {
		t.Fatalf("axolotl should leave: %s", got)
	}
	if p := loadTest(t, st2); p.Equip != nil {
		t.Fatalf("axolotl should be unequipped: %+v", p)
	}
}

func TestShopBuySell(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{Coins: 1000}, now)

	buy := run(t, st, "购买木竿")
	if !strings.Contains(buy, "购买 木竿×1 成功") || !strings.Contains(buy, "花费 100 金币") {
		t.Fatalf("unexpected buy reply: %s", buy)
	}
	p := loadTest(t, st)
	if p.Coins != 900 || len(p.Rods) != 1 || p.Stock["木竿"] != 4 || p.RodTrade != 1 || p.Curse != 1 {
		t.Fatalf("state after buy: %+v", p)
	}

	sell := run(t, st, "出售木竿")
	if !strings.Contains(sell, "获得 80 金币") {
		t.Fatalf("unexpected sell reply: %s", sell)
	}
	p = loadTest(t, st)
	if p.Coins != 980 || len(p.Rods) != 0 || p.RodTrade != 2 || p.Curse != 2 {
		t.Fatalf("state after sell: %+v", p)
	}
}

func TestCouponsAndBuyLimit(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{Coins: 10000, CouponProgress: 19}, now)

	got := run(t, st, "购买鳕鱼")
	if !strings.Contains(got, "花费 10 金币") {
		t.Fatalf("unexpected first buy: %s", got)
	}
	p := loadTest(t, st)
	if p.Coupons != 3 || p.BuyCount != 1 || p.Coins != 9990 {
		t.Fatalf("coupon milestone state: %+v", p)
	}

	got = run(t, st, "购买鳕鱼 10")
	if !strings.Contains(got, "优惠券 3 张") || !strings.Contains(got, "花费 85 金币") {
		t.Fatalf("unexpected coupon buy: %s", got)
	}
	p = loadTest(t, st)
	if p.Coins != 9905 || p.Coupons != 0 || p.BuyCount != 11 {
		t.Fatalf("state after coupon buy: %+v", p)
	}
}

func TestFishCommandCountRange(t *testing.T) {
	st, now := newEnv(t)
	randIntn = fishRand
	seed(t, st, player{Equip: &rodItem{Name: "木竿", Durable: 30}}, now)

	// 超过 59 次不匹配上游正则，不应有任何回复。
	if got := run(t, st, "进行60次钓鱼"); got != "" {
		t.Fatalf("60 times should be ignored: %q", got)
	}
	// 1–59 能匹配，但每日上限 50，超过时与上游一致提示次数错误。
	if got := run(t, st, "进行59次钓鱼"); !strings.Contains(got, "请输入正确的次数") {
		t.Fatalf("59 times = %q", got)
	}
	if got := run(t, st, "进行0次钓鱼"); got != "" {
		t.Fatalf("0 times should be ignored: %q", got)
	}
}

func TestRepair(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{
		Coins: 1000,
		Equip: &rodItem{Name: "木竿", Durable: 10, Maintenance: 9},
	}, now)

	got := run(t, st, "维修鱼竿")
	if !strings.Contains(got, "耐久恢复至 30/30") || !strings.Contains(got, "花费 33 金币") {
		t.Fatalf("unexpected repair reply: %s", got)
	}
	p := loadTest(t, st)
	if p.Coins != 967 || p.Equip.Durable != 30 || p.Equip.Maintenance != 10 {
		t.Fatalf("state after repair: %+v", p)
	}

	st2, now2 := newEnv(t)
	seed(t, st2, player{Coins: 1000, Equip: &rodItem{Name: "木竿", Durable: 10, Maintenance: 10}}, now2)
	if got := run(t, st2, "修复鱼竿"); !strings.Contains(got, "达到上限") {
		t.Fatalf("maintenance limit not enforced: %s", got)
	}
}

func TestEquipAndEnchant(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{
		Equip:    &rodItem{Name: "木竿", Durable: 30},
		Rods:     []rodItem{{Name: "铁竿", Durable: 50}},
		Treasure: map[string]int{"诱钓": 1},
	}, now)

	if got := run(t, st, "装备铁竿"); !strings.Contains(got, "已装备 铁竿") {
		t.Fatalf("unexpected equip reply: %s", got)
	}
	p := loadTest(t, st)
	if p.Equip.Name != "铁竿" || rodCount(&p, "木竿") != 1 {
		t.Fatalf("state after equip: %+v", p)
	}

	randIntn = func(int) int { return 0 }
	if got := run(t, st, "附魔诱钓"); !strings.Contains(got, "附魔成功") || !strings.Contains(got, "诱钓Ⅰ") {
		t.Fatalf("unexpected enchant reply: %s", got)
	}
	p = loadTest(t, st)
	if p.Equip.Induce != 1 || p.Treasure["诱钓"] != 0 {
		t.Fatalf("state after enchant: %+v", p)
	}
	if got := run(t, st, "附魔海之眷顾"); !strings.Contains(got, "没有海之眷顾附魔书") {
		t.Fatalf("missing book should fail: %s", got)
	}
}

func TestCraft(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{Rods: []rodItem{
		{Name: "木竿", Durable: 30}, {Name: "木竿", Durable: 30}, {Name: "木竿", Durable: 30},
	}}, now)

	got := run(t, st, "合成铁竿")
	if !strings.Contains(got, "合成成功") || !strings.Contains(got, "铁竿") {
		t.Fatalf("unexpected craft reply: %s", got)
	}
	p := loadTest(t, st)
	if rodCount(&p, "铁竿") != 1 || rodCount(&p, "木竿") != 0 {
		t.Fatalf("state after craft: %+v", p)
	}

	st2, now2 := newEnv(t)
	seed(t, st2, player{Rods: []rodItem{
		{Name: "木竿", Durable: 30}, {Name: "木竿", Durable: 30}, {Name: "木竿", Durable: 30},
	}}, now2)
	randIntn = func(n int) int {
		if n == 100 {
			return 99
		}
		return 0
	}
	if got := run(t, st2, "合成铁竿"); !strings.Contains(got, "合成失败") {
		t.Fatalf("unexpected craft failure: %s", got)
	}
	if p := loadTest(t, st2); len(p.Rods) != 0 {
		t.Fatalf("failed craft should consume materials: %+v", p)
	}
}

func TestCurseRemovalAndSalePenalty(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{
		Treasure: map[string]int{"净化书": 2, "宝藏诅咒": 3},
		Fish:     map[string]int{"鳕鱼": 10},
	}, now)

	got := run(t, st, "消除绑定诅咒2")
	if !strings.Contains(got, "消除了 2 层诅咒") {
		t.Fatalf("unexpected curse reply: %s", got)
	}
	p := loadTest(t, st)
	if p.Treasure["宝藏诅咒"] != 1 || p.Treasure["净化书"] != 0 {
		t.Fatalf("state after curse removal: %+v", p)
	}

	sell := run(t, st, "出售鳕鱼10")
	if !strings.Contains(sell, "获得 70 金币") || !strings.Contains(sell, "诅咒系数 90%") {
		t.Fatalf("curse should reduce sale price: %s", sell)
	}
	if got := run(t, st, "消除宝藏诅咒"); !strings.Contains(got, "净化书不足") {
		t.Fatalf("missing books should fail: %s", got)
	}
}

func TestSellAllWaste(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{Waste: map[string]int{"垃圾": 3}}, now)

	got := run(t, st, "出售所有垃圾")
	if !strings.Contains(got, "垃圾×3") || !strings.Contains(got, "获得 24 金币") {
		t.Fatalf("unexpected waste sale: %s", got)
	}
	if p := loadTest(t, st); len(p.Waste) != 0 || p.Coins != 24 {
		t.Fatalf("state after waste sale: %+v", p)
	}
}

// TestNonRodTradeDoesNotAddCurse 校验美西螈与三叉戟买卖不累积诅咒。
func TestNonRodTradeDoesNotAddCurse(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{
		Coins: 100000,
		Rods:  []rodItem{{Name: "三叉戟", Durable: 300}, {Name: "美西螈"}},
		Stock: map[string]int{"三叉戟": 1, "美西螈": 1},
	}, now)

	if got := run(t, st, "出售三叉戟"); !strings.Contains(got, "出售") {
		t.Fatalf("sell trident = %q", got)
	}
	if got := run(t, st, "出售美西螈"); !strings.Contains(got, "出售") {
		t.Fatalf("sell axolotl = %q", got)
	}
	if got := run(t, st, "购买三叉戟"); !strings.Contains(got, "购买") {
		t.Fatalf("buy trident = %q", got)
	}
	if got := run(t, st, "购买美西螈"); !strings.Contains(got, "购买") {
		t.Fatalf("buy axolotl = %q", got)
	}
	if p := loadTest(t, st); p.Curse != 0 || curseLayers(&p) != 0 {
		t.Fatalf("non-rod trades should not add curse: %+v", p)
	}
}

func TestProbabilitiesAndRules(t *testing.T) {
	st, now := newEnv(t)
	seed(t, st, player{Equip: &rodItem{Name: "木竿", Durable: 30}}, now)

	got := run(t, st, "当前装备概率明细")
	for _, want := range []string{
		"宝藏 1%", "鱼竿 3%", "鱼类 31%", "垃圾 25%", "赛博空气 40%",
		"木竿：2.10%", "鳕鱼：21.39%",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("probability detail missing %q:\n%s", want, got)
		}
	}

	st2, now2 := newEnv(t)
	seed(t, st2, player{
		Equip: &rodItem{Name: "木竿", Durable: 30},
		Fish:  map[string]int{"鳕鱼": 101},
	}, now2)
	got = run(t, st2, "当前装备概率明细")
	if !strings.Contains(got, "物品概率提高至 90%") || !strings.Contains(got, "宝藏 2%") ||
		!strings.Contains(got, "垃圾 45%") || !strings.Contains(got, "赛博空气 10%") {
		t.Fatalf("boosted probabilities wrong:\n%s", got)
	}

	st3, now3 := newEnv(t)
	seed(t, st3, player{Equip: &rodItem{Name: "木竿", Durable: 30, Favor: 2}}, now3)
	got = run(t, st3, "当前装备概率明细")
	if !strings.Contains(got, "宝藏 3%") || !strings.Contains(got, "鱼竿 5%") {
		t.Fatalf("favor probabilities wrong:\n%s", got)
	}

	rules := run(t, st, "查看钓鱼规则")
	if !strings.Contains(rules, "v5.6.2") || !strings.Contains(rules, "每日最多进行 50 次钓鱼") {
		t.Fatalf("unexpected rules: %s", rules)
	}
}

func TestLegacyStateMigration(t *testing.T) {
	st, _ := newEnv(t)
	legacy := `{"coins":500,"rod_level":2,"last_fish":123,"pack":{"鳕鱼":2,"鲨鱼":1},"weight":3.5}`
	st.data[testKey] = json.RawMessage(legacy)

	p := loadTest(t, st)
	if p.Coins != 500 || p.Equip == nil || p.Equip.Name != "金竿" || p.Equip.Durable != 70 {
		t.Fatalf("legacy coins/rod not migrated: %+v", p)
	}
	if p.Fish["鳕鱼"] != 2 || p.Fish["鲨鱼"] != 1 || p.Pack != nil {
		t.Fatalf("legacy pack not migrated: %+v", p)
	}
	if p.LastFish != 123 || p.Weight != 3.5 || p.RodLevel != 2 {
		t.Fatalf("legacy fields lost: %+v", p)
	}
	if got := run(t, st, "钓鱼背包"); !strings.Contains(got, "鳕鱼×2") || !strings.Contains(got, "鲨鱼×1") {
		t.Fatalf("backpack missing migrated items: %s", got)
	}
}

func TestDailyReset(t *testing.T) {
	st, now := newEnv(t)
	randIntn = fishRand
	seed(t, st, player{
		Equip:     &rodItem{Name: "木竿", Durable: 30},
		FishToday: 50,
		FishDate:  now.AddDate(0, 0, -1).Format("2006-01-02"),
		BuyCount:  30,
		TradeDate: now.AddDate(0, 0, -1).Format("2006-01-02"),
	}, now)

	got := run(t, st, "钓鱼")
	if !strings.Contains(got, "鳕鱼") {
		t.Fatalf("daily counters should reset: %s", got)
	}
	p := loadTest(t, st)
	if p.FishToday != 1 || p.BuyCount != 0 {
		t.Fatalf("daily reset state: %+v", p)
	}
}

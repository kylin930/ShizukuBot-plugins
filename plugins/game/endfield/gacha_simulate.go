package endfield

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const simulateStatePrefix = "endfield:simulate:"

type simulateCommand struct {
	mode  string
	pool  string
	valid bool
}

type simulateState struct {
	PoolType                     string `json:"pool_type,omitempty"`
	SixStarPity                  int    `json:"six_star_pity"`
	FiveStarPity                 int    `json:"five_star_pity"`
	TotalPulls                   int    `json:"total_pulls"`
	GuaranteedLimitedPity        int    `json:"guaranteed_limited_pity"`
	HasReceivedGuaranteedLimited bool   `json:"has_received_guaranteed_limited"`
	IsGuaranteedUp               bool   `json:"is_guaranteed_up"`
	SixStarCount                 int    `json:"six_star_count"`
	FiveStarCount                int    `json:"five_star_count"`
	FourStarCount                int    `json:"four_star_count"`
	UpSixStarCount               int    `json:"up_six_star_count"`
	FreeTenPullsReceived         int    `json:"free_ten_pulls_received"`
}

type simulateStore struct {
	Pools     map[string]*simulateState `json:"pools,omitempty"`
	DailyDate string                    `json:"daily_date,omitempty"`
	Daily     map[string]int            `json:"daily,omitempty"`
}

type simulateRules struct {
	SixStarPity           int
	BaseProbability       float64
	SoftPityStart         int
	SoftPityIncrease      float64
	HasSoftPity           bool
	FiveStarPity          int
	FiveStarProbability   float64
	GuaranteedLimitedPity int
	UpProbability         float64
}

type simulateResult struct {
	PullNumber     int  `json:"pull_number"`
	Rarity         int  `json:"rarity"`
	IsUp           bool `json:"is_up,omitempty"`
	IsLimited      bool `json:"is_limited,omitempty"`
	PityWhenPulled int  `json:"pity_when_pulled,omitempty"`
}

type simulateStats struct {
	TotalPulls     int
	SixStarCount   int
	FiveStarCount  int
	FourStarCount  int
	UpSixStarCount int
	UpRate         string
	CurrentPity    int
}

type simulatePoolChar struct {
	CharID string `json:"char_id"`
	Name   string `json:"name"`
	Cover  string `json:"cover"`
	Rarity int    `json:"rarity"`
	IsUp   bool   `json:"is_up"`
}

type simulatePoolChars struct {
	PoolType string             `json:"pool_type"`
	Star6    []simulatePoolChar `json:"star6_chars"`
	Star5    []simulatePoolChar `json:"star5_chars"`
	Star4    []simulatePoolChar `json:"star4_chars"`
}

func init() {
	registerRoute(130, exactMatch("重置全员抽卡"), handleSimulateResetAll)
	registerRoute(125, exactMatch("重置抽卡", "抽卡重置"), handleSimulateReset)
	registerRoute(112, simulateMatch, handleSimulateDispatch)
}

func simulateMatch(text string) (string, bool) {
	command := parseSimulateCommand(text)
	if !command.valid {
		return "", false
	}
	return "", true
}

func parseSimulateCommand(text string) simulateCommand {
	trimmed := strings.TrimSpace(text)
	modes := []struct {
		mode    string
		keyword string
	}{
		{mode: "hundred", keyword: "百连"},
		{mode: "ten", keyword: "十连"},
		{mode: "single", keyword: "单抽"},
	}
	for _, item := range modes {
		if !strings.HasPrefix(trimmed, item.keyword) {
			continue
		}
		rest := strings.TrimSpace(trimmed[len(item.keyword):])
		rest = strings.TrimSpace(strings.Trim(rest, "（）()"))
		pool := "limited"
		switch rest {
		case "", "UP", "up", "限定":
			pool = "limited"
		case "常驻":
			pool = "standard"
		case "武器":
			pool = "weapon"
		default:
			return simulateCommand{}
		}
		return simulateCommand{mode: item.mode, pool: pool, valid: true}
	}
	return simulateCommand{}
}

func simulateKey(userID string) string { return simulateStatePrefix + userID }

func simulatePoolLabel(pool string) string {
	switch pool {
	case "standard":
		return getMessage("gacha_simulate.pool_standard")
	case "weapon":
		return getMessage("gacha_simulate.pool_weapon")
	default:
		return getMessage("gacha_simulate.pool_limited")
	}
}

func simulateRulesFor(pool string) simulateRules {
	if pool == "weapon" {
		return simulateRules{
			SixStarPity:           40,
			BaseProbability:       0.04,
			SoftPityStart:         33,
			SoftPityIncrease:      0.05,
			HasSoftPity:           false,
			FiveStarPity:          10,
			FiveStarProbability:   0.08,
			GuaranteedLimitedPity: 80,
			UpProbability:         0.5,
		}
	}
	if pool == "standard" {
		return simulateRules{
			SixStarPity:           80,
			BaseProbability:       0.008,
			SoftPityStart:         65,
			SoftPityIncrease:      0.05,
			HasSoftPity:           true,
			FiveStarPity:          10,
			FiveStarProbability:   0.08,
			GuaranteedLimitedPity: 0,
			UpProbability:         0,
		}
	}
	return simulateRules{
		SixStarPity:           80,
		BaseProbability:       0.008,
		SoftPityStart:         65,
		SoftPityIncrease:      0.05,
		HasSoftPity:           true,
		FiveStarPity:          10,
		FiveStarProbability:   0.08,
		GuaranteedLimitedPity: 120,
		UpProbability:         0.5,
	}
}

func simulateCheckAllowed(cfg config, c sdk.Call) (bool, string) {
	if !cfg.SimulateEnabled {
		return false, getMessage("gacha.simulate_disabled")
	}
	if c.Event.GroupID != "" && len(cfg.SimulateGroups) > 0 {
		for _, groupID := range cfg.SimulateGroups {
			if groupID == c.Event.GroupID {
				return true, ""
			}
		}
		return false, getMessage("gacha.simulate_group_not_allowed")
	}
	return true, ""
}

func simulateDailyLimit(cfg config, pool string) int {
	switch pool {
	case "standard":
		return cfg.DailyLimitStandard
	case "weapon":
		return cfg.DailyLimitWeapon
	default:
		return cfg.DailyLimitLimited
	}
}

func simulateLoadStore(ctx context.Context, st sdk.State, userID string) simulateStore {
	store := simulateStore{}
	_ = sdk.LoadJSON(ctx, st, simulateKey(userID), &store)
	if store.Pools == nil {
		store.Pools = map[string]*simulateState{}
	}
	today := time.Now().Format("2006-01-02")
	if store.DailyDate != today {
		store.DailyDate = today
		store.Daily = map[string]int{}
	}
	if store.Daily == nil {
		store.Daily = map[string]int{}
	}
	return store
}

func simulateSaveStore(ctx context.Context, st sdk.State, userID string, store simulateStore) {
	_ = sdk.SaveJSON(ctx, st, simulateKey(userID), store)
}

func simulateStateFor(store *simulateStore, pool string) *simulateState {
	if store.Pools == nil {
		store.Pools = map[string]*simulateState{}
	}
	state := store.Pools[pool]
	if state == nil {
		state = &simulateState{PoolType: pool}
		store.Pools[pool] = state
	}
	return state
}

func simulatePull(state *simulateState, rules simulateRules, allowUp, isLimited bool) simulateResult {
	state.TotalPulls++
	state.SixStarPity++
	state.FiveStarPity++
	if allowUp {
		state.GuaranteedLimitedPity++
	}

	probability := rules.BaseProbability
	if rules.HasSoftPity && state.SixStarPity >= rules.SoftPityStart {
		probability += float64(state.SixStarPity-rules.SoftPityStart+1) * rules.SoftPityIncrease
	}
	if state.SixStarPity >= rules.SixStarPity {
		probability = 1
	}
	if rand.Float64() < probability {
		pull := state.SixStarPity
		state.SixStarPity = 0
		state.FiveStarPity = 0
		isUp := false
		if allowUp {
			hardPity := rules.GuaranteedLimitedPity > 0 && state.GuaranteedLimitedPity >= rules.GuaranteedLimitedPity
			if hardPity || state.IsGuaranteedUp {
				isUp = true
			} else {
				isUp = rand.Float64() < rules.UpProbability
			}
			if isUp {
				if hardPity {
					state.HasReceivedGuaranteedLimited = true
				}
				state.GuaranteedLimitedPity = 0
				state.IsGuaranteedUp = false
			} else {
				state.IsGuaranteedUp = true
			}
		}
		state.SixStarCount++
		if isUp {
			state.UpSixStarCount++
		}
		return simulateResult{PullNumber: state.TotalPulls, Rarity: 6, IsUp: isUp, IsLimited: isLimited, PityWhenPulled: pull}
	}

	fiveProbability := rules.FiveStarProbability
	if state.FiveStarPity >= rules.FiveStarPity {
		fiveProbability = 1
	}
	if rand.Float64() < fiveProbability {
		state.FiveStarCount++
		state.FiveStarPity = 0
		return simulateResult{PullNumber: state.TotalPulls, Rarity: 5, IsLimited: isLimited}
	}
	state.FourStarCount++
	return simulateResult{PullNumber: state.TotalPulls, Rarity: 4, IsLimited: isLimited}
}

func simulateStatsFrom(state *simulateState) simulateStats {
	stats := simulateStats{
		TotalPulls:     state.TotalPulls,
		SixStarCount:   state.SixStarCount,
		FiveStarCount:  state.FiveStarCount,
		FourStarCount:  state.FourStarCount,
		UpSixStarCount: state.UpSixStarCount,
		CurrentPity:    state.SixStarPity,
	}
	if state.SixStarCount > 0 {
		stats.UpRate = fmt.Sprintf("%.2f%%", float64(state.UpSixStarCount)/float64(state.SixStarCount)*100)
	} else {
		stats.UpRate = "-"
	}
	return stats
}

func handleSimulateDispatch(ctx context.Context, c sdk.Call, cfg config, _ string, text string) error {
	command := parseSimulateCommand(text)
	if !command.valid {
		return nil
	}
	if ok, message := simulateCheckAllowed(cfg, c); !ok {
		return replyText(ctx, c, message)
	}
	store := simulateLoadStore(ctx, c.State, c.Event.UserID)
	limit := simulateDailyLimit(cfg, command.pool)
	used := store.Daily[command.pool]
	switch command.mode {
	case "single", "ten":
		if limit > 0 && used >= limit {
			return replyText(ctx, c, getMessage("gacha.simulate_daily_limit_reached"))
		}
	case "hundred":
		if limit > 0 && used+10 > limit {
			return replyText(ctx, c, getMessage("gacha.simulate_daily_limit_reached"))
		}
	}

	rules := simulateRulesFor(command.pool)
	state := simulateStateFor(&store, command.pool)
	isLimited := command.pool == "limited" || command.pool == "joint"
	allowUp := command.pool == "limited" || command.pool == "joint" || command.pool == "weapon"

	var results []simulateResult
	switch command.mode {
	case "single":
		results = append(results, simulatePull(state, rules, allowUp, isLimited))
		store.Daily[command.pool] = used + 1
	case "ten":
		for index := 0; index < 10; index++ {
			results = append(results, simulatePull(state, rules, allowUp, isLimited))
		}
		store.Daily[command.pool] = used + 1
	case "hundred":
		for batch := 0; batch < 10; batch++ {
			for index := 0; index < 10; index++ {
				results = append(results, simulatePull(state, rules, allowUp, isLimited))
			}
		}
		store.Daily[command.pool] = used + 10
	}
	store.Pools[command.pool] = state
	simulateSaveStore(ctx, c.State, c.Event.UserID, store)

	chars := simulateFetchPoolChars(ctx, cfg, command.pool)
	stats := simulateStatsFrom(state)
	return simulateReply(ctx, c, command, rules, state, stats, results, chars)
}

func simulateFetchPoolChars(ctx context.Context, cfg config, pool string) []simulatePoolChars {
	client := newAPIClient(cfg, binding{})
	query := url.Values{}
	query.Set("pool_type", pool)
	res, err := client.request(ctx, http.MethodGet, "/api/endfield/gacha/pool-chars", query, nil)
	if err != nil || !res.ok() {
		return nil
	}
	var data struct {
		Pools []simulatePoolChars `json:"pools"`
	}
	if err := res.decode(&data); err != nil {
		return nil
	}
	return data.Pools
}

func simulatePickChar(pools []simulatePoolChars, poolType string, rarity int, isUp bool) string {
	if len(pools) == 0 {
		return simulateFallbackName(poolType)
	}
	pool := pools[0]
	for _, item := range pools {
		if item.PoolType == poolType {
			pool = item
			break
		}
	}
	var list []simulatePoolChar
	switch rarity {
	case 6:
		list = pool.Star6
	case 5:
		list = pool.Star5
	default:
		list = pool.Star4
	}
	if rarity == 6 && len(list) > 0 {
		filtered := make([]simulatePoolChar, 0, len(list))
		for _, item := range list {
			if item.IsUp == isUp {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) > 0 {
			list = filtered
		}
	}
	if len(list) == 0 {
		return simulateFallbackName(poolType)
	}
	item := list[rand.Intn(len(list))]
	if strings.TrimSpace(item.Name) == "" {
		return simulateFallbackName(poolType)
	}
	return strings.TrimSpace(item.Name)
}

func simulateFallbackName(poolType string) string {
	if poolType == "weapon" {
		return "未知武器"
	}
	return "未知干员"
}

func simulateReply(ctx context.Context, c sdk.Call, command simulateCommand, rules simulateRules, state *simulateState, stats simulateStats, results []simulateResult, chars []simulatePoolChars) error {
	title := getMessage("gacha_simulate.title_single")
	if command.mode == "ten" {
		title = getMessage("gacha_simulate.title_ten")
	} else if command.mode == "hundred" {
		title = getMessage("gacha_simulate.title_hundred")
	}
	poolLabel := simulatePoolLabel(command.pool)

	var body strings.Builder
	body.WriteString(`<div class="tags"><span class="chip">` + htmlEscape(getMessage("gacha_simulate.pool_subtitle", map[string]any{"pool": poolLabel})) + `</span>`)
	body.WriteString(`<span class="chip">` + htmlEscape(simulateProbText(state, rules)) + `</span>`)
	body.WriteString(`<span class="chip ` + simulateBoolChip(state.IsGuaranteedUp) + `">大保底 ` + htmlEscape(simulateGuaranteedText(state)) + `</span>`)
	body.WriteString(`</div>`)

	body.WriteString(`<div class="grid c4" style="margin-top:14px">`)
	body.WriteString(gachaMetric(getMessage("gacha_simulate.summary_total_label"), strconv.Itoa(stats.TotalPulls), ""))
	body.WriteString(gachaMetric(getMessage("gacha_simulate.summary_star6_label"), strconv.Itoa(stats.SixStarCount), gachaPercent(stats.SixStarCount, stats.TotalPulls)))
	body.WriteString(gachaMetric(getMessage("gacha_simulate.summary_star5_label"), strconv.Itoa(stats.FiveStarCount), gachaPercent(stats.FiveStarCount, stats.TotalPulls)))
	body.WriteString(gachaMetric(getMessage("gacha_simulate.summary_not_wai_label"), stats.UpRate, fmt.Sprintf("%d UP", stats.UpSixStarCount)))
	body.WriteString(gachaMetric("当前垫抽", strconv.Itoa(state.SixStarPity), fmt.Sprintf("硬保底 %d 抽", rules.SixStarPity)))
	body.WriteString(`</div>`)

	body.WriteString(`<div class="section"><h2>本次结果</h2><table><thead><tr><th>#</th><th>星级</th><th>名称</th><th>标记</th><th>垫抽</th></tr></thead><tbody>`)
	for index, result := range results {
		name := simulatePickChar(chars, command.pool, result.Rarity, result.IsUp)
		tag := ""
		if result.Rarity == 6 && result.IsUp {
			tag = `<span class="chip green">` + htmlEscape(getMessage("gacha_simulate.tag_up")) + `</span>`
		} else if result.Rarity == 6 {
			tag = `<span class="chip red">` + htmlEscape(getMessage("gacha_simulate.tag_off")) + `</span>`
		}
		pity := "—"
		if result.Rarity == 6 && result.PityWhenPulled > 0 {
			pity = strconv.Itoa(result.PityWhenPulled)
		}
		body.WriteString(`<tr><td>` + strconv.Itoa(index+1) + `</td><td>★` + strconv.Itoa(result.Rarity) +
			`</td><td>` + htmlEscape(name) + `</td><td>` + tag + `</td><td>` + pity + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div>`)

	fallback := simulateFallbackText(command, results, stats, chars)
	html := buildPage(pageOptions{
		title:    title,
		eyebrow:  "终末地 · 模拟寻访",
		subtitle: htmlEscape(getMessage("gacha_simulate.pool_subtitle", map[string]any{"pool": poolLabel})),
		seal:     "模拟",
		tags:     []string{fmt.Sprintf("总抽数 %d", stats.TotalPulls), fmt.Sprintf("六星 %d", stats.SixStarCount)},
		body:     body.String(),
		width:    760,
	})
	return simulateSendCard(ctx, c, html, 760, fallback)
}

func simulateSendCard(ctx context.Context, c sdk.Call, html string, width int, fallback string) error {
	if img, err := renderPNG(ctx, html, width); err == nil {
		if err := c.ReplyImageBytes(ctx, img); err == nil {
			return nil
		}
	}
	for _, chunk := range splitMessage(fallback, 1800) {
		if err := replyText(ctx, c, chunk); err != nil {
			return err
		}
	}
	return nil
}

func simulateFallbackText(command simulateCommand, results []simulateResult, stats simulateStats, chars []simulatePoolChars) string {
	title := getMessage("gacha_simulate.title_single")
	if command.mode == "ten" {
		title = getMessage("gacha_simulate.title_ten")
	} else if command.mode == "hundred" {
		title = getMessage("gacha_simulate.title_hundred")
	}
	if command.mode == "single" && len(results) > 0 {
		result := results[0]
		star := simulateStarLabel(result.Rarity)
		tag := ""
		if result.Rarity == 6 && result.IsUp {
			tag = " " + getMessage("gacha_simulate.tag_up")
		} else if result.Rarity == 6 {
			tag = " " + getMessage("gacha_simulate.tag_off")
		}
		text := getMessage("gacha_simulate.msg_single_header", map[string]any{"title": title, "star": star, "tag": tag}) + "\n"
		if result.Rarity == 6 && result.PityWhenPulled > 0 {
			text += getMessage("gacha_simulate.msg_single_detail_pity", map[string]any{"pull": result.PullNumber, "pity": result.PityWhenPulled})
		} else {
			text += getMessage("gacha_simulate.msg_single_detail", map[string]any{"pull": result.PullNumber})
		}
		return text + "\n" + simulateNameLine(result, command.pool, chars)
	}
	var b strings.Builder
	b.WriteString(getMessage("gacha_simulate.msg_list_header", map[string]any{"title": title}) + "\n")
	for _, result := range results {
		star := simulateStarLabel(result.Rarity)
		tag := ""
		if result.Rarity == 6 && result.IsUp {
			tag = " " + getMessage("gacha_simulate.tag_up")
		} else if result.Rarity == 6 {
			tag = " " + getMessage("gacha_simulate.tag_off")
		}
		b.WriteString(getMessage("gacha_simulate.msg_line", map[string]any{"pull": result.PullNumber, "star": star, "tag": tag}))
		b.WriteString(" ")
		b.WriteString(simulatePickChar(chars, command.pool, result.Rarity, result.IsUp))
		b.WriteString("\n")
	}
	b.WriteString(getMessage("gacha_simulate.separator") + "\n")
	b.WriteString(getMessage("gacha_simulate.msg_summary", map[string]any{
		"star6": stats.SixStarCount, "up": stats.UpSixStarCount, "total": stats.TotalPulls,
	}))
	return b.String()
}

func simulateNameLine(result simulateResult, pool string, chars []simulatePoolChars) string {
	return simulatePickChar(chars, pool, result.Rarity, result.IsUp)
}

func simulateStarLabel(rarity int) string {
	switch rarity {
	case 6:
		return getMessage("gacha_simulate.star_6")
	case 5:
		return getMessage("gacha_simulate.star_5")
	default:
		return getMessage("gacha_simulate.star_4")
	}
}

func simulateProbText(state *simulateState, rules simulateRules) string {
	probability := rules.BaseProbability
	if rules.HasSoftPity && state.SixStarPity >= rules.SoftPityStart {
		probability += float64(state.SixStarPity-rules.SoftPityStart+1) * rules.SoftPityIncrease
	}
	if probability > 1 {
		probability = 1
	}
	return getMessage("gacha_simulate.pity_prob_text", map[string]any{"pct": fmt.Sprintf("%.2f", probability*100)})
}

func simulateGuaranteedText(state *simulateState) string {
	if state.IsGuaranteedUp {
		return getMessage("gacha_simulate.guaranteed_up_on")
	}
	return getMessage("gacha_simulate.guaranteed_up_off")
}

func simulateBoolChip(value bool) string {
	if value {
		return "chip green"
	}
	return "chip"
}

func handleSimulateReset(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if ok, message := simulateCheckAllowed(cfg, c); !ok {
		return replyText(ctx, c, message)
	}
	simulateSaveStore(ctx, c.State, c.Event.UserID, simulateStore{
		Pools: map[string]*simulateState{},
		Daily: map[string]int{},
	})
	return replyText(ctx, c, getMessage("gacha_simulate.reset_ok"))
}

func handleSimulateResetAll(ctx context.Context, c sdk.Call, cfg config, _ string, _ string) error {
	if !cfg.isAdmin(c.Event.UserID) {
		return replyText(ctx, c, getMessage("gacha.simulate_reset_all_no_auth"))
	}
	count := 0
	accounts, err := loadBindings(ctx, c.State, c.Event.UserID)
	if err != nil {
		return replyText(ctx, c, getMessage("common.query_failed", map[string]any{"error": err}))
	}
	for _, account := range accounts {
		roleID := account.roleID()
		_ = sdk.SaveJSON(ctx, c.State, gachaCacheKey(c.Event.UserID, roleID), map[string]any{})
		count++
	}
	simulateSaveStore(ctx, c.State, c.Event.UserID, simulateStore{
		Pools: map[string]*simulateState{},
		Daily: map[string]int{},
	})
	count++
	message := getMessage("gacha.simulate_reset_all_ok", map[string]any{"count": count})
	message += "\n注：State 无法全局枚举，本次仅清理当前用户及其全部账号的抽卡缓存。"
	return replyText(ctx, c, message)
}

package toutai

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"strings"
	"testing"
	"time"

	"shizukubot/events"
	"shizukubot/sdk"
)

type memState struct {
	data map[string]json.RawMessage
}

func newMemState() *memState { return &memState{data: map[string]json.RawMessage{}} }

func (s *memState) Get(_ context.Context, key string) (json.RawMessage, error) {
	return s.data[key], nil
}

func (s *memState) Set(_ context.Context, key string, value json.RawMessage) error {
	if s.data == nil {
		s.data = map[string]json.RawMessage{}
	}
	s.data[key] = value
	return nil
}

type testEnv struct {
	t       *testing.T
	state   *memState
	cfg     map[string]any
	replies []sdk.Reply
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	return &testEnv{t: t, state: newMemState()}
}

func (e *testEnv) send(userID, nickname, text string) {
	e.t.Helper()
	e.replies = nil
	event := sdk.Event{
		BotID:     "bot",
		Source:    events.Group,
		GroupID:   "group",
		UserID:    userID,
		Nickname:  nickname,
		MessageID: "msg",
		Message:   text,
		Reply: func(_ context.Context, reply sdk.Reply) error {
			e.replies = append(e.replies, reply)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: event, Config: e.cfg, State: e.state}); err != nil {
		e.t.Fatalf("Handle(%q): %v", text, err)
	}
}

func (e *testEnv) texts() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "text" {
			out = append(out, reply.Text)
		}
	}
	return out
}

func (e *testEnv) lastText() string {
	texts := e.texts()
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func (e *testEnv) images() []string {
	var out []string
	for _, reply := range e.replies {
		if reply.Kind == "image" {
			out = append(out, reply.URL)
		}
	}
	return out
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func stubRender(t *testing.T) []byte {
	t.Helper()
	data := jpegBytes(t)
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return data, nil
	}
	t.Cleanup(func() { renderHTML = old })
	return data
}

func stubRenderFailure(t *testing.T) {
	t.Helper()
	old := renderHTML
	renderHTML = func(context.Context, []byte, sdk.RenderOptions) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { renderHTML = old })
}

func stubClock(t *testing.T) time.Time {
	t.Helper()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
	return now
}

func stubRand(t *testing.T, seed int64) {
	t.Helper()
	old := newRand
	newRand = func() *rand.Rand { return rand.New(rand.NewSource(seed)) }
	t.Cleanup(func() { newRand = old })
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "toutai" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, ok := def.Manifest.ConfigSchema.Properties["imageType"]; !ok {
		t.Fatal("missing imageType config")
	}
}

func TestDatasetLoads(t *testing.T) {
	data, err := loadDataset()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(data.china.Features) == 0 || len(data.world.Features) == 0 {
		t.Fatal("empty geo data")
	}
	if len(data.worldData) == 0 || len(data.worldBirths) == 0 || len(data.birthDetailed) == 0 {
		t.Fatal("empty statistics data")
	}
	if got := data.totalProvinceCount(); got != 34 {
		t.Fatalf("province count = %d", got)
	}
	if data.mortality["中国"] <= 0 {
		t.Fatal("missing China mortality rate")
	}
}

func TestPercentText(t *testing.T) {
	cases := []struct {
		count, total int
		want         string
	}{
		{0, 0, "0%"},
		{1, 2, "50%"},
		{1, 3, "33%"},
		{1, 100, "1%"},
		{10, 3, "333%"},
	}
	for _, tc := range cases {
		if got := percentText(tc.count, tc.total); got != tc.want {
			t.Fatalf("percentText(%d,%d) = %q, want %q", tc.count, tc.total, got, tc.want)
		}
	}
}

func floatPtrValue(v float64) *float64 { return &v }

func TestBirthsOf(t *testing.T) {
	if got := birthsOf(worldBirthrate{Population: 10, BirthRate: floatPtrValue(5)}); got != 50 {
		t.Fatalf("births = %v", got)
	}
	if got := birthsOf(worldBirthrate{Population: 10}); got != 0 {
		t.Fatalf("nil birth rate = %v", got)
	}
	if got := birthsOf(worldBirthrate{Population: -1, BirthRate: floatPtrValue(5)}); got != 0 {
		t.Fatalf("negative births = %v", got)
	}
}

func TestSimulateWorld(t *testing.T) {
	list := []worldBirthrate{
		{Name: "甲", Population: 0, BirthRate: floatPtrValue(50)},
		{Name: "乙", Population: 10, BirthRate: floatPtrValue(1)},
		{Name: "丙", Population: 1000, BirthRate: floatPtrValue(100)},
	}
	hit := 0
	for seed := int64(0); seed < 200; seed++ {
		country, ok := simulateWorld(list, rand.New(rand.NewSource(seed)))
		if !ok {
			t.Fatal("no country picked")
		}
		if country.Name == "甲" {
			t.Fatal("zero-birth country should never be picked")
		}
		if country.Name == "丙" {
			hit++
		}
	}
	if hit == 0 {
		t.Fatal("heaviest country should be picked sometimes")
	}
	if _, ok := simulateWorld([]worldBirthrate{{Name: "甲"}}, rand.New(rand.NewSource(1))); ok {
		t.Fatal("all-zero weights should fail")
	}
}

func TestSimulateChina(t *testing.T) {
	data, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	birth := simulateChina(data, rand.New(rand.NewSource(1)))
	if birth.Province == "" || birth.Gender == "" || birth.Category == "" || birth.Order == "" {
		t.Fatalf("incomplete birth: %+v", birth)
	}
	if birth.Probability <= 0 || birth.Probability > 1 {
		t.Fatalf("probability = %v", birth.Probability)
	}
	// 随机数恒为 0 时落到第一个省级行政区（跳过 national）。
	zero := simulateChina(data, rand.New(rand.NewSource(0)))
	if zero.Province == "全国" || zero.ID == 0 {
		t.Fatalf("should skip national row: %+v", zero)
	}
}

func TestCheckAlive(t *testing.T) {
	if checkAlive(100, rand.New(rand.NewSource(1))) {
		t.Fatal("100% mortality should always die")
	}
	if !checkAlive(0, rand.New(rand.NewSource(1))) {
		t.Fatal("0% mortality should always live")
	}
}

func TestCooldown(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := record{Timestamp: now.Add(-10 * time.Second).UnixMilli()}
	if got := cooldownRemaining(rec, now, 60); got != 50 {
		t.Fatalf("remaining = %d", got)
	}
	if got := cooldownRemaining(rec, now, 5); got != 0 {
		t.Fatalf("expired remaining = %d", got)
	}
	if got := cooldownRemaining(record{}, now, 60); got != 0 {
		t.Fatalf("empty record remaining = %d", got)
	}
}

func TestDisplayLimit(t *testing.T) {
	cases := []struct{ requested, fallback, max, want int }{
		{0, 20, 100, 20},
		{10, 20, 100, 10},
		{200, 20, 100, 100},
		{0, 0, 100, 100},
		{0, 0, 0, 1 << 30},
		{5, 20, 0, 5},
	}
	for _, tc := range cases {
		if got := displayLimit(tc.requested, tc.fallback, tc.max); got != tc.want {
			t.Fatalf("displayLimit(%d,%d,%d) = %d, want %d", tc.requested, tc.fallback, tc.max, got, tc.want)
		}
	}
}

func sampleRecords() []record {
	return []record{
		{UserID: "a", Username: "甲", NumberOfStillbirthsInChina: 2, BirthResultsInChina: []birthResultInChina{{Gender: "male", Province: "北京"}, {Gender: "male", Province: "北京"}}},
		{UserID: "b", Username: "乙", NumberOfStillbirthsInChina: 5, BirthResultsInChina: []birthResultInChina{{Gender: "female", Province: "上海"}}},
		{UserID: "c", Username: "丙", NumberOfStillbirthsInChina: 2, BirthResultsInChina: []birthResultInChina{{Gender: "female", Province: "广东"}}},
	}
}

func TestRankRowsTies(t *testing.T) {
	rows := rankRows(sampleRecords(), func(r record) int { return len(r.BirthResultsInChina) })
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Rank != 1 || rows[1].Rank != 2 || rows[2].Rank != 2 {
		t.Fatalf("ranks = %d,%d,%d", rows[0].Rank, rows[1].Rank, rows[2].Rank)
	}
	stillbirths := rankRows(sampleRecords(), func(r record) int { return r.NumberOfStillbirthsInChina })
	if stillbirths[0].Rank != 1 || stillbirths[1].Rank != 2 || stillbirths[2].Rank != 2 {
		t.Fatalf("stillbirth ranks = %d,%d,%d", stillbirths[0].Rank, stillbirths[1].Rank, stillbirths[2].Rank)
	}
	if got := rankAmong(sampleRecords(), "b", func(r record) int { return r.NumberOfStillbirthsInChina }); got != 1 {
		t.Fatalf("stillbirths rank = %d", got)
	}
	if got := rankAmong(sampleRecords(), "missing", func(r record) int { return 1 }); got != -1 {
		t.Fatalf("missing user rank = %d", got)
	}
}

func TestAnalyze(t *testing.T) {
	china := analyzeChina([]birthResultInChina{
		{Province: "北京", Gender: "male", Category: "城市", Order: "一"},
		{Province: "北京", Gender: "female", Category: "乡村", Order: "五及以上"},
		{Province: "上海", Gender: "male", Category: "城镇", Order: "三"},
	})
	if china.totalCount != 3 || china.uniqueProvinces != 2 {
		t.Fatalf("analysis = %+v", china)
	}
	if china.favourite.Name != "北京" || china.favourite.Count != 2 {
		t.Fatalf("favourite = %+v", china.favourite)
	}
	if china.orderCounts["五及以上"] != 1 || china.genderCounts["female"] != 1 {
		t.Fatalf("counts = %+v", china)
	}
	world := analyzeWorld([]birthResultInWorld{
		{DictContinent: "亚洲", DictName: "中国"},
		{DictContinent: "亚洲", DictName: "日本"},
		{DictContinent: "欧洲", DictName: "法国"},
	})
	if world.uniqueCountries != 3 || world.continentCounts["亚洲"] != 2 {
		t.Fatalf("world analysis = %+v", world)
	}
}

func TestProvinceHelpers(t *testing.T) {
	results := []birthResultInChina{
		{Index: 1, Province: "北京", Gender: "male"},
		{Index: 2, Province: "上海", Gender: "female"},
		{Index: 3, Province: "北京", Gender: "female"},
		{Index: 4, Province: "北京", Gender: "male"},
	}
	counts := provinceCounts(results)
	if counts[0][0] != "北京" || counts[0][1] != 3 {
		t.Fatalf("counts = %v", counts)
	}
	first := provinceFirstAppearances(results)
	if len(first) != 2 || first[0].Province != "北京" {
		t.Fatalf("first = %+v", first)
	}
	if first[0].Entry.Male == nil || *first[0].Entry.Male != 1 {
		t.Fatalf("male first = %v", first[0].Entry.Male)
	}
	if first[0].Entry.Female == nil || *first[0].Entry.Female != 3 {
		t.Fatalf("female first = %v", first[0].Entry.Female)
	}
}

func TestEscAndFormatting(t *testing.T) {
	if got := esc(`<a&"'>`); got != `＜a＆＂＇＞` {
		t.Fatalf("esc = %q", got)
	}
	if got := orderText("五及以上"); got != "五孩及以上" {
		t.Fatalf("orderText = %q", got)
	}
	if got := trimUsername("一二三四五六七八九十十一"); got != "一二三四五六七八九十…" {
		t.Fatalf("trimUsername = %q", got)
	}
	if got := entry("籍贯", "北京"); got != "　籍　贯　北京" {
		t.Fatalf("entry = %q", got)
	}
}

func TestRenderViews(t *testing.T) {
	data, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	records := sampleRecords()
	china := analyzeChina(records[0].BirthResultsInChina)
	world := analyzeWorld([]birthResultInWorld{{Index: 1, DictName: "中国", DictContinent: "亚洲", Coordinate: []float64{104, 35}, Center: []float64{104, 35}}})

	pages := map[string]string{
		"china overview":   renderChinaOverview("甲", china, 1, 2, 1, data.totalProvinceCount()),
		"world overview":   renderWorldOverview("甲", world, 1, 2, 1),
		"gender":           renderGenderDistribution("甲", records[0].BirthResultsInChina),
		"region":           renderRegionDistribution("甲", records[0].BirthResultsInChina),
		"china history":    renderChinaBirthHistory("甲", records[0].BirthResultsInChina),
		"world history":    renderWorldBirthHistory("甲", []birthResultInWorld{{Index: 1, DictName: "中国", DictContinent: "亚洲"}}),
		"demise history":   renderWorldDemiseHistory("甲", []demiseRecord{{Index: 1, DictName: "中国", DictContinent: "亚洲"}}),
		"first appearance": renderFirstAppearance("甲", records[0].BirthResultsInChina, data.totalProvinceCount()),
		"china map":        renderChinaMap(records[0].BirthResultsInChina, records[0].BirthResultsInChina[0], "甲", data),
		"world map":        renderWorldMap(worldResult(), "甲", data),
		"rankings": renderRankings(records, 10, rankingOptions{
			title: "中国投胎 · 降生次数榜", seal: "降生", valueLabel: "次", tone: "jade",
			pick: func(r record) int { return len(r.BirthResultsInChina) }, selfUserID: "a", tip: "toutai.投胎中国",
		}),
	}
	for name, page := range pages {
		if !strings.Contains(page, "<!DOCTYPE html>") || !strings.Contains(page, `<div class="sheet">`) {
			t.Fatalf("%s page missing skeleton", name)
		}
		if !strings.Contains(page, baseCSS()[:200]) {
			t.Fatalf("%s page missing css", name)
		}
	}
	if !strings.Contains(pages["china map"], "<svg") || !strings.Contains(pages["china map"], "北京") {
		t.Fatal("china map missing svg/marker")
	}
	if !strings.Contains(pages["world map"], "<svg") || !strings.Contains(pages["world map"], "本次落点") {
		t.Fatal("world map missing svg/legend")
	}
	if !strings.Contains(pages["rankings"], "m3-badge--gold") {
		t.Fatal("rankings missing medal")
	}
}

func worldResult() birthResultInWorld {
	return birthResultInWorld{Index: 1, DictName: "中国", DictContinent: "亚洲", Coordinate: []float64{104, 35}, Center: []float64{104, 35}}
}

func TestHandleHelpAndUnknown(t *testing.T) {
	env := newEnv(t)
	env.send("u1", "甲", "toutai")
	if !strings.Contains(env.lastText(), "toutai.投胎中国") {
		t.Fatalf("help = %q", env.lastText())
	}
	env.send("u1", "甲", "toutai.中国投胎记录")
	if !strings.Contains(env.lastText(), "中国投胎记录.总览") {
		t.Fatalf("record help = %q", env.lastText())
	}
	env.send("u1", "甲", "hello")
	if len(env.replies) != 0 {
		t.Fatalf("unexpected replies: %+v", env.replies)
	}
}

func TestRebirthChinaEndToEnd(t *testing.T) {
	stubRender(t)
	now := stubClock(t)
	stubRand(t, 1)
	env := newEnv(t)
	env.send("u1", "甲", "toutai.投胎中国")

	rec, ok, err := loadRecord(context.Background(), env.state, "u1")
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if len(rec.BirthResultsInChina) != 1 {
		t.Fatalf("births = %d", len(rec.BirthResultsInChina))
	}
	if rec.BirthResultsInChina[0].Index != 1 {
		t.Fatalf("index = %d", rec.BirthResultsInChina[0].Index)
	}
	if rec.Timestamp != now.UnixMilli() {
		t.Fatalf("timestamp = %d", rec.Timestamp)
	}
	if len(env.images()) != 1 {
		t.Fatalf("images = %d", len(env.images()))
	}
	text := env.lastText()
	if !strings.Contains(text, "第 1 次轮回") || !strings.Contains(text, "性　别") {
		t.Fatalf("text = %q", text)
	}

	env.send("u1", "甲", "toutai.投胎中国")
	if !strings.Contains(env.lastText(), "轮回未启") || !strings.Contains(env.lastText(), "60 秒") {
		t.Fatalf("cooldown = %q", env.lastText())
	}
}

func TestRebirthChinaStillbirth(t *testing.T) {
	stubRender(t)
	stubClock(t)
	stubRand(t, 1)
	oldMortality := mortalityOverride
	mortalityOverride = func(name string) (float64, bool) { return 100, true }
	t.Cleanup(func() { mortalityOverride = oldMortality })

	env := newEnv(t)
	env.send("u1", "甲", "toutai.投胎中国")
	rec, _, _ := loadRecord(context.Background(), env.state, "u1")
	if rec.NumberOfStillbirthsInChina != 1 || len(rec.BirthResultsInChina) != 0 {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.Contains(env.lastText(), "未能落地") {
		t.Fatalf("text = %q", env.lastText())
	}
	env.send("u1", "甲", "toutai.中国投胎记录.总览")
	if !strings.Contains(env.lastText(), "尚无此人的中国投胎记录") {
		t.Fatalf("overview = %q", env.lastText())
	}
}

func TestRebirthWorldEndToEnd(t *testing.T) {
	data, err := loadDataset()
	if err != nil {
		t.Fatal(err)
	}
	var seed int64 = -1
	var expected country
	for candidate := int64(0); candidate < 200; candidate++ {
		rnd := rand.New(rand.NewSource(candidate))
		picked, ok := simulateWorld(data.worldBirths, rnd)
		if !ok {
			continue
		}
		found, exists := findCountry(data.worldData, picked.Name)
		if !exists {
			continue
		}
		if checkAlive(data.mortality[found.NameCn], rnd) {
			seed = candidate
			expected = found
			break
		}
	}
	if seed < 0 {
		t.Fatal("no suitable seed")
	}
	stubRender(t)
	stubClock(t)
	stubRand(t, seed)

	env := newEnv(t)
	env.send("u1", "甲", "toutai.投胎世界")
	rec, _, _ := loadRecord(context.Background(), env.state, "u1")
	if len(rec.BirthResultsInWorld) != 1 {
		t.Fatalf("world births = %d", len(rec.BirthResultsInWorld))
	}
	if rec.BirthResultsInWorld[0].DictName != expected.NameCn {
		t.Fatalf("country = %q, want %q", rec.BirthResultsInWorld[0].DictName, expected.NameCn)
	}
	if len(env.images()) != 1 {
		t.Fatalf("images = %d", len(env.images()))
	}
}

func TestRankingTextFallback(t *testing.T) {
	stubRenderFailure(t)
	env := newEnv(t)
	for _, rec := range sampleRecords() {
		if err := saveRecord(context.Background(), env.state, rec); err != nil {
			t.Fatal(err)
		}
		if err := rememberUser(context.Background(), env.state, rec.UserID); err != nil {
			t.Fatal(err)
		}
	}
	env.send("a", "甲", "toutai.中国投胎排行榜.成功次数 2")
	text := env.lastText()
	if !strings.Contains(text, "中国投胎 · 降生次数排行榜") {
		t.Fatalf("text = %q", text)
	}
	if !strings.Contains(text, "• 1") || !strings.Contains(text, "你位居第") {
		t.Fatalf("text = %q", text)
	}
}

func TestRankingImage(t *testing.T) {
	stubRender(t)
	env := newEnv(t)
	for _, rec := range sampleRecords() {
		if err := saveRecord(context.Background(), env.state, rec); err != nil {
			t.Fatal(err)
		}
		if err := rememberUser(context.Background(), env.state, rec.UserID); err != nil {
			t.Fatal(err)
		}
	}
	env.send("b", "乙", "toutai.世界投胎排行榜.成功次数")
	if len(env.images()) != 1 {
		t.Fatalf("images = %d", len(env.images()))
	}
	if len(env.texts()) != 0 {
		t.Fatalf("should only send image, got %v", env.texts())
	}
}

func TestRecordCommandsWithMention(t *testing.T) {
	stubRender(t)
	stubClock(t)
	stubRand(t, 1)
	env := newEnv(t)
	env.send("u1", "甲", "toutai.投胎中国")
	env.send("u2", "乙", `toutai.中国投胎记录.总览 <qqbot-at-user id="u1" />`)
	if len(env.images()) != 1 {
		t.Fatalf("mention overview images = %d", len(env.images()))
	}
	// 冷却后再投一次，验证成功历史。
	old := nowFunc
	nowFunc = func() time.Time { return old().Add(2 * time.Minute) }
	t.Cleanup(func() { nowFunc = old })
	env.send("u1", "甲", "toutai.投胎中国")
	env.send("u1", "甲", "toutai.中国投胎记录.成功历史")
	for i, url := range env.images() {
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Fatalf("image %d = %s", i, url)
		}
	}
}

func TestWorldRecordCommands(t *testing.T) {
	records := []record{
		{UserID: "u1", Username: "甲", NumberOfStillbirthsInWorld: 1,
			BirthResultsInWorld:             []birthResultInWorld{{Index: 1, DictName: "中国", DictContinent: "亚洲"}},
			UnfortunateDemiseRecordsInWorld: []demiseRecord{{Index: 1, DictName: "日本", DictContinent: "亚洲"}}},
	}
	env := newEnv(t)
	for _, rec := range records {
		_ = saveRecord(context.Background(), env.state, rec)
		_ = rememberUser(context.Background(), env.state, rec.UserID)
	}
	stubRenderFailure(t)
	env.send("u1", "甲", "toutai.世界投胎记录.夭折历史")
	if !strings.Contains(env.lastText(), "世界投胎 · 夭折历史") {
		t.Fatalf("text = %q", env.lastText())
	}
	env.send("u1", "甲", "toutai.世界投胎记录.成功历史")
	if !strings.Contains(env.lastText(), "世界投胎 · 降生纪年") {
		t.Fatalf("text = %q", env.lastText())
	}
}

func TestDecodeConfig(t *testing.T) {
	cfg := decodeConfig(nil)
	if cfg.DefaultMaxDisplayCount != 20 || cfg.MaxDisplayCount != 100 || cfg.ImageType != "png" || !cfg.ShouldPrefixUsername {
		t.Fatalf("defaults = %+v", cfg)
	}
	override := decodeConfig(map[string]any{
		"defaultMaxDisplayCount":               5,
		"maxDisplayCount":                      "8",
		"nextReincarnationCooldownSeconds":     float64(10),
		"shouldPrefixUsernameInMessageSending": false,
		"isMapImageIncludedAfterRebirth":       false,
		"imageType":                            "jpeg",
	})
	if override.DefaultMaxDisplayCount != 5 || override.MaxDisplayCount != 8 || override.NextReincarnationCooldownSeconds != 10 {
		t.Fatalf("override = %+v", override)
	}
	if override.ShouldPrefixUsername || override.IsMapImageIncluded || override.ImageType != "jpeg" {
		t.Fatalf("override flags = %+v", override)
	}
	invalid := decodeConfig(map[string]any{"imageType": "gif"})
	if invalid.ImageType != "png" {
		t.Fatalf("invalid image type = %q", invalid.ImageType)
	}
}

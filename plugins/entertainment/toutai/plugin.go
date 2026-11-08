package toutai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	pluginVersion = "1.0.0"
	renderTimeout = 30 * time.Second
)

// config 对齐上游 Config。
type config struct {
	DefaultMaxDisplayCount           int
	MaxDisplayCount                  int
	NextReincarnationCooldownSeconds int
	ShouldPrefixUsername             bool
	RetractDelay                     int
	IsMapImageIncluded               bool
	ImageType                        string
}

func defaultConfig() config {
	return config{
		DefaultMaxDisplayCount:           20,
		MaxDisplayCount:                  100,
		NextReincarnationCooldownSeconds: 60,
		ShouldPrefixUsername:             true,
		RetractDelay:                     0,
		IsMapImageIncluded:               true,
		ImageType:                        "png",
	}
}

// Definition 注册「toutai.*」指令族。
func Definition() sdk.Definition {
	events := append([]sdk.EventType{}, sdk.MessageEvents...)
	events = append(events, sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage)
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "toutai",
		Name:        "投胎模拟器",
		Version:     pluginVersion,
		Description: "随机投胎模拟，记录与排行榜",
	}, events, Handle, []triggercmd.Command{
		{Name: "toutai", Usage: "用法：发送「toutai.投胎中国」或「toutai.投胎世界」开始；记录与排行榜下还有分项，发送其指令名展开。"},
	}, sdk.WithPermissions("state.read", "state.write", "config.read"), sdk.WithConfig(configSchema()))
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"defaultMaxDisplayCount":               {Type: "integer", Description: "排行榜默认显示的人数，0 表示全部（仍受上限约束）", Default: json.RawMessage("20"), Minimum: floatPtr(0)},
			"maxDisplayCount":                      {Type: "integer", Description: "排行榜最多显示的人数，0 表示不设上限", Default: json.RawMessage("100"), Minimum: floatPtr(0)},
			"nextReincarnationCooldownSeconds":     {Type: "integer", Description: "两次投胎之间的冷却时间（秒）", Default: json.RawMessage("60"), Minimum: floatPtr(0)},
			"shouldPrefixUsernameInMessageSending": {Type: "boolean", Description: "回复时 @ 用户", Default: json.RawMessage("true")},
			"retractDelay":                         {Type: "integer", Description: "上一条消息的自动撤回延迟（秒），0 表示不撤回；宿主无法获取已发送消息 ID 时不生效", Default: json.RawMessage("0"), Minimum: floatPtr(0)},
			"isMapImageIncludedAfterRebirth":       {Type: "boolean", Description: "投胎后附上一张地图", Default: json.RawMessage("true")},
			"imageType":                            {Type: "string", Description: "发送的图片格式", Default: json.RawMessage(`"png"`), Enum: []any{"png", "jpeg", "webp"}},
		},
	}
}

func floatPtr(v float64) *float64 { return &v }

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	readInt := func(key string, dst *int) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case float64:
				*dst = int(v)
			case int:
				*dst = v
			case json.Number:
				if n, err := v.Int64(); err == nil {
					*dst = int(n)
				}
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					*dst = n
				}
			}
		}
	}
	readBool := func(key string, dst *bool) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case bool:
				*dst = v
			case string:
				if parsed, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
					*dst = parsed
				}
			}
		}
	}
	readString := func(key string, dst *string) {
		if value, ok := raw[key]; ok {
			if v, ok := value.(string); ok && strings.TrimSpace(v) != "" {
				*dst = strings.TrimSpace(v)
			}
		}
	}
	readInt("defaultMaxDisplayCount", &cfg.DefaultMaxDisplayCount)
	readInt("maxDisplayCount", &cfg.MaxDisplayCount)
	readInt("nextReincarnationCooldownSeconds", &cfg.NextReincarnationCooldownSeconds)
	readBool("shouldPrefixUsernameInMessageSending", &cfg.ShouldPrefixUsername)
	readInt("retractDelay", &cfg.RetractDelay)
	readBool("isMapImageIncludedAfterRebirth", &cfg.IsMapImageIncluded)
	readString("imageType", &cfg.ImageType)
	switch cfg.ImageType {
	case "jpeg", "webp", "png":
	default:
		cfg.ImageType = "png"
	}
	return cfg
}

var (
	nowFunc   = time.Now
	randPool  = rand.New(rand.NewSource(time.Now().UnixNano()))
	randMutex sync.Mutex
)

var renderHTML = func(ctx context.Context, html []byte, opts sdk.RenderOptions) ([]byte, error) {
	return sdk.RenderHTML(ctx, html, opts)
}

// mortalityOverride 供单测替换新生儿死亡率。
var mortalityOverride func(name string) (float64, bool)

func mortalityRate(data *dataset, name string) float64 {
	if mortalityOverride != nil {
		if value, ok := mortalityOverride(name); ok {
			return value
		}
	}
	return data.mortality[name]
}

func random() float64 {
	randMutex.Lock()
	defer randMutex.Unlock()
	return randPool.Float64()
}

var newRand = func() *rand.Rand {
	randMutex.Lock()
	defer randMutex.Unlock()
	return rand.New(rand.NewSource(randPool.Int63()))
}

// commandInfo 描述一条指令，用于帮助列表与匹配。
type commandInfo struct {
	name string
	desc string
}

var commandTable = []commandInfo{
	{"toutai.投胎中国", "投胎到中国"},
	{"toutai.投胎世界", "投胎到世界"},
	{"toutai.中国投胎记录.第一次出现", "查看各省的初见次序"},
	{"toutai.中国投胎记录.地区分布", "查看降生的省份分布"},
	{"toutai.中国投胎记录.成功历史", "查看历次降生"},
	{"toutai.中国投胎记录.性别分布", "查看男女比例"},
	{"toutai.中国投胎记录.总览", "查看降生与夭折的总账"},
	{"toutai.世界投胎记录.夭折历史", "查看夭折的国度"},
	{"toutai.世界投胎记录.成功历史", "查看历次降生"},
	{"toutai.世界投胎记录.总览", "查看降生与夭折的总账"},
	{"toutai.中国投胎排行榜.成功次数", "查看降生次数排行榜"},
	{"toutai.中国投胎排行榜.夭折次数", "查看夭折次数排行榜"},
	{"toutai.中国投胎排行榜.男孩次数", "查看男孩降生次数排行榜"},
	{"toutai.中国投胎排行榜.女孩次数", "查看女孩降生次数排行榜"},
	{"toutai.世界投胎排行榜.成功次数", "查看降生次数排行榜"},
	{"toutai.世界投胎排行榜.夭折次数", "查看夭折次数排行榜"},
	{"toutai.中国投胎记录", "列出各项投胎记录"},
	{"toutai.世界投胎记录", "列出各项投胎记录"},
	{"toutai.中国投胎排行榜", "列出各类投胎排行榜"},
	{"toutai.世界投胎排行榜", "列出各类投胎排行榜"},
}

func init() {
	for _, continent := range continentOrder {
		commandTable = append(commandTable, commandInfo{
			name: "toutai.世界投胎排行榜." + continent,
			desc: "查看" + continent + "降生次数排行榜",
		})
	}
}

// Handle 解析 toutai 指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.CommandText(c.Event))
	text = strings.TrimPrefix(text, "/")
	text = strings.TrimPrefix(text, "／")
	if !strings.HasPrefix(text, "toutai") {
		return nil
	}
	cfg := decodeConfig(c.Config)
	data, err := loadDataset()
	if err != nil {
		return c.Reply(ctx, "数据加载失败："+err.Error())
	}
	userID := c.Event.UserID
	username := c.Event.Nickname
	if username == "" {
		username = userID
	}
	if err := touchRecord(ctx, c.State, userID, username); err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	for _, command := range commandTable {
		rest, ok := cutCommand(text, command.name)
		if !ok {
			continue
		}
		return dispatch(ctx, c, cfg, data, command.name, strings.TrimSpace(rest))
	}
	if cut, ok := cutCommand(text, "toutai"); ok && strings.TrimSpace(cut) == "" {
		return c.Reply(ctx, helpText("toutai",
			"发送「toutai.投胎中国」或「toutai.投胎世界」开始；记录与排行榜下还有分项，发送其指令名展开。"))
	}
	return nil
}

func cutCommand(text, name string) (string, bool) {
	if !strings.HasPrefix(text, name) {
		return "", false
	}
	rest := text[len(name):]
	if rest == "" {
		return "", true
	}
	if strings.TrimSpace(rest) != rest {
		return rest, true
	}
	return "", false
}

func dispatch(ctx context.Context, c sdk.Call, cfg config, data *dataset, name, args string) error {
	switch name {
	case "toutai":
		return c.Reply(ctx, helpText("toutai",
			"发送「toutai.投胎中国」或「toutai.投胎世界」开始；记录与排行榜下还有分项，发送其指令名展开。"))
	case "toutai.投胎中国":
		return rebirthChina(ctx, c, cfg, data)
	case "toutai.投胎世界":
		return rebirthWorld(ctx, c, cfg, data)
	case "toutai.中国投胎记录":
		return c.Reply(ctx, helpText(name, "指令后可加 @某人，查看对方的记录。"))
	case "toutai.世界投胎记录":
		return c.Reply(ctx, helpText(name, "指令后可加 @某人，查看对方的记录。"))
	case "toutai.中国投胎排行榜":
		return c.Reply(ctx, helpText(name, "指令后可加人数，如「toutai.中国投胎排行榜.成功次数 10」。"))
	case "toutai.世界投胎排行榜":
		return c.Reply(ctx, helpText(name, "指令后可加人数，如「toutai.世界投胎排行榜.成功次数 10」。"))
	}
	if strings.HasPrefix(name, "toutai.中国投胎记录.") {
		return chinaRecord(ctx, c, cfg, data, name, args)
	}
	if strings.HasPrefix(name, "toutai.世界投胎记录.") {
		return worldRecord(ctx, c, cfg, data, name, args)
	}
	if strings.HasPrefix(name, "toutai.中国投胎排行榜.") {
		return chinaRanking(ctx, c, cfg, name, args)
	}
	if strings.HasPrefix(name, "toutai.世界投胎排行榜.") {
		return worldRanking(ctx, c, cfg, name, args)
	}
	return nil
}

func helpText(name, tail string) string {
	lines := []string{}
	if name == "toutai" {
		lines = append(lines, "📋 投胎模拟器 · 一趟人间的随机开局")
	} else {
		lines = append(lines, "📋 "+name)
	}
	for _, command := range commandTable {
		if strings.HasPrefix(command.name, name+".") {
			lines = append(lines, "　"+command.name+"　"+command.desc)
		}
	}
	lines = append(lines, "　"+tail)
	return strings.Join(lines, "\n")
}

// touchRecord 确保用户有一行命簿并同步昵称。
func touchRecord(ctx context.Context, st sdk.State, userID, username string) error {
	rec, ok, err := loadRecord(ctx, st, userID)
	if err != nil {
		return err
	}
	if !ok {
		rec = record{UserID: userID}
	}
	rec.Username = username
	if err := saveRecord(ctx, st, rec); err != nil {
		return err
	}
	return rememberUser(ctx, st, userID)
}

var mutating sync.Map

func rebirthChina(ctx context.Context, c sdk.Call, cfg config, data *dataset) error {
	userID := c.Event.UserID
	if _, busy := mutating.LoadOrStore(userID, true); busy {
		return c.Reply(ctx, "⏳ 上一条还在落笔，稍候再试。")
	}
	defer mutating.Delete(userID)

	username := displayName(c)
	now := nowFunc()
	rec, _, err := loadRecord(ctx, c.State, userID)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	if remaining := cooldownRemaining(rec, now, cfg.NextReincarnationCooldownSeconds); remaining > 0 {
		return c.Reply(ctx, fmt.Sprintf(`⏳ 轮回未启
　黄泉路上尚在排队，再候 %d 秒。
　发送「toutai.中国投胎记录」翻翻旧账。`, remaining))
	}
	rnd := newRand()
	if !checkAlive(mortalityRate(data, "中国"), rnd) {
		rec.NumberOfStillbirthsInChina++
		rec.Timestamp = now.UnixMilli()
		if err := saveRecord(ctx, c.State, rec); err != nil {
			return c.Reply(ctx, "命簿读写失败："+err.Error())
		}
		attempts := len(rec.BirthResultsInChina) + rec.NumberOfStillbirthsInChina
		return sendText(ctx, c, cfg, fmt.Sprintf(`🕯 第 %d 次叩门 · 未能落地
　%s
　累计夭折 %d 次，再候 %d 秒。
　发送「toutai.投胎中国」再叩一次门。`,
			attempts, pickOne(laments, rnd), rec.NumberOfStillbirthsInChina, cfg.NextReincarnationCooldownSeconds), true)
	}

	birth := simulateChina(data, rnd)
	birth.Index = len(rec.BirthResultsInChina) + 1
	rec.BirthResultsInChina = append(rec.BirthResultsInChina, birth)
	rec.Timestamp = now.UnixMilli()
	if err := saveRecord(ctx, c.State, rec); err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}

	isSpecialRegion := birth.Province == "香港" || birth.Province == "澳门" || birth.Province == "台湾"
	region := birth.Province + " · " + birth.Category
	if isSpecialRegion {
		region = birth.Province
	}
	lines := []string{
		fmt.Sprintf("🍼 第 %d 次轮回 · 落地平安", birth.Index),
		entry("籍贯", region),
		entry("性别", translateGenderChild(birth.Gender)),
	}
	if !isSpecialRegion {
		lines = append(lines, entry("胎次", "家中"+orderText(birth.Order)))
	}
	lines = append(lines, "　"+pickOne(blessings, rnd))

	if cfg.IsMapImageIncluded {
		if img, err := renderPNG(ctx, renderChinaMap(rec.BirthResultsInChina, birth, username, data), cfg); err == nil {
			if err := c.ReplyImageBytes(ctx, img); err != nil {
				// 地图发送失败时只发文本，不打断这一趟投胎。
			}
		}
	}
	return sendText(ctx, c, cfg, strings.Join(lines, "\n"), true)
}

func rebirthWorld(ctx context.Context, c sdk.Call, cfg config, data *dataset) error {
	userID := c.Event.UserID
	if _, busy := mutating.LoadOrStore(userID, true); busy {
		return c.Reply(ctx, "⏳ 上一条还在落笔，稍候再试。")
	}
	defer mutating.Delete(userID)

	username := displayName(c)
	now := nowFunc()
	rec, _, err := loadRecord(ctx, c.State, userID)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	if remaining := cooldownRemaining(rec, now, cfg.NextReincarnationCooldownSeconds); remaining > 0 {
		return c.Reply(ctx, fmt.Sprintf(`⏳ 轮回未启
　黄泉路上尚在排队，再候 %d 秒。
　发送「toutai.世界投胎记录」翻翻旧账。`, remaining))
	}
	rnd := newRand()
	country, ok := simulateWorld(data.worldBirths, rnd)
	if !ok {
		return c.Reply(ctx, "世界出生数据缺失，暂时无法投胎。")
	}
	found, exists := findCountry(data.worldData, country.Name)
	if !exists {
		return c.Reply(ctx, "世界地图数据缺失，暂时无法投胎。")
	}
	continent := continentDict[found.Continent]
	if !checkAlive(mortalityRate(data, found.NameCn), rnd) {
		rec.UnfortunateDemiseRecordsInWorld = append(rec.UnfortunateDemiseRecordsInWorld, demiseRecord{
			Index:         len(rec.UnfortunateDemiseRecordsInWorld) + 1,
			DictName:      found.NameCn,
			DictContinent: continent,
		})
		rec.NumberOfStillbirthsInWorld++
		rec.Timestamp = now.UnixMilli()
		if err := saveRecord(ctx, c.State, rec); err != nil {
			return c.Reply(ctx, "命簿读写失败："+err.Error())
		}
		return sendText(ctx, c, cfg, fmt.Sprintf(`🕯 投身于 %s · %s
　%s
　累计夭折 %d 次，再候 %d 秒。
　发送「toutai.投胎世界」再叩一次门。`,
			continent, found.NameCn, pickOne(laments, rnd), rec.NumberOfStillbirthsInWorld, cfg.NextReincarnationCooldownSeconds), true)
	}

	birth := birthResultInWorld{
		Index:         len(rec.BirthResultsInWorld) + 1,
		DictName:      found.NameCn,
		DictContinent: continent,
		Coordinate:    found.Position,
		Center:        found.Position,
	}
	rec.BirthResultsInWorld = append(rec.BirthResultsInWorld, birth)
	rec.Timestamp = now.UnixMilli()
	if err := saveRecord(ctx, c.State, rec); err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}

	if cfg.IsMapImageIncluded {
		if img, err := renderPNG(ctx, renderWorldMap(birth, username, data), cfg); err == nil {
			_ = c.ReplyImageBytes(ctx, img)
		}
	}
	message := strings.Join([]string{
		fmt.Sprintf("🌍 第 %d 次轮回 · 落地平安", birth.Index),
		entry("大洲", continent),
		entry("国度", found.NameCn),
		"　" + pickOne(blessings, rnd),
	}, "\n")
	return sendText(ctx, c, cfg, message, true)
}

func findCountry(worldData map[string]country, nameCn string) (country, bool) {
	for _, item := range worldData {
		if item.NameCn == nameCn {
			return item, true
		}
	}
	return country{}, false
}

func chinaRecord(ctx context.Context, c sdk.Call, cfg config, data *dataset, name, args string) error {
	targetID, targetName := resolveTarget(c)
	records, err := listRecords(ctx, c.State)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	rec, ok, err := loadRecord(ctx, c.State, targetID)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	if !ok || len(rec.BirthResultsInChina) == 0 {
		return c.Reply(ctx, "📋 命簿上尚无此人的中国投胎记录\n发送「toutai.投胎中国」走一遭，名字便落上去了。")
	}
	if rec.Username == "" {
		rec.Username = targetName
	}
	userRank := rankAmong(records, targetID, func(r record) int { return len(r.BirthResultsInChina) })
	stillbirthsRank := rankAmong(records, targetID, func(r record) int { return r.NumberOfStillbirthsInChina })
	analysis := analyzeChina(rec.BirthResultsInChina)
	switch name {
	case "toutai.中国投胎记录.总览":
		return sendImageOrText(ctx, c, cfg,
			renderChinaOverview(rec.Username, analysis, userRank, stillbirthsRank, rec.NumberOfStillbirthsInChina, data.totalProvinceCount()),
			chinaOverviewText(rec.Username, analysis, userRank, stillbirthsRank, rec.NumberOfStillbirthsInChina))
	case "toutai.中国投胎记录.成功历史":
		last := lastN(rec.BirthResultsInChina, 20)
		return sendImageOrText(ctx, c, cfg,
			renderChinaBirthHistory(rec.Username, last),
			chinaBirthHistoryText(rec.Username, rec.BirthResultsInChina))
	case "toutai.中国投胎记录.地区分布":
		return sendImageOrText(ctx, c, cfg,
			renderRegionDistribution(rec.Username, rec.BirthResultsInChina),
			regionDistributionText(rec.Username, rec.BirthResultsInChina))
	case "toutai.中国投胎记录.性别分布":
		return sendImageOrText(ctx, c, cfg,
			renderGenderDistribution(rec.Username, rec.BirthResultsInChina),
			genderDistributionText(rec.Username, rec.BirthResultsInChina))
	case "toutai.中国投胎记录.第一次出现":
		return sendImageOrText(ctx, c, cfg,
			renderFirstAppearance(rec.Username, rec.BirthResultsInChina, data.totalProvinceCount()),
			firstAppearanceText(rec.Username, rec.BirthResultsInChina, data.totalProvinceCount()))
	}
	return nil
}

func worldRecord(ctx context.Context, c sdk.Call, cfg config, data *dataset, name, args string) error {
	targetID, targetName := resolveTarget(c)
	records, err := listRecords(ctx, c.State)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	rec, ok, err := loadRecord(ctx, c.State, targetID)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	if rec.Username == "" {
		rec.Username = targetName
	}
	if name == "toutai.世界投胎记录.夭折历史" {
		if !ok || len(rec.UnfortunateDemiseRecordsInWorld) == 0 {
			return c.Reply(ctx, "📋 命簿上尚无此人的世界夭折记录\n愿它一直空着。\n发送「toutai.投胎世界」走一遭，名字才会落到这里。")
		}
		last := lastN(rec.UnfortunateDemiseRecordsInWorld, 20)
		return sendImageOrText(ctx, c, cfg,
			renderWorldDemiseHistory(rec.Username, last),
			worldDemiseHistoryText(rec.Username, rec.UnfortunateDemiseRecordsInWorld))
	}
	if !ok || len(rec.BirthResultsInWorld) == 0 {
		return c.Reply(ctx, "📋 命簿上尚无此人的世界投胎记录\n发送「toutai.投胎世界」走一遭，名字便落上去了。")
	}
	userRank := rankAmong(records, targetID, func(r record) int { return len(r.BirthResultsInWorld) })
	stillbirthsRank := rankAmong(records, targetID, func(r record) int { return r.NumberOfStillbirthsInWorld })
	analysis := analyzeWorld(rec.BirthResultsInWorld)
	switch name {
	case "toutai.世界投胎记录.总览":
		return sendImageOrText(ctx, c, cfg,
			renderWorldOverview(rec.Username, analysis, userRank, stillbirthsRank, rec.NumberOfStillbirthsInWorld),
			worldOverviewText(rec.Username, analysis, userRank, stillbirthsRank, rec.NumberOfStillbirthsInWorld))
	case "toutai.世界投胎记录.成功历史":
		last := lastN(rec.BirthResultsInWorld, 20)
		return sendImageOrText(ctx, c, cfg,
			renderWorldBirthHistory(rec.Username, last),
			worldBirthHistoryText(rec.Username, rec.BirthResultsInWorld))
	}
	return nil
}

func chinaRanking(ctx context.Context, c sdk.Call, cfg config, name, args string) error {
	requested := parseCount(args)
	count := displayLimit(requested, cfg.DefaultMaxDisplayCount, cfg.MaxDisplayCount)
	records, err := listRecords(ctx, c.State)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	selfID := c.Event.UserID
	options := rankingOptions{
		valueLabel: "次",
		tone:       "jade",
		selfUserID: selfID,
		tip:        "toutai.投胎中国",
	}
	switch name {
	case "toutai.中国投胎排行榜.成功次数":
		options.title = "中国投胎 · 降生次数榜"
		options.seal = "降生"
		options.pick = func(r record) int { return len(r.BirthResultsInChina) }
	case "toutai.中国投胎排行榜.夭折次数":
		options.title = "中国投胎 · 夭折次数榜"
		options.seal = "长夜"
		options.tone = "cinnabar"
		options.pick = func(r record) int { return r.NumberOfStillbirthsInChina }
	case "toutai.中国投胎排行榜.男孩次数", "toutai.中国投胎排行榜.女孩次数":
		gender := "male"
		label := "男孩"
		options.tone = "azure"
		options.seal = "青阳"
		if name == "toutai.中国投胎排行榜.女孩次数" {
			gender = "female"
			label = "女孩"
			options.tone = "rose"
			options.seal = "绛雪"
		}
		options.title = "中国投胎 · " + label + "次数榜"
		options.pick = func(r record) int {
			count := 0
			for _, item := range r.BirthResultsInChina {
				if item.Gender == gender {
					count++
				}
			}
			return count
		}
	default:
		return nil
	}
	return sendImageOrText(ctx, c, cfg,
		renderRankings(records, count, options),
		rankingsText(records, count, options))
}

func worldRanking(ctx context.Context, c sdk.Call, cfg config, name, args string) error {
	requested := parseCount(args)
	count := displayLimit(requested, cfg.DefaultMaxDisplayCount, cfg.MaxDisplayCount)
	records, err := listRecords(ctx, c.State)
	if err != nil {
		return c.Reply(ctx, "命簿读写失败："+err.Error())
	}
	options := rankingOptions{
		valueLabel: "次",
		tone:       "azure",
		selfUserID: c.Event.UserID,
		tip:        "toutai.投胎世界",
		seal:       "寰宇",
	}
	switch name {
	case "toutai.世界投胎排行榜.成功次数":
		options.title = "世界投胎 · 降生次数榜"
		options.tone = "jade"
		options.pick = func(r record) int { return len(r.BirthResultsInWorld) }
	case "toutai.世界投胎排行榜.夭折次数":
		options.title = "世界投胎 · 夭折次数榜"
		options.tone = "cinnabar"
		options.seal = "长夜"
		options.pick = func(r record) int { return r.NumberOfStillbirthsInWorld }
	default:
		continent := strings.TrimPrefix(name, "toutai.世界投胎排行榜.")
		found := false
		for _, item := range continentOrder {
			if item == continent {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		options.title = "世界投胎 · " + continent + "次数榜"
		options.pick = func(r record) int {
			count := 0
			for _, birth := range r.BirthResultsInWorld {
				if birth.DictContinent == continent {
					count++
				}
			}
			return count
		}
	}
	return sendImageOrText(ctx, c, cfg,
		renderRankings(records, count, options),
		rankingsText(records, count, options))
}

func resolveTarget(c sdk.Call) (string, string) {
	self := c.Event.UserID
	if ids := sdk.MentionUserIDs(c.Event); len(ids) > 0 && ids[0] != "" {
		return ids[0], ids[0]
	}
	if c.Event.Nickname != "" {
		return self, c.Event.Nickname
	}
	return self, self
}

func parseCount(args string) int {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.Atoi(fields[0])
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func lastN[T any](items []T, n int) []T {
	if len(items) <= n {
		out := append([]T(nil), items...)
		reverse(out)
		return out
	}
	out := append([]T(nil), items[len(items)-n:]...)
	reverse(out)
	return out
}

func reverse[T any](items []T) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func displayName(c sdk.Call) string {
	if c.Event.Nickname != "" {
		return c.Event.Nickname
	}
	if c.Event.UserID != "" {
		return c.Event.UserID
	}
	return "无名氏"
}

// sendText 发送纯文本；shouldAt 为真时按配置 @ 用户。
func sendText(ctx context.Context, c sdk.Call, cfg config, text string, shouldAt bool) error {
	if cfg.ShouldPrefixUsername && shouldAt && c.Event.UserID != "" {
		return c.ReplyAt(ctx, "~\n"+text, c.Event.UserID)
	}
	return c.Reply(ctx, text)
}

// sendImageOrText 图片优先；渲染失败时改发等价文本。
func sendImageOrText(ctx context.Context, c sdk.Call, cfg config, html, fallback string) error {
	img, err := renderPNG(ctx, html, cfg)
	if err == nil {
		if err := c.ReplyImageBytes(ctx, img); err == nil {
			return nil
		}
	}
	return sendText(ctx, c, cfg, fallback, false)
}

// renderPNG 渲染 HTML 并按配置输出图片。
func renderPNG(ctx context.Context, html string, cfg config) ([]byte, error) {
	if strings.TrimSpace(html) == "" {
		return nil, fmt.Errorf("empty html")
	}
	data, err := renderHTML(ctx, []byte(html), sdk.RenderOptions{
		Width:    cardWidth,
		Height:   800,
		FullPage: true,
		Timeout:  renderTimeout,
	})
	if err != nil {
		return nil, err
	}
	if cfg.ImageType == "jpeg" {
		return data, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

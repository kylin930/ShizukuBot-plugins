// Package patina 移植上游 koishi-plugin-patina 的 HTML/Canvas 图像特效指令：
// 幻影 / 幻影解图、像素化、相机镜框、光棱 / 光棱取图、原图坦克、
// 原图坦克2.合成 / 提取。
//
// 上游依赖 puppeteer 的 setContent、文件选择器和 ffmpeg；宿主浏览器只提供
// Navigate / Evaluate / ElementScreenshot，因此这里把页面写入临时文件后用
// file:// 打开，通过 DataTransfer 注入图片，原图坦克则改用 Go 的 image/gif
// 在本地合成。
package patina

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/events"
	"shizukubot/sdk"
)

const (
	pluginVersion         = "1.0.0"
	promptTimeout         = 30 * time.Second
	defaultBrowserTimeout = 180 * time.Second
)

type config struct {
	EnableMirage    bool
	MirageName      string
	MirageSplitName string
	MirageFullColor bool
	MirageSize      int
	MirageWeight    float64

	EnablePixelate bool
	PixelateName   string
	Pixelate       int

	EnableCamera     bool
	CameraName       string
	CameraAlignment  string
	CameraQuality    int
	EnablePrism      bool
	PrismName        string
	PrismDecodeName  string
	PrismFullColor   bool
	PrismInnerThresh int
	PrismCoverThresh int
	PrismInnerContr  int
	PrismCoverContr  int
	PrismSize        int
	PrismReverse     bool
	PrismMethod      string
	PrismDecodeThr   int
	PrismDecodeOpt   string

	EnableTank    bool
	TankName      string
	TankLoopCount int
	TankFinalMS   int

	EnableAPNG       bool
	APNGName         string
	APNGComposeName  string
	APNGExtractName  string
	APNGFirstFrameMS int
	APNGBackground   string

	BrowserTimeout time.Duration
}

func defaultConfig() config {
	return config{
		EnableMirage:    false,
		MirageName:      "幻影",
		MirageSplitName: "幻影解图",
		MirageSize:      1200,
		MirageWeight:    0.7,

		EnablePixelate: false,
		PixelateName:   "像素化",
		Pixelate:       80,

		EnableCamera:    false,
		CameraName:      "相机镜框",
		CameraAlignment: "居中填充",
		CameraQuality:   50,

		EnablePrism:      false,
		PrismName:        "光棱",
		PrismDecodeName:  "光棱取图",
		PrismFullColor:   true,
		PrismInnerThresh: 32,
		PrismCoverThresh: 96,
		PrismInnerContr:  50,
		PrismCoverContr:  50,
		PrismSize:        1200,
		PrismMethod:      "chess",
		PrismDecodeThr:   128,
		PrismDecodeOpt:   "white",

		EnableTank:    false,
		TankName:      "原图坦克",
		TankLoopCount: 1,
		TankFinalMS:   50000,

		EnableAPNG:       false,
		APNGName:         "原图坦克2",
		APNGComposeName:  "合成",
		APNGExtractName:  "提取",
		APNGFirstFrameMS: 10,
		APNGBackground:   "#ffffff",

		BrowserTimeout: defaultBrowserTimeout,
	}
}

// Definition 注册图像特效插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "patina",
		Name:        "图像特效",
		Version:     pluginVersion,
		Description: "幻影/光棱/像素化/相机镜框/原图坦克/GIF与APNG合成，基于本地浏览器渲染",
	}, []sdk.EventType{
		sdk.GroupMessage, sdk.GroupAtMessage, sdk.C2CMessage,
		sdk.GuildMessage, sdk.AtMessage, sdk.DirectMessage,
	}, Handle, []triggercmd.Command{
		{Name: "patina", Usage: "发送带图片的「幻影/像素化/相机镜框/光棱」等指令触发"},
	}, sdk.WithConfig(configSchema()))
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"enableMirage":    {Type: "boolean", Description: "开启幻影/幻影解图", Default: json.RawMessage(`false`)},
			"mirageName":      {Type: "string", Description: "幻影指令名称", Default: json.RawMessage(`"幻影"`)},
			"mirageSplitName": {Type: "string", Description: "幻影解图指令名称", Default: json.RawMessage(`"幻影解图"`)},
			"mirageFullColor": {Type: "boolean", Description: "幻影全彩输出，关闭后变成黑白图", Default: json.RawMessage(`false`)},
			"mirageSize":      {Type: "integer", Description: "幻影输出尺寸（长宽中的较大值，0 为不指定）", Default: json.RawMessage(`1200`), Minimum: floatPtr(0)},
			"mirageWeight":    {Type: "number", Description: "里图混合权重（0~1，越大里图越明显）", Default: json.RawMessage(`0.7`), Minimum: floatPtr(0), Maximum: floatPtr(1)},

			"enablePixelate": {Type: "boolean", Description: "开启像素化", Default: json.RawMessage(`false`)},
			"pixelateName":   {Type: "string", Description: "像素化指令名称", Default: json.RawMessage(`"像素化"`)},
			"pixelate":       {Type: "integer", Description: "默认像素化百分比", Default: json.RawMessage(`80`), Minimum: floatPtr(0), Maximum: floatPtr(100)},

			"enableCamera":    {Type: "boolean", Description: "开启相机镜框", Default: json.RawMessage(`false`)},
			"cameraName":      {Type: "string", Description: "相机镜框指令名称", Default: json.RawMessage(`"相机镜框"`)},
			"cameraAlignment": {Type: "string", Description: "输入图片的对齐逻辑", Enum: []any{"居中填充", "拉伸", "适应"}, Default: json.RawMessage(`"居中填充"`)},
			"cameraQuality":   {Type: "integer", Description: "输出 JPEG 压缩质量（%）", Default: json.RawMessage(`50`), Minimum: floatPtr(0), Maximum: floatPtr(100)},

			"enablePrism":          {Type: "boolean", Description: "开启光棱/光棱取图", Default: json.RawMessage(`false`)},
			"prismName":            {Type: "string", Description: "光棱指令名称", Default: json.RawMessage(`"光棱"`)},
			"prismDecodeName":      {Type: "string", Description: "光棱取图指令名称", Default: json.RawMessage(`"光棱取图"`)},
			"prismFullColor":       {Type: "boolean", Description: "全彩输出，关闭后变成黑白图", Default: json.RawMessage(`true`)},
			"prismInnerThreshold":  {Type: "integer", Description: "里图色阶端点（越小隐写效果越好，但里图质量越差）", Default: json.RawMessage(`32`)},
			"prismCoverThreshold":  {Type: "integer", Description: "表图色阶端点（越大显形效果越好，但表图质量越差）", Default: json.RawMessage(`96`)},
			"prismInnerContrast":   {Type: "integer", Description: "里图对比度（降低可提高隐写效果）", Default: json.RawMessage(`50`)},
			"prismCoverContrast":   {Type: "integer", Description: "表图对比度", Default: json.RawMessage(`50`)},
			"prismSize":            {Type: "integer", Description: "输出尺寸（长宽中的较大值）", Default: json.RawMessage(`1200`)},
			"prismReverse":         {Type: "boolean", Description: "是否反向隐写", Default: json.RawMessage(`false`)},
			"prismMethod":          {Type: "string", Description: "像素混合方式", Enum: []any{"chess", "gap_2", "gap_3", "gap_5", "col_1", "row_1"}, Default: json.RawMessage(`"chess"`)},
			"prismDecodeThreshold": {Type: "integer", Description: "光棱取图阈值", Default: json.RawMessage(`128`)},
			"prismDecodeOption":    {Type: "string", Description: "光棱取图像素处理方式", Enum: []any{"black", "white", "trans", "lcopy", "ucopy", "luavg"}, Default: json.RawMessage(`"white"`)},

			"enableTank":     {Type: "boolean", Description: "开启原图坦克", Default: json.RawMessage(`false`)},
			"tankName":       {Type: "string", Description: "原图坦克指令名称", Default: json.RawMessage(`"原图坦克"`)},
			"tankLoopCount":  {Type: "integer", Description: "GIF 循环次数（0 为无限循环）", Default: json.RawMessage(`1`)},
			"tankFinalDelay": {Type: "integer", Description: "最后一帧延迟（毫秒）", Default: json.RawMessage(`50000`)},

			"enableAPNG":      {Type: "boolean", Description: "开启原图坦克2", Default: json.RawMessage(`false`)},
			"apngName":        {Type: "string", Description: "原图坦克2 指令主名称", Default: json.RawMessage(`"原图坦克2"`)},
			"apngComposeName": {Type: "string", Description: "合成指令后缀", Default: json.RawMessage(`"合成"`)},
			"apngExtractName": {Type: "string", Description: "提取指令后缀", Default: json.RawMessage(`"提取"`)},
			"apngFirstFrame":  {Type: "integer", Description: "APNG 第一帧停留毫秒数", Default: json.RawMessage(`10`), Minimum: floatPtr(1), Maximum: floatPtr(65535)},
			"apngBackground":  {Type: "string", Description: "表图背景填充颜色", Default: json.RawMessage(`"#ffffff"`)},
			"browserTimeout":  {Type: "integer", Description: "浏览器超时（秒）", Default: json.RawMessage(`180`), Minimum: floatPtr(1)},
		},
	}
}

func floatPtr(v float64) *float64 { return &v }

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	cfg.EnableMirage = boolValue(raw, "enableMirage", cfg.EnableMirage)
	cfg.MirageName = stringValue(raw, "mirageName", cfg.MirageName)
	cfg.MirageSplitName = stringValue(raw, "mirageSplitName", cfg.MirageSplitName)
	cfg.MirageFullColor = boolValue(raw, "mirageFullColor", cfg.MirageFullColor)
	cfg.MirageSize = intValue(raw, "mirageSize", cfg.MirageSize)
	cfg.MirageWeight = floatValue(raw, "mirageWeight", cfg.MirageWeight)

	cfg.EnablePixelate = boolValue(raw, "enablePixelate", cfg.EnablePixelate)
	cfg.PixelateName = stringValue(raw, "pixelateName", cfg.PixelateName)
	cfg.Pixelate = intValue(raw, "pixelate", cfg.Pixelate)

	cfg.EnableCamera = boolValue(raw, "enableCamera", cfg.EnableCamera)
	cfg.CameraName = stringValue(raw, "cameraName", cfg.CameraName)
	cfg.CameraAlignment = stringValue(raw, "cameraAlignment", cfg.CameraAlignment)
	cfg.CameraQuality = intValue(raw, "cameraQuality", cfg.CameraQuality)

	cfg.EnablePrism = boolValue(raw, "enablePrism", cfg.EnablePrism)
	cfg.PrismName = stringValue(raw, "prismName", cfg.PrismName)
	cfg.PrismDecodeName = stringValue(raw, "prismDecodeName", cfg.PrismDecodeName)
	cfg.PrismFullColor = boolValue(raw, "prismFullColor", cfg.PrismFullColor)
	cfg.PrismInnerThresh = intValue(raw, "prismInnerThreshold", cfg.PrismInnerThresh)
	cfg.PrismCoverThresh = intValue(raw, "prismCoverThreshold", cfg.PrismCoverThresh)
	cfg.PrismInnerContr = intValue(raw, "prismInnerContrast", cfg.PrismInnerContr)
	cfg.PrismCoverContr = intValue(raw, "prismCoverContrast", cfg.PrismCoverContr)
	cfg.PrismSize = intValue(raw, "prismSize", cfg.PrismSize)
	cfg.PrismReverse = boolValue(raw, "prismReverse", cfg.PrismReverse)
	cfg.PrismMethod = stringValue(raw, "prismMethod", cfg.PrismMethod)
	cfg.PrismDecodeThr = intValue(raw, "prismDecodeThreshold", cfg.PrismDecodeThr)
	cfg.PrismDecodeOpt = stringValue(raw, "prismDecodeOption", cfg.PrismDecodeOpt)

	cfg.EnableTank = boolValue(raw, "enableTank", cfg.EnableTank)
	cfg.TankName = stringValue(raw, "tankName", cfg.TankName)
	cfg.TankLoopCount = intValue(raw, "tankLoopCount", cfg.TankLoopCount)
	cfg.TankFinalMS = intValue(raw, "tankFinalDelay", cfg.TankFinalMS)

	cfg.EnableAPNG = boolValue(raw, "enableAPNG", cfg.EnableAPNG)
	cfg.APNGName = stringValue(raw, "apngName", cfg.APNGName)
	cfg.APNGComposeName = stringValue(raw, "apngComposeName", cfg.APNGComposeName)
	cfg.APNGExtractName = stringValue(raw, "apngExtractName", cfg.APNGExtractName)
	cfg.APNGFirstFrameMS = intValue(raw, "apngFirstFrame", cfg.APNGFirstFrameMS)
	cfg.APNGBackground = stringValue(raw, "apngBackground", cfg.APNGBackground)

	secs := intValue(raw, "browserTimeout", int(cfg.BrowserTimeout/time.Second))
	if secs <= 0 {
		secs = int(defaultBrowserTimeout / time.Second)
	}
	cfg.BrowserTimeout = time.Duration(secs) * time.Second
	return cfg
}

func boolValue(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(t)
		if err == nil {
			return b
		}
	}
	return def
}

func stringValue(m map[string]any, key, def string) string {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return def
}

func intValue(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func floatValue(m map[string]any, key string, def float64) float64 {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
	}
	return def
}

func qqSource(s events.Source) bool {
	switch s {
	case events.Group, events.C2C, events.Guild, events.Direct:
		return true
	}
	return false
}

// Handle 解析指令文本并把图片交给对应的处理方法。
func Handle(ctx context.Context, c sdk.Call) error {
	if !qqSource(c.Event.Source) {
		return nil
	}
	key := conversationKey(c)
	if job, ok := takePending(key); ok {
		return continuePending(ctx, c, job)
	}
	cfg := decodeConfig(c.Config)
	text := trimPrefix(strings.TrimSpace(sdk.Text(c.Event)))
	for _, cand := range candidates(cfg) {
		rest, ok := cutCommand(text, cand.name)
		if !ok {
			continue
		}
		return start(ctx, c, cfg, cand.kind, rest)
	}
	return nil
}

type candidate struct {
	name string
	kind string
}

func candidates(cfg config) []candidate {
	var list []candidate
	add := func(enable bool, name, kind string) {
		name = strings.TrimSpace(name)
		if enable && name != "" {
			list = append(list, candidate{name: name, kind: kind})
		}
	}
	add(cfg.EnableMirage, cfg.MirageName, "mirage")
	add(cfg.EnableMirage, cfg.MirageSplitName, "mirageSplit")
	add(cfg.EnablePixelate, cfg.PixelateName, "pixelate")
	add(cfg.EnableCamera, cfg.CameraName, "camera")
	add(cfg.EnablePrism, cfg.PrismName, "prism")
	add(cfg.EnablePrism, cfg.PrismDecodeName, "prismDecode")
	add(cfg.EnableTank, cfg.TankName, "tank")
	if cfg.EnableAPNG {
		base := strings.TrimSpace(cfg.APNGName)
		if base != "" {
			add(true, base+"."+cfg.APNGComposeName, "apngCompose")
			add(true, base+"."+cfg.APNGExtractName, "apngExtract")
		}
	}
	// 长名称优先，避免「幻影」抢先匹配「幻影解图」。
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && len([]rune(list[j].name)) > len([]rune(list[j-1].name)); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	return list
}

func trimPrefix(text string) string {
	lower := strings.ToLower(text)
	for _, p := range []string{"patina ", "patina.", "patina/"} {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(text[len(p):])
		}
	}
	return text
}

func cutCommand(text, name string) (string, bool) {
	if !strings.HasPrefix(text, name) {
		return "", false
	}
	rest := text[len(name):]
	if rest == "" {
		return "", true
	}
	r := []rune(rest)[0]
	if unicode.IsSpace(r) {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func needOf(kind string) int {
	switch kind {
	case "mirage", "prism", "tank", "apngCompose":
		return 2
	}
	return 1
}

func promptsFor(cfg config, kind string) []string {
	switch kind {
	case "mirage", "prism":
		return []string{"请发送一张图片作为【表图】：", "请发送一张图片作为【里图】："}
	case "tank":
		return []string{"请发送第一张图片（表图）", "请发送第二张图片（里图）"}
	case "apngCompose":
		return []string{"请发送第一张图片（表图）：", "请发送第二张图片（里图）："}
	case "apngExtract":
		return []string{"请发送一张 APNG 图片："}
	case "mirageSplit":
		return []string{"请发送一张幻影坦克图片："}
	case "prismDecode":
		return []string{"请发送一张光棱坦克图片："}
	}
	return []string{"请发送一张图片："}
}

func start(ctx context.Context, c sdk.Call, cfg config, kind, rest string) error {
	args := parseArgs(rest, flagTable[kind])
	sources := collectSources(c, args.positional)
	need := needOf(kind)
	if len(sources) > need {
		sources = sources[:need]
	}
	if len(sources) < need {
		storePending(conversationKey(c), &pendingJob{
			created: time.Now(),
			kind:    kind,
			flags:   args.flags,
			sources: sources,
			need:    need,
		})
		prompts := promptsFor(cfg, kind)
		return c.Reply(ctx, prompts[len(sources)])
	}
	return execute(ctx, c, cfg, kind, args.flags, sources)
}

func continuePending(ctx context.Context, c sdk.Call, job *pendingJob) error {
	cfg := decodeConfig(c.Config)
	source := firstSource(c)
	if source == "" {
		return c.Reply(ctx, "未检测到有效的图片，请重试。")
	}
	job.sources = append(job.sources, source)
	if len(job.sources) < job.need {
		storePending(conversationKey(c), job)
		prompts := promptsFor(cfg, job.kind)
		return c.Reply(ctx, prompts[len(job.sources)])
	}
	return execute(ctx, c, cfg, job.kind, job.flags, job.sources)
}

func execute(ctx context.Context, c sdk.Call, cfg config, kind string, flags map[string]string, sources []string) error {
	args := &argsSet{flags: flags}
	switch kind {
	case "mirage":
		return runMirage(ctx, c, cfg, args, sources)
	case "mirageSplit":
		return runMirageSplit(ctx, c, cfg, sources)
	case "pixelate":
		return runPixelate(ctx, c, cfg, args, sources)
	case "camera":
		return runCamera(ctx, c, cfg, sources)
	case "prism":
		return runPrism(ctx, c, cfg, args, sources)
	case "prismDecode":
		return runPrismDecode(ctx, c, cfg, args, sources)
	case "tank":
		return runTank(ctx, c, cfg, args, sources)
	case "apngCompose":
		return runAPNGCompose(ctx, c, cfg, sources)
	case "apngExtract":
		return runAPNGExtract(ctx, c, cfg, sources)
	}
	return nil
}

func collectSources(c sdk.Call, positional []string) []string {
	var out []string
	for _, id := range sdk.MentionUserIDs(c.Event) {
		if id != "" {
			out = append(out, qlogoURL(id))
		}
	}
	out = append(out, sdk.ImageURLs(c.Event)...)
	for _, token := range positional {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		// 上游 extractImageUrl 会把纯数字 QQ 号转换为 qlogo 头像。
		if isQQNumber(token) {
			out = append(out, qlogoURL(token))
			continue
		}
		out = append(out, token)
	}
	return out
}

func firstSource(c sdk.Call) string {
	sources := collectSources(c, nil)
	if len(sources) == 0 {
		if text := strings.TrimSpace(sdk.Text(c.Event)); isQQNumber(text) {
			return qlogoURL(text)
		}
		return ""
	}
	return sources[0]
}

func isQQNumber(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func qlogoURL(id string) string {
	return "http://q.qlogo.cn/headimg_dl?dst_uin=" + id + "&spec=640"
}

type flagDef struct {
	key   string
	value bool
}

var flagTable = map[string]map[string]flagDef{
	"mirage": {
		"-f": {key: "fullColor"},
		"-s": {key: "size", value: true},
		"-w": {key: "weight", value: true},
	},
	"pixelate": {
		"-p": {key: "pixelate", value: true},
	},
	"prism": {
		"-g":  {key: "grayscale"},
		"-it": {key: "innerThreshold", value: true},
		"-ct": {key: "coverThreshold", value: true},
		"-ic": {key: "innerContrast", value: true},
		"-cc": {key: "coverContrast", value: true},
		"-s":  {key: "size", value: true},
		"-z":  {key: "size", value: true},
		"-r":  {key: "reverse"},
		"-m":  {key: "method", value: true},
	},
	"prismDecode": {
		"-t":         {key: "threshold", value: true},
		"-o":         {key: "option", value: true},
		"-r":         {key: "reverse"},
		"--contrast": {key: "contrast", value: true},
	},
	"tank": {
		"-w": {key: "width", value: true},
		"-h": {key: "height", value: true},
		"-d": {key: "delay", value: true},
		"-q": {key: "quality", value: true},
	},
}

type parsedArgs struct {
	flags      map[string]string
	positional []string
}

func parseArgs(rest string, table map[string]flagDef) parsedArgs {
	args := parsedArgs{flags: map[string]string{}}
	tokens := strings.Fields(rest)
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		def, ok := table[tok]
		if !ok {
			if strings.HasPrefix(tok, "-") {
				continue
			}
			args.positional = append(args.positional, tok)
			continue
		}
		if !def.value {
			args.flags[def.key] = "true"
			continue
		}
		if i+1 >= len(tokens) {
			continue
		}
		i++
		args.flags[def.key] = tokens[i]
	}
	return args
}

type argsSet struct {
	flags map[string]string
}

func (a *argsSet) has(key string) bool {
	if a == nil {
		return false
	}
	_, ok := a.flags[key]
	return ok
}

func (a *argsSet) bool(key string, def bool) bool {
	if a == nil {
		return def
	}
	v, ok := a.flags[key]
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func (a *argsSet) int(key string, def int) int {
	if a == nil {
		return def
	}
	v, ok := a.flags[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func (a *argsSet) float(key string, def float64) float64 {
	if a == nil {
		return def
	}
	v, ok := a.flags[key]
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func (a *argsSet) string(key, def string) string {
	if a == nil {
		return def
	}
	v, ok := a.flags[key]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

type pendingJob struct {
	created time.Time
	kind    string
	flags   map[string]string
	sources []string
	need    int
}

var (
	pendingMu sync.Mutex
	pendings  = map[string]*pendingJob{}
)

func conversationKey(c sdk.Call) string {
	chat := c.Event.GroupID
	if chat == "" {
		chat = c.Event.ChannelID
	}
	return fmt.Sprintf("%s|%s|%s|%s", c.Event.BotID, c.Event.Source, chat, c.Event.UserID)
}

func storePending(key string, job *pendingJob) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	cleanupPendingLocked()
	pendings[key] = job
}

func takePending(key string) (*pendingJob, bool) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	cleanupPendingLocked()
	job, ok := pendings[key]
	if !ok {
		return nil, false
	}
	delete(pendings, key)
	return job, true
}

func cleanupPendingLocked() {
	now := time.Now()
	for key, job := range pendings {
		if now.Sub(job.created) > promptTimeout {
			delete(pendings, key)
		}
	}
}

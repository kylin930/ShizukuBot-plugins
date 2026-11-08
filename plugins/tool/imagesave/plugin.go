// Package imagesave 将消息中的图片保存到本地目录。
// 对齐上游 koishi-plugin-image-save-path 的交互模式、路径选择、重名检查、
// 自动命名、重复图片监听保存与“获取链接”调试指令。
package imagesave

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	pluginVersion = "1.0.0"
	promptTimeout = 30 * time.Second
	defaultRules  = "${YYYY}-${MM}-${DD}-${BB}-${BB}-${BB}-${CCC}"
)

type savePath struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type groupMapping struct {
	Enable          bool   `json:"enable"`
	GroupList       string `json:"groupList"`
	Count           int    `json:"count"`
	DefaultSavePath string `json:"defaultsavepath"`
}

type config struct {
	InteractionMode  string
	CommandName      string
	ShowSavePath     bool
	CheckDuplicate   bool
	ImageSaveMode    bool
	SavePaths        []savePath
	AutoRenameRules  string
	AutosavePics     bool
	GroupMappings    []groupMapping
	ConsoleInfo      bool
	CommandOfGetLink bool
}

func defaultConfig() config {
	return config{
		InteractionMode: "1",
		CommandName:     "保存图片",
		CheckDuplicate:  true,
		SavePaths: []savePath{
			{Name: "第一个", Path: "images"},
			{Name: "第二个", Path: "images-2"},
		},
		AutoRenameRules: defaultRules,
		GroupMappings: []groupMapping{
			{Enable: true, GroupList: "114514", Count: 2, DefaultSavePath: "images"},
			{Enable: true, GroupList: "private:1919810", Count: 2, DefaultSavePath: "images"},
		},
	}
}

// Definition 注册图片保存插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "image-save-path",
		Name:        "图片保存",
		Version:     pluginVersion,
		Description: "保存消息/回复中的图片到本地目录，支持路径映射、重名检查与重复图片监听",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "image-save-path", Usage: "发送或回复图片即可保存；保存指令由配置的 commandName 决定"},
	},
		sdk.WithConfig(configSchema()),
		sdk.WithPermissions("state.read", "state.write"),
	)
}

func configSchema() sdk.Schema {
	return sdk.Schema{
		Type: "object",
		Properties: map[string]sdk.Schema{
			"Interaction_mode": {
				Type:        "string",
				Description: "交互模式 1~5（1: 名称+文件夹；2: 文件夹+名称；3: 名称+首个路径；4: 文件夹+自动命名；5: 自动命名+首个路径）",
				Default:     json.RawMessage(`"1"`),
			},
			"commandName": {
				Type:        "string",
				Description: "注册的指令名称",
				Default:     json.RawMessage(`"保存图片"`),
			},
			"showSavePath": {
				Type:        "boolean",
				Description: "保存成功后告知具体文件保存路径",
				Default:     json.RawMessage(`false`),
			},
			"checkDuplicate": {
				Type:        "boolean",
				Description: "检查重名文件，避免覆盖，同名时追加 (1)、(2)…",
				Default:     json.RawMessage(`true`),
			},
			"imageSaveMode": {
				Type:        "boolean",
				Description: "缺省路径参数时使用第一行文件夹",
				Default:     json.RawMessage(`false`),
			},
			"savePaths": {
				Type:        "array",
				Description: "图片保存路径的名称和地址映射",
				Default: json.RawMessage(`[{"name":"第一个","path":"images"},` +
					`{"name":"第二个","path":"images-2"}]`),
				Items: &sdk.Schema{Type: "object", Properties: map[string]sdk.Schema{
					"name": {Type: "string", Description: "文件夹备注"},
					"path": {Type: "string", Description: "文件夹路径"},
				}},
			},
			"ImageExtension": {
				Type:        "array",
				Description: "图片保存时的命名格式与默认扩展名（仅第一行有效，保留以兼容上游配置）",
				Items: &sdk.Schema{Type: "object", Properties: map[string]sdk.Schema{
					"prefix": {Type: "string", Description: "前缀"},
					"suffix": {Type: "string", Description: "后缀"},
				}},
			},
			"autoRenameRules": {
				Type:        "string",
				Description: "图片自动重命名使用的名称格式，可用变量 ${YYYY} ${MM} ${DD} ${A} ${BB} ${CCC} ${session.x} ${config.x}",
				Default:     json.RawMessage(`"` + defaultRules + `"`),
			},
			"autosavePics": {
				Type:        "boolean",
				Description: "自动保存总开关：对重复一定次数的图片进行保存",
				Default:     json.RawMessage(`false`),
			},
			"groupListmapping": {
				Type:        "array",
				Description: "各群组自动保存的路径映射（私信使用 private: 前缀）",
				Default: json.RawMessage(`[{"enable":true,"groupList":"114514","count":2,` +
					`"defaultsavepath":"images"},` +
					`{"enable":true,"groupList":"private:1919810","count":2,` +
					`"defaultsavepath":"images"}]`),
				Items: &sdk.Schema{Type: "object", Properties: map[string]sdk.Schema{
					"enable":          {Type: "boolean", Description: "勾选后启用自动保存"},
					"groupList":       {Type: "string", Description: "需要监听的群组ID"},
					"count":           {Type: "number", Description: "触发自动保存的重复次数", Default: json.RawMessage(`2`)},
					"defaultsavepath": {Type: "string", Description: "保存到的文件夹路径"},
				}},
			},
			"consoleinfo": {
				Type:        "boolean",
				Description: "日志调试模式",
				Default:     json.RawMessage(`false`),
			},
			"command_of_get_link": {
				Type:        "boolean",
				Description: "是否开启“获取链接”调试指令",
				Default:     json.RawMessage(`false`),
			},
		},
	}
}

// ---- 运行时状态 ----

type saveFlow struct {
	fileName   string
	pathName   string
	inlineName string
	images     []string
}

type pendingEntry struct {
	step     string // image | path | getlink
	flow     *saveFlow
	deadline time.Time
}

var pending sync.Map // conversation key -> pendingEntry

type hashRecord struct {
	Count int    `json:"count"`
	Path  string `json:"path"`
	Saved bool   `json:"saved"`
}

var recordsMu sync.Mutex

// 抽成包级变量，测试时替换。
var fetchImage = fetchImageHTTP

// Handle 处理所有消息事件：命令、交互等待与重复图片监听。
func Handle(ctx context.Context, c sdk.Call) error {
	cfg := decodeConfig(c.Config)
	r := &runner{ctx: ctx, c: c, cfg: cfg}
	key := pendingKey(c.Event)

	if entry, ok := takePending(key); ok {
		return r.continuePending(entry)
	}

	if cfg.AutosavePics {
		r.autosave()
	}

	text := strings.TrimSpace(sdk.Text(c.Event))
	if cfg.CommandOfGetLink {
		if rest, ok := commandRest(text, "获取链接"); ok {
			return r.startGetLink(rest)
		}
	}
	if rest, ok := commandRest(text, cfg.CommandName); ok {
		return r.startSave(rest)
	}
	return nil
}

type runner struct {
	ctx context.Context
	c   sdk.Call
	cfg config
}

func (r *runner) reply(text string) error { return r.c.Reply(r.ctx, text) }

func (r *runner) startSave(rest string) error {
	args := splitArgs(rest)
	flow := &saveFlow{}
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-n" || arg == "--name":
			if i+1 < len(args) {
				i++
				flow.inlineName = args[i]
			}
		case strings.HasPrefix(arg, "--name="):
			flow.inlineName = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "-n="):
			flow.inlineName = strings.TrimPrefix(arg, "-n=")
		default:
			positional = append(positional, arg)
		}
	}

	switch r.cfg.InteractionMode {
	case "2":
		flow.pathName = positionalAt(positional, 0)
		flow.fileName = positionalAt(positional, 1)
	case "3":
		flow.fileName = positionalAt(positional, 0)
	case "4":
		flow.pathName = positionalAt(positional, 0)
	case "5":
	default:
		flow.fileName = positionalAt(positional, 0)
		flow.pathName = positionalAt(positional, 1)
	}

	flow.images = sdk.ImageURLs(r.c.Event)
	if len(flow.images) == 0 {
		// 指令里显式给出了图片位置却没有图片消息段时，上游直接提示图片无效并返回，
		// 不会进入 30 秒等待，避免下一条无关图片被误保存。
		if imageArgIndex(r.cfg.InteractionMode) < len(positional) {
			return r.reply("输入的图片无效。")
		}
		if err := r.reply("请发送图片："); err != nil {
			return err
		}
		putPending(pendingKey(r.c.Event), "image", flow)
		return nil
	}
	return r.resolve(flow)
}

// imageArgIndex 返回各交互模式下图片参数的下标，与上游的解构顺序一致。
func imageArgIndex(mode string) int {
	switch mode {
	case "2":
		return 2
	case "3", "4":
		return 1
	case "5":
		return 0
	default:
		return 2
	}
}

func (r *runner) startGetLink(rest string) error {
	images := sdk.ImageURLs(r.c.Event)
	if len(images) == 0 {
		if strings.TrimSpace(rest) != "" {
			return r.reply("输入的图片无效。")
		}
		if err := r.reply("请发送图片："); err != nil {
			return err
		}
		putPending(pendingKey(r.c.Event), "getlink", &saveFlow{})
		return nil
	}
	return r.sendLinks(images)
}

func (r *runner) continuePending(entry pendingEntry) error {
	switch entry.step {
	case "image":
		images := sdk.ImageURLs(r.c.Event)
		if len(images) == 0 {
			return r.reply("输入的图片无效。")
		}
		entry.flow.images = images
		return r.resolve(entry.flow)
	case "path":
		name := strings.TrimSpace(sdk.Text(r.c.Event))
		p, ok := r.findPath(name)
		if !ok {
			return r.reply("请选择正确的路径名称。")
		}
		return r.saveWithPath(entry.flow, p)
	case "getlink":
		images := sdk.ImageURLs(r.c.Event)
		if len(images) == 0 {
			return r.reply("输入的图片无效。")
		}
		return r.sendLinks(images)
	}
	return nil
}

// resolve 完成路径判定并进入保存。
func (r *runner) resolve(flow *saveFlow) error {
	if !r.cfg.CheckDuplicate && len(flow.images) > 1 {
		return r.reply("未开启重名检查时不允许一次性输入多张图片。")
	}
	flow.fileName = r.finalFileName(flow)

	switch r.cfg.InteractionMode {
	case "3", "5":
		if len(r.cfg.SavePaths) == 0 {
			return r.reply("没有设置默认保存路径。")
		}
		return r.saveWithPath(flow, r.cfg.SavePaths[0])
	}

	if r.cfg.InteractionMode == "4" || flow.pathName != "" || r.cfg.ImageSaveMode {
		if flow.pathName == "" && r.cfg.ImageSaveMode {
			if len(r.cfg.SavePaths) == 0 {
				return r.reply("没有设置默认保存路径。")
			}
			return r.saveWithPath(flow, r.cfg.SavePaths[0])
		}
		if p, ok := r.findPath(flow.pathName); ok {
			return r.saveWithPath(flow, p)
		}
	}
	return r.promptPath(flow)
}

func (r *runner) promptPath(flow *saveFlow) error {
	if err := r.reply(pathPrompt(r.cfg.SavePaths)); err != nil {
		return err
	}
	putPending(pendingKey(r.c.Event), "path", flow)
	return nil
}

func pathPrompt(paths []savePath) string {
	var b strings.Builder
	b.WriteString("路径名称无效，请选择路径的名称（冒号左侧为名称）：")
	for _, p := range paths {
		b.WriteString("\n")
		b.WriteString(p.Name)
		b.WriteString(": ")
		b.WriteString(p.Path)
	}
	return b.String()
}

func (r *runner) findPath(name string) (savePath, bool) {
	for _, p := range r.cfg.SavePaths {
		if p.Name == name && p.Path != "" {
			return p, true
		}
	}
	return savePath{}, false
}

func (r *runner) finalFileName(flow *saveFlow) string {
	if flow.inlineName != "" {
		flow.fileName = flow.inlineName
	}
	if flow.fileName != "" {
		return flow.fileName
	}
	return generateFilename(r.cfg, r.c.Event)
}

func (r *runner) saveWithPath(flow *saveFlow, p savePath) error {
	if p.Path != "" {
		if err := os.MkdirAll(p.Path, 0o755); err != nil {
			return r.reply("保存图片时出错：" + err.Error())
		}
	}
	firstMessageSent := false
	var duplicates []string
	for _, url := range flow.images {
		data, ext, err := fetchImage(url)
		if err != nil {
			if replyErr := r.reply("保存图片时出错：" + err.Error()); replyErr != nil {
				return replyErr
			}
			continue
		}
		root := filepath.Join(p.Path, flow.fileName)
		final := root + ext
		index := 0
		if r.cfg.CheckDuplicate {
			for {
				if _, statErr := os.Stat(final); statErr != nil {
					break
				}
				index++
				final = fmt.Sprintf("%s(%d)%s", root, index, ext)
			}
		}
		if len(data) == 0 {
			if err := r.reply("保存图片时出错：下载的数据为空"); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(final, data, 0o644); err != nil {
			if replyErr := r.reply("保存图片时出错：" + err.Error()); replyErr != nil {
				return replyErr
			}
			continue
		}
		if index > 0 {
			duplicates = append(duplicates, fmt.Sprintf("出现同名文件，已保存为 %s(%d)%s", flow.fileName, index, ext))
		} else if !firstMessageSent {
			if r.cfg.ShowSavePath {
				if err := r.reply("图片已保存到：" + final); err != nil {
					return err
				}
			} else if err := r.reply("图片已成功保存。"); err != nil {
				return err
			}
			firstMessageSent = true
		}
	}
	if len(duplicates) > 0 {
		return r.reply(strings.Join(duplicates, "\n"))
	}
	return nil
}

func (r *runner) sendLinks(images []string) error {
	type linkInfo struct {
		url string
		ext string
	}
	list := make([]linkInfo, 0, len(images))
	for _, url := range images {
		data, ext, err := fetchImage(url)
		if err != nil || len(data) == 0 {
			return r.reply("获取图片信息失败。")
		}
		list = append(list, linkInfo{url: url, ext: ext})
	}
	var b strings.Builder
	b.WriteString("图片信息:\n")
	for _, it := range list {
		fmt.Fprintf(&b, "URL: %s\nType: %s\nFilename: temp%s\n\n", it.url, it.ext, it.ext)
	}
	return r.reply(b.String())
}

// autosave 在配置的群组中统计相同图片出现次数，达到阈值后落盘。
func (r *runner) autosave() {
	mapping, ok := r.cfg.mappingFor(r.c.Event)
	if !ok {
		return
	}
	urls := sdk.ImageURLs(r.c.Event)
	if len(urls) == 0 {
		return
	}

	recordsMu.Lock()
	defer recordsMu.Unlock()
	records := r.loadRecords()

	for _, url := range urls {
		data, ext, err := fetchImage(url)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:])
		rec := records[key]
		if rec == nil {
			rec = &hashRecord{}
			records[key] = rec
		}
		rec.Count++
		if rec.Count >= mapping.Count && !rec.Saved {
			savePath := mapping.DefaultSavePath
			if savePath == "" && len(r.cfg.SavePaths) > 0 {
				savePath = r.cfg.SavePaths[0].Path
			}
			if savePath == "" {
				continue
			}
			final := filepath.Join(savePath, generateFilename(r.cfg, r.c.Event)+ext)
			if err := os.WriteFile(final, data, 0o644); err != nil {
				continue
			}
			rec.Path = final
			rec.Saved = true
		}
	}
	r.saveRecords(records)
}

func (r *runner) loadRecords() map[string]*hashRecord {
	records := map[string]*hashRecord{}
	if r.c.State != nil {
		_ = sdk.LoadJSON(r.ctx, r.c.State, "hash_records", &records)
	}
	if records == nil {
		records = map[string]*hashRecord{}
	}
	return records
}

func (r *runner) saveRecords(records map[string]*hashRecord) {
	if r.c.State == nil {
		return
	}
	_ = sdk.SaveJSON(r.ctx, r.c.State, "hash_records", records)
}

func (c config) mappingFor(e sdk.Event) (groupMapping, bool) {
	channel := channelID(e)
	for _, m := range c.GroupMappings {
		if m.Enable && m.GroupList == channel {
			if m.Count <= 0 {
				m.Count = 1
			}
			return m, true
		}
	}
	return groupMapping{}, false
}

func channelID(e sdk.Event) string {
	switch {
	case e.GroupID != "":
		return e.GroupID
	case e.GuildID != "":
		return e.GuildID
	case e.ChannelID != "":
		return e.ChannelID
	default:
		return "private:" + e.UserID
	}
}

func pendingKey(e sdk.Event) string {
	return e.BotID + "|" + channelID(e)
}

func putPending(key, step string, flow *saveFlow) {
	pending.Store(key, pendingEntry{step: step, flow: flow, deadline: time.Now().Add(promptTimeout)})
}

func takePending(key string) (pendingEntry, bool) {
	v, ok := pending.LoadAndDelete(key)
	if !ok {
		return pendingEntry{}, false
	}
	entry := v.(pendingEntry)
	if time.Now().After(entry.deadline) {
		return pendingEntry{}, false
	}
	return entry, true
}

// generateFilename 按自动命名规则生成并清洗文件名。
func generateFilename(cfg config, e sdk.Event) string {
	rules := cfg.AutoRenameRules
	if strings.TrimSpace(rules) == "" {
		rules = defaultRules
	}
	nowTime := time.Now()
	session := map[string]string{
		"platform":  string(e.Source),
		"selfId":    e.BotID,
		"userId":    e.UserID,
		"channelId": e.ChannelID,
		"guildId":   e.GuildID,
		"messageId": e.MessageID,
		"content":   e.Message,
	}
	name := replaceVars(rules, func(key string) (string, bool) {
		switch key {
		case "YYYY":
			return fmt.Sprintf("%04d", nowTime.Year()), true
		case "MM":
			return fmt.Sprintf("%02d", int(nowTime.Month())), true
		case "DD":
			return fmt.Sprintf("%02d", nowTime.Day()), true
		case "A":
			return strconv.Itoa(rand.Intn(10)), true
		case "BB":
			return fmt.Sprintf("%02d", rand.Intn(100)), true
		case "CCC":
			return fmt.Sprintf("%03d", rand.Intn(1000)), true
		}
		if strings.HasPrefix(key, "session.") {
			v, ok := session[strings.TrimPrefix(key, "session.")]
			return v, ok && v != ""
		}
		return "", false
	})
	return sanitizeFilename(name)
}

func replaceVars(format string, lookup func(string) (string, bool)) string {
	var b strings.Builder
	for {
		start := strings.Index(format, "${")
		if start < 0 {
			b.WriteString(format)
			return b.String()
		}
		b.WriteString(format[:start])
		end := strings.Index(format[start:], "}")
		if end < 0 {
			b.WriteString(format[start:])
			return b.String()
		}
		end += start
		key := format[start+2 : end]
		if v, ok := lookup(key); ok {
			b.WriteString(v)
		} else {
			b.WriteString(format[start : end+1])
		}
		format = format[end+1:]
	}
}

func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return '_'
		}
		return r
	}, name)
}

// ---- 配置解码 ----

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	if v, ok := raw["Interaction_mode"].(string); ok && v != "" {
		cfg.InteractionMode = v
	}
	if v, ok := raw["commandName"].(string); ok && v != "" {
		cfg.CommandName = v
	}
	cfg.ShowSavePath = boolValue(raw["showSavePath"], cfg.ShowSavePath)
	cfg.CheckDuplicate = boolValue(raw["checkDuplicate"], cfg.CheckDuplicate)
	cfg.ImageSaveMode = boolValue(raw["imageSaveMode"], cfg.ImageSaveMode)
	cfg.AutosavePics = boolValue(raw["autosavePics"], cfg.AutosavePics)
	cfg.ConsoleInfo = boolValue(raw["consoleinfo"], cfg.ConsoleInfo)
	cfg.CommandOfGetLink = boolValue(raw["command_of_get_link"], cfg.CommandOfGetLink)
	if v, ok := raw["autoRenameRules"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.AutoRenameRules = v
	}
	if arr, ok := raw["savePaths"].([]any); ok {
		cfg.SavePaths = parseSavePaths(arr)
	}
	if arr, ok := raw["groupListmapping"].([]any); ok {
		cfg.GroupMappings = parseMappings(arr)
	}
	return cfg
}

func parseSavePaths(arr []any) []savePath {
	out := make([]savePath, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		path, _ := m["path"].(string)
		out = append(out, savePath{Name: name, Path: path})
	}
	return out
}

func parseMappings(arr []any) []groupMapping {
	out := make([]groupMapping, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		mapping := groupMapping{
			Enable:          boolValue(m["enable"], false),
			GroupList:       stringValue(m["groupList"]),
			DefaultSavePath: stringValue(m["defaultsavepath"]),
			Count:           2,
		}
		switch v := m["count"].(type) {
		case float64:
			mapping.Count = int(v)
		case int:
			mapping.Count = v
		case json.Number:
			if n, err := v.Int64(); err == nil {
				mapping.Count = int(n)
			}
		}
		out = append(out, mapping)
	}
	return out
}

func boolValue(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---- 工具函数 ----

// commandRest 判断 text 是否以 name 作为完整命令开头，并返回其后的参数部分。
func commandRest(text, name string) (string, bool) {
	if name == "" || !strings.HasPrefix(text, name) {
		return "", false
	}
	rest := text[len(name):]
	if rest == "" {
		return "", true
	}
	r, _ := utf8DecodeRune(rest)
	if unicode.IsSpace(r) {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func utf8DecodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// splitArgs 按空白切分参数并处理单双引号与反斜杠转义。
func splitArgs(s string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func positionalAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func fetchImageHTTP(rawURL string) ([]byte, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("状态码 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	ext := extensionForType(resp.Header.Get("Content-Type"))
	if ext == "" {
		ext = extensionForType(http.DetectContentType(data))
	}
	if ext == "" {
		ext = ".png"
	}
	return data, ext, nil
}

func extensionForType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	}
	return ""
}

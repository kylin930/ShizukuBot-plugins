// Package status 提供机器人/计算机状态自检卡片。
// 对齐上游 NanoBot-Plugin status 插件的可见输出：
// 检查身体 / 自检 / 启动自检 / 系统状态 四个全匹配触发词，
// 展示 CPU、RAM、SWAP、磁盘占用、系统信息、运行时长与编译信息。
package status

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	pluginVersion = "1.0.0"
	cardWidth     = 1280
)

// 上游白天/黑夜两套进度色，按 >90 / >70 / 其余 三档取色。
var (
	lightColors = [3]string{"rgb(255,70,0)", "rgb(255,165,0)", "rgb(145,240,145)"}
	darkColors  = [3]string{"rgb(215,50,0)", "rgb(205,135,0)", "rgb(115,200,115)"}
)

// 触发词与上游 OnMessageFullMatchGroup 一致。
var triggers = map[string]bool{
	"检查身体": true,
	"自检":   true,
	"启动自检": true,
	"系统状态": true,
}

// 抽成包级变量，测试时替换采集与渲染，避免依赖真实系统和 Chrome。
var (
	collect    = collectInfo
	renderHTML = sdk.RenderHTML
	now        = time.Now
	bootTime   = time.Now()
)

// statusItem 对应上游 status 结构体：百分比、名称与若干行说明。
type statusItem struct {
	Percent float64
	Name    string
	Text    []string
}

// info 是一次自检采集到的原始数据。
type info struct {
	Day        bool
	Basic      []statusItem
	Disks      []statusItem
	More       []statusItem
	BotRuntime string
	BotStatus  string
}

type gaugeView struct {
	Name         string
	PercentLabel string
	Style        template.CSS
	Text         []string
}

type diskView struct {
	Name         string
	Text         string
	PercentLabel string
	BarStyle     template.CSS
}

type rowView struct {
	Name  string
	Value string
}

type cardView struct {
	Day        bool
	Avatar     string
	BotName    string
	BotRuntime string
	BotStatus  string
	Basic      []gaugeView
	Disks      []diskView
	More       []rowView
	Footer     string
}

// Definition 注册中文触发的事件插件。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "status",
		Name:        "系统状态",
		Version:     pluginVersion,
		Description: "查询机器人/计算机状态：检查身体 | 自检 | 启动自检 | 系统状态",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "status", Trigger: "系统状态"}})
}

// Handle 采集状态并渲染卡片图片。
func Handle(ctx context.Context, c sdk.Call) error {
	if !triggers[strings.TrimSpace(sdk.Text(c.Event))] {
		return nil
	}
	in, err := collect(c)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	botName := strings.TrimSpace(c.Event.BotID)
	if botName == "" {
		botName = "ShizukuBot"
	}
	page, err := buildCard(in, botName)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	img, err := renderHTML(ctx, page.Bytes(), sdk.RenderOptions{
		Width:    cardWidth,
		FullPage: true,
		Timeout:  30 * time.Second,
	})
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if err := c.ReplyImageBytes(ctx, img); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return nil
}

func collectInfo(c sdk.Call) (info, error) {
	var in info
	in.Day = isDay(now().Hour())

	basic, err := basicState()
	if err != nil {
		return in, err
	}
	in.Basic = basic

	disks, err := diskState()
	if err != nil {
		return in, err
	}
	in.Disks = disks

	more, hostInfo, err := moreInfo(len(c.Event.PluginIDs))
	if err != nil {
		return in, err
	}
	in.More = more
	in.BotRuntime = botRuntime(now(), bootTime, hostInfo.Uptime)
	in.BotStatus = botStatus(now(), hostInfo.OS)
	return in, nil
}

func isDay(hour int) bool { return hour > 7 && hour < 19 }

func basicState() ([]statusItem, error) {
	percent, err := cpu.Percent(time.Second, true)
	if err != nil {
		return nil, err
	}
	cpuinfo, err := cpu.Info()
	if err != nil {
		return nil, err
	}
	cpucore, err := cpu.Counts(false)
	if err != nil {
		return nil, err
	}
	cputhread, err := cpu.Counts(true)
	if err != nil {
		return nil, err
	}

	cores := strconv.Itoa(cpucore) + "C" + strconv.Itoa(cputhread) + "T"
	times := "最大 未知"
	if len(cpuinfo) > 0 {
		times = "最大 " + strconv.FormatFloat(cpuinfo[0].Mhz/1000, 'f', 1, 64) + "Ghz"
	}
	cpuPercent := 0.0
	if len(percent) > 0 {
		cpuPercent = percent[0]
	}

	raminfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	swapinfo, err := mem.SwapMemory()
	if err != nil {
		return nil, err
	}

	return []statusItem{
		{Percent: math.Round(cpuPercent), Name: "CPU", Text: []string{cores, times}},
		{Percent: math.Round(raminfo.UsedPercent), Name: "RAM", Text: []string{
			"总共 " + storagefmt(float64(raminfo.Total)),
			"已用 " + storagefmt(float64(raminfo.Used)),
			"剩余 " + storagefmt(float64(raminfo.Free)),
		}},
		{Percent: math.Round(swapinfo.UsedPercent), Name: "SWAP", Text: []string{
			"总共 " + storagefmt(float64(swapinfo.Total)),
			"已用 " + storagefmt(float64(swapinfo.Used)),
			"剩余 " + storagefmt(float64(swapinfo.Free)),
		}},
	}, nil
}

func diskState() ([]statusItem, error) {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil, err
	}
	items := make([]statusItem, 0, len(parts))
	for _, v := range parts {
		mp := v.Mountpoint
		if strings.HasPrefix(mp, "/snap/") || strings.HasPrefix(mp, "/apex/") {
			continue
		}
		usage, err := disk.Usage(mp)
		if err != nil {
			continue
		}
		items = append(items, statusItem{
			Percent: math.Round(usage.UsedPercent),
			Name:    mp,
			Text:    []string{storagefmt(float64(usage.Used)) + " / " + storagefmt(float64(usage.Total))},
		})
	}
	return items, nil
}

func moreInfo(pluginCount int) ([]statusItem, *host.InfoStat, error) {
	var mems runtime.MemStats
	runtime.ReadMemStats(&mems)

	hostinfo, err := host.Info()
	if err != nil {
		return nil, nil, err
	}
	cpuinfo, err := cpu.Info()
	if err != nil {
		return nil, nil, err
	}
	model := "未知"
	if len(cpuinfo) > 0 {
		model = strings.TrimSpace(cpuinfo[0].ModelName)
	}

	return []statusItem{
		{Name: "OS", Text: []string{hostinfo.Platform}},
		{Name: "CPU", Text: []string{model}},
		{Name: "Version", Text: []string{hostinfo.PlatformVersion}},
		{Name: "Plugin", Text: []string{"共 " + strconv.Itoa(pluginCount) + " 个"}},
		{Name: "Memory", Text: []string{"已用 " + storagefmt(float64(mems.Sys))}},
	}, hostinfo, nil
}

// storagefmt 逐级除以 1024，与上游实现完全一致。
func storagefmt(num float64) string {
	if num /= 1024; num < 1 {
		return strconv.FormatFloat(num*1024, 'f', 2, 64) + "B"
	}
	if num /= 1024; num < 1 {
		return strconv.FormatFloat(num*1024, 'f', 2, 64) + "KB"
	}
	if num /= 1024; num < 1 {
		return strconv.FormatFloat(num*1024, 'f', 2, 64) + "MB"
	}
	if num /= 1024; num < 1 {
		return strconv.FormatFloat(num*1024, 'f', 2, 64) + "GB"
	}
	return strconv.FormatFloat(num, 'f', 2, 64) + "TB"
}

func botRuntime(nowTime, boot time.Time, uptime uint64) string {
	var b strings.Builder
	b.WriteString("ShizukuBot 已运行 ")
	b.WriteString(strconv.FormatInt((nowTime.Unix()-boot.Unix())/86400, 10))
	b.WriteString(" 天 ")
	b.WriteString(time.Unix(nowTime.Unix()-boot.Unix(), 0).UTC().Format("15:04:05"))
	b.WriteString(" | 系统运行 ")
	b.WriteString(strconv.FormatInt(int64(uptime)/86400, 10))
	b.WriteString(" 天 ")
	b.WriteString(time.Unix(int64(uptime), 0).UTC().Format("15:04:05"))
	return b.String()
}

func botStatus(nowTime time.Time, osName string) string {
	return nowTime.Format("2006-01-02 15:04:05") +
		" | Compiled by " + runtime.Version() +
		" | " + cases.Title(language.English).String(osName) +
		" " + runtime.GOARCH
}

func colorFor(percent float64, day bool) string {
	colors := darkColors
	if day {
		colors = lightColors
	}
	switch {
	case percent > 90:
		return colors[0]
	case percent > 70:
		return colors[1]
	default:
		return colors[2]
	}
}

func percentText(percent float64) string {
	return strconv.FormatFloat(percent, 'f', 0, 64)
}

func buildCard(in info, botName string) (*bytes.Buffer, error) {
	avatar := "#"
	if r, _ := utf8.DecodeRuneInString(botName); r != utf8.RuneError {
		avatar = strings.ToUpper(string(r))
	}

	view := cardView{
		Day:        in.Day,
		Avatar:     avatar,
		BotName:    botName,
		BotRuntime: in.BotRuntime,
		BotStatus:  in.BotStatus,
		Footer:     "Created By ShizukuBot-Plugin " + pluginVersion,
	}
	for _, v := range in.Basic {
		p := percentText(v.Percent)
		view.Basic = append(view.Basic, gaugeView{
			Name:         v.Name,
			PercentLabel: p + "%",
			Style:        template.CSS("--p:" + p + ";--c:" + colorFor(v.Percent, in.Day)),
			Text:         v.Text,
		})
	}
	for _, v := range in.Disks {
		p := percentText(v.Percent)
		text := ""
		if len(v.Text) > 0 {
			text = v.Text[0]
		}
		view.Disks = append(view.Disks, diskView{
			Name:         v.Name,
			Text:         text,
			PercentLabel: p + "%",
			BarStyle:     template.CSS(fmt.Sprintf("width:%s%%;background:%s", p, colorFor(v.Percent, in.Day))),
		})
	}
	for _, v := range in.More {
		value := ""
		if len(v.Text) > 0 {
			value = v.Text[0]
		}
		view.More = append(view.More, rowView{Name: v.Name, Value: value})
	}

	var buf bytes.Buffer
	if err := cardTemplate.Execute(&buf, view); err != nil {
		return nil, err
	}
	return &buf, nil
}

var cardTemplate = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; width: 1280px; }
  body {
    font-family: "Microsoft YaHei", "PingFang SC", "Noto Sans CJK SC", sans-serif;
    color: #ebebeb;
    background: radial-gradient(circle at 20% 0%, #2c3e50 0%, #1a1a2e 55%, #0f0f1a 100%);
    padding: 70px;
  }
  body.day { color: #1e1e1e; background: radial-gradient(circle at 20% 0%, #fdfbfb 0%, #ebedee 60%, #d7e1ec 100%); }
  .card {
    position: relative; border-radius: 16px; border: 3px solid rgba(0,0,0,0.4);
    background: rgba(0,0,0,0.28); margin-bottom: 40px; padding: 40px;
  }
  body.day .card { border-color: rgba(255,255,255,0.55); background: rgba(255,255,255,0.38); }
  .title { display: flex; align-items: center; height: 250px; }
  .title .avatar {
    width: 200px; height: 200px; border-radius: 50%; flex: none;
    background: rgba(255,255,255,0.18); display: flex; align-items: center; justify-content: center;
    font-size: 90px; font-weight: 700;
  }
  body.day .title .avatar { background: rgba(0,0,0,0.08); }
  .title .tinfo { flex: 1; margin-left: 40px; }
  .title .name { font-size: 72px; font-weight: 700; }
  .title .line { height: 2px; background: rgba(255,255,255,0.5); margin: 26px 0; }
  body.day .title .line { background: rgba(0,0,0,0.35); }
  .title .meta { font-size: 24px; margin: 8px 0; }
  .basic { display: flex; justify-content: space-around; align-items: flex-start; height: 380px; padding-top: 36px; }
  .gauge { width: 200px; text-align: center; }
  .donut {
    width: 200px; height: 200px; border-radius: 50%; margin: 0 auto;
    display: flex; align-items: center; justify-content: center;
    background: conic-gradient(var(--c) calc(var(--p) * 1%), rgba(0,0,0,0.35) 0);
  }
  body.day .donut { background: conic-gradient(var(--c) calc(var(--p) * 1%), rgba(0,0,0,0.12) 0); }
  .hole {
    width: 160px; height: 160px; border-radius: 50%; background: #262626;
    display: flex; align-items: center; justify-content: center; font-size: 42px; color: #d5d5d5;
  }
  body.day .hole { background: #ebebeb; color: #333333; }
  .gname { font-size: 32px; font-weight: 600; margin-top: 20px; }
  .gtext { font-size: 20px; margin-top: 8px; }
  .diskrow { position: relative; height: 50px; border-radius: 12px; background: #393939; margin-bottom: 20px; overflow: hidden; }
  body.day .diskrow { background: #c0c0c0; }
  .diskbar { position: absolute; left: 0; top: 0; height: 100%; border-radius: 12px; }
  .diskmeta { position: absolute; inset: 0; display: flex; align-items: center; padding: 0 12px; font-size: 24px; }
  .diskmeta .dname { flex: 1; text-align: left; }
  .diskmeta .dtext { flex: none; margin: 0 16px; }
  .diskmeta .dpct { width: 90px; text-align: center; }
  .more .row { display: flex; justify-content: space-between; font-size: 26px; padding: 14px 0; }
  .footer { text-align: center; font-size: 28px; color: #ffffff; text-shadow: 2px 2px 3px #000000; }
</style>
</head>
<body class="{{if .Day}}day{{else}}night{{end}}">
  <div class="card title">
    <div class="avatar">{{.Avatar}}</div>
    <div class="tinfo">
      <div class="name">{{.BotName}}</div>
      <div class="line"></div>
      <div class="meta">{{.BotRuntime}}</div>
      <div class="meta">{{.BotStatus}}</div>
    </div>
  </div>
  <div class="card basic">
    {{range .Basic}}
    <div class="gauge">
      <div class="donut" style="{{.Style}}"><div class="hole">{{.PercentLabel}}</div></div>
      <div class="gname">{{.Name}}</div>
      {{range .Text}}<div class="gtext">{{.}}</div>{{end}}
    </div>
    {{end}}
  </div>
  <div class="card disks">
    {{range .Disks}}
    <div class="diskrow"><div class="diskbar" style="{{.BarStyle}}"></div><div class="diskmeta"><span class="dname">{{.Name}}</span><span class="dtext">{{.Text}}</span><span class="dpct">{{.PercentLabel}}</span></div></div>
    {{end}}
  </div>
  <div class="card more">
    {{range .More}}<div class="row"><span>{{.Name}}</span><span>{{.Value}}</span></div>{{end}}
  </div>
  <div class="footer">{{.Footer}}</div>
</body>
</html>
`))

package score

import (
	"html"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// cardInfo 描述签到卡片上除用户数据外的动态文案。
type cardInfo struct {
	Title  string
	Reward string
	Hint   string
}

type signCardView struct {
	Preset   int
	Theme    string
	Avatar   template.URL
	Nickname string
	UID      string
	Title    string
	Hint     string
	Reward   string
	Time     string
	CoinName string
	Level    int
	Exp      int
	Next     int
	Coins    int
	BarStyle template.CSS
}

type rankRowView struct {
	Rank     int
	Name     string
	UID      string
	Avatar   template.URL
	Value    int
	Medal    string
	BarStyle template.CSS
}

type rankView struct {
	Title string
	Unit  string
	Date  string
	Rows  []rankRowView
}

var signCardTmpl = template.Must(template.New("sign-card").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
:root{--accent:#5b8cff;--ink:#1b2537;--muted:#6b7280;--card:rgba(255,255,255,.9)}
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:640px}
body{min-height:360px;padding:26px;font-family:"Microsoft YaHei","PingFang SC",sans-serif;color:var(--ink);background:linear-gradient(135deg,#667eea,#764ba2)}
body.bg-aurora{background:linear-gradient(135deg,#3a7bd5,#00d2ff 55%,#4facfe)}
body.bg-sunset{background:linear-gradient(135deg,#ff7e5f,#feb47b)}
body.bg-ocean{background:linear-gradient(135deg,#0f2027,#203a43,#2c5364)}
body.bg-sakura{background:linear-gradient(135deg,#ffdde1,#ee9ca7)}
body.bg-midnight{background:linear-gradient(135deg,#141e30,#243b55)}
.card{position:relative;display:flex;gap:26px;align-items:center;padding:30px;border-radius:26px;background:var(--card);box-shadow:0 22px 60px rgba(15,23,42,.28);overflow:hidden}
.card::after{content:"";position:absolute;right:-80px;top:-80px;width:200px;height:200px;border-radius:50%;background:var(--accent);opacity:.12}
.avatar{width:112px;height:112px;border-radius:50%;border:4px solid #fff;box-shadow:0 10px 26px rgba(0,0,0,.22);object-fit:cover;flex:none;background:#e5e7eb}
.info{flex:1;min-width:0;position:relative;z-index:1}
.name{font-size:30px;font-weight:800;line-height:1.2;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.title{margin-top:4px;font-size:18px;font-weight:700;color:var(--accent)}
.hint{margin-top:6px;font-size:15px;color:var(--muted);line-height:1.5}
.stats{display:flex;flex-wrap:wrap;gap:8px;margin-top:14px}
.chip{font-size:14px;padding:6px 12px;border-radius:999px;background:#eef2ff;color:#3949ab;font-weight:600}
.reward{margin-top:12px;font-size:22px;font-weight:800;color:#e8590c}
.exp{margin-top:16px}
.exp-head{display:flex;justify-content:space-between;gap:10px;font-size:13px;color:var(--muted);margin-bottom:6px}
.bar{height:12px;border-radius:999px;background:rgba(15,23,42,.12);overflow:hidden}
.bar>i{display:block;height:100%;border-radius:999px;background:linear-gradient(90deg,#4facfe,#00f2fe)}
.foot{margin-top:16px;display:flex;justify-content:space-between;gap:10px;font-size:12px;color:var(--muted)}
body.p1{--accent:#0284c7;--card:rgba(255,255,255,.58)}
body.p1 .card{flex-direction:column;text-align:center;align-items:center;border:2px solid rgba(255,255,255,.75);backdrop-filter:blur(14px)}
body.p1 .info{display:flex;flex-direction:column;align-items:center;width:100%}
body.p1 .stats,body.p1 .exp,body.p1 .exp-head{width:100%}
body.p2{--accent:#15803d;--card:rgba(255,255,255,.97);--ink:#14532d;--muted:#166534}
body.p2 .card{flex-direction:column;align-items:stretch;border-radius:14px}
body.p2 .avatar{width:80px;height:80px}
body.p2 .reward{color:#b45309}
body.p3{--accent:#f59e0b;--card:rgba(17,24,39,.92);--ink:#f9fafb;--muted:#9ca3af}
body.p3 .card{flex-direction:row-reverse;text-align:right}
body.p3 .stats{justify-content:flex-end}
body.p3 .chip{background:rgba(245,158,11,.18);color:#fbbf24}
body.p3 .reward{color:#fbbf24}
body.p3 .bar{background:rgba(255,255,255,.16)}
</style>
</head>
<body class="p{{.Preset}} bg-{{.Theme}}">
  <div class="card">
    <img class="avatar" src="{{.Avatar}}" alt="avatar">
    <div class="info">
      <div class="name">{{.Nickname}}</div>
      <div class="title">{{.Title}}</div>
      <div class="hint">{{.Hint}}</div>
      <div class="stats">
        <span class="chip">等级 {{.Level}}</span>
        <span class="chip">经验 {{.Exp}}/{{.Next}}</span>
        <span class="chip">{{.CoinName}} {{.Coins}}</span>
      </div>
      <div class="reward">{{.Reward}}</div>
      <div class="exp">
        <div class="exp-head"><span>LEVEL {{.Level}}</span><span>今日签到</span><span>{{.Exp}} / {{.Next}}</span></div>
        <div class="bar"><i style="{{.BarStyle}}"></i></div>
      </div>
      <div class="foot"><span>QQ {{.UID}}</span><span>{{.Time}}</span></div>
    </div>
  </div>
</body>
</html>`))

var rankTmpl = template.Must(template.New("rank").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:720px}
body{min-height:420px;padding:24px;font-family:"Microsoft YaHei","PingFang SC",sans-serif;background:linear-gradient(135deg,#0f172a,#1e293b);color:#e5e7eb}
.panel{background:rgba(255,255,255,.06);border:1px solid rgba(255,255,255,.12);border-radius:22px;padding:24px 26px;box-shadow:0 24px 60px rgba(0,0,0,.35)}
.head{display:flex;align-items:flex-end;justify-content:space-between;margin-bottom:18px}
h1{font-size:26px;letter-spacing:1px}
.sub{font-size:13px;color:#94a3b8}
.row{display:flex;align-items:center;gap:14px;padding:9px 0}
.rank{width:34px;text-align:center;font-size:18px;font-weight:800;color:#94a3b8;font-variant-numeric:tabular-nums}
.rank.top1{color:#fbbf24}
.rank.top2{color:#e5e7eb}
.rank.top3{color:#fb923c}
.avatar{width:42px;height:42px;border-radius:50%;border:2px solid rgba(255,255,255,.35);object-fit:cover;background:#334155;flex:none}
.meta{flex:1;min-width:0}
.line{display:flex;align-items:baseline;justify-content:space-between;gap:12px}
.nm{font-size:16px;font-weight:700;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.uid{font-size:12px;color:#94a3b8;margin-left:6px;font-weight:400}
.val{font-size:18px;font-weight:800;color:#fbbf24;font-variant-numeric:tabular-nums;white-space:nowrap}
.bar{height:7px;border-radius:999px;background:rgba(255,255,255,.12);margin-top:7px;overflow:hidden}
.bar>i{display:block;height:100%;border-radius:999px;background:linear-gradient(90deg,#22d3ee,#818cf8)}
.foot{margin-top:18px;font-size:12px;color:#64748b;text-align:right}
</style>
</head>
<body>
<div class="panel">
  <div class="head"><h1>{{.Title}}</h1><span class="sub">{{.Date}} · TOP {{len .Rows}}</span></div>
  {{range .Rows}}
  <div class="row">
    <div class="rank {{.Medal}}">{{.Rank}}</div>
    <img class="avatar" src="{{.Avatar}}" alt="avatar">
    <div class="meta">
      <div class="line"><span class="nm">{{.Name}}<span class="uid">QQ {{.UID}}</span></span><span class="val">{{.Value}} {{$.Unit}}</span></div>
      <div class="bar"><i style="{{.BarStyle}}"></i></div>
    </div>
  </div>
  {{end}}
  <div class="foot">Powered by ShizukuBot Score</div>
</div>
</body>
</html>`))

type backgroundView struct {
	Theme     string
	ThemeName string
	Name      string
	Date      string
}

var backgroundTmpl = template.Must(template.New("background").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:640px;height:420px}
body{position:relative;font-family:"Microsoft YaHei","PingFang SC",sans-serif;color:#fff;background:linear-gradient(135deg,#667eea,#764ba2);overflow:hidden}
body.bg-aurora{background:linear-gradient(135deg,#3a7bd5,#00d2ff 55%,#4facfe)}
body.bg-sunset{background:linear-gradient(135deg,#ff7e5f,#feb47b)}
body.bg-ocean{background:linear-gradient(135deg,#0f2027,#203a43,#2c5364)}
body.bg-sakura{background:linear-gradient(135deg,#ffdde1,#ee9ca7)}
body.bg-midnight{background:linear-gradient(135deg,#141e30,#243b55)}
.glow{position:absolute;width:360px;height:360px;border-radius:50%;background:rgba(255,255,255,.22);filter:blur(30px)}
.glow.a{right:-110px;top:-120px}
.glow.b{left:-130px;bottom:-160px;background:rgba(255,255,255,.14)}
.panel{position:absolute;left:44px;right:44px;bottom:44px;padding:26px 30px;border-radius:22px;background:rgba(15,23,42,.28);backdrop-filter:blur(8px)}
.label{font-size:14px;letter-spacing:3px;opacity:.85}
.name{margin-top:10px;font-size:34px;font-weight:800;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.date{margin-top:8px;font-size:16px;opacity:.9}
</style>
</head>
<body class="bg-{{.Theme}}">
<div class="glow a"></div><div class="glow b"></div>
<div class="panel">
  <div class="label">今日签到背景 · {{.ThemeName}}</div>
  <div class="name">{{.Name}}</div>
  <div class="date">{{.Date}}</div>
</div>
</body>
</html>`))

// backgroundHTML 渲染“获得签到背景”返回的背景图。
func backgroundHTML(theme, name, day string) []byte {
	if !validTheme(theme) {
		theme = backgroundThemes[0]
	}
	view := backgroundView{
		Theme:     theme,
		ThemeName: backgroundNames[theme],
		Name:      name,
		Date:      day,
	}
	var b strings.Builder
	if err := backgroundTmpl.Execute(&b, view); err != nil {
		return []byte(`<html><body><h1>` + html.EscapeString(name) + `</h1></body></html>`)
	}
	return []byte(b.String())
}

func signCardHTML(u userState, info cardInfo, preset int, bg string, now time.Time, coin string) []byte {
	preset = normalizePreset(preset)
	if !validTheme(bg) {
		bg = backgroundThemes[0]
	}
	next := nextRankScore(getrank(u.Exp))
	view := signCardView{
		Preset:   preset,
		Theme:    bg,
		Avatar:   avatarURL(u.UID),
		Nickname: displayName(u),
		UID:      u.UID,
		Title:    info.Title,
		Hint:     info.Hint,
		Reward:   info.Reward,
		Time:     now.Format("2006-01-02 15:04"),
		CoinName: coin,
		Level:    getrank(u.Exp),
		Exp:      u.Exp,
		Next:     next,
		Coins:    u.Coins,
		BarStyle: percentStyle(u.Exp, next),
	}
	var b strings.Builder
	if err := signCardTmpl.Execute(&b, view); err != nil {
		return []byte(`<html><body><h1>` + html.EscapeString(info.Title) + `</h1><p>` +
			html.EscapeString(displayName(u)) + `</p></body></html>`)
	}
	return []byte(b.String())
}

func rankHTML(title, unit string, entries []rankEntry, now time.Time) []byte {
	if len(entries) == 0 {
		return []byte(`<html><body><h1>` + html.EscapeString(title) + `</h1></body></html>`)
	}
	max := entries[0].Value
	if max <= 0 {
		max = 1
	}
	rows := make([]rankRowView, 0, len(entries))
	for i, e := range entries {
		medal := ""
		switch i {
		case 0:
			medal = "top1"
		case 1:
			medal = "top2"
		case 2:
			medal = "top3"
		}
		rows = append(rows, rankRowView{
			Rank:     i + 1,
			Name:     e.Name,
			UID:      e.UID,
			Avatar:   avatarURL(e.UID),
			Value:    e.Value,
			Medal:    medal,
			BarStyle: percentStyle(e.Value, max),
		})
	}
	view := rankView{
		Title: title,
		Unit:  unit,
		Date:  now.Format("2006-01-02"),
		Rows:  rows,
	}
	var b strings.Builder
	if err := rankTmpl.Execute(&b, view); err != nil {
		return []byte(`<html><body><h1>` + html.EscapeString(title) + `</h1></body></html>`)
	}
	return []byte(b.String())
}

func avatarURL(uid string) template.URL {
	return template.URL("https://q1.qlogo.cn/g?b=qq&nk=" + url.QueryEscape(uid) + "&s=640")
}

func percentStyle(value, max int) template.CSS {
	pct := 0
	if max > 0 {
		pct = value * 100 / max
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if pct > 0 && pct < 6 {
		pct = 6
	}
	return template.CSS("width:" + strconv.Itoa(pct) + "%")
}

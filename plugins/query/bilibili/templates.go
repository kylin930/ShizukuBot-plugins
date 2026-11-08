package bilibili

import (
	"bytes"
	"html/template"
)

const cardBaseCSS = `
*{box-sizing:border-box;margin:0;padding:0}
body{background:#f4f6fb;color:#1f2430;font-family:"Microsoft YaHei","PingFang SC",sans-serif;-webkit-font-smoothing:antialiased}
.card{width:100%;min-height:100vh;padding:28px 32px 32px}
.header{display:flex;align-items:center;gap:24px;padding:24px;border-radius:18px;background:linear-gradient(135deg,#ffffff 0%,#eef4ff 100%);box-shadow:0 10px 30px rgba(48,80,160,.10)}
.avatar{width:116px;height:116px;border-radius:50%;object-fit:cover;background:#dfe7f5;border:4px solid #fff;box-shadow:0 6px 16px rgba(0,0,0,.12);flex:none}
.title{font-size:34px;font-weight:700;line-height:1.2}
.pill{display:inline-block;margin-left:10px;padding:3px 12px;border-radius:999px;background:#e4e7ed;color:#59616f;font-size:17px;font-weight:400;vertical-align:middle}
.meta{display:flex;gap:26px;margin-top:12px;font-size:19px;color:#4a5365;flex-wrap:wrap}
.meta b{color:#1f2430}
.stats{display:flex;gap:16px;margin-top:20px}
.stat{flex:1;padding:16px 18px;border-radius:14px;background:#fff;box-shadow:0 4px 14px rgba(48,80,160,.07)}
.stat .label{font-size:15px;color:#7b8598}
.stat .value{margin-top:6px;font-size:22px;font-weight:700}
.accent{color:#2f7cf6}
.notice{margin-top:16px;padding:12px 16px;border-radius:10px;background:#fff7e8;color:#8a5b00;font-size:16px;line-height:1.6}
.section{margin-top:24px;padding:20px 22px;border-radius:16px;background:#fff;box-shadow:0 4px 14px rgba(48,80,160,.06)}
.section h2{font-size:20px;margin-bottom:14px;color:#2a3140}
.row{display:flex;align-items:center;gap:12px;padding:11px 6px;border-bottom:1px solid #eef1f6;font-size:18px}
.row:last-child{border-bottom:none}
.row .name{font-weight:600;max-width:360px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.row .uid{color:#7b8598;font-size:15px}
.row .spacer{flex:1}
.tag{display:inline-block;padding:2px 8px;border-radius:6px;background:#eaf2ff;color:#2f7cf6;font-size:14px;font-weight:600}
.medal{display:inline-flex;align-items:center;overflow:hidden;border-radius:6px;font-size:15px;color:#fff;line-height:24px;background:linear-gradient(90deg,var(--ms),var(--me));border:1px solid var(--mb)}
.medal img{width:22px;height:22px;object-fit:cover;margin-right:4px}
.medal .mname{padding:0 6px}
.medal .mlevel{padding:0 6px;color:var(--mb);background:#fff;font-weight:700}
.footer{margin-top:18px;text-align:right;color:#98a1b3;font-size:14px}
`

const componentCSS = cardBaseCSS + `
.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-top:20px}
.percent{font-size:30px}
`

var componentTemplate = template.Must(template.New("component").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>B站成分</title>
<style>` + componentCSS + `</style></head>
<body><div class="card">
  <div class="header">
    {{if .Face}}<img class="avatar" src="{{.Face}}" alt="avatar">{{else}}<div class="avatar"></div>{{end}}
    <div style="flex:1">
      <div class="title">{{.Name}}<span class="pill">UID {{.UID}}</span></div>
      <div class="meta">
        <span>粉丝 <b>{{.Fans}}</b></span>
        <span>关注 <b>{{.Attention}}</b></span>
        {{if .Level}}<span>等级 <b>{{.Level}}</b></span>{{end}}
      </div>
    </div>
  </div>
  <div class="grid">
    <div class="stat"><div class="label">管人痴成分</div><div class="value percent accent">{{.Percent}}</div></div>
    <div class="stat"><div class="label">关注的 VUP</div><div class="value">{{.Hit}} / {{.Total}}</div></div>
    <div class="stat"><div class="label">粉丝牌</div><div class="value">{{.MedalCount}} 块</div></div>
  </div>
  <div class="stats">
    <div class="stat"><div class="label">注册时间</div><div class="value" style="font-size:19px">{{.RegTime}}</div></div>
    <div class="stat"><div class="label">查询时间</div><div class="value" style="font-size:19px">{{.QueryTime}}</div></div>
  </div>
  {{range .Notices}}<div class="notice">{{.}}</div>{{end}}
  <div class="section">
    <h2>成分明细 <span class="uid">（粉丝牌优先，其次为关注的 VUP）</span></h2>
    {{if .Rows}}
      {{range .Rows}}
      <div class="row">
        <span class="name">{{.Uname}}</span>
        <span class="uid">UID {{.UID}}</span>
        {{if .VUP}}<span class="tag">VUP</span>{{end}}
        <span class="spacer"></span>
        {{if .MedalName}}
        <span class="medal" style="--ms:{{.MedalStart}};--me:{{.MedalEnd}};--mb:{{.MedalBorder}}">
          {{if .MedalIcon}}<img src="{{.MedalIcon}}" alt="">{{end}}
          <span class="mname">{{.MedalName}}</span><span class="mlevel">{{.MedalLevel}}</span>
        </span>
        {{end}}
      </div>
      {{end}}
    {{else}}
      <div class="row"><span class="uid">暂无可展示的关注 VUP 或粉丝牌</span></div>
    {{end}}
    {{if .Truncated}}<div class="footer">仅展示前 {{len .Rows}} 条</div>{{end}}
  </div>
  <div class="footer">{{.SpaceURL}}</div>
</div></body></html>`))

// buildComponentHTML 渲染成分卡片 HTML。
func buildComponentHTML(view componentView) ([]byte, error) {
	var buf bytes.Buffer
	if err := componentTemplate.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const danmakuCSS = cardBaseCSS + `
.live{margin-top:22px;padding:20px 22px;border-radius:16px;background:#fff;box-shadow:0 4px 14px rgba(48,80,160,.06)}
.live-head{display:flex;gap:16px;align-items:center}
.live-face{width:76px;height:76px;border-radius:12px;object-fit:cover;background:#dfe7f5;flex:none}
.live-title{font-size:22px;font-weight:700;color:#2f7cf6}
.live-anchor{font-size:16px;color:#5b6577;margin-top:4px}
.live-meta{display:flex;gap:22px;flex-wrap:wrap;margin-top:14px;font-size:16px;color:#4a5365}
.live-meta b{color:#1f2430}
.live-meta .red{color:#e5484d;font-weight:700}
.living{color:#0a8f3c;font-weight:700}
.danmakus{margin-top:14px;border-top:1px dashed #e3e8f0;padding-top:10px}
.dm{display:flex;gap:10px;padding:5px 2px;font-size:16px;line-height:1.55;border-bottom:1px solid #f5f7fa}
.dm:last-child{border-bottom:none}
.dm .time{color:#98a1b3;flex:none;width:74px}
.dm .user{color:#2f7cf6;font-weight:600;flex:none;max-width:150px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dm .msg{color:#2a3140;word-break:break-all}
.dm.gift .msg,.dm.guard .msg,.dm.sc .msg{color:#e5484d;font-weight:600}
.dm.guard .msg{color:#8e24aa}
.dm.sc .msg{color:#0055ff}
.dm.notice .msg{color:#8a5b00}
.empty{padding:14px;color:#98a1b3;font-size:16px}
`

var danmakuTemplate = template.Must(template.New("danmaku").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>B站查弹幕</title>
<style>` + danmakuCSS + `</style></head>
<body><div class="card">
  <div class="header">
    {{if .Face}}<img class="avatar" src="{{.Face}}" alt="avatar">{{else}}<div class="avatar"></div>{{end}}
    <div style="flex:1">
      <div class="title">{{.Name}}<span class="pill">UID {{.UID}}</span></div>
      <div class="meta">
        <span>粉丝 <b>{{.Fans}}</b></span>
        <span>关注 <b>{{.Attention}}</b></span>
        <span>页码 <b>{{.Page}}</b></span>
      </div>
    </div>
  </div>
  {{range .Notices}}<div class="notice">{{.}}</div>{{end}}
  {{range .Lives}}
  <div class="live">
    <div class="live-head">
      {{if .Face}}<img class="live-face" src="{{.Face}}" alt="live">{{else}}<div class="live-face"></div>{{end}}
      <div>
        <div class="live-title">标题：{{.Title}}</div>
        <div class="live-anchor">主播：{{.Anchor}}{{if .RoomID}} ｜ 房间号 {{.RoomID}}{{end}}</div>
      </div>
    </div>
    <div class="live-meta">
      <span>开始时间：<b>{{.Start}}</b></span>
      <span>结束时间：{{if .Living}}<span class="living">正在直播</span>{{else}}<b>{{.End}}</b>{{end}}</span>
      <span>直播时长：<b>{{.Duration}}</b></span>
      <span>弹幕数量：<b>{{.DanmakuNum}}</b></span>
      <span>观看次数：<b>{{.WatchNum}}</b></span>
      <span>收益：<span class="red">{{.Income}}</span></span>
    </div>
    <div class="danmakus">
      {{if .Items}}
        {{range .Items}}
        <div class="dm {{.Type}}"><span class="time">{{.Clock}}</span><span class="user">{{.UName}}</span><span class="msg">{{.Message}}{{if .Price}} {{.Price}}{{end}}</span></div>
        {{end}}
        {{if .HiddenCount}}<div class="empty">（还有 {{.HiddenCount}} 条弹幕未展示）</div>{{end}}
      {{else}}
        <div class="empty">（本场没有记录到弹幕）</div>
      {{end}}
    </div>
  </div>
  {{end}}
  <div class="footer">{{.Link}} ｜ 查询时间 {{.QueryTime}}</div>
</div></body></html>`))

// buildDanmakuHTML 渲染弹幕卡片 HTML。
func buildDanmakuHTML(view danmakuView) ([]byte, error) {
	var buf bytes.Buffer
	if err := danmakuTemplate.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

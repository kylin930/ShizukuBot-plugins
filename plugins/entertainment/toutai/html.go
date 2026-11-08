package toutai

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var numericValue = regexp.MustCompile(`^[\d.,]+%?$`)

func esc(value string) string {
	return strings.NewReplacer(
		"<", "＜",
		">", "＞",
		"&", "＆",
		`"`, "＂",
		"'", "＇",
	).Replace(value)
}

// sealMarkup 生成两字竖排朱印。
func sealMarkup(text string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 2 {
		runes = runes[:2]
	}
	var b strings.Builder
	b.WriteString(`<div class="seal" aria-hidden="true">`)
	for _, r := range runes {
		fmt.Fprintf(&b, "<span>%c</span>", r)
	}
	b.WriteString(`</div>`)
	return b.String()
}

type pageOptions struct {
	docTitle      string
	title         string
	eyebrow       string
	subtitle      string
	tags          string
	seal          string
	body          string
	colophonLeft  string
	colophonRight string
	style         string
}

// buildPage 生成统一的纸张、题头与版记。
func buildPage(o pageOptions) string {
	eyebrow := o.eyebrow
	if eyebrow == "" {
		eyebrow = "投胎模拟器"
	}
	left := o.colophonLeft
	if left == "" {
		left = "轮回簿"
	}
	var subtitle, tags string
	if o.subtitle != "" {
		subtitle = `<p class="subtitle">` + o.subtitle + `</p>`
	}
	if o.tags != "" {
		tags = `<div class="tags">` + o.tags + `</div>`
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
<style>%s%s</style>
</head>
<body>
<div class="sheet">
    <header class="masthead">
        <div class="text">
            <p class="eyebrow">%s</p>
            <h1 class="title">%s</h1>
            %s
            %s
        </div>
        %s
    </header>
    %s
    <footer class="colophon">
        <span class="l">%s</span>
        <span>%s</span>
    </footer>
</div>
</body>
</html>`,
		o.docTitle, baseCSS(), o.style, eyebrow, o.title, subtitle, tags,
		sealMarkup(o.seal), o.body, left, o.colophonRight)
}

func subtitleOf(username, rest string) string {
	return `命主 <b>` + esc(username) + `</b><span class="sep">·</span>` + rest
}

func section(title, body string, aside ...string) string {
	asideHTML := ""
	if len(aside) > 0 && aside[0] != "" {
		asideHTML = `<span class="aside">` + aside[0] + `</span>`
	}
	return `<section class="section">
    <div class="sec-hd">
        <h2>` + title + `</h2>
        ` + asideHTML + `
    </div>
    ` + body + `
</section>`
}

// track 生成 0～1 的比例条。
func track(ratio float64) string {
	r := ratio
	if math.IsNaN(r) || math.IsInf(r, 0) {
		r = 0
	}
	r = math.Min(1, math.Max(0, r))
	fill := ""
	rest := ""
	if r > 0 {
		fill = fmt.Sprintf(`<i class="fill" style="flex: %.4f 1 0"></i>`, r)
	}
	if r < 1 {
		rest = fmt.Sprintf(`<i class="rest" style="flex: %.4f 1 0"></i>`, 1-r)
	}
	return `<span class="track">` + fill + rest + `</span>`
}

type statOptions struct {
	label string
	value string
	unit  string
	note  string
	tone  string
	ratio *float64
	hero  bool
}

func statCard(o statOptions) string {
	tone := o.tone
	if tone == "" {
		tone = "ink"
	}
	class := "stat t-" + tone
	if o.hero {
		class += " hero"
	}
	valueClass := "v"
	if !numericValue.MatchString(o.value) {
		valueClass += " text"
	}
	unit := ""
	if o.unit != "" {
		unit = "<small>" + o.unit + "</small>"
	}
	note := ""
	if o.note != "" {
		note = `<span class="note">` + o.note + `</span>`
	}
	meter := ""
	if o.ratio != nil {
		meter = `<div class="meter">` + track(*o.ratio) + `</div>`
	}
	return `<div class="` + class + `">
    <span class="k">` + o.label + `</span>
    <span class="` + valueClass + `">` + o.value + unit + `</span>
    ` + note + `
    ` + meter + `
</div>`
}

func chip(text, tone string, withDot bool) string {
	if tone == "" {
		tone = "ink"
	}
	dot := ""
	if withDot {
		dot = `<i class="dot"></i>`
	}
	return `<span class="chip t-` + tone + `">` + dot + text + `</span>`
}

var medalClass = []string{"", "m3-badge--gold", "m3-badge--silver", "m3-badge--bronze"}

func medal(rank int) string {
	if rank >= 1 && rank <= 3 {
		return fmt.Sprintf(`<span class="medal m3-badge %s">%d</span>`, medalClass[rank], rank)
	}
	return strconv.Itoa(rank)
}

func rankText(rank int) string {
	if rank > 0 {
		return "第 " + strconv.Itoa(rank) + " 位"
	}
	return "未上榜"
}

func translateGender(gender string) string {
	switch gender {
	case "male":
		return "男"
	case "female":
		return "女"
	default:
		return gender
	}
}

func translateGenderChild(gender string) string {
	switch gender {
	case "male":
		return "男孩"
	case "female":
		return "女孩"
	default:
		return gender
	}
}

func orderText(order string) string {
	if order == "五及以上" {
		return "五孩及以上"
	}
	return "第" + order + "孩"
}

func trimUsername(username string) string {
	runes := []rune(username)
	if len(runes) <= 10 {
		return username
	}
	return string(runes[:10]) + "…"
}

type donutPart struct {
	value int
	color string
}

// donut 生成带间隙与圆头的环图。
func donut(parts []donutPart, center, caption string) string {
	const (
		radius = 78.0
		width  = 26.0
	)
	circumference := 2 * math.Pi * radius
	total := 0
	live := make([]donutPart, 0, len(parts))
	for _, part := range parts {
		total += part.value
		if part.value > 0 {
			live = append(live, part)
		}
	}
	if total == 0 {
		live = nil
	}
	round := len(live) > 1
	if round {
		for _, part := range live {
			if float64(part.value)/float64(total)*circumference-8-width <= 4 {
				round = false
				break
			}
		}
	}
	var arcs strings.Builder
	cursor := 0.0
	for _, part := range live {
		if len(live) == 1 {
			fmt.Fprintf(&arcs, `<circle cx="100" cy="100" r="%.0f" fill="none" stroke="%s" stroke-width="%.0f"></circle>`, radius, part.color, width)
			continue
		}
		span := float64(part.value) / float64(total) * circumference
		gap := 3.0
		capWidth := 0.0
		lineCap := "butt"
		if round {
			gap = 8
			capWidth = width
			lineCap = "round"
		}
		dash := span - gap - capWidth
		start := cursor + gap/2 + capWidth/2
		cursor += span
		fmt.Fprintf(&arcs, `<circle cx="100" cy="100" r="%.0f" fill="none" stroke="%s" stroke-width="%.0f"
            stroke-linecap="%s"
            stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f"></circle>`,
			radius, part.color, width, lineCap, dash, circumference-dash, -start)
	}
	return `<div class="donut">
    <svg viewBox="0 0 200 200" aria-hidden="true">
        <circle cx="100" cy="100" r="78" fill="none" stroke="#f3e6da" stroke-width="26"></circle>
        <g transform="rotate(-90 100 100)">` + arcs.String() + `</g>
    </svg>
    <div class="c"><b>` + center + `</b><span>` + caption + `</span></div>
</div>`
}

func toneSolid(tone string) string {
	switch tone {
	case "azure":
		return "#0062a1"
	case "rose":
		return "#973f6e"
	case "jade":
		return "#006d42"
	case "cinnabar":
		return "#9e412f"
	case "gold":
		return "#7f5600"
	default:
		return "#7d581d"
	}
}

func formatFloat(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

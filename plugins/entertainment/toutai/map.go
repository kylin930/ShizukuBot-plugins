package toutai

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	mapWidth       = 700
	worldMapHeight = 400
	chinaMapHeight = 560

	mapLand     = "#fff5ec"
	mapWater    = "#dce3e9"
	mapBoundary = "#cab9a5"
	mapSelected = "#a2331e"
	mapOnSel    = "#fffbff"
	mapLabel    = "#201b13"
)

var heatRamp = []string{
	"#ffe8c5", "#f9ce7f", "#f1c473", "#eabd69", "#e4b762",
	"#dfb15b", "#dbac54", "#d6a84f", "#d3a449",
}

// heatColor 在九档旧迹色阶上取样。
func heatColor(ratio float64) string {
	t := math.Sqrt(math.Max(0, math.Min(1, ratio)))
	position := t * float64(len(heatRamp)-1)
	index := int(math.Floor(position))
	if index >= len(heatRamp)-1 {
		return heatRamp[len(heatRamp)-1]
	}
	fraction := position - float64(index)
	return blendHex(heatRamp[index], heatRamp[index+1], fraction)
}

func blendHex(a, b string, t float64) string {
	parse := func(hex string) (int, int, int) {
		value, _ := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 64)
		return int(value >> 16 & 255), int(value >> 8 & 255), int(value & 255)
	}
	ar, ag, ab := parse(a)
	br, bg, bb := parse(b)
	mix := func(x, y int) int { return int(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

type projection struct {
	centerLng float64
	centerLat float64
	scale     float64
	width     float64
	height    float64
}

func (p projection) point(coord []float64) (float64, float64) {
	if len(coord) < 2 {
		return p.width / 2, p.height / 2
	}
	x := (coord[0]-p.centerLng)*p.scale + p.width/2
	y := -(coord[1]-p.centerLat)*p.scale + p.height/2
	return x, y
}

func polygonPath(feature geoFeature, p projection) string {
	var b strings.Builder
	for _, ring := range feature.rings() {
		for i, coord := range ring {
			x, y := p.point(coord)
			if i == 0 {
				fmt.Fprintf(&b, "M%.1f %.1f", x, y)
			} else {
				fmt.Fprintf(&b, "L%.1f %.1f", x, y)
			}
		}
		b.WriteString("Z")
	}
	return b.String()
}

// markerSVG 画定位针与名牌。
func markerSVG(x, y float64, label string, chartHeight float64) string {
	textY := y + 34.0
	if y+52 > chartHeight-6 {
		textY = y - 40
	}
	width := float64(len([]rune(label))*16) + 24
	rectX := x - width/2
	rectY := textY - 21
	baseY := y - 38
	return fmt.Sprintf(`<g>
  <circle cx="%.1f" cy="%.1f" r="7" fill="%s" stroke="%s" stroke-width="2"/>
  <circle cx="%.1f" cy="%.1f" r="3" fill="%s"/>
  <path d="M16 0c-5.523 0-10 4.477-10 10 0 10 10 22 10 22s10-12 10-22c0-5.523-4.477-10-10-10zM16 16c-3.314 0-6-2.686-6-6s2.686-6 6-6 6 2.686 6 6-2.686 6-6 6z"
        transform="translate(%.1f %.1f) scale(0.9)" fill="%s" stroke="%s" stroke-width="2"/>
  <rect x="%.1f" y="%.1f" width="%.1f" height="30" rx="10" fill="%s" opacity="0.95"/>
  <text x="%.1f" y="%.1f" text-anchor="middle" font-size="16" font-weight="600" fill="%s">%s</text>
</g>`,
		x, y, mapOnSel, mapLabel,
		x, y, mapLabel,
		x-16, baseY, mapLabel, mapOnSel,
		rectX, rectY, width, mapSelected,
		x, textY, mapOnSel, esc(label))
}

// renderWorldMap 世界落点图。
func renderWorldMap(result birthResultInWorld, username string, d *dataset) string {
	nameEn := ""
	for _, country := range d.worldData {
		if country.NameCn == result.DictName {
			nameEn = country.NameEn
			break
		}
	}
	center := result.Center
	if len(center) < 2 {
		center = result.Coordinate
	}
	p := projection{centerLng: valueAt(center, 0), centerLat: valueAt(center, 1), scale: 700.0 / 360.0 * 2, width: mapWidth, height: worldMapHeight}

	var shapes strings.Builder
	for _, feature := range d.world.Features {
		fill := mapLand
		strokeWidth := 0.6
		if nameEn != "" && feature.Properties.Name == nameEn {
			fill = mapSelected
			strokeWidth = 1.2
		}
		path := polygonPath(feature, p)
		if path == "" {
			continue
		}
		fmt.Fprintf(&shapes, `<path d="%s" fill="%s" stroke="%s" stroke-width="%.1f" fill-rule="evenodd"/>`,
			path, fill, mapBoundary, strokeWidth)
	}
	x, y := p.point(result.Coordinate)
	svg := fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="100%%" height="%d" xmlns="http://www.w3.org/2000/svg" style="display:block;background:%s">%s%s</svg>`,
		mapWidth, worldMapHeight, worldMapHeight, mapWater, shapes.String(), markerSVG(x, y, result.DictName, worldMapHeight))

	legend := `<span class="key"><i aria-hidden="true">◎</i>本次落点</span>
<span class="spacer"></span>
<span>` + coordinateText(result.Coordinate) + `</span>`

	return buildPage(pageOptions{
		docTitle:      "世界投胎落点",
		eyebrow:       "投胎模拟器 · 世界",
		title:         result.DictName,
		subtitle:      subtitleOf(username, "第 "+strconv.Itoa(result.Index)+" 次轮回 · 已落人间"),
		tags:          chip(result.DictContinent, "ink", false),
		seal:          "寰宇",
		body:          `<div class="plate">` + svg + `</div><div class="legend">` + legend + `</div>`,
		colophonRight: "天涯何处不为家",
		style:         mapStyle,
	})
}

// renderChinaMap 中国落点图。
func renderChinaMap(results []birthResultInChina, result birthResultInChina, username string, d *dataset) string {
	weights := map[string]float64{}
	for _, item := range results {
		weights[item.Province] += item.Probability
	}
	maxWeight := 0.0
	for _, weight := range weights {
		if weight > maxWeight {
			maxWeight = weight
		}
	}
	center := []float64{104, 35}
	selectedCP := center
	for _, feature := range d.china.Features {
		if feature.Properties.Name == result.Province && len(feature.Properties.CP) >= 2 {
			selectedCP = feature.Properties.CP
			center = selectedCP
		}
	}
	p := projection{centerLng: valueAt(center, 0), centerLat: valueAt(center, 1), scale: 700.0 / 62 * 1.2, width: mapWidth, height: chinaMapHeight}

	var shapes, labels strings.Builder
	for _, feature := range d.china.Features {
		name := feature.Properties.Name
		path := polygonPath(feature, p)
		if path == "" {
			continue
		}
		fill := mapLand
		strokeWidth := 0.8
		switch {
		case name == result.Province:
			fill = mapSelected
			strokeWidth = 1.4
		case weights[name] > 0:
			fill = heatColor(weights[name] / maxWeight)
		}
		fmt.Fprintf(&shapes, `<path d="%s" fill="%s" stroke="%s" stroke-width="%.1f" fill-rule="evenodd"/>`,
			path, fill, mapBoundary, strokeWidth)
		if name != result.Province && len(feature.Properties.CP) >= 2 {
			x, y := p.point(feature.Properties.CP)
			if name == "香港" {
				x += 24
				y -= 4
			} else if name == "澳门" {
				x -= 24
				y += 10
			}
			fmt.Fprintf(&labels, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="14" font-weight="500" fill="%s">%s</text>`,
				x, y, mapLabel, esc(name))
		}
	}
	x, y := p.point(selectedCP)
	svg := fmt.Sprintf(`<svg viewBox="0 0 %d %d" width="100%%" height="%d" xmlns="http://www.w3.org/2000/svg" style="display:block;background:%s">%s%s%s</svg>`,
		mapWidth, chinaMapHeight, chinaMapHeight, mapWater, shapes.String(), labels.String(), markerSVG(x, y, result.Province, chinaMapHeight))

	isSpecialRegion := result.Province == "香港" || result.Province == "澳门" || result.Province == "台湾"
	genderTone := "rose"
	if result.Gender == "male" {
		genderTone = "azure"
	}
	var tags strings.Builder
	if !isSpecialRegion {
		tags.WriteString(chip(result.Category, "ink", false))
	}
	tags.WriteString(chip(translateGenderChild(result.Gender), genderTone, true))
	if !isSpecialRegion {
		tags.WriteString(chip(orderText(result.Order), "ink", false))
	}

	legend := `<span class="key"><i aria-hidden="true">◎</i>本次落点</span>
<span class="scale"><span>旧迹深浅　浅</span><span class="ramp" aria-hidden="true"></span><span>深</span></span>
<span class="spacer"></span>
<span>已踏足 <b>` + strconv.Itoa(len(weights)) + `</b> / ` + strconv.Itoa(d.totalProvinceCount()) + ` 省</span>`

	return buildPage(pageOptions{
		docTitle:      "中国投胎落点",
		eyebrow:       "投胎模拟器 · 中国",
		title:         result.Province,
		subtitle:      subtitleOf(username, "第 "+strconv.Itoa(result.Index)+" 次轮回 · 已落人间"),
		tags:          tags.String(),
		seal:          "降生",
		body:          `<div class="plate">` + svg + `</div><div class="legend">` + legend + `</div>`,
		colophonRight: "山河万里，此处是家",
		style:         mapStyle,
	})
}

const mapStyle = `
.plate { border-radius: var(--md-sys-shape-corner-extra-large); overflow: hidden; background: #dce3e9; }
.legend {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 20px;
    margin-top: 20px;
    font-size: 16px;
    line-height: 24px;
    color: var(--md-sys-color-on-surface-variant);
}
.legend .key {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    height: 32px;
    padding: 0 16px 0 12px;
    border-radius: var(--md-sys-shape-corner-full);
    background: #a2331e;
    color: #fffbff;
    font-weight: 600;
}
.legend .scale { display: inline-flex; align-items: center; gap: 10px; }
.legend .ramp { width: 96px; height: 12px; border-radius: var(--md-sys-shape-corner-full); background: linear-gradient(90deg, #ffe8c5 0%, #f9ce7f 12.5%, #f1c473 25%, #eabd69 37.5%, #e4b762 50%, #dfb15b 62.5%, #dbac54 75%, #d6a84f 87.5%, #d3a449 100%); box-shadow: inset 0 0 0 1px #cab9a5; }
.legend .spacer { flex: 1; }
.legend b { font-weight: 600; color: var(--md-sys-color-on-surface); }
`

// coordinateText 把经纬度读成人话。
func coordinateText(coord []float64) string {
	lng := valueAt(coord, 0)
	lat := valueAt(coord, 1)
	latLabel := "北纬"
	if lat < 0 {
		latLabel = "南纬"
	}
	lngLabel := "东经"
	if lng < 0 {
		lngLabel = "西经"
	}
	return latLabel + " " + formatFloat(math.Abs(lat), 1) + `°<span class="sep">·</span>` +
		lngLabel + " " + formatFloat(math.Abs(lng), 1) + "°"
}

func valueAt(coord []float64, index int) float64 {
	if index < len(coord) {
		return coord[index]
	}
	return 0
}

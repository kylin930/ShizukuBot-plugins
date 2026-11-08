// Package movies 猫眼电影查询：今日热映、预售电影与按片名搜索电影详情。
package movies

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// ua 与上游一致，猫眼移动端接口对 User-Agent 较敏感。
	ua = "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/86.0.4240.198 Mobile Safari/537.36"
	// maxList 单个榜单最多展示的条数，避免消息过长。
	maxList = 8
	// maxIntro 简介截断长度（按字符计）。
	maxIntro = 120
	// cardWidth 榜单卡片宽度，与上游 800px 画布一致。
	cardWidth = 800
	// maxPosterBytes 单张海报下载上限，避免把整页拖垮。
	maxPosterBytes = 2 << 20
)

// 猫眼接口地址均为包级变量，测试时可替换。
var (
	movieOnURL = "https://m.maoyan.com/ajax/movieOnInfoList"
	comingURL  = "https://m.maoyan.com/ajax/comingList?token="
	detailURL  = "https://m.maoyan.com/ajax/detailmovie?movieId="
	searchURL  = "https://m.maoyan.com/ajax/search?kw="
	referer    = "https://m.maoyan.com/"

	client = &http.Client{Timeout: 10 * time.Second}

	// renderHTML 与 fetchImage 抽成包级变量，测试时可替换为桩。
	renderHTML = sdk.RenderHTML
	fetchImage = downloadImage

	cardTemplate = template.Must(template.New("movies").Parse(cardHTML))
)

const helpText = "电影查询用法：\n" +
	"- 今日电影：当日热映电影榜单\n" +
	"- 预售电影：即将上映/预售电影\n" +
	"- 电影搜索 <片名>：按片名搜索电影详情\n" +
	"- 电影详情 <片名>：同「电影搜索」"

// movie 同时兼容列表、搜索与详情接口返回的字段。
type movie struct {
	ID          int64   `json:"id"`
	Img         string  `json:"img"`
	Nm          string  `json:"nm"`
	Dir         string  `json:"dir"`
	Star        string  `json:"star"`
	Cat         string  `json:"cat"`
	OriLang     string  `json:"oriLang"`
	Version     string  `json:"version"`
	Src         string  `json:"src"`
	Rt          string  `json:"rt"`
	ShowInfo    string  `json:"showInfo"`
	ComingTitle string  `json:"comingTitle"`
	Sc          float64 `json:"sc"`
	Wish        int64   `json:"wish"`
	Watched     int64   `json:"watched"`
	Dur         int     `json:"dur"`
	Dra         string  `json:"dra"`
}

type movieOnList struct {
	MovieList []movie `json:"movieList"`
}

type comingList struct {
	MovieList []movie `json:"coming"`
}

type movieShow struct {
	MovieInfo movie `json:"detailMovie"`
}

type searchResult struct {
	Movies struct {
		List []movie `json:"list"`
	} `json:"movies"`
}

type cardView struct {
	Title string
	Items []cardItem
}

type cardItem struct {
	Rank   int
	Name   string
	Poster string
	Lines  []string
	Score  string
	Count  string
}

const cardHTML = `<!doctype html>
<html><head><meta charset="utf-8"><style>
html,body{margin:0;padding:0;background:#14161b;}
body{width:800px;box-sizing:border-box;padding:28px;font-family:"PingFang SC","Microsoft YaHei",sans-serif;color:#1f2329;}
.card{background:#fff;border-radius:20px;padding:24px 28px 18px;}
.title{font-size:32px;font-weight:700;text-align:center;margin:0 0 22px;letter-spacing:2px;}
.movie{display:flex;align-items:center;gap:16px;background:#f7f8fa;border-radius:16px;padding:14px;margin-bottom:12px;}
.poster{width:64px;height:90px;border-radius:10px;object-fit:cover;background:#e5e7eb;flex:none;}
.poster-empty{width:64px;height:90px;border-radius:10px;background:#e5e7eb;flex:none;}
.rank{width:34px;height:34px;border-radius:50%;background:#f0b43c;color:#fff;font-size:16px;font-weight:700;display:flex;align-items:center;justify-content:center;flex:none;}
.main{flex:1;min-width:0;}
.name{font-size:20px;font-weight:700;margin-bottom:6px;}
.line{font-size:13px;line-height:1.7;color:#5c6370;}
.side{width:110px;text-align:right;flex:none;}
.score{font-size:26px;font-weight:700;color:#f0a020;line-height:1;}
.score-label{font-size:12px;color:#9aa0a6;margin-top:4px;}
.count{font-size:13px;color:#5c6370;margin-top:10px;}
</style></head><body><div class="card">
<div class="title">{{.Title}}</div>
{{range .Items}}<div class="movie">
<div class="rank">{{.Rank}}</div>
{{if .Poster}}<img class="poster" src="{{.Poster}}">{{else}}<div class="poster-empty"></div>{{end}}
<div class="main"><div class="name">{{.Name}}</div>{{range .Lines}}<div class="line">{{.}}</div>{{end}}</div>
<div class="side"><div class="score">{{.Score}}</div><div class="score-label">评分</div><div class="count">{{.Count}}</div></div>
</div>
{{end}}</div></body></html>`

// Definition subscribes to the movie queries.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "movies",
		Name:        "猫眼电影查询",
		Version:     "1.1.0",
		Description: "发送「今日电影」「预售电影」「电影搜索 片名」查询猫眼电影信息",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "movies", Trigger: "电影搜索"},
	})
}

// Handle dispatches the supported movie queries.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "今日电影"):
		return today(ctx, c)
	case sdk.MatchFull(text, "预售电影"):
		return coming(ctx, c)
	case sdk.MatchFull(text, "电影帮助"):
		return c.Reply(ctx, helpText)
	}
	if keyword, ok := sdk.MatchPrefix(text, "电影搜索"); ok {
		return search(ctx, c, keyword)
	}
	if keyword, ok := sdk.MatchPrefix(text, "电影详情"); ok {
		return search(ctx, c, keyword)
	}
	return nil
}

// today 拉取当日热映列表，按评分与观看数排序后回复前若干条。
func today(ctx context.Context, c sdk.Call) error {
	var parsed movieOnList
	if err := getJSON(ctx, movieOnURL, &parsed); err != nil {
		return c.Reply(ctx, "今日电影查询失败："+err.Error())
	}
	if len(parsed.MovieList) == 0 {
		return c.Reply(ctx, "今天暂时没有热映电影")
	}
	list := parsed.MovieList
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Sc != list[j].Sc {
			return list[i].Sc > list[j].Sc
		}
		return list[i].Watched > list[j].Watched
	})
	if len(list) > maxList {
		list = list[:maxList]
	}
	return replyCard(ctx, c, "今日电影", list, false)
}

// coming 拉取预售列表，按评分与想看人数排序后回复前若干条。
func coming(ctx context.Context, c sdk.Call) error {
	var parsed comingList
	if err := getJSON(ctx, comingURL, &parsed); err != nil {
		return c.Reply(ctx, "预售电影查询失败："+err.Error())
	}
	if len(parsed.MovieList) == 0 {
		return c.Reply(ctx, "暂时没有预售电影")
	}
	list := parsed.MovieList
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Sc != list[j].Sc {
			return list[i].Sc > list[j].Sc
		}
		return list[i].Wish > list[j].Wish
	})
	if len(list) > maxList {
		list = list[:maxList]
	}
	return replyCard(ctx, c, "预售电影", list, true)
}

// replyCard 补全详情后排成榜单卡片；渲染或发送失败时回退纯文本。
func replyCard(ctx context.Context, c sdk.Call, title string, list []movie, presale bool) error {
	for i := range list {
		if list[i].ID == 0 {
			continue
		}
		var show movieShow
		// 详情只用于补齐导演/演员/语言等字段，失败时保留列表数据。
		if err := getJSON(ctx, detailURL+strconv.FormatInt(list[i].ID, 10), &show); err == nil {
			list[i] = mergeDetail(list[i], show.MovieInfo)
		}
	}
	page := buildCardHTML(ctx, title, list, presale)
	if page == "" {
		return c.Reply(ctx, formatList(title, list, presale))
	}
	if img, err := renderHTML(ctx, []byte(page), sdk.RenderOptions{
		Width:    cardWidth,
		FullPage: true,
		Timeout:  30 * time.Second,
	}); err == nil {
		if err = c.ReplyImageBytes(ctx, img); err == nil {
			return nil
		}
	}
	return c.Reply(ctx, formatList(title, list, presale))
}

// buildCardHTML 把榜单渲染成 HTML，海报下载后以 data URI 内嵌。
func buildCardHTML(ctx context.Context, title string, list []movie, presale bool) string {
	view := cardView{Title: html.EscapeString(title)}
	for i, m := range list {
		movieType := "2D"
		if m.Version != "" {
			movieType = m.Version
		}
		lines := []string{
			"导演：" + html.EscapeString(orUnknown(m.Dir)),
			"演员：" + html.EscapeString(orUnknown(m.Star)),
			"标签：" + html.EscapeString(orUnknown(m.Cat)),
			"语言：" + html.EscapeString(orUnknown(m.OriLang)) + "    类型：" + html.EscapeString(movieType),
		}
		if presale {
			lines = append(lines, "上映时间："+html.EscapeString(orUnknown(m.Rt))+
				"    播放时间："+html.EscapeString(orUnknown(m.ComingTitle)))
		} else {
			lines = append(lines, "上映时间："+html.EscapeString(orUnknown(m.Rt)))
		}
		item := cardItem{
			Rank:   i + 1,
			Name:   html.EscapeString(fmt.Sprintf("%s（%d）", orUnknown(m.Nm), m.ID)),
			Poster: fetchImage(ctx, m.Img),
			Lines:  lines,
			Score:  scoreBadge(m.Sc),
		}
		if presale {
			item.Count = humanCount(m.Wish) + " 人期待"
		} else {
			item.Count = humanCount(m.Watched) + " 人已看"
		}
		view.Items = append(view.Items, item)
	}
	var builder strings.Builder
	if err := cardTemplate.Execute(&builder, view); err != nil {
		return ""
	}
	return builder.String()
}

// mergeDetail 用详情接口补齐搜索结果或榜单中缺失的字段。
func mergeDetail(base, detail movie) movie {
	if detail.ID == 0 {
		detail.ID = base.ID
	}
	if detail.Nm == "" {
		detail.Nm = base.Nm
	}
	if detail.Img == "" {
		detail.Img = base.Img
	}
	if detail.Dir == "" {
		detail.Dir = base.Dir
	}
	if detail.Star == "" {
		detail.Star = base.Star
	}
	if detail.Cat == "" {
		detail.Cat = base.Cat
	}
	if detail.OriLang == "" {
		detail.OriLang = base.OriLang
	}
	if detail.Version == "" {
		detail.Version = base.Version
	}
	if detail.Src == "" {
		detail.Src = base.Src
	}
	if detail.Rt == "" {
		detail.Rt = base.Rt
	}
	if detail.ComingTitle == "" {
		detail.ComingTitle = base.ComingTitle
	}
	if detail.Sc == 0 {
		detail.Sc = base.Sc
	}
	if detail.Wish == 0 {
		detail.Wish = base.Wish
	}
	if detail.Watched == 0 {
		detail.Watched = base.Watched
	}
	if detail.Dur == 0 {
		detail.Dur = base.Dur
	}
	if detail.Dra == "" {
		detail.Dra = base.Dra
	}
	return detail
}

// search 按片名搜索，取首条结果并尽量补全详情。
func search(ctx context.Context, c sdk.Call, keyword string) error {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return c.Reply(ctx, "用法：电影搜索 <片名>（例如：电影搜索 哪吒）")
	}
	var result searchResult
	if err := getJSON(ctx, searchURL+url.QueryEscape(keyword)+"&cityId=1&stype=1", &result); err != nil {
		return c.Reply(ctx, "电影搜索失败："+err.Error())
	}
	if len(result.Movies.List) == 0 {
		return c.Reply(ctx, "没有找到电影「"+keyword+"」，换个片名试试")
	}
	item := result.Movies.List[0]
	// 搜索结果缺少时长与简介，再取一次详情；详情失败时降级用搜索字段。
	var show movieShow
	if err := getJSON(ctx, detailURL+strconv.FormatInt(item.ID, 10), &show); err == nil {
		item = mergeDetail(item, show.MovieInfo)
	}
	return c.Reply(ctx, formatDetail(item))
}

// formatList 把榜单格式化为纯文本，作为卡片渲染失败时的回退。
func formatList(title string, list []movie, presale bool) string {
	lines := []string{fmt.Sprintf("%s（共 %d 条）", title, len(list))}
	for i, m := range list {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("%d. %s（%d）", i+1, orUnknown(m.Nm), m.ID))
		lines = append(lines, "   类型："+orUnknown(m.Cat))
		lines = append(lines, "   上映："+orUnknown(m.Rt))
		if presale && m.ComingTitle != "" {
			lines = append(lines, "   预售："+m.ComingTitle)
		}
		lines = append(lines, "   评分："+scoreText(m.Sc))
		lines = append(lines, "   主演："+orUnknown(m.Star))
		if presale {
			if m.Wish > 0 {
				lines = append(lines, "   想看："+humanCount(m.Wish)+"人")
			}
		} else if m.Watched > 0 {
			lines = append(lines, "   已看："+humanCount(m.Watched)+"人")
		}
	}
	return strings.Join(lines, "\n")
}

// formatDetail 把单部电影的详情格式化为纯文本。
func formatDetail(m movie) string {
	lines := []string{"《" + orUnknown(m.Nm) + "》"}
	lines = append(lines, "导演："+orUnknown(m.Dir))
	lines = append(lines, "主演："+orUnknown(m.Star))
	lines = append(lines, "类型："+orUnknown(m.Cat))
	lines = append(lines, "地区："+orUnknown(region(m)))
	lines = append(lines, "时长："+durationText(m.Dur))
	lines = append(lines, "上映："+orUnknown(m.Rt))
	lines = append(lines, "评分："+scoreText(m.Sc))
	if intro := truncate(strings.TrimSpace(m.Dra), maxIntro); intro != "" {
		lines = append(lines, "简介："+intro)
	}
	return strings.Join(lines, "\n")
}

func orUnknown(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "暂无"
	}
	return s
}

func region(m movie) string {
	if m.OriLang != "" {
		return m.OriLang
	}
	return m.Src
}

func scoreText(sc float64) string {
	if sc <= 0 {
		return "暂无评分"
	}
	return strconv.FormatFloat(sc, 'f', 1, 64) + " 分"
}

// scoreBadge 是卡片右侧的短评分。
func scoreBadge(sc float64) string {
	if sc <= 0 {
		return "暂无"
	}
	return strconv.FormatFloat(sc, 'f', 1, 64)
}

func durationText(dur int) string {
	if dur <= 0 {
		return "暂无"
	}
	return strconv.Itoa(dur) + " 分钟"
}

func humanCount(n int64) string {
	switch {
	case n >= 100000000:
		return strconv.FormatFloat(float64(n)/100000000, 'f', 2, 64) + "亿"
	case n >= 10000:
		return strconv.FormatFloat(float64(n)/10000, 'f', 2, 64) + "万"
	default:
		return strconv.FormatInt(n, 10)
	}
}

// truncate 按字符截断文本，超长时追加省略号。
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// secureImageURL 把海报的 http 地址升级为 https，页面内嵌时更稳定。
func secureImageURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "http://") {
		return "https://" + strings.TrimPrefix(rawURL, "http://")
	}
	return rawURL
}

// downloadImage 下载海报并转成 data URI；失败、超限或非图片时返回空串。
func downloadImage(ctx context.Context, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secureImageURL(rawURL), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", referer)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPosterBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxPosterBytes {
		return ""
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// getJSON 带上猫眼要求的请求头拉取 JSON，响应体限制在 1 MiB 以内。
func getJSON(ctx context.Context, rawURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

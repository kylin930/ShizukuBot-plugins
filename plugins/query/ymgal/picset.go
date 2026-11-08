package ymgal

import (
	"context"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

// 月幕官网图包抓取。上游依赖懒加载的 sqlite 图包库，这里改为运行时解析官网 HTML。

const (
	picsetSite   = "https://www.ymgal.games"
	cgCategory   = "Gal CG"
	emoticonType = "其他"

	// maxPicsetImages 单个图包最多发送的图片数量，避免一次刷屏。
	maxPicsetImages = 50
	// keywordScanPages 关键词匹配最多翻的列表页数。
	keywordScanPages = 10
	keywordTimeout   = 20 * time.Second
	updateTimeout    = 90 * time.Second
	maxListPages     = 200
	picsetBodyLimit  = 4 << 20
	// picsetCacheKey 保存「更新gal」扫描到的图包 ID，供随机/关键词查询复用。
	picsetCacheKey = "picset_cache"
)

var (
	// 下列地址与 UA 均为包级变量，测试中替换为 httptest 服务。
	cgListURL       = picsetSite + "/search?type=picset&sort=default&category=Gal%20CG&page="
	emoticonListURL = picsetSite + "/search?type=picset&sort=default&category=%E5%85%B6%E4%BB%96&page="
	picsetDetailURL = picsetSite + "/co/picset/"
	picsetUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	picsetClient    = &http.Client{Timeout: 10 * time.Second}

	// randIntn 便于测试固定随机结果。
	randIntn = rand.Intn

	picsetLinkRe = regexp.MustCompile(`(?i)/co/picset/(\d+)`)
	pageQueryRe  = regexp.MustCompile(`(?i)[?&]page=(\d+)`)
	htmlTagRe    = regexp.MustCompile(`(?is)<(?:img|div|meta)\b[^>]*>`)
	htmlAttrRe   = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	urlInValueRe = regexp.MustCompile(`(?i)(?:https?:)?//[^\s"'()<>]+`)
	cssURLRe     = regexp.MustCompile(`(?is)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
	titleTagRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

var errPicPackNotFound = errors.New("没有找到匹配的图包")

// picsetIndex 是「更新gal」写入 state 的图包索引。
type picsetIndex struct {
	UpdatedAt int64   `json:"updated_at"`
	CG        []int64 `json:"cg,omitempty"`
	Emoticon  []int64 `json:"emoticon,omitempty"`
}

// picPack 是官网上的一个图包。
type picPack struct {
	ID          int64
	Title       string
	Description string
	Images      []string
	Link        string
}

func (p picPack) link() string {
	if p.Link != "" {
		return p.Link
	}
	return picsetDetailURL + strconv.FormatInt(p.ID, 10)
}

func categoryListURL(category string) string {
	if category == emoticonType || category == "表情包" {
		return emoticonListURL
	}
	return cgListURL
}

// randomPicPack 随机取一个图包：优先使用「更新gal」缓存的索引，否则现取列表页。
func randomPicPack(ctx context.Context, c sdk.Call, category string) (picPack, error) {
	if ids := cachedPicsetIDs(ctx, c, category); len(ids) > 0 {
		return fetchCachedPicPack(ctx, ids[randIntn(len(ids))])
	}
	base := categoryListURL(category)
	first, err := getPicsetPage(ctx, base+"1")
	if err != nil {
		return picPack{}, fmt.Errorf("抓取图包列表失败：%w", err)
	}
	ids := parsePicsetIDs(first)
	if maxPage := parseMaxPage(first); maxPage > 1 {
		if maxPage > maxListPages {
			maxPage = maxListPages
		}
		page := randIntn(maxPage) + 1
		if page != 1 {
			pageHTML, err := getPicsetPage(ctx, base+strconv.Itoa(page))
			if err != nil {
				return picPack{}, fmt.Errorf("抓取第 %d 页失败：%w", page, err)
			}
			if list := parsePicsetIDs(pageHTML); len(list) > 0 {
				ids = list
			}
		}
	}
	if len(ids) == 0 {
		return picPack{}, errors.New("列表页没有解析到图包，站点结构可能已变化")
	}
	return fetchCachedPicPack(ctx, ids[randIntn(len(ids))])
}

// fetchCachedPicPack 按缓存的图包 ID 拉取详情。
func fetchCachedPicPack(ctx context.Context, id int64) (picPack, error) {
	pack, err := fetchPicPack(ctx, id)
	if err != nil {
		return picPack{}, fmt.Errorf("抓取图包 %d 失败：%w", id, err)
	}
	return pack, nil
}

// findPicPackByKey 进入详情页匹配标题或简介，命中即返回。
func findPicPackByKey(ctx context.Context, c sdk.Call, category, key string) (picPack, error) {
	ctx, cancel := context.WithTimeout(ctx, keywordTimeout)
	defer cancel()

	needle := strings.ToLower(strings.TrimSpace(key))
	if ids := cachedPicsetIDs(ctx, c, category); len(ids) > 0 {
		return matchPicsetIDs(ctx, ids, needle)
	}

	base := categoryListURL(category)
	pageHTML, err := getPicsetPage(ctx, base+"1")
	if err != nil {
		return picPack{}, fmt.Errorf("抓取图包列表失败：%w", err)
	}
	pages := parseMaxPage(pageHTML)
	if pages > keywordScanPages {
		pages = keywordScanPages
	}
	for page := 1; page <= pages; page++ {
		if page > 1 {
			pageHTML, err = getPicsetPage(ctx, base+strconv.Itoa(page))
			if err != nil {
				if ctx.Err() != nil {
					return picPack{}, errors.New("搜索超时，请稍后再试")
				}
				continue
			}
		}
		pack, err := matchPicsetIDs(ctx, parsePicsetIDs(pageHTML), needle)
		if err == nil {
			return pack, nil
		}
		if !errors.Is(err, errPicPackNotFound) {
			return picPack{}, err
		}
	}
	return picPack{}, errPicPackNotFound
}

// matchPicsetIDs 按顺序检查图包详情，返回首个标题或简介命中关键词的图包。
func matchPicsetIDs(ctx context.Context, ids []int64, needle string) (picPack, error) {
	for _, id := range ids {
		if ctx.Err() != nil {
			return picPack{}, errors.New("搜索超时，请稍后再试")
		}
		pack, err := fetchPicPackDetail(ctx, id)
		if err != nil || len(pack.Images) == 0 {
			continue
		}
		if strings.Contains(strings.ToLower(pack.Title), needle) ||
			strings.Contains(strings.ToLower(pack.Description), needle) {
			return pack, nil
		}
	}
	return picPack{}, errPicPackNotFound
}

// updatePicSource 扫描两类图源并把图包索引写入 state。
func updatePicSource(ctx context.Context, c sdk.Call) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	total := 0
	lines := make([]string, 0, 2)
	index := picsetIndex{}
	for _, item := range []struct{ name, base string }{
		{cgCategory, cgListURL},
		{emoticonType, emoticonListURL},
	} {
		ids, pages, err := collectPicPacks(ctx, item.base)
		if err != nil {
			return "", fmt.Errorf("%s 图源不可访问：%w", item.name, err)
		}
		total += len(ids)
		if item.name == cgCategory {
			index.CG = ids
		} else {
			index.Emoticon = ids
		}
		lines = append(lines, fmt.Sprintf("%s：%d 个图包（%d 页）", item.name, len(ids), pages))
	}
	index.UpdatedAt = time.Now().Unix()
	if err := sdk.SaveJSON(ctx, c.State, picsetCacheKey, index); err != nil {
		return "", fmt.Errorf("保存图包缓存失败：%w", err)
	}
	return fmt.Sprintf("图源可访问，共可抓取 %d 个图包\n%s", total, strings.Join(lines, "\n")), nil
}

// cachedPicsetIDs 读取指定分类的缓存图包 ID。
func cachedPicsetIDs(ctx context.Context, c sdk.Call, category string) []int64 {
	var index picsetIndex
	if err := sdk.LoadJSON(ctx, c.State, picsetCacheKey, &index); err != nil {
		return nil
	}
	if category == emoticonType || category == "表情包" {
		return index.Emoticon
	}
	return index.CG
}

func collectPicPacks(ctx context.Context, base string) ([]int64, int, error) {
	first, err := getPicsetPage(ctx, base+"1")
	if err != nil {
		return nil, 0, err
	}
	pages := parseMaxPage(first)
	if pages > maxListPages {
		pages = maxListPages
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, 32)
	add := func(list []int64) {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	add(parsePicsetIDs(first))
	for page := 2; page <= pages; page++ {
		pageHTML, err := getPicsetPage(ctx, base+strconv.Itoa(page))
		if err != nil {
			return nil, 0, fmt.Errorf("列表第 %d 页抓取失败：%w", page, err)
		}
		add(parsePicsetIDs(pageHTML))
	}
	return ids, pages, nil
}

func fetchPicPack(ctx context.Context, id int64) (picPack, error) {
	pack, err := fetchPicPackDetail(ctx, id)
	if err != nil {
		return pack, err
	}
	if len(pack.Images) == 0 {
		return pack, errors.New("详情页没有解析到图片")
	}
	return pack, nil
}

func fetchPicPackDetail(ctx context.Context, id int64) (picPack, error) {
	link := picsetDetailURL + strconv.FormatInt(id, 10)
	page, err := getPicsetPage(ctx, link)
	if err != nil {
		return picPack{}, err
	}
	pack := parsePicPack(page)
	pack.ID = id
	pack.Link = link
	return pack, nil
}

func getPicsetPage(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", picsetUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Referer", picsetSite+"/")
	resp, err := picsetClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("请求超时")
		}
		return "", errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("图源返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, picsetBodyLimit))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parsePicPack(page string) picPack {
	title := metaContent(page, "name")
	if title == "" {
		title = metaContent(page, "og:title")
	}
	if title == "" {
		if m := titleTagRe.FindStringSubmatch(page); m != nil {
			title = m[1]
		}
	}
	desc := metaContent(page, "description")
	if desc == "" {
		desc = metaContent(page, "og:description")
	}
	return picPack{
		Title:       cleanText(title),
		Description: cleanText(desc),
		Images:      extractPicsetImages(page),
	}
}

func metaContent(page, name string) string {
	name = strings.ToLower(name)
	for _, tag := range htmlTagRe.FindAllString(page, -1) {
		if !strings.HasPrefix(strings.ToLower(tag), "<meta") {
			continue
		}
		attrs := parseHTMLAttrs(tag)
		key := strings.ToLower(firstAttr(attrs, "name"))
		if key == "" {
			key = strings.ToLower(firstAttr(attrs, "property"))
		}
		if key == name {
			return firstAttr(attrs, "content")
		}
	}
	return ""
}

// extractPicsetImages 在详情主体区域内收集图片地址，兼容懒加载与 swiper。
func extractPicsetImages(page string) []string {
	if i := strings.Index(page, "main-picset-warp"); i >= 0 {
		page = page[i:]
	}
	seen := map[string]bool{}
	images := make([]string, 0, 16)
	add := func(raw string) bool {
		found := false
		for _, u := range imageURLs(raw) {
			if seen[u] {
				continue
			}
			seen[u] = true
			images = append(images, u)
			found = true
		}
		return found
	}
	for _, tag := range htmlTagRe.FindAllString(page, -1) {
		lower := strings.ToLower(tag)
		attrs := parseHTMLAttrs(tag)
		switch {
		case strings.HasPrefix(lower, "<img"):
			// 每个 img 只取一个最优地址：原图 > 懒加载 > 缩略图。
			for _, key := range []string{"data-original", "data-src", "data-lazy-src", "data-url", "src"} {
				if add(firstAttr(attrs, key)) {
					break
				}
			}
		case strings.HasPrefix(lower, "<div"):
			for _, attr := range attrs {
				add(attr.val)
			}
		}
	}
	return images
}

func imageURLs(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	raw := make([]string, 0, 2)
	for _, m := range cssURLRe.FindAllStringSubmatch(value, -1) {
		raw = append(raw, m[1])
	}
	raw = append(raw, urlInValueRe.FindAllString(value, -1)...)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if u := normalizeImageURL(item); u != "" {
			out = append(out, u)
		}
	}
	// 兼容 src="/upload/1.jpg" 这类相对地址。
	if u := normalizeImageURL(value); u != "" {
		out = append(out, u)
	}
	return out
}

func normalizeImageURL(raw string) string {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `"'`))
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	} else if strings.HasPrefix(s, "/") {
		s = picsetSite + s
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || !isImagePath(u.Path) {
		return ""
	}
	return s
}

func isImagePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif":
		return true
	}
	return false
}

type htmlAttribute struct {
	key string
	val string
}

func parseHTMLAttrs(tag string) []htmlAttribute {
	matches := htmlAttrRe.FindAllStringSubmatch(tag, -1)
	attrs := make([]htmlAttribute, 0, len(matches))
	for _, m := range matches {
		val := m[2]
		if val == "" {
			val = m[3]
		}
		if val == "" {
			val = m[4]
		}
		attrs = append(attrs, htmlAttribute{key: strings.ToLower(m[1]), val: stdhtml.UnescapeString(val)})
	}
	return attrs
}

func firstAttr(attrs []htmlAttribute, key string) string {
	for _, attr := range attrs {
		if attr.key == key {
			return attr.val
		}
	}
	return ""
}

func cleanText(s string) string {
	s = stdhtml.UnescapeString(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}

func parsePicsetIDs(page string) []int64 {
	matches := picsetLinkRe.FindAllStringSubmatch(page, -1)
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func parseMaxPage(page string) int {
	maxPage := 1
	for _, m := range pageQueryRe.FindAllStringSubmatch(page, -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

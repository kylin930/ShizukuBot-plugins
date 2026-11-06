// Package jandan 煎蛋网无聊图：抓取列表页并随机返回一张图片。
package jandan

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

var (
	// pageURL 上游无聊图列表页，测试可替换。
	pageURL = "https://jandan.net/pic"

	client   = &http.Client{Timeout: 10 * time.Second}
	randIntn = rand.Intn

	picTrigger    = regexp.MustCompile(`^来份[屌弔吊]图$`)
	anchorPattern = regexp.MustCompile(`<a\b[^>]*>`)
	attrPattern   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"`)
)

// Definition subscribes to the 「来份屌图」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "jandan",
		Name:        "煎蛋无聊图",
		Version:     "1.0.0",
		Description: "发送「来份屌图」「来份弔图」或「来份吊图」，随机返回一张煎蛋网无聊图",
	}, sdk.MessageEvents, Handle)
}

// Handle 抓取列表页、提取 view_img_link 图片后随机发送一张。上游的「更新屌图」
// 需要超管权限并全站爬取写入本地 sqlite，框架不引入大资源，因此未移植。
func Handle(ctx context.Context, c sdk.Call) error {
	if sdk.MatchRegexp(sdk.Text(c.Event), picTrigger) == nil {
		return nil
	}
	links, err := fetchLinks(ctx)
	if err != nil {
		return c.Reply(ctx, "获取煎蛋图片失败: "+err.Error())
	}
	return c.ReplyImage(ctx, links[randIntn(len(links))])
}

func fetchLinks(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	links := imageLinks(string(body))
	if len(links) == 0 {
		return nil, errors.New("未找到图片")
	}
	return links, nil
}

// imageLinks 提取 class 含 view_img_link 的 a 标签 href。
func imageLinks(page string) []string {
	var links []string
	for _, tag := range anchorPattern.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2])
		}
		href := strings.TrimSpace(attrs["href"])
		if href == "" || !strings.Contains(attrs["class"], "view_img_link") {
			continue
		}
		links = append(links, normalize(href))
	}
	return links
}

func normalize(href string) string {
	switch {
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "/"):
		return "https://jandan.net" + href
	default:
		return href
	}
}

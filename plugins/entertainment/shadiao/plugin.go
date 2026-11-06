// Package shadiao 沙雕 app：聚合几个公开的文案接口。
package shadiao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

var (
	// 上游地址均为包级变量，测试可替换。
	shadiaoURL      = "https://api.shadiao.pro"
	chpURL          = shadiaoURL + "/chp"
	duURL           = shadiaoURL + "/du"
	pyqURL          = shadiaoURL + "/pyq"
	yduanziURL      = "http://www.yduanzi.com/duanzi/getduanzi"
	chayiURL        = "https://api.lovelive.tools/api/SweetNothings/Web/0"
	ganhaiURL       = "https://api.lovelive.tools/api/SweetNothings/Web/1"
	ergofabulousURL = "https://ergofabulous.org/luther/?"

	ua              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.110 Safari/537.36"
	sdReferer       = "https://api.shadiao.pro"
	yduanziReferer  = "http://www.yduanzi.com/?utm_source=shadiao.app"
	loveliveReferer = "https://lovelive.tools/"

	client = &http.Client{Timeout: 10 * time.Second}

	lutherPattern = regexp.MustCompile(`(?s)<p class="larger">(.+?)</p>`)
	tagPattern    = regexp.MustCompile(`<[^>]+>`)
)

type action struct {
	keyword string
	fetch   func(context.Context) (string, error)
}

var actions = []action{
	{"哄我", func(ctx context.Context) (string, error) { return fetchShadiao(ctx, chpURL) }},
	{"来碗毒鸡汤", func(ctx context.Context) (string, error) { return fetchShadiao(ctx, duURL) }},
	{"发个朋友圈", func(ctx context.Context) (string, error) { return fetchShadiao(ctx, pyqURL) }},
	{"来碗绿茶", func(ctx context.Context) (string, error) { return fetchSweet(ctx, chayiURL) }},
	{"渣我", func(ctx context.Context) (string, error) { return fetchSweet(ctx, ganhaiURL) }},
	{"讲个段子", fetchDuanzi},
	{"马丁路德骂我", fetchLuther},
}

// Definition subscribes to the 沙雕 app triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "shadiao",
		Name:        "沙雕app",
		Version:     "1.0.0",
		Description: "发送「哄我」「渣我」「来碗绿茶」「发个朋友圈」「来碗毒鸡汤」「讲个段子」「马丁路德骂我」获取随机文案",
	}, sdk.MessageEvents, Handle)
}

// Handle dispatches one of the fixed triggers to its upstream API.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	for _, a := range actions {
		if !sdk.MatchFull(text, a.keyword) {
			continue
		}
		out, err := a.fetch(ctx)
		if err != nil {
			return c.Reply(ctx, "获取失败: "+err.Error())
		}
		return c.Reply(ctx, out)
	}
	return nil
}

func fetchShadiao(ctx context.Context, url string) (string, error) {
	var body struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := request(ctx, http.MethodGet, url, sdReferer, &body); err != nil {
		return "", err
	}
	return nonEmpty(body.Data.Text)
}

func fetchSweet(ctx context.Context, url string) (string, error) {
	var body struct {
		ReturnObj struct {
			Content string `json:"content"`
		} `json:"returnObj"`
	}
	if err := request(ctx, http.MethodGet, url, loveliveReferer, &body); err != nil {
		return "", err
	}
	return nonEmpty(body.ReturnObj.Content)
}

func fetchDuanzi(ctx context.Context) (string, error) {
	var body struct {
		Duanzi string `json:"duanzi"`
	}
	if err := request(ctx, http.MethodPost, yduanziURL, yduanziReferer, &body); err != nil {
		return "", err
	}
	return nonEmpty(strings.ReplaceAll(body.Duanzi, "<br>", "\n"))
}

func fetchLuther(ctx context.Context) (string, error) {
	data, err := get(ctx, http.MethodGet, ergofabulousURL, loveliveReferer)
	if err != nil {
		return "", err
	}
	match := lutherPattern.FindStringSubmatch(string(data))
	if len(match) < 2 {
		return "", errors.New("未找到马丁路德的骂人语录")
	}
	return nonEmpty(html.UnescapeString(tagPattern.ReplaceAllString(match[1], "")))
}

func request(ctx context.Context, method, url, referer string, out any) error {
	data, err := get(ctx, method, url, referer)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func get(ctx context.Context, method, url, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<10))
}

func nonEmpty(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("上游返回空内容")
	}
	return text, nil
}

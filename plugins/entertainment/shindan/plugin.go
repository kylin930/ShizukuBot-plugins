// Package shindan 基于 shindanmaker.com 的测定小功能。
package shindan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

var (
	// baseURL 与上游一致，测试可替换。
	baseURL   = "https://shindanmaker.com"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.110 Safari/537.36"

	client = &http.Client{Timeout: 10 * time.Second}

	inputPattern  = regexp.MustCompile(`<input\b[^>]*>`)
	attrPattern   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"`)
	imgPattern    = regexp.MustCompile(`<img\b[^>]*>`)
	brPattern     = regexp.MustCompile(`<br\s*/?>`)
	blockPattern  = regexp.MustCompile(`</(p|div|li|h[1-6]|tr)>`)
	anyTagPattern = regexp.MustCompile(`<[^>]+>`)
)

var commands = []struct {
	trigger string
	id      int64
}{
	{"异世界转生", 587874},
	{"今天是什么少女", 162207},
	{"卖萌", 360578},
	{"今日老婆", 1075116},
	{"黄油角色", 1115465},
}

// Definition subscribes to the five shindan triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:      "shindan",
		Name:    "shindan测定",
		Version: "1.0.0",
		Description: "发送「异世界转生」「今天是什么少女」「卖萌」「今日老婆」「黄油角色」，" +
			"可附带名字，返回 shindanmaker 的测定结果",
	}, sdk.MessageEvents, Handle)
}

// Handle matches a trigger prefix and queries shindanmaker with the given name.
// 没有名字时退回用户 ID；框架没有昵称字段，因此不再读取昵称。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	for _, cmd := range commands {
		name, ok := sdk.MatchPrefix(text, cmd.trigger)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = c.Event.UserID
		}
		out, err := shindan(ctx, cmd.id, name)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		return c.Reply(ctx, out)
	}
	return nil
}

// shindan 抓取页面 token 后提交测定表单，并返回纯文本结果。
func shindan(ctx context.Context, id int64, name string) (string, error) {
	pageURL := fmt.Sprintf("%s/%d", baseURL, id)
	page, cookie, err := fetchPage(ctx, pageURL)
	if err != nil {
		return "", err
	}
	token, err := findToken(page)
	if err != nil {
		return "", err
	}
	now := time.Now()
	seed := fmt.Sprintf("%d%d%d", now.Year(), int(now.Month()), now.Day())
	result, err := submit(ctx, pageURL, cookie, token, name+seed)
	if err != nil {
		return "", err
	}
	text, err := resultText(result)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(text, seed, ""), nil
}

func fetchPage(ctx context.Context, url string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	cookie := ""
	for _, value := range resp.Header.Values("Set-Cookie") {
		pair := strings.SplitN(value, ";", 2)[0]
		if strings.Contains(pair, "_session") {
			cookie = pair
		}
	}
	if cookie == "" {
		return "", "", errors.New("刷新 cookie 时发生错误")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	return string(body), cookie, nil
}

func findToken(page string) (string, error) {
	for _, tag := range inputPattern.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, m := range attrPattern.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2])
		}
		if attrs["name"] == "_token" && attrs["value"] != "" {
			return attrs["value"], nil
		}
	}
	return "", errors.New("刷新 token 时发生错误")
}

func submit(ctx context.Context, url, cookie, token, name string) (string, error) {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for _, field := range [][2]string{
		{"_token", token},
		{"user_input_value_1", name},
		{"randname", "名無しのR"},
		{"type", "name"},
	} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &payload)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func resultText(page string) (string, error) {
	idx := strings.Index(page, `id="shindanResult"`)
	if idx < 0 {
		return "", errors.New("无法查找到结果, 请稍后再试")
	}
	open := strings.LastIndex(page[:idx], "<")
	if open < 0 {
		return "", errors.New("无法查找到结果, 请稍后再试")
	}
	end := strings.Index(page[open:], ">")
	if end < 0 {
		return "", errors.New("无法查找到结果, 请稍后再试")
	}
	tag := tagName(page[open : open+end+1])
	start := open + end + 1
	closeIdx := matchingClose(page, start, tag)
	if closeIdx < 0 {
		return "", errors.New("无法查找到结果, 请稍后再试")
	}
	text := toText(page[start:closeIdx])
	if text == "" {
		return "", errors.New("无法查找到结果, 请稍后再试")
	}
	return text, nil
}

func tagName(tag string) string {
	tag = strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">")
	fields := strings.Fields(tag)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func matchingClose(page string, start int, tag string) int {
	if tag == "" {
		return -1
	}
	openTok, closeTok := "<"+tag, "</"+tag
	depth, pos := 1, start
	for pos < len(page) {
		o := strings.Index(page[pos:], openTok)
		c := strings.Index(page[pos:], closeTok)
		if c < 0 {
			return -1
		}
		if o >= 0 && o < c {
			depth++
			pos += o + len(openTok)
			continue
		}
		depth--
		if depth == 0 {
			return pos + c
		}
		pos += c + len(closeTok)
	}
	return -1
}

func toText(inner string) string {
	inner = imgPattern.ReplaceAllString(inner, "")
	inner = brPattern.ReplaceAllString(inner, "\n")
	inner = blockPattern.ReplaceAllString(inner, "\n")
	inner = anyTagPattern.ReplaceAllString(inner, "")
	inner = html.UnescapeString(inner)
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(inner, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

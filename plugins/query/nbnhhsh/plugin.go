// Package nbnhhsh 能不能好好说话：查询拼音首字母缩写的中文释义。
package nbnhhsh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

// guessURL 与上游 lab.magiconch.com 一致，测试可替换。
var guessURL = "https://lab.magiconch.com/api/nbnhhsh/guess"

var client = &http.Client{Timeout: 10 * time.Second}

// guessPattern 匹配「?yyds」「？？yyds」等缩写查询。
var guessPattern = regexp.MustCompile(`^[?？]{1,2} ?([a-z0-9]+)$`)

type guess struct {
	Trans     []string `json:"trans"`
	Inputting []string `json:"inputting"`
}

// Definition subscribes to the 「?? 缩写」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "nbnhhsh",
		Name:        "拼音首字母释义",
		Version:     "1.0.0",
		Description: "发送「?? 缩写」，查询该拼音首字母缩写的中文释义",
	}, sdk.MessageEvents, Handle)
}

// Handle looks up the abbreviation and replies with its translations.
func Handle(ctx context.Context, c sdk.Call) error {
	m := sdk.MatchRegexp(sdk.Text(c.Event), guessPattern)
	if m == nil {
		return nil
	}
	keyword := m[1]
	values, err := lookup(ctx, keyword)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(values) == 0 {
		return c.Reply(ctx, keyword+": 没有找到释义")
	}
	return c.Reply(ctx, keyword+": "+strings.Join(values, ", "))
}

func lookup(ctx context.Context, keyword string) ([]string, error) {
	form := url.Values{}
	form.Set("text", keyword)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, guessURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	var results []guess
	if err = json.Unmarshal(body, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	if len(results[0].Trans) > 0 {
		return results[0].Trans, nil
	}
	return results[0].Inputting, nil
}

// Package ygocdb 游戏王卡查：通过百鸽 API 查询游戏王卡片信息与卡图。
package ygocdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	// 上游 API 前缀，测试可替换。
	apiPrefix = "https://ygocdb.com/api/v0/?search="
	// 卡图地址前缀，测试可替换。
	picPrefix = "https://cdn.233.momobako.com/ygopro/pics/"
)

var (
	searchURL = apiPrefix
	imageBase = picPrefix
	client    = &http.Client{Timeout: 10 * time.Second}

	// commandPattern 匹配 /ydp、/yds、/ydb 三种查询方式。
	commandPattern = regexp.MustCompile(`^/yd(p|s|b)\s?(.*)$`)
)

type card struct {
	ID     int    `json:"id"`
	CnName string `json:"cn_name"`
	JpName string `json:"jp_name"`
	EnName string `json:"en_name"`
	Text   struct {
		Types string `json:"types"`
		Pdesc string `json:"pdesc"`
		Desc  string `json:"desc"`
	} `json:"text"`
}

type searchResult struct {
	Result []card `json:"result"`
}

// Definition subscribes to the /ydp, /yds and /ydb triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "ygocdb",
		Name:        "游戏王卡查",
		Version:     "1.0.0",
		Description: "发送「/ydp 卡名」「/yds 卡名」「/ydb 卡名」，查询游戏王卡片卡图与效果",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the image and/or text of the first matching card.
func Handle(ctx context.Context, c sdk.Call) error {
	m := sdk.MatchRegexp(sdk.Text(c.Event), commandPattern)
	if m == nil {
		return nil
	}
	mode, keyword := m[1], strings.TrimSpace(m[2])
	if keyword == "" {
		return c.Reply(ctx, "你是想查询「空手假象」吗？")
	}
	result, err := search(ctx, keyword)
	if err != nil {
		return c.Reply(ctx, "[ygocdb]error:"+err.Error())
	}
	if len(result.Result) == 0 {
		return c.Reply(ctx, "没有找到相关的卡片额")
	}
	item := result.Result[0]
	image := imageBase + strconv.Itoa(item.ID) + ".jpg"
	switch mode {
	case "p":
		return c.ReplyImage(ctx, image)
	case "s":
		return c.Reply(ctx, cardtext(item))
	default:
		if err := c.ReplyImage(ctx, image); err != nil {
			return err
		}
		return c.Reply(ctx, cardtext(item))
	}
}

func cardtext(item card) string {
	lines := []string{"中文卡名：\n    " + item.CnName}
	if item.JpName == "" {
		lines = append(lines, "英文卡名：\n    "+item.EnName)
	} else {
		lines = append(lines, "日文卡名：\n    "+item.JpName)
	}
	lines = append(lines, "卡片密码："+strconv.Itoa(item.ID), item.Text.Types)
	if item.Text.Pdesc != "" {
		lines = append(lines, "[灵摆效果]\n"+item.Text.Pdesc)
		if strings.Contains(item.Text.Types, "效果") {
			lines = append(lines, "[怪兽效果]")
		} else {
			lines = append(lines, "[怪兽描述]")
		}
	}
	lines = append(lines, item.Text.Desc)
	return strings.Join(lines, "\n")
}

func search(ctx context.Context, keyword string) (searchResult, error) {
	var result searchResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+url.QueryEscape(keyword), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return result, err
	}
	return result, nil
}

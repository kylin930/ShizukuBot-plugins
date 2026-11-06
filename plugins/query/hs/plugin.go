// Package hs 炉石搜卡：通过 hs.fbigame.com 查询炉石传说卡牌图片。
package hs

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

const (
	// siteURL 与上游一致，测试可替换。
	siteURL = "https://hs.fbigame.com"
	// 卡牌图片地址前缀，测试可替换。
	cardImageBase = "https://res.fbigame.com/hs/v13/"
	// 固定查询参数，与上游页面保持一致。
	queryParams = "mod=get_cards_list&mode=-1&extend=-1&mutil_extend=&hero=-1&rarity=-1&cost=-1&mutil_cost=&techlevel=-1&type=-1&collectible=-1&isbacon=-1&page=1&search_type=1&deckmode=normal"
)

var (
	homeURL   = siteURL
	ajaxURL   = siteURL + "/ajax.php"
	imageBase = cardImageBase
	client    = &http.Client{Timeout: 10 * time.Second}
)

// hashPattern 从首页脚本中提取 AJAX 所需的 hash。
var hashPattern = regexp.MustCompile(`var hash = "([^"]+)"`)

type card struct {
	CardID  string `json:"CardID"`
	AuthKey string `json:"auth_key"`
}

type cardList struct {
	List []card `json:"list"`
}

// Definition subscribes to the 「搜卡」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "hs",
		Name:        "炉石搜卡",
		Version:     "1.0.0",
		Description: "发送「搜卡 卡名」，返回最多 5 张炉石传说卡牌图片",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with up to five matching card images.
func Handle(ctx context.Context, c sdk.Call) error {
	keyword, ok := sdk.MatchPrefix(sdk.Text(c.Event), "搜卡")
	if !ok || keyword == "" {
		return nil
	}
	list, err := search(ctx, keyword)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(list.List) == 0 {
		return c.Reply(ctx, "查询为空！")
	}
	for i, item := range list.List {
		if i >= 5 {
			break
		}
		if err := c.ReplyImage(ctx, imageBase+item.CardID+".png?auth_key="+item.AuthKey); err != nil {
			return err
		}
	}
	return nil
}

func search(ctx context.Context, keyword string) (cardList, error) {
	var list cardList
	hash, err := fetchHash(ctx)
	if err != nil {
		return list, err
	}
	u := ajaxURL + "?" + queryParams + "&hash=" + url.QueryEscape(hash) + "&search=" + url.QueryEscape(keyword)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return list, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return list, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return list, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return list, err
	}
	if err = json.Unmarshal(body, &list); err != nil {
		return list, err
	}
	return list, nil
}

func fetchHash(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, homeURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	m := hashPattern.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("未找到页面 hash")
	}
	return strings.TrimSpace(string(m[1])), nil
}

const userAgent = "Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/86.0.4240.198 Mobile Safari/537.36"

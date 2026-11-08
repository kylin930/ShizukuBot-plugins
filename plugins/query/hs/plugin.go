// Package hs 炉石搜卡：通过 hs.fbigame.com 查询炉石传说卡牌图片。
package hs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// siteURL 与上游一致，测试可替换。
	siteURL = "https://hs.fbigame.com"
	// 卡牌图片地址前缀，测试可替换。
	cardImageBase = "https://res.fbigame.com/hs/v13/"
	// 卡牌查询页，用于回复里的可点击来源链接。
	cardPageURL = "https://fbigame.com/card"
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

// deckPattern 匹配炉石卡组代码，与上游一致。
var deckPattern = regexp.MustCompile(`AAE[a-zA-Z0-9/+=]{70,}`)

type card struct {
	CardID  string `json:"CardID"`
	AuthKey string `json:"auth_key"`
}

type cardList struct {
	List []card `json:"list"`
}

// Definition subscribes to the 「搜卡」 trigger.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "hs",
		Name:        "炉石搜卡",
		Version:     "1.0.0",
		Description: "发送「搜卡 卡名」返回最多 5 张卡牌图片；发送卡组代码（AAE…）生成卡组图",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "hs", Trigger: "搜卡"},
	})
}

// Handle replies with up to five matching card images or a deck image.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if code := deckPattern.FindString(text); code != "" {
		return deckImage(ctx, c, code)
	}
	keyword, ok := sdk.MatchPrefix(text, "搜卡")
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
	// 图片消息不能带链接：先发一条可点击的来源 Markdown，再发卡图。
	link := fmt.Sprintf("[在 fbigame 查看「%s」](%s?search=%s)", keyword, cardPageURL, url.QueryEscape(keyword))
	if err := c.ReplyMarkdown(ctx, link); err != nil {
		if err := c.Reply(ctx, link); err != nil {
			return err
		}
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

// deckResp 是卡组图接口响应。
type deckResp struct {
	Img string `json:"img"`
}

// deckImage 按卡组代码请求 general_deck_image 并发送卡组图。
func deckImage(ctx context.Context, c sdk.Call, code string) error {
	hash, err := fetchHash(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	u := ajaxURL + "?" + queryParams + "&mod=general_deck_image&deck_code=" + url.QueryEscape(code) +
		"&deck_text=&hash=" + url.QueryEscape(hash) + "&search=" + url.QueryEscape(code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.Reply(ctx, "查询失败: 上游返回状态 "+resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	var deck deckResp
	if err := json.Unmarshal(body, &deck); err != nil {
		return c.Reply(ctx, "查询失败: 响应解析失败")
	}
	if deck.Img == "" {
		return c.Reply(ctx, "查询失败: 未获取到卡组图")
	}
	data := deck.Img
	// 兼容接口直接返回 data URI 的情况。
	if strings.HasPrefix(data, "data:") {
		if i := strings.Index(data, ","); i >= 0 {
			data = data[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "="))
	}
	if err != nil {
		return c.Reply(ctx, "查询失败: 卡组图解析失败")
	}
	return c.ReplyImageBytes(ctx, raw)
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

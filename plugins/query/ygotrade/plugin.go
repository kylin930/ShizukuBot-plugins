// Package ygotrade 游戏王卡价查询：通过集换社 API 查询卡片价格与在售卡店。
package ygotrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// 集换社卡价查询接口，测试可替换。
	rarityTradeAPI = "https://api.jihuanshe.com/api/market/search/match-product?game_key=ygo&game_sub_key=ocg&page=1&keyword="
	// 集换社卡店查询接口，测试可替换。
	storeTradeAPI = "https://api.jihuanshe.com/api/market/card-versions/products?game_key=ygo&game_sub_key=ocg&page=1&condition=1&card_version_id="
	// 商品详情页前缀：上游接口未返回网页地址，按商品 ID 拼接。
	productPage = "https://www.jihuanshe.com/market/product/"
	// 集换社接口需要浏览器 UA 与 Referer。
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36"
	referer   = "https://api.jihuanshe.com/"
	// usage 参数缺失时的用法提示。
	usage = "用法：\n查卡价 卡名\n查卡价 卡名 -r 稀有度\n查卡店 卡名\n查卡店 卡名 -r 稀有度"
)

var (
	rarityTradeURL = rarityTradeAPI
	storeTradeURL  = storeTradeAPI
	detailBase     = productPage
	client         = &http.Client{Timeout: 10 * time.Second}

	// errNoCard 与 errNoStore 对应上游「没有找到相关卡片或输入参数错误」。
	errNoCard  = errors.New("没有找到相关卡片或输入参数错误")
	errNoStore = errors.New("没有找到在售卡店")

	// linkPattern 用于把 Markdown 链接降级成纯文本。
	linkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

type apiInfo struct {
	Data []tradeInfo `json:"data"`
}

type tradeInfo struct {
	// 卡片信息
	ID       int    `json:"id"`
	NameCn   string `json:"name_cn"`
	CardID   int    `json:"card_id"`
	Number   string `json:"number"`
	Rarity   string `json:"rarity"`
	ImageURL string `json:"image_url"`
	MinPrice string `json:"min_price"`
	// 卡店信息
	SellerUserID     int    `json:"seller_user_id"`
	SellerUsername   string `json:"seller_username"`
	SellerUserAvatar string `json:"seller_user_avatar"`
	SellerProvince   string `json:"seller_province"`
	SellerCity       string `json:"seller_city"`
	SellerCreditRank string `json:"seller_credit_rank"`
	Quantity         string `json:"quantity"`
	CardVersionImage string `json:"card_version_image"`
}

// Definition subscribes to the 「查卡价」「查卡店」 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "ygotrade",
		Name:        "游戏王卡价查询",
		Version:     "1.0.0",
		Description: "发送「查卡价 卡名」「查卡店 卡名」，通过集换社查询游戏王卡价与在售卡店，可加「-r 稀有度」过滤",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "ygotrade", Trigger: "查卡价"},
	})
}

// Handle dispatches 查卡价 / 查卡店 queries.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if args, ok := sdk.MatchPrefix(text, "查卡价"); ok {
		return handlePrice(ctx, c, args)
	}
	if args, ok := sdk.MatchPrefix(text, "查卡店"); ok {
		return handleStore(ctx, c, args)
	}
	return nil
}

// parseArgs 拆分「卡名 -r 稀有度」，与上游的切分方式一致。
func parseArgs(args string) (cardName, rarity string) {
	cardName, rarity, _ = strings.Cut(strings.TrimSpace(args), " -r ")
	return strings.TrimSpace(cardName), strings.TrimSpace(rarity)
}

func handlePrice(ctx context.Context, c sdk.Call, args string) error {
	cardName, rarity := parseArgs(args)
	if cardName == "" {
		return c.Reply(ctx, "请提供要查询的卡名。\n"+usage)
	}
	list, err := searchCards(ctx, cardName, rarity)
	if err != nil {
		return replyError(ctx, c, err)
	}
	total := len(list)
	// 按价格升序展示全部条目，与上游一致。
	sort.SliceStable(list, func(i, j int) bool {
		return priceValue(list[i].MinPrice) < priceValue(list[j].MinPrice)
	})
	head := fmt.Sprintf("「%s」共 %d 条在售", cardName, total)
	if rarity != "" {
		head += "（稀有度：" + rarity + "）"
	}
	head += "，按价格升序："
	items := make([]string, 0, len(list))
	for _, item := range list {
		items = append(items, priceText(item))
	}
	if err := c.Reply(ctx, head+"\n\n"+strings.Join(items, "\n\n")); err != nil {
		return err
	}
	replyImages(ctx, c, list, false)
	return nil
}

func handleStore(ctx context.Context, c sdk.Call, args string) error {
	cardName, rarity := parseArgs(args)
	if cardName == "" {
		return c.Reply(ctx, "请提供要查询的卡名。\n"+usage)
	}
	if strings.Contains(rarity, " ") || strings.Contains(rarity, "\u3000") {
		return c.Reply(ctx, "卡店查询不支持查找多个稀有度")
	}
	cards, err := searchCards(ctx, cardName, rarity)
	if err != nil {
		return replyError(ctx, c, err)
	}
	stores, err := searchStore(ctx, cards[0].ID)
	if err != nil {
		return replyError(ctx, c, err)
	}
	total := len(stores)
	head := fmt.Sprintf("「%s」共 %d 家在售卡店", cardName, total)
	if rarity != "" {
		head += "（稀有度：" + rarity + "）"
	}
	lines := make([]string, 0, len(stores))
	for i, item := range stores {
		lines = append(lines, storeText(item, i+1))
	}
	text := head + "\n\n" + strings.Join(lines, "\n\n")
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		if err = c.Reply(ctx, plainLinks(text)); err != nil {
			return err
		}
	}
	replyImages(ctx, c, stores, true)
	return nil
}

// replyImages 在文本汇总后逐条补发卡图；上游用转发节点图文，框架没有转发，
// 这里按顺序单独发送图片，单张失败直接跳过。
func replyImages(ctx context.Context, c sdk.Call, list []tradeInfo, store bool) {
	for _, item := range list {
		image := strings.TrimSpace(item.ImageURL)
		if store {
			image = strings.TrimSpace(item.CardVersionImage)
		}
		if image == "" {
			continue
		}
		_ = c.ReplyImage(ctx, image)
	}
}

func replyError(ctx context.Context, c sdk.Call, err error) error {
	if errors.Is(err, errNoCard) || errors.Is(err, errNoStore) {
		return c.Reply(ctx, err.Error())
	}
	return c.Reply(ctx, "查询失败："+err.Error())
}

func priceText(item tradeInfo) string {
	lines := []string{
		"卡名：" + item.NameCn,
		"稀有度：" + item.Rarity,
		"当前最低价：" + displayPrice(item.MinPrice),
	}
	if item.ID != 0 {
		lines = append(lines, "编号："+strconv.Itoa(item.ID))
	}
	if item.Number != "" {
		lines = append(lines, "卡序："+item.Number)
	}
	if item.Quantity != "" {
		lines = append(lines, "在售数量："+item.Quantity)
	}
	return strings.Join(lines, "\n")
}

func storeText(item tradeInfo, index int) string {
	name := item.SellerUsername
	if name == "" {
		name = "未知店铺"
	}
	lines := []string{
		fmt.Sprintf("%d. 卖家名：%s", index, name),
		"价格：" + displayPrice(item.MinPrice),
	}
	if item.SellerUserID != 0 {
		lines = append(lines, "卖家ID："+strconv.Itoa(item.SellerUserID))
	}
	if item.SellerCity != "" {
		lines = append(lines, "地区："+item.SellerCity)
	}
	if item.SellerCreditRank != "" {
		lines = append(lines, "信誉度："+item.SellerCreditRank)
	}
	if item.Quantity != "" {
		lines = append(lines, "在售数量："+item.Quantity)
	}
	if item.ID != 0 {
		lines = append(lines, fmt.Sprintf("链接：[查看商品](%s%d)", detailBase, item.ID))
	}
	return strings.Join(lines, "\n")
}

func displayPrice(price string) string {
	price = strings.TrimSpace(price)
	if price == "" {
		return "未知"
	}
	return "¥" + price
}

// priceValue 把字符串价格转成可排序的数值，无法解析时排到最后。
func priceValue(price string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(price), 64)
	if err != nil {
		return math.MaxFloat64
	}
	return v
}

// plainLinks 把 Markdown 链接降级为「文本 (URL)」，用于平台不支持 Markdown 时。
func plainLinks(s string) string {
	return linkPattern.ReplaceAllString(s, "$1 ($2)")
}

func searchCards(ctx context.Context, keyword, rarity string) ([]tradeInfo, error) {
	var info apiInfo
	u := rarityTradeURL + url.QueryEscape(keyword) + "&rarity=" + url.QueryEscape(rarity)
	if err := getJSON(ctx, u, &info); err != nil {
		return nil, err
	}
	if len(info.Data) == 0 {
		return nil, errNoCard
	}
	return info.Data, nil
}

func searchStore(ctx context.Context, cardID int) ([]tradeInfo, error) {
	var info apiInfo
	u := storeTradeURL + url.QueryEscape(strconv.Itoa(cardID))
	if err := getJSON(ctx, u, &info); err != nil {
		return nil, err
	}
	if len(info.Data) == 0 {
		return nil, errNoStore
	}
	return info.Data, nil
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "application/json")
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

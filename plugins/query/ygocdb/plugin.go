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

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// 上游 API 前缀，测试可替换。
	apiPrefix = "https://ygocdb.com/api/v0/?search="
	// 卡图地址前缀，测试可替换。
	picPrefix = "https://cdn.233.momobako.com/ygopro/pics/"
	// 卡片详情页，用于回复里的可点击链接。
	detailBase = "https://ygocdb.com/card/"
)

const (
	// pageSize 与上游一致，列表每页展示 10 条。
	pageSize = 10
	// pendingTimeout 与上游一致，数字选择有效期为 20 秒。
	pendingTimeout = 20 * time.Second
)

var (
	searchURL = apiPrefix
	imageBase = picPrefix
	client    = &http.Client{Timeout: 10 * time.Second}
	nowFunc   = time.Now

	// commandPattern 匹配 /ydp、/yds、/ydb 三种查询方式。
	commandPattern = regexp.MustCompile(`^/yd(p|s|b)\s?(.*)$`)
)

// pendingSearch 保存一次多结果查询的选择状态。
type pendingSearch struct {
	Cards    []card `json:"cards"`
	Page     int    `json:"page"`
	Deadline int64  `json:"deadline"`
}

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
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "ygocdb",
		Name:        "游戏王卡查",
		Version:     "1.0.0",
		Description: "发送「/ydp 卡名」「/yds 卡名」「/ydb 卡名」，查询游戏王卡片卡图与效果",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "ygocdb", Trigger: "/ydp"},
	}, sdk.WithPermissions("state.read", "state.write"))
}

// Handle 优先消费多结果选择态，否则发起新的查询。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if handled, err := consumePending(ctx, c, text); handled {
		return err
	}
	m := sdk.MatchRegexp(text, commandPattern)
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
	if mode == "p" {
		return c.ReplyImage(ctx, image)
	}
	if len(result.Result) == 1 {
		if mode == "s" {
			return replyCard(ctx, c, item)
		}
		if err := c.ReplyImage(ctx, image); err != nil {
			return err
		}
		return replyCard(ctx, c, item)
	}
	pending := pendingSearch{
		Cards:    result.Result,
		Page:     0,
		Deadline: nowFunc().Add(pendingTimeout).Unix(),
	}
	if err := sdk.SaveJSON(ctx, c.State, pendingKey(c), pending); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	return c.Reply(ctx, listText(pending))
}

// consumePending 处理数字选择、翻页与取消，返回是否已消费该消息。
func consumePending(ctx context.Context, c sdk.Call, text string) (bool, error) {
	pending := pendingSearch{}
	if err := sdk.LoadJSON(ctx, c.State, pendingKey(c), &pending); err != nil {
		return false, nil
	}
	if pending.Deadline == 0 {
		return false, nil
	}
	if nowFunc().Unix() > pending.Deadline {
		clearPending(ctx, c)
		return true, c.Reply(ctx, "等待超时,搜索结束")
	}
	switch text {
	case "取消":
		clearPending(ctx, c)
		return true, c.Reply(ctx, "用户取消,搜索结束")
	case "下一页":
		pending.Deadline = nowFunc().Add(pendingTimeout).Unix()
		notice := nextPage(&pending)
		if err := sdk.SaveJSON(ctx, c.State, pendingKey(c), pending); err != nil {
			return true, c.Reply(ctx, "保存状态失败："+err.Error())
		}
		if notice != "" {
			if err := c.Reply(ctx, notice); err != nil {
				return true, err
			}
		}
		return true, c.Reply(ctx, listText(pending))
	}
	index, err := strconv.Atoi(text)
	if err != nil {
		return false, nil
	}
	if index < 0 || index >= pending.Page*pageSize+pageCount(pending) {
		pending.Deadline = nowFunc().Add(pendingTimeout).Unix()
		_ = sdk.SaveJSON(ctx, c.State, pendingKey(c), pending)
		return true, c.ReplyAt(ctx, "请输入正确的序号", c.Event.UserID)
	}
	item := pending.Cards[index]
	clearPending(ctx, c)
	image := imageBase + strconv.Itoa(item.ID) + ".jpg"
	if err := c.ReplyImage(ctx, image); err != nil {
		return true, err
	}
	return true, replyCard(ctx, c, item)
}

// nextPage 推进页码，到末尾时回到第一页并返回提示。
func nextPage(pending *pendingSearch) string {
	total := len(pending.Cards)
	if total < pageSize+1 {
		return ""
	}
	pending.Page++
	if pending.Page*pageSize >= total {
		pending.Page = 0
		return "已是最后一页，返回到第一页"
	}
	return ""
}

// pageCount 返回当前页应展示的卡片数量。
func pageCount(pending pendingSearch) int {
	start := pending.Page * pageSize
	if start >= len(pending.Cards) {
		return 0
	}
	count := len(pending.Cards) - start
	if count > pageSize {
		count = pageSize
	}
	return count
}

// listText 生成与上游一致的卡名列表文案。
func listText(pending pendingSearch) string {
	start := pending.Page * pageSize
	end := start + pageCount(pending)
	names := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		names = append(names, strconv.Itoa(i)+"."+pending.Cards[i].CnName)
	}
	return "找到" + strconv.Itoa(len(pending.Cards)) + "张相关卡片,当前显示以下卡名：\n" +
		strings.Join(names, "\n") +
		"\n————————————\n输入对应数字获取卡片信息," +
		"\n或回复“取消”、“下一页”指令"
}

func pendingKey(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return "pending:" + c.Event.GroupID + ":" + c.Event.UserID
	}
	return "pending:c2c:" + c.Event.UserID
}

func clearPending(ctx context.Context, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, c.State, pendingKey(c), pendingSearch{})
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
	lines = append(lines, "", "详情：[ygocdb]("+detailBase+strconv.Itoa(item.ID)+")")
	return strings.Join(lines, "\n")
}

// replyCard 发送带详情链接的卡牌文本，平台不支持 Markdown 时退回纯文本。
func replyCard(ctx context.Context, c sdk.Call, item card) error {
	text := cardtext(item)
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
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

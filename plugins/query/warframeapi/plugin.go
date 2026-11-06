// Package warframeapi 星际战甲世界状态：查询平原时间、警报、仲裁与每日特惠。
package warframeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"shizukubot/sdk"
)

// worldStateURL 与上游 api.warframestat.us 一致，测试可替换。
var worldStateURL = "https://api.warframestat.us/pc"

var client = &http.Client{Timeout: 10 * time.Second}

type cycle struct {
	State    string    `json:"state"`
	IsDay    bool      `json:"isDay"`
	IsWarm   bool      `json:"isWarm"`
	Active   string    `json:"active"`
	TimeLeft string    `json:"timeLeft"`
	Expiry   time.Time `json:"expiry"`
}

type worldState struct {
	CetusCycle   cycle `json:"cetusCycle"`
	VallisCycle  cycle `json:"vallisCycle"`
	CambionCycle cycle `json:"cambionCycle"`
	Alerts       []struct {
		Mission struct {
			Node          string `json:"node"`
			Type          string `json:"type"`
			MinEnemyLevel int    `json:"minEnemyLevel"`
			MaxEnemyLevel int    `json:"maxEnemyLevel"`
			Reward        struct {
				AsString string `json:"asString"`
			} `json:"reward"`
		} `json:"mission"`
		Active bool   `json:"active"`
		Eta    string `json:"eta"`
	} `json:"alerts"`
	Arbitration struct {
		Node   string    `json:"node"`
		Type   string    `json:"type"`
		Enemy  string    `json:"enemy"`
		Expiry time.Time `json:"expiry"`
	} `json:"arbitration"`
	DailyDeals []struct {
		Item          string `json:"item"`
		OriginalPrice int    `json:"originalPrice"`
		SalePrice     int    `json:"salePrice"`
		Total         int    `json:"total"`
		Sold          int    `json:"sold"`
		Eta           string `json:"eta"`
	} `json:"dailyDeals"`
}

// Definition subscribes to the 星际战甲 queries.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "warframeapi",
		Name:        "星际战甲世界状态",
		Version:     "1.0.0",
		Description: "发送「地球/金星/火卫二平原时间」「wf警报」「wf仲裁」「wf每日特惠」查询星际战甲世界状态",
	}, sdk.MessageEvents, Handle)
}

// Handle dispatches the supported world-state queries.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	switch {
	case sdk.MatchFull(text, "wf警报"):
		return alerts(ctx, c)
	case sdk.MatchFull(text, "wf仲裁"):
		return arbitration(ctx, c)
	case sdk.MatchFull(text, "wf每日特惠"):
		return deals(ctx, c)
	}
	if prefix, ok := sdk.MatchSuffix(text, "平原时间"); ok {
		return plains(ctx, c, prefix)
	}
	return nil
}

func plains(ctx context.Context, c sdk.Call, prefix string) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "获取服务器时间失败: "+err.Error())
	}
	var name, desc string
	switch prefix {
	case "地球", "夜灵":
		name = "地球平原"
		if w.CetusCycle.IsDay {
			desc = "白天"
		} else {
			desc = "夜晚"
		}
		return c.Reply(ctx, formatPlain(name, desc, w.CetusCycle))
	case "金星", "奥布山谷":
		name = "金星平原"
		if w.VallisCycle.IsWarm {
			desc = "温暖"
		} else {
			desc = "寒冷"
		}
		return c.Reply(ctx, formatPlain(name, desc, w.VallisCycle))
	case "魔胎之境", "火卫二", "火卫":
		name = "火卫二平原"
		desc = w.CambionCycle.Active
		return c.Reply(ctx, formatPlain(name, desc, w.CambionCycle))
	default:
		return c.Reply(ctx, "ERROR: 平原不存在")
	}
}

func formatPlain(name, desc string, t cycle) string {
	left := t.TimeLeft
	if left == "" && !t.Expiry.IsZero() {
		left = time.Until(t.Expiry).Round(time.Second).String()
	}
	return fmt.Sprintf("%s: %s, 下次更新: %s", name, desc, left)
}

func alerts(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(w.Alerts) == 0 {
		return c.Reply(ctx, "当前没有警报")
	}
	items := make([]string, 0, len(w.Alerts))
	for _, v := range w.Alerts {
		items = append(items, fmt.Sprintf("激活: %t\n节点: %s\n类型: %s\n敌人等级: %d~%d\n奖励: %s\n剩余时间: %s",
			v.Active, v.Mission.Node, v.Mission.Type, v.Mission.MinEnemyLevel, v.Mission.MaxEnemyLevel,
			v.Mission.Reward.AsString, v.Eta))
	}
	return c.Reply(ctx, strings.Join(items, "\n\n"))
}

func arbitration(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	a := w.Arbitration
	if a.Node == "" {
		return c.Reply(ctx, "当前没有仲裁")
	}
	minutes := 0
	if !a.Expiry.IsZero() {
		minutes = int(time.Until(a.Expiry).Minutes())
	}
	return c.Reply(ctx, fmt.Sprintf("节点: %s\n类型: %s\n阵营: %s\n剩余时间: %dm", a.Node, a.Type, a.Enemy, minutes))
}

func deals(ctx context.Context, c sdk.Call) error {
	w, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if len(w.DailyDeals) == 0 {
		return c.Reply(ctx, "当前没有每日特惠")
	}
	items := make([]string, 0, len(w.DailyDeals))
	for _, d := range w.DailyDeals {
		items = append(items, fmt.Sprintf("物品: %s\n价格: %d→%d\n数量: (%d/%d)\n时间: %s",
			d.Item, d.OriginalPrice, d.SalePrice, d.Total, d.Sold, d.Eta))
	}
	return c.Reply(ctx, strings.Join(items, "\n\n"))
}

func fetch(ctx context.Context) (worldState, error) {
	var w worldState
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, worldStateURL, nil)
	if err != nil {
		return w, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return w, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return w, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return w, err
	}
	if err = json.Unmarshal(body, &w); err != nil {
		return w, err
	}
	return w, nil
}

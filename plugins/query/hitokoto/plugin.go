// Package hitokoto 一言：从一言 API 取一句随机句子。
package hitokoto

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

// apiURL 与上游 v1.hitokoto.cn 一致，测试可替换。
var apiURL = "https://v1.hitokoto.cn/"

var client = &http.Client{Timeout: 10 * time.Second}

type sentence struct {
	Hitokoto string `json:"hitokoto"`
	From     string `json:"from"`
	FromWho  string `json:"from_who"`
}

// Definition subscribes to the 「一言」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "hitokoto",
		Name:        "一言",
		Version:     "1.0.0",
		Description: "发送「一言」，随机返回一句一言与出处",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with one random sentence. 额外参数会被忽略，保持与上游
// 「一言[xxx]」的宽松行为一致。
func Handle(ctx context.Context, c sdk.Call) error {
	if _, ok := sdk.MatchPrefix(sdk.Text(c.Event), "一言"); !ok {
		return nil
	}
	item, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "获取一言失败: "+err.Error())
	}
	source := item.From
	if item.FromWho != "" && item.From != "" {
		source = item.FromWho + "《" + item.From + "》"
	}
	return c.Reply(ctx, item.Hitokoto+"\n——"+source)
}

func fetch(ctx context.Context) (sentence, error) {
	var item sentence
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return item, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return item, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return item, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal(body, &item); err != nil {
		return item, err
	}
	if strings.TrimSpace(item.Hitokoto) == "" {
		return item, fmt.Errorf("一言内容为空")
	}
	return item, nil
}

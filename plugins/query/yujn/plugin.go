// Package yujn 遇见API：随机返回骚话、情话、美句等文本。
package yujn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"shizukubot/sdk"
)

// 上游接口均为公开接口，测试可替换。
var textEndpoints = map[string]endpoint{
	"随机骚话":  {url: "https://api.yujn.cn/api/saohua.php"},
	"土味情话":  {url: "https://api.yujn.cn/api/qinghua.php"},
	"随机污句子": {url: "https://api.yujn.cn/api/text_wu.php"},
	"随机美句":  {url: "https://api.yujn.cn/api/wenan.php", cleanParagraph: true},
}

var client = &http.Client{Timeout: 10 * time.Second}

type endpoint struct {
	url            string
	cleanParagraph bool
}

// Definition subscribes to the text triggers provided by yujn.cn.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "yujn",
		Name:        "遇见API",
		Version:     "1.0.0",
		Description: "发送「随机骚话」「土味情话」「随机美句」等，随机返回一段文本",
	}, sdk.MessageEvents, Handle)
}

// Handle replies with the text returned by the matching upstream endpoint.
func Handle(ctx context.Context, c sdk.Call) error {
	ep, ok := textEndpoints[sdk.Text(c.Event)]
	if !ok {
		return nil
	}
	text, err := fetch(ctx, ep)
	if err != nil {
		return c.Reply(ctx, "获取失败: "+err.Error())
	}
	return c.Reply(ctx, text)
}

func fetch(ctx context.Context, ep endpoint) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(body))
	if ep.cleanParagraph {
		text = strings.ReplaceAll(text, "<p>", "")
		text = strings.ReplaceAll(text, "</p>", "")
	}
	if text == "" {
		return "", errors.New("上游返回空内容")
	}
	return text, nil
}

// Package kfccrazythursday 疯狂星期四文案。
package kfccrazythursday

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

// crazyURL 与上游一致，可在测试中替换。
var crazyURL = "https://v50.deno.dev/"

var client = &http.Client{Timeout: 10 * time.Second}

// Definition subscribes to the 「疯狂星期四」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "kfc-crazy-thursday",
		Name:        "疯狂星期四",
		Version:     "1.0.0",
		Description: "发送「疯狂星期四」，随机获取一段肯德基疯狂星期四文案",
	}, sdk.MessageEvents, Handle)
}

// Handle fetches one piece of copy from the upstream API.
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.MatchFull(sdk.Text(c.Event), "疯狂星期四") {
		return nil
	}
	text, err := fetch(ctx)
	if err != nil {
		return c.Reply(ctx, "获取疯狂星期四文案失败: "+err.Error())
	}
	return c.Reply(ctx, text)
}

func fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, crazyURL, nil)
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
	if text == "" {
		return "", errors.New("上游返回空内容")
	}
	return text, nil
}

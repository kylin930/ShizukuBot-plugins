// Package emozi 颜文字抽象转写，调用 emozi.seku.su 的公开接口。
package emozi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"shizukubot/sdk"
)

// apiBase 与上游一致，测试可替换。
var apiBase = "https://emozi.seku.su/api/"

var client = &http.Client{Timeout: 10 * time.Second}

type encodeRequest struct {
	Random bool   `json:"random"`
	Text   string `json:"text"`
	Choice []int  `json:"choice,omitempty"`
}

type encodeResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  struct {
		Text   string `json:"text"`
		Choice []int  `json:"choice"`
	} `json:"result"`
}

type decodeRequest struct {
	Force bool   `json:"force"`
	Text  string `json:"text"`
}

type decodeResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  string `json:"result"`
}

// Definition subscribes to the 「抽象转写」「抽象还原」 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "emozi",
		Name:        "颜文字抽象转写",
		Version:     "1.0.0",
		Description: "发送「抽象转写 文段」或「抽象还原 文段」，在中文与颜文字之间转换",
	}, sdk.MessageEvents, Handle)
}

// Handle 处理「抽象转写」与「抽象还原」两个前缀触发。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if input, ok := sdk.MatchPrefix(text, "抽象转写"); ok {
		if input == "" {
			return c.Reply(ctx, "用法：抽象转写 一段中文")
		}
		out, choices, err := encode(ctx, input, nil)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		if len(choices) > 0 {
			// 无法多轮询问多音字，统一取每个字的第一个读音。
			out, _, err = encode(ctx, input, make([]int, len(choices)))
			if err != nil {
				return c.Reply(ctx, "ERROR: "+err.Error())
			}
		}
		if out == "" {
			return c.Reply(ctx, "ERROR: 上游返回空内容")
		}
		return c.Reply(ctx, out)
	}
	if input, ok := sdk.MatchPrefix(text, "抽象还原"); ok {
		if input == "" {
			return c.Reply(ctx, "用法：抽象还原 一段颜文字")
		}
		out, err := decode(ctx, input)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		if out == "" {
			return c.Reply(ctx, "ERROR: 上游返回空内容")
		}
		return c.Reply(ctx, out)
	}
	return nil
}

func encode(ctx context.Context, text string, choices []int) (string, []int, error) {
	var result encodeResponse
	body := encodeRequest{Random: false, Text: text, Choice: choices}
	if err := post(ctx, "encode", body, &result); err != nil {
		return "", nil, err
	}
	if result.Code != 0 {
		return "", nil, fmt.Errorf("%s", result.Message)
	}
	return result.Result.Text, result.Result.Choice, nil
}

func decode(ctx context.Context, text string) (string, error) {
	var result decodeResponse
	if err := post(ctx, "decode", decodeRequest{Force: false, Text: text}, &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s", result.Message)
	}
	return result.Result, nil
}

func post(ctx context.Context, path string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

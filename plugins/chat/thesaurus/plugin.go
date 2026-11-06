// Package thesaurus 词典匹配回复，仅 @ 机器人时触发。
package thesaurus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"shizukubot/sdk"
)

// apiURL 是 AnimeAPI/kimoi 使用的公开词典接口，测试可替换。
// 访问码不再写死在源码中，改由插件配置 api_code 或环境变量
// SHIZUKU_THESAURUS_CODE 提供。
var apiURL = "https://ninex.azurewebsites.net/api/chat"

var client = &http.Client{Timeout: 10 * time.Second}

type response struct {
	Reply      string  `json:"reply"`
	Confidence float64 `json:"confidence"`
}

// Definition subscribes to the 词典匹配 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "thesaurus",
		Name:        "词典匹配回复",
		Version:     "1.0.0",
		Description: "@机器人并发送文本，按词典接口匹配度回复",
	}, sdk.MessageEvents, Handle,
		sdk.WithPermissions("config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"api_code": {
					Type:        "string",
					Description: "词典接口访问码；留空时读取环境变量 SHIZUKU_THESAURUS_CODE",
				},
			},
		}))
}

// Handle queries the dictionary at most four times until a confident match is
// found. 图片消息没有可用的纯文本，因此不会触发。
func Handle(ctx context.Context, c sdk.Call) error {
	if !sdk.Mentioned(c.Event) {
		return nil
	}
	msg := strings.TrimSpace(sdk.Text(c.Event))
	if msg == "" {
		return nil
	}
	code := apiCode(c.Config)
	for i := 0; i < 4; i++ {
		r, err := query(ctx, msg, code)
		if err != nil {
			return nil
		}
		if r.Confidence >= 0.2 && strings.TrimSpace(r.Reply) != "" {
			return c.Reply(ctx, r.Reply)
		}
	}
	return nil
}

func apiCode(config map[string]any) string {
	if code, ok := config["api_code"].(string); ok && strings.TrimSpace(code) != "" {
		return strings.TrimSpace(code)
	}
	return strings.TrimSpace(os.Getenv("SHIZUKU_THESAURUS_CODE"))
}

func query(ctx context.Context, msg, code string) (response, error) {
	var r response
	endpoint := apiURL
	if code != "" {
		endpoint += "?code=" + url.QueryEscape(code)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(msg))
	if err != nil {
		return r, err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	return r, nil
}

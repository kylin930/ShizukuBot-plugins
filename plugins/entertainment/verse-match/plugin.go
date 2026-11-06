// Package versematch 对对联：用户发上联，插件从上游抓取下联。
package versematch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"shizukubot/sdk"
)

// poetURL 与上游一致，测试可替换。
var poetURL = "https://ai-poet.com/"

var (
	client  = &http.Client{Timeout: 30 * time.Second}
	chinese = regexp.MustCompile(`^[\p{Han}。？！，、；：“”‘’（）《》]*$`)
	couplet = regexp.MustCompile(`下联：(.+?)</p>`)
)

// Definition subscribes to the 「对对联」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "verse-match",
		Name:        "对对联",
		Version:     "1.0.0",
		Description: "发送「对对联 上联」，返回匹配的下联",
	}, sdk.MessageEvents, Handle)
}

// Handle validates the upper line and replies with the matched lower line.
func Handle(ctx context.Context, c sdk.Call) error {
	upper, ok := sdk.MatchPrefix(sdk.Text(c.Event), "对对联")
	if !ok {
		return nil
	}
	if upper == "" {
		return c.Reply(ctx, "用法：对对联 春风又绿江南岸")
	}
	if !chinese.MatchString(upper) {
		return c.Reply(ctx, "请不要输入英文或空格哦~")
	}
	lower, err := fetchLower(ctx, upper)
	if err != nil {
		return c.Reply(ctx, "请求过程中发生错误，请稍后再试")
	}
	return c.Reply(ctx, "▲上联:"+upper+"\n================\n▼下联:"+lower)
}

func fetchLower(ctx context.Context, upper string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, poetURL+"?text="+url.QueryEscape(upper), nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	match := couplet.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", fmt.Errorf("未能找到合适的下联")
	}
	return strings.TrimSpace(match[1]), nil
}

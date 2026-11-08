// Package versematch 对对联：用户发上联，插件从上游抓取下联。
package versematch

import (
	"context"
	"errors"
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

// errNoCouplet 表示上游页面可访问但没有解析到下联。
var errNoCouplet = errors.New("未能找到合适的下联")

// poetURL 与上游一致，测试可替换。
var poetURL = "https://ai-poet.com/"

var (
	client  = &http.Client{Timeout: 30 * time.Second}
	chinese = regexp.MustCompile(`^[\p{Han}。？！，、；：“”‘’（）《》]*$`)
	couplet = regexp.MustCompile(`下联：(.+?)</p>`)
)

// Definition subscribes to the 「verse-match」「对对联」 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "verse-match",
		Name:        "对对联",
		Version:     "1.0.0",
		Description: "发送「verse-match 上联」或「对对联 上联」，返回匹配的下联",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "verse-match", Trigger: "verse-match"},
	})
}

// commandArg 解析「verse-match 上联」与别名「对对联 上联」。
func commandArg(text string) (string, bool) {
	for _, cmd := range []string{"verse-match", "对对联"} {
		if arg, ok := sdk.MatchPrefix(text, cmd); ok {
			return arg, true
		}
	}
	return "", false
}

// Handle validates the upper line and replies with the matched lower line.
func Handle(ctx context.Context, c sdk.Call) error {
	upper, ok := commandArg(sdk.Text(c.Event))
	if !ok {
		return nil
	}
	if upper == "" {
		return c.Reply(ctx, "用法：verse-match 春风又绿江南岸")
	}
	if !chinese.MatchString(upper) {
		return c.Reply(ctx, "请不要输入英文或空格哦~")
	}
	if err := c.Reply(ctx, "正在对联中..."); err != nil {
		return err
	}
	lower, err := fetchLower(ctx, upper)
	if errors.Is(err, errNoCouplet) {
		return c.Reply(ctx, "未能找到合适的下联。")
	}
	if err != nil {
		return c.Reply(ctx, "请求过程中发生错误，请稍后再试。")
	}
	text := "▲上联:" + upper + "\n================\n▼下联:" + lower +
		"\n\n来源：[对联网](https://ai-poet.com/)"
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
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
		return "", errNoCouplet
	}
	return strings.TrimSpace(match[1]), nil
}

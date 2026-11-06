// Package github GitHub 仓库搜索：按关键词查询 GitHub 上的第一个仓库。
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shizukubot/sdk"
)

const (
	// API 与上游 api.github.com 一致，测试可替换。
	apiURL = "https://api.github.com/search/repositories"
	// ogURL 仓库预览图地址前缀，测试可替换。
	ogURL = "https://opengraph.githubassets.com/0/"
)

var (
	searchURL   = apiURL
	previewBase = ogURL
	client      = &http.Client{Timeout: 10 * time.Second}
)

type repo struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Watchers    int    `json:"watchers"`
	Forks       int    `json:"forks"`
	OpenIssues  int    `json:"open_issues"`
	Language    string `json:"language"`
	License     struct {
		Key string `json:"key"`
	} `json:"license"`
	PushedAt string `json:"pushed_at"`
	HTMLURL  string `json:"html_url"`
}

type searchResult struct {
	TotalCount int    `json:"total_count"`
	Items      []repo `json:"items"`
}

// Definition subscribes to the 「>github」 trigger.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "github",
		Name:        "GitHub 仓库搜索",
		Version:     "1.0.0",
		Description: "发送「>github 关键词」，返回 GitHub 上匹配的仓库信息",
	}, sdk.MessageEvents, Handle)
}

// Handle searches GitHub and replies with the first matching repository.
// 支持「>github -t 关键词」只看文字与「>github -p 关键词」只看预览图。
func Handle(ctx context.Context, c sdk.Call) error {
	rest, ok := sdk.MatchPrefix(sdk.Text(c.Event), ">github")
	if !ok {
		return nil
	}
	mode := ""
	if strings.HasPrefix(rest, "-") {
		if i := strings.IndexByte(rest, ' '); i > 0 {
			mode = rest[:i]
			rest = strings.TrimSpace(rest[i:])
		}
	}
	keyword := strings.TrimSpace(rest)
	if keyword == "" {
		return c.Reply(ctx, "用法: >github [-p|-t] 关键词")
	}
	item, err := search(ctx, keyword)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if item.FullName == "" {
		return c.Reply(ctx, "没有找到这样的仓库")
	}
	image := previewBase + item.FullName
	if mode == "-p" {
		return c.ReplyImage(ctx, image)
	}
	if err := c.Reply(ctx, format(item)); err != nil {
		return err
	}
	if mode == "-t" {
		return nil
	}
	return c.ReplyImage(ctx, image)
}

func format(item repo) string {
	return fmt.Sprintf("%s\nDescription: %s\nStar/Fork/Issue: %d/%d/%d\nLanguage: %s\nLicense: %s\nLast pushed: %s\nJump: %s",
		item.FullName, item.Description, item.Watchers, item.Forks, item.OpenIssues,
		notnull(item.Language), notnull(strings.ToUpper(item.License.Key)), item.PushedAt, item.HTMLURL)
}

func search(ctx context.Context, keyword string) (repo, error) {
	var result searchResult
	u, err := url.Parse(searchURL)
	if err != nil {
		return repo{}, err
	}
	u.RawQuery = url.Values{"q": []string{keyword}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return repo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ShizukuBot-Plugin")
	resp, err := client.Do(req)
	if err != nil {
		return repo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return repo{}, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return repo{}, err
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return repo{}, err
	}
	if len(result.Items) == 0 {
		return repo{}, nil
	}
	return result.Items[0], nil
}

// notnull 如果传入文本为空，则返回默认值。
func notnull(text string) string {
	if text == "" {
		return "None"
	}
	return text
}

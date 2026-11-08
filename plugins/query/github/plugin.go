// Package github GitHub 仓库搜索：按关键词查询仓库，或解析群里的 GitHub 仓库链接。
package github

import (
	"context"
	"encoding/json"
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

const (
	// API 与上游 api.github.com 一致，测试可替换。
	apiURL = "https://api.github.com/search/repositories"
	// repoAPI 单个仓库接口，用于链接解析，测试可替换。
	repoAPI = "https://api.github.com/repos/"
	// ogURL 仓库预览图地址前缀，测试可替换。
	ogURL = "https://opengraph.githubassets.com/0/"
)

var (
	searchURL   = apiURL
	repoURL     = repoAPI
	previewBase = ogURL
	client      = &http.Client{Timeout: 10 * time.Second}
	// linkRe 匹配群消息里的 GitHub 仓库链接。
	linkRe = regexp.MustCompile(`https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)
	// reservedOwners 是 github.com 下的保留路径，不当作仓库作者。
	reservedOwners = map[string]bool{
		"about": true, "apps": true, "blog": true, "changelog": true, "codespaces": true,
		"collections": true, "contact": true, "customers": true, "dashboard": true,
		"discussions": true, "docs": true, "education": true, "enterprise": true,
		"events": true, "explore": true, "features": true, "issues": true, "join": true,
		"login": true, "marketplace": true, "new": true, "notifications": true,
		"organizations": true, "orgs": true, "partners": true, "pricing": true,
		"pulls": true, "readme": true, "resources": true, "search": true, "security": true,
		"settings": true, "site": true, "solutions": true, "sponsor": true, "status": true,
		"support": true, "team": true, "topics": true, "trending": true, "users": true,
	}
)

type repo struct {
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Watchers    int    `json:"watchers"`
	Stargazers  int    `json:"stargazers_count"`
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

// Definition subscribes to the 「>github」 trigger and GitHub repo links.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "github",
		Name:        "GitHub 仓库搜索",
		Version:     "1.0.0",
		Description: "发送「>github 关键词」搜索仓库；群里发 GitHub 仓库链接时自动解析",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "github", Trigger: ">github"},
	})
}

// Handle searches GitHub and replies with the first matching repository.
// 支持「>github -t 关键词」只看文字与「>github -p 关键词」只看预览图。
// 未命中指令时，若消息里有 GitHub 仓库链接则自动解析该仓库。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	rest, ok := sdk.MatchPrefix(text, ">github")
	if !ok {
		if owner, name, matched := matchLink(text); matched {
			return replyRepo(ctx, c, owner, name)
		}
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
	if err := replyMarkdown(ctx, c, format(item)); err != nil {
		return err
	}
	if mode == "-t" {
		return nil
	}
	return c.ReplyImage(ctx, image)
}

func format(item repo) string {
	stars := item.Watchers
	if stars == 0 {
		stars = item.Stargazers
	}
	return fmt.Sprintf("[%s](%s)\nDescription: %s\nStar/Fork/Issue: %d/%d/%d\nLanguage: %s\nLicense: %s\nLast pushed: %s",
		item.FullName, item.HTMLURL, item.Description, stars, item.Forks, item.OpenIssues,
		notnull(item.Language), notnull(strings.ToUpper(item.License.Key)), item.PushedAt)
}

// matchLink 从消息里提取 GitHub 仓库链接的作者与仓库名。
func matchLink(text string) (string, string, bool) {
	m := linkRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	owner := m[1]
	name := strings.TrimRight(strings.TrimSuffix(m[2], ".git"), ".")
	if name == "" || reservedOwners[strings.ToLower(owner)] {
		return "", "", false
	}
	return owner, name, true
}

// replyRepo 查询单个仓库并回复信息与预览图。
func replyRepo(ctx context.Context, c sdk.Call, owner, name string) error {
	item, err := fetchRepo(ctx, owner, name)
	if err != nil {
		return c.Reply(ctx, "查询失败: "+err.Error())
	}
	if item.FullName == "" {
		return c.Reply(ctx, "没有找到这样的仓库")
	}
	if err := replyMarkdown(ctx, c, format(item)); err != nil {
		return err
	}
	return c.ReplyImage(ctx, previewBase+item.FullName)
}

func fetchRepo(ctx context.Context, owner, name string) (repo, error) {
	var item repo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoURL+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return item, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ShizukuBot-Plugin")
	resp, err := client.Do(req)
	if err != nil {
		return item, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return item, nil
	}
	if resp.StatusCode != http.StatusOK {
		return item, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal(body, &item); err != nil {
		return item, err
	}
	return item, nil
}

// replyMarkdown 发送可点击链接的 Markdown，平台不支持时退回纯文本。
func replyMarkdown(ctx context.Context, c sdk.Call, text string) error {
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
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

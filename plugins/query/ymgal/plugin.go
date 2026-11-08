// Package ymgal 月幕 Galgame：通过月幕开放 API 查询作品信息，并抓取官网 Gal CG / 表情包图包。
package ymgal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// 月幕开放 API 地址，测试可替换。
	tokenBase   = "https://www.ymgal.games/oauth/token"
	searchBase  = "https://www.ymgal.games/open/archive/search-game"
	archiveBase = "https://www.ymgal.games/open/archive"
	randomBase  = "https://www.ymgal.games/open/archive/random-game"
	// 作品详情页，用于回复里的可点击链接。
	detailBase = "https://www.ymgal.games/GA"

	configClientID     = "client_id"
	configClientSecret = "client_secret"

	helpText = "月幕 Galgame：\n" +
		"- gal <关键词> / gal查询 <关键词>：搜索作品信息\n" +
		"- 随机gal：随机推荐一部作品\n" +
		"- 随机galCG / 随机gal表情包：随机发送一个图包\n" +
		"- galCG <关键词> / gal表情包 <关键词>：按标题或简介查找图包\n" +
		"- 更新gal：管理员检查图源、统计可抓取的图包并缓存索引\n" +
		"作品搜索需要在插件配置中填写月幕开放平台的 client_id 与 client_secret。"
)

var (
	tokenURL     = tokenBase
	searchURL    = searchBase
	archiveURL   = archiveBase
	randomURL    = randomBase
	client       = &http.Client{Timeout: 10 * time.Second}
	picFull      = regexp.MustCompile(`(?i)^gal(CG|表情包)$`)
	picRandom    = regexp.MustCompile(`(?i)^随机gal(CG|表情包)$`)
	picKey       = regexp.MustCompile(`(?i)^gal(CG|表情包)\s*(.+)$`)
	queryPattern = regexp.MustCompile(`^gal(?:查询\s*|\s+)(.+)$`)
)

// flexID 兼容月幕 API 把 int64 返回为数字或字符串两种形式。
type flexID int64

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexID(n)
	return nil
}

type game struct {
	GID          flexID `json:"gid"`
	ID           flexID `json:"id"`
	OrgName      string `json:"orgName"`
	Name         string `json:"name"`
	ChineseName  string `json:"chineseName"`
	Introduction string `json:"introduction"`
	MainImg      string `json:"mainImg"`
	ReleaseDate  string `json:"releaseDate"`
}

func (g game) gameID() int64 {
	if g.GID > 0 {
		return int64(g.GID)
	}
	return int64(g.ID)
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Definition subscribes to the ymgal triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "ymgal",
		Name:        "月幕Galgame",
		Version:     "1.1.0",
		Description: "发送「gal 关键词」「随机gal」查询月幕 Galgame 作品；「随机galCG」「随机gal表情包」「galCG 关键词」抓取官网图包",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "gal", Trigger: "gal"},
	}, sdk.WithConfig(sdk.Schema{
		Type:        "object",
		Description: "月幕开放平台凭证，可在 https://www.ymgal.games/developer 获取",
		Properties: map[string]sdk.Schema{
			configClientID:     {Type: "string", Description: "月幕开放平台 client_id"},
			configClientSecret: {Type: "string", Description: "月幕开放平台 client_secret"},
			"admin_users": {
				Type:        "array",
				Description: "允许执行「更新gal」的 member_openid 列表；成员可发送“我的ID”查看",
				Items:       &sdk.Schema{Type: "string"},
				Default:     json.RawMessage(`[]`),
			},
		},
	}))
}

// Handle dispatches the supported ymgal triggers.
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "更新gal") {
		return update(ctx, c)
	}
	if m := sdk.MatchRegexp(text, picRandom); m != nil {
		return randomPack(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, picFull); m != nil {
		return randomPack(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, picKey); m != nil {
		return keyPack(ctx, c, m[1], strings.TrimSpace(m[2]))
	}
	switch {
	case sdk.MatchFull(text, "随机gal"):
		return random(ctx, c)
	case sdk.MatchFull(text, "gal"), sdk.MatchFull(text, "gal帮助"),
		sdk.MatchFull(text, "gal查询"), sdk.MatchFull(text, "月幕gal"):
		return c.Reply(ctx, helpText)
	}
	if m := sdk.MatchRegexp(text, queryPattern); m != nil {
		return search(ctx, c, strings.TrimSpace(m[1]))
	}
	return nil
}

// randomPack 随机发送一个官网图包。
func randomPack(ctx context.Context, c sdk.Call, pictureType string) error {
	pack, err := randomPicPack(ctx, c, pictureType)
	if err != nil {
		return c.Reply(ctx, "发送图包失败："+err.Error())
	}
	return sendPicPack(ctx, c, pack)
}

// keyPack 按关键词匹配图包标题或简介。
func keyPack(ctx context.Context, c sdk.Call, pictureType, key string) error {
	if key == "" {
		return c.Reply(ctx, "请提供关键词，例如：galCG 近月")
	}
	pack, err := findPicPackByKey(ctx, c, pictureType, key)
	if err != nil {
		if errors.Is(err, errPicPackNotFound) {
			return c.Reply(ctx, "暂时没有找到匹配「"+key+"」的"+pictureType+"，换个关键词试试吧")
		}
		return c.Reply(ctx, "查找图包失败："+err.Error())
	}
	return sendPicPack(ctx, c, pack)
}

// update 管理员校验图源并统计图包数量。
func update(ctx context.Context, c sdk.Call) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	msg, err := updatePicSource(ctx, c)
	if err != nil {
		return c.Reply(ctx, "更新失败："+err.Error())
	}
	return c.Reply(ctx, msg)
}

// sendPicPack 先发标题/简介/链接，再逐张发送图片。
func sendPicPack(ctx context.Context, c sdk.Call, pack picPack) error {
	title := pack.Title
	if title == "" {
		title = "图包 " + strconv.FormatInt(pack.ID, 10)
	}
	lines := []string{"图包：" + title}
	if desc := truncate(pack.Description, 200); desc != "" {
		lines = append(lines, "简介："+desc)
	}
	lines = append(lines, "链接："+pack.link())
	if err := c.Reply(ctx, strings.Join(lines, "\n")); err != nil {
		return err
	}
	for i, image := range pack.Images {
		if i >= maxPicsetImages {
			break
		}
		if err := c.ReplyImage(ctx, image); err != nil {
			return err
		}
	}
	return nil
}

const noPermission = "没有权限。请先在插件配置的 admin_users 中加入你的 openid。"

// isAdmin 使用插件配置中的 admin_users 做授权。
func isAdmin(c sdk.Call) bool {
	if c.Event.UserID == "" {
		return false
	}
	for _, id := range adminUsers(c.Config) {
		if id == c.Event.UserID {
			return true
		}
	}
	return false
}

// adminUsers 兼容 []string 与 []any 两种配置形态。
func adminUsers(config map[string]any) []string {
	if config == nil {
		return nil
	}
	switch v := config["admin_users"].(type) {
	case []string:
		return v
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if id, ok := item.(string); ok {
				ids = append(ids, id)
			}
		}
		return ids
	default:
		return nil
	}
}

func search(ctx context.Context, c sdk.Call, keyword string) error {
	if keyword == "" {
		return c.Reply(ctx, helpText)
	}
	if utf8.RuneCountInString(keyword) > 95 {
		return c.Reply(ctx, "关键词太长了，月幕 API 最多支持 95 个字符")
	}
	clientID, secret := credentials(c)
	if clientID == "" || secret == "" {
		return missingCredential(ctx, c)
	}
	token, err := fetchToken(ctx, clientID, secret)
	if err != nil {
		return c.Reply(ctx, "获取月幕访问令牌失败："+err.Error())
	}
	items, err := searchGames(ctx, token, keyword)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	if len(items) == 0 {
		return c.Reply(ctx, "没有找到相关的 Galgame，换个关键词试试吧")
	}
	return replyGame(ctx, c, enrich(ctx, token, items[0]))
}

func random(ctx context.Context, c sdk.Call) error {
	clientID, secret := credentials(c)
	if clientID == "" || secret == "" {
		return missingCredential(ctx, c)
	}
	token, err := fetchToken(ctx, clientID, secret)
	if err != nil {
		return c.Reply(ctx, "获取月幕访问令牌失败："+err.Error())
	}
	g, err := randomGame(ctx, token)
	if err != nil {
		return c.Reply(ctx, "随机获取失败："+err.Error())
	}
	return replyGame(ctx, c, enrich(ctx, token, g))
}

func credentials(c sdk.Call) (string, string) {
	id, _ := c.Config[configClientID].(string)
	secret, _ := c.Config[configClientSecret].(string)
	return strings.TrimSpace(id), strings.TrimSpace(secret)
}

func missingCredential(ctx context.Context, c sdk.Call) error {
	return c.Reply(ctx, "还没有配置月幕开放平台密钥。请在插件配置中填写 client_id 与 client_secret（公共凭证见 https://www.ymgal.games/developer），然后重试。")
}

// enrich 用作品详情补全简介等列表接口没有返回的字段，详情失败时保留列表数据。
func enrich(ctx context.Context, token string, g game) game {
	id := g.gameID()
	if id <= 0 {
		return g
	}
	d, err := findGame(ctx, token, id)
	if err != nil {
		return g
	}
	if d.Name != "" {
		g.Name = d.Name
	}
	if d.ChineseName != "" {
		g.ChineseName = d.ChineseName
	}
	if d.Introduction != "" {
		g.Introduction = d.Introduction
	}
	if d.MainImg != "" {
		g.MainImg = d.MainImg
	}
	if d.ReleaseDate != "" {
		g.ReleaseDate = d.ReleaseDate
	}
	if d.OrgName != "" {
		g.OrgName = d.OrgName
	}
	if d.gameID() > 0 {
		g.GID = flexID(d.gameID())
	}
	return g
}

func replyGame(ctx context.Context, c sdk.Call, g game) error {
	if err := c.Reply(ctx, formatGame(g)); err != nil {
		return err
	}
	if g.MainImg != "" {
		// 封面发送失败不影响已经发出的文字信息。
		_ = c.ReplyImage(ctx, g.MainImg)
	}
	return nil
}

func formatGame(g game) string {
	lines := make([]string, 0, 7)
	name := g.ChineseName
	if name == "" {
		name = g.Name
	}
	if name != "" {
		lines = append(lines, "名称："+name)
	}
	if g.Name != "" && g.Name != name {
		lines = append(lines, "原名："+g.Name)
	}
	if g.OrgName != "" {
		lines = append(lines, "会社："+g.OrgName)
	}
	if g.ReleaseDate != "" {
		lines = append(lines, "发售日："+g.ReleaseDate)
	}
	if intro := truncate(strings.TrimSpace(g.Introduction), 200); intro != "" {
		lines = append(lines, "简介："+intro)
	}
	if g.MainImg != "" {
		lines = append(lines, "封面："+g.MainImg)
	}
	if id := g.gameID(); id > 0 {
		lines = append(lines, "详情："+detailBase+strconv.FormatInt(id, 10))
	}
	if len(lines) == 0 {
		return "月幕没有返回作品信息"
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func fetchToken(ctx context.Context, clientID, secret string) (string, error) {
	u, err := url.Parse(tokenURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("grant_type", "client_credentials")
	q.Set("client_id", clientID)
	q.Set("client_secret", secret)
	q.Set("scope", "public")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json;charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	var t tokenResponse
	if resp.StatusCode != http.StatusOK {
		if json.Unmarshal(body, &t) == nil && t.Error != "" {
			msg := t.ErrorDescription
			if msg == "" {
				msg = t.Error
			}
			return "", errors.New(msg)
		}
		return "", fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return "", err
	}
	if t.AccessToken == "" {
		msg := t.ErrorDescription
		if msg == "" {
			msg = t.Error
		}
		if msg == "" {
			msg = "上游没有返回 access_token"
		}
		return "", errors.New(msg)
	}
	return t.AccessToken, nil
}

func searchGames(ctx context.Context, token, keyword string) ([]game, error) {
	u, err := url.Parse(searchURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("mode", "list")
	q.Set("keyword", keyword)
	q.Set("pageNum", "1")
	q.Set("pageSize", "5")
	u.RawQuery = q.Encode()

	data, err := getAPI(ctx, u.String(), token)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var page struct {
		Result []game `json:"result"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, err
	}
	return page.Result, nil
}

func findGame(ctx context.Context, token string, gid int64) (game, error) {
	u, err := url.Parse(archiveURL)
	if err != nil {
		return game{}, err
	}
	q := u.Query()
	q.Set("gid", strconv.FormatInt(gid, 10))
	u.RawQuery = q.Encode()

	data, err := getAPI(ctx, u.String(), token)
	if err != nil {
		return game{}, err
	}
	var detail struct {
		Game game `json:"game"`
	}
	if err := json.Unmarshal(data, &detail); err != nil {
		return game{}, err
	}
	return detail.Game, nil
}

func randomGame(ctx context.Context, token string) (game, error) {
	u, err := url.Parse(randomURL)
	if err != nil {
		return game{}, err
	}
	q := u.Query()
	q.Set("num", "1")
	u.RawQuery = q.Encode()

	data, err := getAPI(ctx, u.String(), token)
	if err != nil {
		return game{}, err
	}
	var list []game
	if err := json.Unmarshal(data, &list); err != nil {
		return game{}, err
	}
	if len(list) == 0 {
		return game{}, errors.New("月幕没有返回作品")
	}
	return list[0], nil
}

func getAPI(ctx context.Context, rawURL, token string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json;charset=utf-8")
	req.Header.Set("version", "1")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if !env.Success || env.Code != 0 {
		msg := strings.TrimSpace(env.Msg)
		if msg == "" {
			msg = "上游返回错误码 " + strconv.Itoa(env.Code)
		}
		return nil, errors.New(msg)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, nil
	}
	return env.Data, nil
}

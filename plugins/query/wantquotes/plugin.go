// Package wantquotes 据意查句：按语义检索名言、佳句、台词与古谚。
package wantquotes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	stateKey = "wantquotes"
	// semanticAPI 是据意查句的语义检索接口，测试中替换为 httptest 服务。
	semanticAPI = "https://wantquotes.net/api/semantic/"
	// maxKeyword 与上游限制一致，关键词不超过 25 个字符。
	maxKeyword = 25
)

var (
	endpoint = semanticAPI
	client   = &http.Client{Timeout: 10 * time.Second}
	nowFunc  = time.Now

	// typeList 与上游的分类序号保持一致。
	typeList = []string{
		"现", "现-名言", "现-佳句", "现-佳句-文学", "现-佳句-诗歌", "现-佳句-其他",
		"现-网络", "现-台词", "现-台词-影视剧", "现-台词-动漫", "现-台词-综艺",
		"古", "谚", "谚-谚语", "谚-俗语", "谚-惯用语", "歇",
	}
)

// selectionTimeout 与上游一致，分类选择有效期为 10 秒。
const selectionTimeout = 10 * time.Second

// selection 保存一次待选择的查询。
type selection struct {
	Keyword  string `json:"keyword"`
	Deadline int64  `json:"deadline"`
}

type quote struct {
	Quote      string `json:"quote"`
	SourceType string `json:"source_type"`
	Author     string `json:"author"`
	Work       string `json:"work"`
}

type semanticResp struct {
	Quotes []quote `json:"quotes"`
	TopSim float64 `json:"top_sim"`
}

// storedKey 保存管理员配置的「unionid|secret」。
type storedKey struct {
	Key string `json:"key"`
}

// Definition subscribes to 据意查句 指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "wantquotes",
		Name:        "据意查句",
		Version:     "1.0.0",
		Description: "发送「据意查句 关键词」按语义检索名言佳句；管理员可用「设置据意查句 unionid|secret」配置密钥",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "wantquotes", Trigger: "据意查句"},
	},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"admin_users": {
					Type:        "array",
					Description: "允许配置据意查句密钥的 member_openid 列表；成员可发送“我的ID”查看",
					Items:       &sdk.Schema{Type: "string"},
					Default:     json.RawMessage(`[]`),
				},
			},
		}))
}

// Handle 处理设置密钥、分类选择与据意查句指令，未命中时保持静默。
func Handle(ctx context.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	if rest, ok := sdk.MatchPrefix(text, "设置据意查句"); ok {
		return setKey(ctx, c, rest)
	}
	if handled, err := consumeSelection(ctx, c, text); handled {
		return err
	}
	rest, ok := sdk.MatchPrefix(text, "据意查句")
	if !ok {
		return nil
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return c.Reply(ctx, helpText)
	}
	if hasIndex(rest) {
		typ, keyword, ok := parseArg(rest)
		if !ok {
			return c.Reply(ctx, helpText)
		}
		if len([]rune(keyword)) > maxKeyword {
			return c.Reply(ctx, fmt.Sprintf("关键词太长了，最多 %d 个字符。", maxKeyword))
		}
		return query(ctx, c, typ, keyword)
	}
	if len([]rune(rest)) > maxKeyword {
		return c.Reply(ctx, fmt.Sprintf("关键词太长了，最多 %d 个字符。", maxKeyword))
	}
	return startSelection(ctx, c, rest)
}

// startSelection 发送分类序号列表并进入 10 秒选择态。
func startSelection(ctx context.Context, c sdk.Call, keyword string) error {
	if err := sdk.SaveJSON(ctx, c.State, selectionKey(c), selection{
		Keyword:  keyword,
		Deadline: nowFunc().Add(selectionTimeout).Unix(),
	}); err != nil {
		return c.Reply(ctx, "保存状态失败："+err.Error())
	}
	var b strings.Builder
	b.WriteString("请下列选择查询名句的类型\n")
	for i, name := range typeList {
		b.WriteString(strconv.Itoa(i))
		b.WriteString(". ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	return c.Reply(ctx, strings.TrimRight(b.String(), "\n"))
}

// consumeSelection 消费分类选择，超时与上游一致地回退到「现」。
func consumeSelection(ctx context.Context, c sdk.Call, text string) (bool, error) {
	pending := selection{}
	if err := sdk.LoadJSON(ctx, c.State, selectionKey(c), &pending); err != nil {
		return false, nil
	}
	if pending.Deadline == 0 {
		return false, nil
	}
	if nowFunc().Unix() > pending.Deadline {
		clearSelection(ctx, c)
		if err := c.Reply(ctx, "时间太久啦！帮你选择查询名句类型"); err != nil {
			return true, err
		}
		return true, query(ctx, c, typeList[0], pending.Keyword)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		// 上游对非数字回复按 0 处理。
		n = 0
	}
	if n < 0 || n >= len(typeList) {
		return true, c.Reply(ctx, "序号非法!")
	}
	clearSelection(ctx, c)
	return true, query(ctx, c, typeList[n], pending.Keyword)
}

// query 校验密钥后调用接口并展示结果。
func query(ctx context.Context, c sdk.Call, typ, keyword string) error {
	key, err := loadKey(ctx, c)
	if err != nil {
		return c.Reply(ctx, "读取密钥失败："+err.Error())
	}
	if key == "" {
		return c.Reply(ctx, "还没有配置据意查句密钥。管理员可发送「设置据意查句 unionid|secret」。")
	}
	unionID, secret, ok := strings.Cut(key, "|")
	if !ok || unionID == "" || secret == "" {
		return c.Reply(ctx, "据意查句密钥格式不正确，应为 unionid|secret。")
	}
	quotes, err := search(ctx, keyword, typ, unionID, secret)
	if err != nil {
		return c.Reply(ctx, "据意查句查询失败："+err.Error())
	}
	valid := make([]quote, 0, len(quotes))
	for _, q := range quotes {
		if strings.TrimSpace(q.Quote) != "" {
			valid = append(valid, q)
		}
	}
	if len(valid) == 0 {
		return c.Reply(ctx, "没有找到合适的句子。")
	}
	if err := c.Reply(ctx, fmt.Sprintf("据意查句「%s」· %s", keyword, typ)); err != nil {
		return err
	}
	// 与上游一致逐条发送全部句子，不再截断或合并。
	for _, q := range valid {
		text := strings.TrimSpace(q.Quote)
		if credit := creditLine(q); credit != "" {
			text += "\n—— " + credit
		}
		if err := c.Reply(ctx, text); err != nil {
			return err
		}
	}
	return nil
}

func selectionKey(c sdk.Call) string {
	if c.Event.GroupID != "" {
		return "selection:" + c.Event.GroupID + ":" + c.Event.UserID
	}
	return "selection:c2c:" + c.Event.UserID
}

func clearSelection(ctx context.Context, c sdk.Call) {
	_ = sdk.SaveJSON(ctx, c.State, selectionKey(c), selection{})
}

const helpText = "用法：据意查句 关键词\n" +
	"发送后按提示回复分类序号（10 秒内），也可直接指定：据意查句 3 关键词\n" +
	"0. 现 ｜ 1. 现-名言 ｜ 2. 现-佳句 ｜ 3. 现-佳句-文学\n" +
	"4. 现-佳句-诗歌 ｜ 5. 现-佳句-其他 ｜ 6. 现-网络\n" +
	"7. 现-台词 ｜ 8. 现-台词-影视剧 ｜ 9. 现-台词-动漫 ｜ 10. 现-台词-综艺\n" +
	"11. 古 ｜ 12. 谚 ｜ 13. 谚-谚语 ｜ 14. 谚-俗语 ｜ 15. 谚-惯用语 ｜ 16. 歇\n" +
	"首次使用需管理员配置：设置据意查句 unionid|secret"

// hasIndex 判断参数是否以「序号 + 关键词」开头。
func hasIndex(arg string) bool {
	head, tail := arg, ""
	if idx := strings.IndexFunc(arg, unicode.IsSpace); idx >= 0 {
		head, tail = arg[:idx], strings.TrimSpace(arg[idx:])
	}
	if tail == "" {
		return false
	}
	_, err := strconv.Atoi(head)
	return err == nil
}

// parseArg 解析「[序号] 关键词」，未带序号时使用默认分类「现」。
func parseArg(arg string) (typ, keyword string, ok bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", false
	}
	typ = typeList[0]
	head, tail := arg, ""
	if idx := strings.IndexFunc(arg, unicode.IsSpace); idx >= 0 {
		head, tail = arg[:idx], strings.TrimSpace(arg[idx:])
	}
	if n, err := strconv.Atoi(head); err == nil && tail != "" {
		if n < 0 || n >= len(typeList) {
			return "", "", false
		}
		return typeList[n], tail, true
	}
	if tail == "" {
		// 只有一个词：当作关键词；纯数字则视为查看分类列表。
		if _, err := strconv.Atoi(head); err == nil {
			return "", "", false
		}
		return typ, head, true
	}
	return typ, arg, true
}

// creditLine 拼接「作者 《作品》」，缺失部分自动省略。
func creditLine(q quote) string {
	var parts []string
	if author := strings.TrimSpace(q.Author); author != "" && author != "佚名" {
		parts = append(parts, author)
	}
	if work := strings.TrimSpace(q.Work); work != "" {
		parts = append(parts, "《"+work+"》")
	}
	return strings.Join(parts, " ")
}

// search 调用据意查句接口并返回句子列表。
func search(ctx context.Context, keyword, typ, unionID, secret string) ([]quote, error) {
	params := url.Values{}
	params.Set("query", keyword)
	params.Set("type", typ)
	params.Set("unionid", unionID)
	params.Set("secret", secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://wantquotes.net")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil, err
	}
	var rsp semanticResp
	if err := json.Unmarshal(body, &rsp); err != nil {
		return nil, err
	}
	return rsp.Quotes, nil
}

// loadKey 读取已保存的密钥。
func loadKey(ctx context.Context, c sdk.Call) (string, error) {
	var stored storedKey
	if err := sdk.LoadJSON(ctx, c.State, stateKey, &stored); err != nil {
		return "", err
	}
	return strings.TrimSpace(stored.Key), nil
}

// setKey 校验管理员身份后保存密钥。
func setKey(ctx context.Context, c sdk.Call, arg string) error {
	if !isAdmin(c) {
		return c.Reply(ctx, noPermission)
	}
	arg = strings.TrimSpace(arg)
	unionID, secret, ok := strings.Cut(arg, "|")
	if !ok || strings.TrimSpace(unionID) == "" || strings.TrimSpace(secret) == "" {
		return c.Reply(ctx, "用法：设置据意查句 unionid|secret")
	}
	if err := sdk.SaveJSON(ctx, c.State, stateKey, storedKey{Key: strings.TrimSpace(unionID) + "|" + strings.TrimSpace(secret)}); err != nil {
		return c.Reply(ctx, "保存密钥失败："+err.Error())
	}
	return c.Reply(ctx, "据意查句密钥已保存。")
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

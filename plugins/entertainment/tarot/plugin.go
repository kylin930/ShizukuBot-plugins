// Package tarot 塔罗牌：抽牌、解牌与牌阵占卜，牌库内嵌在源码中。
package tarot

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// card 是一张塔罗牌，Description 为正位释义，Reverse 为逆位释义。
type card struct {
	Name        string
	Description string
	Reverse     string
	ImgURL      string
}

// tarotRecord 对应上游 tarots.json 的单个条目。
type tarotRecord struct {
	Name string `json:"name"`
	Info struct {
		Description        string `json:"description"`
		ReverseDescription string `json:"reverseDescription"`
		ImgURL             string `json:"imgUrl"`
	} `json:"info"`
}

//go:embed tarots.json
var tarotsJSON []byte

// formation 是一组牌阵，Positions 与抽牌数量一一对应。
type formation struct {
	Name      string
	Cards     int
	Positions []string
}

const (
	// majorEnd 是大阿卡纳在牌库中的结束下标（0-21），小阿卡纳紧随其后。
	majorEnd = 22
	// deckEnd 是整副牌（78 张）的结束下标。
	deckEnd = 78
)

var (
	// 触发「抽 n 张 xxx」。
	drawPattern = regexp.MustCompile(`^抽(\d{1,2}张)?((?:塔罗牌|大阿(?:尔)?卡纳)|小阿(?:尔)?卡纳)\s?(.*)$`)
	// 触发「解塔罗牌 xxx」。
	explainPattern = regexp.MustCompile(`^解塔罗牌\s?(.*)$`)
	// 触发「xx牌阵 xxx」。
	formationPattern = regexp.MustCompile(`^((?:塔罗|大阿(?:尔)?卡纳)|小阿(?:尔)?卡纳|混合)牌阵\s?(.*)$`)
)

// 抽牌开场白与正逆位文案，保留原始版本措辞。
var (
	reasons  = [...]string{"您抽到的是~\n", "锵锵锵，塔罗牌的预言是~\n", "诶，让我看看您抽到了~\n"}
	position = [...]string{"『正位』", "『逆位』"}
)

// deck 是完整的 78 张塔罗牌，数据来自上游 tarots.json。
var deck []card

func init() {
	var records map[string]tarotRecord
	if err := json.Unmarshal(tarotsJSON, &records); err != nil {
		panic("tarot: invalid tarots data: " + err.Error())
	}
	deck = make([]card, 0, deckEnd)
	for i := 0; i < deckEnd; i++ {
		record, ok := records[strconv.Itoa(i)]
		if !ok {
			panic("tarot: missing card " + strconv.Itoa(i))
		}
		deck = append(deck, card{
			Name:        record.Name,
			Description: record.Info.Description,
			Reverse:     record.Info.ReverseDescription,
			ImgURL:      record.Info.ImgURL,
		})
	}
}

// formations 是可用的牌阵，保留原始版本支持的全部牌阵。
var formations = []formation{
	{"圣三角", 3, []string{"过去", "现在", "未来"}},
	{"时间之流", 4, []string{"过去", "现在", "未来", "建议"}},
	{"四要素", 4, []string{"火", "水", "土", "风"}},
	{"五牌阵", 5, []string{"现状", "挑战", "目标", "建议", "结果"}},
	{"吉普赛十字", 5, []string{"现况", "阻碍", "原因", "建议", "结果"}},
	{"马蹄", 7, []string{"过去", "现在", "未来", "建议", "环境", "期望", "结果"}},
	{"六芒星", 7, []string{"过去", "现在", "未来", "对策", "环境", "他人", "结果"}},
}

// Definition subscribes to the 抽牌 / 解牌 / 牌阵 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "tarot",
		Name:        "塔罗牌",
		Version:     "1.0.0",
		Description: "发送「抽塔罗牌」「抽3张大阿卡纳」「解塔罗牌愚者」或「塔罗牌阵圣三角」进行塔罗占卜",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "tarot", Trigger: "抽塔罗牌"},
	},
		sdk.WithPermissions("config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"ai_base_url": {Type: "string", Description: "OpenAI 兼容接口的 base URL，留空则不启用 AI 解读", Default: json.RawMessage(`""`)},
				"ai_api_key":  {Type: "string", Description: "大模型 API 密钥", Default: json.RawMessage(`""`)},
				"ai_model":    {Type: "string", Description: "使用的模型名，留空时使用 gpt-4o-mini", Default: json.RawMessage(`""`)},
			},
		}))
}

// Handle 按指令分派抽牌、解牌与牌阵。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if match := sdk.MatchRegexp(text, drawPattern); match != nil {
		return handleDraw(ctx, c, match)
	}
	if match := sdk.MatchRegexp(text, explainPattern); match != nil {
		return handleExplain(ctx, c, strings.TrimSpace(match[1]))
	}
	if match := sdk.MatchRegexp(text, formationPattern); match != nil {
		return handleFormation(ctx, c, match[1], strings.TrimSpace(match[2]))
	}
	return nil
}

// drawRange 根据牌组名称返回抽取范围：默认大阿卡纳，含「小」为小阿卡纳，「混合」取全牌库。
func drawRange(cardType string) (start, end int) {
	switch {
	case strings.Contains(cardType, "小"):
		return majorEnd, deckEnd
	case strings.Contains(cardType, "混合"):
		return 0, deckEnd
	default:
		return 0, majorEnd
	}
}

// draw 在指定范围内不放回地随机抽取 n 张牌。
func draw(start, end, n int) []card {
	size := end - start
	if n > size {
		n = size
	}
	cards := make([]card, 0, n)
	for _, i := range rand.Perm(size)[:n] {
		cards = append(cards, deck[start+i])
	}
	return cards
}

// handleDraw 处理「抽[n张]塔罗牌/大阿卡纳/小阿卡纳」，末尾的文本视为询问的事情。
func handleDraw(ctx context.Context, c sdk.Call, match []string) error {
	countText, cardType := match[1], match[2]
	question := strings.TrimSpace(match[3])
	n := 1
	if countText != "" {
		v, err := strconv.Atoi(strings.TrimSuffix(countText, "张"))
		if err != nil {
			return c.Reply(ctx, "张数格式不正确")
		}
		if v <= 0 {
			return c.Reply(ctx, "张数必须为正")
		}
		if v > 20 {
			return c.Reply(ctx, "抽取张数过多")
		}
		n = v
	}
	start, end := drawRange(cardType)
	cards := draw(start, end, n)
	faces := make([]string, 0, len(cards))
	for _, crd := range cards {
		faces = append(faces, describe(crd))
	}
	if len(cards) == 1 {
		text := reasons[rand.IntN(len(reasons))] + faces[0]
		if question != "" {
			text += "\n问：" + question
		}
		if err := c.Reply(ctx, text); err != nil {
			return err
		}
		return maybeInterpret(ctx, c, question, faces)
	}
	var b strings.Builder
	b.WriteString("您抽到了 ")
	b.WriteString(strconv.Itoa(len(cards)))
	b.WriteString(" 张塔罗牌：\n")
	for _, crd := range faces {
		b.WriteString(reasons[rand.IntN(len(reasons))])
		b.WriteString(crd)
		b.WriteString("\n")
	}
	if question != "" {
		b.WriteString("问：")
		b.WriteString(question)
		b.WriteString("\n")
	}
	if err := c.Reply(ctx, strings.TrimRight(b.String(), "\n")); err != nil {
		return err
	}
	return maybeInterpret(ctx, c, question, faces)
}

// describe 生成「正/逆位的『牌名』\n其释义为: 释义」。
func describe(crd card) string {
	p := rand.IntN(2)
	description := crd.Description
	if p == 1 {
		description = crd.Reverse
	}
	return position[p] + "的『" + crd.Name + "』\n其释义为: " + description
}

// handleExplain 处理「解塔罗牌 牌名」，未命中时返回牌库列表。
func handleExplain(ctx context.Context, c sdk.Call, name string) error {
	for _, crd := range deck {
		if crd.Name == name {
			return c.Reply(ctx, name+"的含义是~\n『正位』:"+crd.Description+"\n『逆位』:"+crd.Reverse)
		}
	}
	var b strings.Builder
	b.WriteString("没有找到")
	b.WriteString(name)
	b.WriteString("噢~\n塔罗牌列表\n大阿尔卡纳:\n")
	for i := 0; i < majorEnd; i += 7 {
		if i > 0 {
			b.WriteString("\n")
		}
		names := make([]string, 0, 7)
		for _, crd := range deck[i:min(i+7, majorEnd)] {
			names = append(names, crd.Name)
		}
		b.WriteString(strings.Join(names, " "))
	}
	b.WriteString("\n小阿尔卡纳:\n[圣杯|星币|宝剑|权杖] [一至十|侍从|骑士|王后|国王]")
	return c.Reply(ctx, b.String())
}

// handleFormation 处理「xx牌阵 牌阵名」，按牌阵位置逐张抽牌。
func handleFormation(ctx context.Context, c sdk.Call, cardType, rest string) error {
	var matched *formation
	for i := range formations {
		f := &formations[i]
		if strings.HasPrefix(rest, f.Name) && (matched == nil || len(f.Name) > len(matched.Name)) {
			matched = f
		}
	}
	if matched == nil {
		names := make([]string, 0, len(formations))
		for _, f := range formations {
			names = append(names, f.Name)
		}
		return c.Reply(ctx, "没有找到"+rest+"噢~\n现有牌阵列表: \n"+strings.Join(names, "\n"))
	}
	question := strings.TrimSpace(strings.TrimPrefix(rest, matched.Name))
	start, end := drawRange(cardType)
	cards := draw(start, end, matched.Cards)
	nickname := c.Event.Nickname
	if nickname == "" {
		nickname = "你"
	}
	var b strings.Builder
	b.WriteString(nickname)
	b.WriteString("---")
	b.WriteString(matched.Name)
	b.WriteString("\n")
	faces := make([]string, 0, len(cards))
	for i, crd := range cards {
		face := describe(crd)
		faces = append(faces, matched.Positions[i]+":"+face)
		b.WriteString(matched.Positions[i])
		b.WriteString(":")
		b.WriteString(face)
		b.WriteString("\n")
	}
	if question != "" {
		b.WriteString("问：")
		b.WriteString(question)
		b.WriteString("\n")
	}
	if err := c.Reply(ctx, "少女祈祷中..."); err != nil {
		return err
	}
	if err := c.Reply(ctx, strings.TrimRight(b.String(), "\n")); err != nil {
		return err
	}
	return maybeInterpret(ctx, c, question, faces)
}

// aiConfig 是可选的大模型解读配置。
type aiConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// aiURL 把 base URL 转成 chat/completions 地址，测试时可替换。
var aiURL = func(base string) string {
	return strings.TrimRight(base, "/") + "/chat/completions"
}

var aiClient = &http.Client{Timeout: 30 * time.Second}

// aiConfigFrom 读取插件配置中的大模型字段。
func aiConfigFrom(c sdk.Call) aiConfig {
	get := func(key string) string {
		if c.Config == nil {
			return ""
		}
		value, _ := c.Config[key].(string)
		return strings.TrimSpace(value)
	}
	return aiConfig{BaseURL: get("ai_base_url"), APIKey: get("ai_api_key"), Model: get("ai_model")}
}

func (a aiConfig) enabled() bool { return a.BaseURL != "" && a.APIKey != "" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// maybeInterpret 在配置了 AI 且带问题时追加解读；未配置或失败时静默降级。
func maybeInterpret(ctx context.Context, c sdk.Call, question string, faces []string) error {
	if question == "" {
		return nil
	}
	cfg := aiConfigFrom(c)
	if !cfg.enabled() {
		return nil
	}
	reading, err := interpret(ctx, cfg, question, faces)
	if err != nil || reading == "" {
		return nil
	}
	return c.Reply(ctx, "塔罗解读：\n"+reading)
}

// interpret 调用 OpenAI 兼容的 chat/completions 接口生成解读。
func interpret(ctx context.Context, cfg aiConfig, question string, faces []string) (string, error) {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	request := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: "你是塔罗牌解读师。请结合牌面解读用户的问题，使用中文，控制在 300 字以内。"},
			{Role: "user", Content: "问题：" + question + "\n牌面：\n" + strings.Join(faces, "\n")},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aiURL(cfg.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := aiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI 返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	var result chatResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", errors.New("AI 返回为空")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

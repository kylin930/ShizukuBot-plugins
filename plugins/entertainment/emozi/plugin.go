// Package emozi 颜文字抽象转写，调用 emozi.seku.su 的公开接口。
package emozi

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// apiBase 与上游一致，测试可替换。
var apiBase = "https://emozi.seku.su/api/"

// 登录接口单独暴露为包级变量，便于测试替换。
var (
	saltURL  = apiBase + "getLoginSalt"
	loginURL = apiBase + "login"
)

// siteURL 颜文字站点地址，用于回复里的可点击来源链接。
const siteURL = "https://emozi.seku.su/"

const (
	authKey        = "auth"
	credentialsKey = "credentials"
	authTTL        = time.Hour
	pendingTTL     = 5 * time.Minute
)

var (
	client  = &http.Client{Timeout: 10 * time.Second}
	nowFunc = time.Now
)

// credentials 是持久化的账号密码，来源为插件配置或「抽象登录」指令。
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// authState 保存 token 与过期时间，用于无后台定时器的按需续登。
type authState struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// pendingChoices 保存一次转写中尚未完成的多音字选择。
type pendingChoices struct {
	Text     string `json:"text"`
	Counts   []int  `json:"counts"`
	Picked   []int  `json:"picked"`
	Deadline int64  `json:"deadline"`
}

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

type loginSaltResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  struct {
		Salt string `json:"salt"`
	} `json:"result"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Salt     string `json:"salt"`
}

type loginResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  struct {
		Token string `json:"token"`
	} `json:"result"`
}

// Definition subscribes to the 「抽象转写」「抽象还原」「抽象登录」 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "emozi",
		Name:        "颜文字抽象转写",
		Version:     "1.0.0",
		Description: "发送「抽象转写 文段」或「抽象还原 文段」，在中文与颜文字之间转换；可在配置中填写账号密码或直接使用「抽象登录」",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "emozi", Trigger: "抽象转写"}},
		sdk.WithPermissions("state.read", "state.write", "config.read"),
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				"username": {Type: "string", Description: "emozi 账号，留空则匿名访问", Default: json.RawMessage(`""`)},
				"password": {Type: "string", Description: "emozi 密码，留空则匿名访问", Default: json.RawMessage(`""`)},
				"token":    {Type: "string", Description: "可选的登录 token/cookie，填写后直接作为 Authorization", Default: json.RawMessage(`""`)},
			},
		}))
}

// Handle 处理「抽象登录」「抽象转写」「抽象还原」三个前缀触发。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if handled, err := consumePending(ctx, c, text); handled {
		return err
	}
	if input, ok := sdk.MatchPrefix(text, "抽象登录"); ok {
		return handleLogin(ctx, c, input)
	}
	if input, ok := sdk.MatchPrefix(text, "抽象转写"); ok {
		if input == "" {
			return c.Reply(ctx, "用法：抽象转写 一段中文")
		}
		token := ensureAuth(ctx, c)
		out, choices, err := encode(ctx, token, input, nil)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		if len(choices) > 0 {
			pending := pendingChoices{
				Text:     input,
				Counts:   choices,
				Deadline: nowFunc().Add(pendingTTL).Unix(),
			}
			if err := sdk.SaveJSON(ctx, c.State, pendingKey(c), pending); err != nil {
				return c.Reply(ctx, "保存选择状态失败："+err.Error())
			}
			return c.Reply(ctx, choicePrompt(0, choices[0]))
		}
		if out == "" {
			return c.Reply(ctx, "ERROR: 上游返回空内容")
		}
		return replyEmozi(ctx, c, out)
	}
	if input, ok := sdk.MatchPrefix(text, "抽象还原"); ok {
		if input == "" {
			return c.Reply(ctx, "用法：抽象还原 一段颜文字")
		}
		token := ensureAuth(ctx, c)
		out, err := decode(ctx, token, input)
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		if out == "" {
			return c.Reply(ctx, "ERROR: 上游返回空内容")
		}
		return replyEmozi(ctx, c, out)
	}
	return nil
}

// consumePending 消费多音字逐字选择；只吞掉数字输入，其他指令照常处理。
func consumePending(ctx context.Context, c sdk.Call, text string) (bool, error) {
	pending := pendingChoices{}
	if err := sdk.LoadJSON(ctx, c.State, pendingKey(c), &pending); err != nil {
		return false, nil
	}
	if pending.Deadline == 0 || len(pending.Counts) == 0 {
		return false, nil
	}
	if nowFunc().Unix() > pending.Deadline {
		_ = sdk.SaveJSON(ctx, c.State, pendingKey(c), pendingChoices{})
		return false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return false, nil
	}
	idx := len(pending.Picked)
	if n < 1 || n > pending.Counts[idx] {
		_ = sdk.SaveJSON(ctx, c.State, pendingKey(c), pendingChoices{})
		return true, c.Reply(ctx, "ERROR: 输入越界")
	}
	pending.Picked = append(pending.Picked, n-1)
	if len(pending.Picked) < len(pending.Counts) {
		pending.Deadline = nowFunc().Add(pendingTTL).Unix()
		if err := sdk.SaveJSON(ctx, c.State, pendingKey(c), pending); err != nil {
			return true, c.Reply(ctx, "保存选择状态失败："+err.Error())
		}
		return true, c.Reply(ctx, choicePrompt(len(pending.Picked), pending.Counts[len(pending.Picked)]))
	}
	_ = sdk.SaveJSON(ctx, c.State, pendingKey(c), pendingChoices{})
	token := ensureAuth(ctx, c)
	out, _, err := encode(ctx, token, pending.Text, pending.Picked)
	if err != nil {
		return true, c.Reply(ctx, "ERROR: "+err.Error())
	}
	if out == "" {
		return true, c.Reply(ctx, "ERROR: 上游返回空内容")
	}
	return true, replyEmozi(ctx, c, out)
}

// pendingKey 按会话与用户隔离多音字选择状态。
func pendingKey(c sdk.Call) string {
	return "emozi:pending:" + c.Event.GroupID + ":" + c.Event.UserID
}

// choicePrompt 复刻上游的逐字选择提示。
func choicePrompt(index, count int) string {
	return "请选择第" + strconv.Itoa(index) + "个多音字(1~" + strconv.Itoa(count) + ")"
}

// handleLogin 保存账号密码并立即登录，成功文案与上游一致。
func handleLogin(ctx context.Context, c sdk.Call, input string) error {
	if c.Event.GroupID != "" || c.Event.GuildID != "" || c.Event.ChannelID != "" {
		return nil
	}
	fields := strings.Fields(input)
	if len(fields) < 2 {
		return c.Reply(ctx, "用法：抽象登录 用户名 密码")
	}
	username, password := fields[0], strings.Join(fields[1:], " ")
	token, err := login(ctx, username, password)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	_ = sdk.SaveJSON(ctx, c.State, credentialsKey, credentials{Username: username, Password: password})
	_ = sdk.SaveJSON(ctx, c.State, authKey, authState{Token: token, ExpiresAt: nowFunc().Add(authTTL).Unix()})
	return c.Reply(ctx, "成功")
}

// ensureAuth 在每次请求前按需登录；未配置或登录失败时回退匿名。
func ensureAuth(ctx context.Context, c sdk.Call) string {
	if token := configString(c, "token"); token != "" {
		return token
	}
	cred := loadCredentials(ctx, c)
	if cred.Username == "" || cred.Password == "" {
		return ""
	}
	now := nowFunc()
	var state authState
	_ = sdk.LoadJSON(ctx, c.State, authKey, &state)
	if now.Unix() < state.ExpiresAt {
		return state.Token
	}
	token, err := login(ctx, cred.Username, cred.Password)
	if err != nil {
		// 一小时内不再重试，避免每条消息都打一次登录接口。
		_ = sdk.SaveJSON(ctx, c.State, authKey, authState{ExpiresAt: now.Add(authTTL).Unix()})
		return ""
	}
	_ = sdk.SaveJSON(ctx, c.State, authKey, authState{Token: token, ExpiresAt: now.Add(authTTL).Unix()})
	return token
}

// loadCredentials 优先使用「抽象登录」保存的凭证，其次读取配置。
func loadCredentials(ctx context.Context, c sdk.Call) credentials {
	var saved credentials
	_ = sdk.LoadJSON(ctx, c.State, credentialsKey, &saved)
	if saved.Username != "" && saved.Password != "" {
		return saved
	}
	return credentials{Username: configString(c, "username"), Password: configString(c, "password")}
}

func configString(c sdk.Call, key string) string {
	if c.Config == nil {
		return ""
	}
	value, _ := c.Config[key].(string)
	return strings.TrimSpace(value)
}

// login 复刻上游流程：取 salt，md5(密码+salt)，再换取 token。
func login(ctx context.Context, username, password string) (string, error) {
	var saltResult loginSaltResponse
	if err := requestJSON(ctx, http.MethodGet, saltURL+"?username="+url.QueryEscape(username), "", nil, &saltResult); err != nil {
		return "", err
	}
	if saltResult.Code != 0 {
		return "", errors.New(saltResult.Message)
	}
	sum := md5.Sum([]byte(password + saltResult.Result.Salt))
	challenge := hex.EncodeToString(sum[:])
	var loginResult loginResponse
	body := loginRequest{Username: username, Password: challenge, Salt: saltResult.Result.Salt}
	if err := requestJSON(ctx, http.MethodPost, loginURL, "", body, &loginResult); err != nil {
		return "", err
	}
	if loginResult.Code != 0 {
		return "", errors.New(loginResult.Message)
	}
	if loginResult.Result.Token == "" {
		return "", errors.New("上游返回空 token")
	}
	return loginResult.Result.Token, nil
}

// replyEmozi 在转换结果后附上可点击的来源链接，平台不支持 Markdown 时退回纯文本。
func replyEmozi(ctx context.Context, c sdk.Call, out string) error {
	text := out + "\n\n来源：[emozi](" + siteURL + ")"
	if err := c.ReplyMarkdown(ctx, text); err != nil {
		return c.Reply(ctx, text)
	}
	return nil
}

func encode(ctx context.Context, token, text string, choices []int) (string, []int, error) {
	var result encodeResponse
	body := encodeRequest{Random: false, Text: text, Choice: choices}
	if err := requestJSON(ctx, http.MethodPost, apiBase+"encode", token, body, &result); err != nil {
		return "", nil, err
	}
	if result.Code != 0 {
		return "", nil, fmt.Errorf("%s", result.Message)
	}
	return result.Result.Text, result.Result.Choice, nil
}

func decode(ctx context.Context, token, text string) (string, error) {
	var result decodeResponse
	if err := requestJSON(ctx, http.MethodPost, apiBase+"decode", token, decodeRequest{Force: false, Text: text}, &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s", result.Message)
	}
	return result.Result, nil
}

// requestJSON 发送 JSON 请求并按需附加登录 token。
func requestJSON(ctx context.Context, method, endpoint, token string, in, out any) error {
	var reader io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
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

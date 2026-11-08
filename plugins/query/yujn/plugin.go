// Package yujn 遇见API：随机返回骚话、情话、美句等文本。
package yujn

import (
	"context"
	"encoding/json"
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

// 上游接口均为公开接口，测试可替换。
var textEndpoints = map[string]endpoint{
	"随机骚话":  {url: "https://api.yujn.cn/api/saohua.php"},
	"土味情话":  {url: "https://api.yujn.cn/api/qinghua.php"},
	"随机污句子": {url: "https://api.yujn.cn/api/text_wu.php"},
	"随机美句":  {url: "https://api.yujn.cn/api/wenan.php", cleanParagraph: true},
}

// voiceEndpoints 直接返回音频地址，与上游 message.Record 行为一致。
var voiceEndpoints = map[string]string{
	"御姐撒娇": "https://api.yujn.cn/api/yujie.php",
	"绿茶语音": "https://api.yujn.cn/api/lvcha.php",
	"怼人语音": "https://api.yujn.cn/api/duiren.php",
}

// yuyinURL 是「让lulu说…」的语音合成接口，返回 JSON 中的 url 字段。
var yuyinURL = "https://api.yujn.cn/api/yuyin.php?type=json&from=%s&msg=%s"

var (
	client = &http.Client{Timeout: 10 * time.Second}

	yuyinRe = regexp.MustCompile(`^让(lulu)说([\s\x{4e00}-\x{9fa5}\x{3040}-\x{309F}\x{30A0}-\x{30FF}\w\p{P}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}]+)$`)
)

type endpoint struct {
	url            string
	cleanParagraph bool
}

// Definition subscribes to the text triggers provided by yujn.cn.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "yujn",
		Name:        "遇见API",
		Version:     "1.0.0",
		Description: "发送「随机骚话」「土味情话」「随机美句」等返回文本；「御姐撒娇」「绿茶语音」「怼人语音」与「让lulu说…」返回语音",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "yujn", Trigger: "随机骚话"},
	})
}

// Handle replies with the text, voice or synthesized audio of the matching command.
func Handle(ctx context.Context, c sdk.Call) error {
	command := strings.TrimSpace(sdk.Text(c.Event))
	if ep, ok := textEndpoints[command]; ok {
		text, err := fetch(ctx, ep)
		if err != nil {
			return c.Reply(ctx, "获取失败: "+err.Error())
		}
		keyboard := sdk.NewKeyboard(sdk.NewRow(
			sdk.CommandButton("yujn-more", "换一句", command),
		))
		return c.ReplyKeyboardOrText(ctx, text, keyboard)
	}
	if voice, ok := voiceEndpoints[command]; ok {
		return c.ReplyVoice(ctx, voice)
	}
	if m := yuyinRe.FindStringSubmatch(command); m != nil {
		return speak(ctx, c, m[1], m[2])
	}
	return nil
}

// speak 调用语音合成接口并把返回的 url 作为语音发送。
func speak(ctx context.Context, c sdk.Call, name, message string) error {
	recordURL, err := synthesize(ctx, name, message)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	if strings.TrimSpace(recordURL) == "" {
		return c.Reply(ctx, "ERROR: 语音生成失败")
	}
	return c.ReplyVoice(ctx, recordURL)
}

// synthesize 请求 yuyin 接口并解析 url 字段。
func synthesize(ctx context.Context, name, message string) (string, error) {
	rawURL := fmt.Sprintf(yuyinURL, url.QueryEscape(name), url.QueryEscape(message))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	var rsp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &rsp); err != nil {
		return "", err
	}
	return rsp.URL, nil
}

func fetch(ctx context.Context, ep endpoint) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.url, nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(body))
	if ep.cleanParagraph {
		text = strings.ReplaceAll(text, "<p>", "")
		text = strings.ReplaceAll(text, "</p>", "")
	}
	if text == "" {
		return "", errors.New("上游返回空内容")
	}
	return text, nil
}

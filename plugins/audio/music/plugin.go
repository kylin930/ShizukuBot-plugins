// Package music 点歌：搜索网易云音乐并发送语音。
package music

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

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

var (
	// searchURL 网易云搜索接口，测试中可替换。
	searchURL = "https://music.163.com/api/search/get/web?s=%s&type=1&limit=1"
	// songURL 网易云外链播放地址，测试中可替换。
	songURL = "https://music.163.com/song/media/outer/url?id=%s.mp3"
	// pageURL 歌曲网页地址，作为语音发送失败时的兜底。
	pageURL = "https://music.163.com/#/song?id=%s"
)

// pointRe 匹配「[平台]点歌 [关键词]」，平台最多两个字符。
var pointRe = regexp.MustCompile(`^(.{0,2})点歌\s?(.{1,25})$`)

// song 搜索结果的必要字段。
type song struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Duration int64  `json:"duration"`
	Artists  []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
}

// searchResult 网易云搜索接口的响应结构。
type searchResult struct {
	Result struct {
		Songs []song `json:"songs"`
	} `json:"result"`
}

// Definition subscribes to the 「点歌」triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "music",
		Name:        "点歌",
		Version:     "1.0.0",
		Description: "发送「点歌 晴天」或「网易点歌 晴天」搜索并播放歌曲；其他平台暂不支持",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "music", Trigger: "点歌"}})
}

// Handle 解析平台与关键词，搜索后发送语音。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "点歌") {
		return c.Reply(ctx, "用法：点歌 晴天（目前支持网易云：网易点歌 晴天）")
	}
	m := sdk.MatchRegexp(text, pointRe)
	if m == nil {
		return nil
	}
	platform := strings.TrimSpace(m[1])
	if !supportedPlatform(platform) {
		return c.Reply(ctx, "暂不支持「"+platform+"」点歌，目前只支持网易云，例如「网易点歌 晴天」")
	}
	keyword := strings.TrimSpace(m[2])
	if keyword == "" {
		return c.Reply(ctx, "想听什么歌？例如「点歌 晴天」")
	}

	s, err := search(ctx, keyword)
	if err != nil {
		return c.Reply(ctx, "点歌失败："+err.Error())
	}
	if s == nil {
		return c.Reply(ctx, "没有找到「"+keyword+"」，换个关键词试试？")
	}
	title := songTitle(*s)
	link := fmt.Sprintf(pageURL, strconv.FormatInt(s.ID, 10))
	if err := c.ReplyVoice(ctx, fmt.Sprintf(songURL, strconv.FormatInt(s.ID, 10))); err != nil {
		return c.Reply(ctx, "语音发送失败，可到 "+link+" 收听《"+title+"》")
	}
	return c.Reply(ctx, "正在播放《"+title+"》")
}

// supportedPlatform 目前只支持网易云；空平台同样走网易云。
func supportedPlatform(platform string) bool {
	switch strings.ToLower(platform) {
	case "", "网易", "网易云":
		return true
	default:
		return false
	}
}

// songTitle 拼出「歌名 - 歌手」。
func songTitle(s song) string {
	names := make([]string, 0, len(s.Artists))
	for _, a := range s.Artists {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	if len(names) == 0 {
		return s.Name
	}
	return s.Name + " - " + strings.Join(names, "/")
}

// search 调用网易云搜索接口取第一条结果。
func search(ctx context.Context, keyword string) (*song, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(searchURL, url.QueryEscape(keyword)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("搜索接口返回 " + resp.Status)
	}

	var result searchResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, errors.New("搜索结果解析失败")
	}
	if len(result.Result.Songs) == 0 {
		return nil, nil
	}
	return &result.Result.Songs[0], nil
}

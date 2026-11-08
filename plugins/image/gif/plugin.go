// Package gif 制图
package gif

import (
	stdctx "context"
	"encoding/base64"
	"errors"
	"os"
	"regexp"
	"strings"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

// Definition 返回插件定义
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "gif",
		Name:        "制图",
		Version:     "1.0.0",
		Description: "基于头像的 GIF / PNG 表情制图，支持搓、摸、亲、垃圾桶等命令",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "gif", Trigger: "搓"}},
		sdk.WithPermissions("state.read", "state.write"))
}

var tailQQ = regexp.MustCompile(`(\d+)$`)

// Handle 处理制图命令
func Handle(ctx stdctx.Context, c sdk.Call) error {
	text := strings.TrimSpace(sdk.Text(c.Event))
	if text == "" {
		return nil
	}
	name, rest, ok := matchCommand(text)
	if !ok {
		return nil
	}
	var spec logoSpec
	switch {
	case len(sdk.ImageURLs(c.Event)) > 0:
		spec.imageURL = sdk.ImageURLs(c.Event)[0]
	case len(sdk.MentionUserIDs(c.Event)) > 0:
		spec.qq = sdk.MentionUserIDs(c.Event)[0]
	default:
		if m := tailQQ.FindStringSubmatch(rest); m != nil {
			spec.qq = m[1]
			rest = rest[:len(rest)-len(m[0])]
		}
	}
	if spec.imageURL == "" && spec.qq == "" {
		return nil
	}
	cc := newContext(c.Event.UserID, spec.qq)
	if err := cc.prepareLogos(spec); err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	pic, err := cmdMap[name](cc, strings.Split(rest, " ")...)
	if err != nil {
		return c.Reply(ctx, "ERROR: "+err.Error())
	}
	return sendPicture(ctx, c, pic)
}

func matchCommand(text string) (name, rest string, ok bool) {
	for k := range cmdMap {
		if len(k) > len(name) && strings.HasPrefix(text, k) {
			name = k
		}
	}
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimPrefix(text, name), true
}

func sendPicture(ctx stdctx.Context, c sdk.Call, pic string) error {
	switch {
	case strings.HasPrefix(pic, "base64://"):
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(pic, "base64://"))
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		return c.ReplyImageBytes(ctx, data)
	case strings.HasPrefix(pic, "data:"):
		idx := strings.IndexByte(pic, ',')
		if idx < 0 {
			return c.Reply(ctx, "ERROR: 无法解析图片数据")
		}
		data, err := base64.StdEncoding.DecodeString(pic[idx+1:])
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		return c.ReplyImageBytes(ctx, data)
	case strings.HasPrefix(pic, "file:///"):
		data, err := os.ReadFile(strings.TrimPrefix(pic, "file:///"))
		if err != nil {
			return c.Reply(ctx, "ERROR: "+err.Error())
		}
		return c.ReplyImageBytes(ctx, data)
	case strings.HasPrefix(pic, "http://"), strings.HasPrefix(pic, "https://"):
		return c.ReplyImage(ctx, pic)
	}
	return c.Reply(ctx, "ERROR: "+errors.New("未知的图片格式").Error())
}

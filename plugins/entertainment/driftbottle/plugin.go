// Package driftbottle 漂流瓶：群内投递瓶子并随机捞取。
package driftbottle

import (
	"context"
	"fmt"
	"hash/crc64"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	bottlesKey = "bottles"
	maxKeep    = 100
	minRunes   = 10
	// pickBurst/pickWindow 与上游 ctxext.LimitByGroup 一致：每群 10 秒最多捞 5 次。
	pickBurst  = 5
	pickWindow = 10 * time.Second
)

var (
	nowFunc  = time.Now
	randIntn = rand.Intn
	mu       sync.Mutex
	pickMu   sync.Mutex
	pickHits = map[string][]time.Time{}
)

type bottle struct {
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	Name    string `json:"name"`
	GroupID string `json:"group_id"`
	Time    string `json:"time"`
	Msg     string `json:"msg"`
}

// Definition subscribes to the 漂流瓶 triggers.
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "driftbottle",
		Name:        "漂流瓶",
		Version:     "1.0.0",
		Description: "发送「扔漂流瓶 <内容>」投递瓶子，发送「捞漂流瓶」随机捞取一个瓶子",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{{Name: "driftbottle", Trigger: "捞漂流瓶"}},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle 只处理群消息，私聊不参与漂流瓶。
func Handle(ctx context.Context, c sdk.Call) error {
	if c.Event.GroupID == "" {
		return nil
	}
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "捞漂流瓶") || sdk.MatchFull(text, "pick") {
		return pick(ctx, c)
	}
	if msg, ok := sdk.MatchPrefix(text, "扔漂流瓶"); ok {
		return throw(ctx, c, msg)
	}
	if msg, ok := sdk.MatchPrefix(text, "throw"); ok {
		return throw(ctx, c, msg)
	}
	return nil
}

func throw(ctx context.Context, c sdk.Call, msg string) error {
	msg = strings.TrimSpace(msg)
	if utf8.RuneCountInString(msg) < minRunes {
		return c.Reply(ctx, fmt.Sprintf("需要投递的内容过少（至少%d个字）", minRunes))
	}
	now := nowFunc().Format("2006-01-02 15:04:05")
	raw := fmt.Sprintf("%s_%s_%s_%s", c.Event.GroupID, c.Event.UserID, now, msg)
	b := bottle{
		ID:      strconv.FormatUint(crc64.Checksum([]byte(raw), crc64.MakeTable(crc64.ISO)), 10),
		UserID:  c.Event.UserID,
		Name:    strings.TrimSpace(c.Event.Nickname),
		GroupID: c.Event.GroupID,
		Time:    now,
		Msg:     msg,
	}
	mu.Lock()
	var list []bottle
	_ = sdk.LoadJSON(ctx, c.State, bottlesKey, &list)
	list = append(list, b)
	if len(list) > maxKeep {
		list = list[len(list)-maxKeep:]
	}
	_ = sdk.SaveJSON(ctx, c.State, bottlesKey, list)
	mu.Unlock()
	return c.ReplyKeyboardOrText(ctx, "已经帮你丢出去了哦~", throwKeyboard())
}

func pick(ctx context.Context, c sdk.Call) error {
	if !allowPick(c.Event.GroupID, nowFunc()) {
		return nil
	}
	mu.Lock()
	var list []bottle
	_ = sdk.LoadJSON(ctx, c.State, bottlesKey, &list)
	mu.Unlock()
	if len(list) == 0 {
		return c.Reply(ctx, "海里还没有漂流瓶，先扔一个吧~")
	}
	b := list[randIntn(len(list))]
	return c.ReplyKeyboardOrText(ctx, fmt.Sprintf("试着帮你捞出来了这个~\nID: %s\n投递人: %s\n群号: %s\n时间: %s\n内容: \n%s",
		b.ID, senderLabel(b), b.GroupID, b.Time, b.Msg), pickKeyboard())
}

// senderLabel 展示投递人昵称与 QQ；兼容旧瓶子没有昵称的情况。
func senderLabel(b bottle) string {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return b.UserID
	}
	return name + "(" + b.UserID + ")"
}

// allowPick 按群做滑动窗口限频，超出后静默丢弃。
func allowPick(groupID string, now time.Time) bool {
	pickMu.Lock()
	defer pickMu.Unlock()
	cutoff := now.Add(-pickWindow)
	hits := pickHits[groupID]
	kept := hits[:0]
	for _, hit := range hits {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	if len(kept) >= pickBurst {
		pickHits[groupID] = kept
		return false
	}
	pickHits[groupID] = append(kept, now)
	return true
}

// pickKeyboard 捞出漂流瓶后的「再捞一个」按钮。
func pickKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("bottle-pick", "再捞一个", "捞漂流瓶")))
}

// throwKeyboard 扔瓶成功后的「我也捞一个」按钮。
func throwKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(sdk.CommandButton("bottle-pick", "我也捞一个", "捞漂流瓶")))
}

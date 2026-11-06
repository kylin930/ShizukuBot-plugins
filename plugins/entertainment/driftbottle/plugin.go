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

	"shizukubot/sdk"
)

const (
	bottlesKey = "bottles"
	maxKeep    = 100
	minRunes   = 10
)

var (
	nowFunc  = time.Now
	randIntn = rand.Intn
	mu       sync.Mutex
)

type bottle struct {
	ID      string `json:"id"`
	UserID  string `json:"user_id"`
	GroupID string `json:"group_id"`
	Time    string `json:"time"`
	Msg     string `json:"msg"`
}

// Definition subscribes to the 漂流瓶 triggers.
func Definition() sdk.Definition {
	return sdk.EventPlugin(sdk.Meta{
		ID:          "driftbottle",
		Name:        "漂流瓶",
		Version:     "1.0.0",
		Description: "发送「扔漂流瓶 <内容>」投递瓶子，发送「捞漂流瓶」随机捞取一个瓶子",
	}, sdk.MessageEvents, Handle, sdk.WithPermissions("state.read", "state.write"))
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
	return c.Reply(ctx, "已经帮你丢出去了哦~")
}

func pick(ctx context.Context, c sdk.Call) error {
	mu.Lock()
	var list []bottle
	_ = sdk.LoadJSON(ctx, c.State, bottlesKey, &list)
	mu.Unlock()
	if len(list) == 0 {
		return c.Reply(ctx, "海里还没有漂流瓶，先扔一个吧~")
	}
	b := list[randIntn(len(list))]
	return c.Reply(ctx, fmt.Sprintf("试着帮你捞出来了这个~\nID: %s\n投递人: %s\n群号: %s\n时间: %s\n内容: \n%s",
		b.ID, b.UserID, b.GroupID, b.Time, b.Msg))
}

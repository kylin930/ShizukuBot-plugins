// Package triggercmd 让事件插件在保留原有聊天触发词的同时，额外注册 /指令。
//
// 宿主只在消息以 /指令名 开头时调用命令处理器，且命令名必须匹配
// sdk.ValidID（ASCII 字母、数字、下划线与短横线）。因此中文触发词无法直接
// 注册，这里用 Trigger 把 /指令 还原成插件原本认识的触发文本，再交给原处理器。
package triggercmd

import (
	"context"
	"strings"

	"shizukubot/sdk"
)

// Command 描述一个额外注册的 /指令。
type Command struct {
	// Name 是注册到宿主的命令名，必须符合 sdk.ValidID。
	Name string
	// Trigger 是调用原处理器时合成的触发文本。为空表示不转发给处理器。
	Trigger string
	// Usage 在 Trigger 为空时作为 /指令 的回复；用于纯被动插件的说明。
	Usage string
}

// EventPlugin 注册事件监听与附加的 /指令，权限与 sdk.Define 相同。
// 事件类型会去重；commands 为空时等同于 sdk.EventPlugin。
func EventPlugin(meta sdk.Meta, types []sdk.EventType, handler sdk.EventHandler, commands []Command, options ...sdk.Option) sdk.Definition {
	unique := make([]sdk.EventType, 0, len(types))
	seen := map[sdk.EventType]bool{}
	for _, typ := range types {
		if !seen[typ] {
			seen[typ] = true
			unique = append(unique, typ)
		}
	}
	return sdk.Define(meta, func(r sdk.Registrar) error {
		for _, typ := range unique {
			if err := r.On(typ, handler); err != nil {
				return err
			}
		}
		return register(r, handler, commands)
	}, options...)
}

// Define 只注册 /指令，不监听聊天事件。
func Define(meta sdk.Meta, commands []Command, options ...sdk.Option) sdk.Definition {
	return sdk.Define(meta, func(r sdk.Registrar) error {
		return register(r, nil, commands)
	}, options...)
}

func register(r sdk.Registrar, handler sdk.EventHandler, commands []Command) error {
	for _, command := range commands {
		if err := r.Command(command.Name, Wrap(handler, command)); err != nil {
			return err
		}
	}
	return nil
}

// Wrap 把 /指令 及其参数还原成插件原本的触发文本后交给 handler。
// Trigger 为空时只回复 Usage，避免与监听任意文本的插件重复回复。
func Wrap(handler sdk.EventHandler, command Command) sdk.CommandHandler {
	return func(ctx context.Context, c sdk.Call) error {
		text := strings.TrimSpace(command.Trigger)
		if len(c.Args) > 0 {
			if text == "" {
				text = strings.Join(c.Args, " ")
			} else {
				text += " " + strings.Join(c.Args, " ")
			}
		}
		if command.Trigger == "" {
			if command.Usage == "" {
				return nil
			}
			return c.Reply(ctx, command.Usage)
		}
		if strings.TrimSpace(text) == "" || handler == nil {
			return nil
		}
		event := c.Event
		event.Message = text
		c.Event = event
		return handler(ctx, c)
	}
}

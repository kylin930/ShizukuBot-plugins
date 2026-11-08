// Package plugintest 提供插件测试用的最小 Host，用于捕获 Load 期间注册的命令。
package plugintest

import (
	"context"

	"shizukubot/sdk"
)

// Commands 校验 Definition 并加载到 fake host，返回已注册的命令处理器。
func Commands(def sdk.Definition) (map[string]sdk.CommandHandler, error) {
	if err := def.Validate(); err != nil {
		return nil, err
	}
	r := &registrar{}
	if err := def.New().Load(context.Background(), &host{registrar: r}); err != nil {
		return nil, err
	}
	return r.commands, nil
}

type registrar struct {
	commands map[string]sdk.CommandHandler
}

func (r *registrar) Command(name string, handler sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = handler
	return nil
}

func (r *registrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

type host struct{ *registrar }

func (h *host) Context() context.Context { return context.Background() }
func (h *host) BotID() string            { return "test" }
func (h *host) Config() map[string]any   { return nil }
func (h *host) State() sdk.State         { return nil }
func (h *host) Log(string)               {}

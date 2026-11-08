package endfield

import (
	"fmt"
	"strings"
	"sync"
)

var (
	messageOnce sync.Once
	messageData map[string]any
	messageErr  error
)

// messageTree 懒加载 defSet/message.json。
func messageTree() map[string]any {
	messageOnce.Do(func() {
		messageData = map[string]any{}
		messageErr = loadAssetJSON("defSet/message.json", &messageData)
	})
	return messageData
}

// getMessage 按点号路径读取提示文案并替换 {name} 一类占位符。
func getMessage(path string, params ...map[string]any) string {
	value := lookupMessage(messageTree(), path)
	if value == "" {
		return "[消息未配置: " + path + "]"
	}
	if len(params) == 0 {
		return value
	}
	for key, item := range params[0] {
		value = strings.ReplaceAll(value, "{"+key+"}", fmt.Sprint(item))
	}
	return value
}

func lookupMessage(tree map[string]any, path string) string {
	var current any = tree
	for _, key := range strings.Split(path, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = mapping[key]
		if !ok {
			return ""
		}
	}
	text, _ := current.(string)
	return text
}

// mustMessage 用于测试与调试：确认路径存在。
func mustMessage(path string) error {
	if messageErr != nil {
		return messageErr
	}
	if lookupMessage(messageTree(), path) == "" {
		return fmt.Errorf("missing message: %s", path)
	}
	return nil
}

// Package endfield 移植终末地查询插件：账号绑定、便签、干员、公告、日历、抽卡、Wiki 与 MaaEnd。
package endfield

import (
	"embed"
	"encoding/json"
	"sync"
)

//go:embed assets
var assetsFS embed.FS

func asset(path string) ([]byte, error) {
	return assetsFS.ReadFile("assets/" + path)
}

var (
	assetOnce sync.Once
	assetData map[string][]byte
	assetErr  error
)

// loadAssetJSON 读取并缓存一个 JSON 资源。
func loadAssetJSON(path string, v any) error {
	assetOnce.Do(func() {
		assetData = map[string][]byte{}
	})
	data, ok := assetData[path]
	if !ok {
		data, assetErr = asset(path)
		if assetErr != nil {
			return assetErr
		}
		assetData[path] = data
	}
	return json.Unmarshal(data, v)
}

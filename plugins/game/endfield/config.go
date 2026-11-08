package endfield

import (
	"encoding/json"
	"strconv"
	"strings"

	"shizukubot/sdk"
)

// config 是插件配置，键名与上游 common.yaml / gacha.yaml / sign.yaml 对应（改为 camelCase）。
type config struct {
	APIKey             string
	BackendBaseURL     string
	UseWikiStrategy    bool
	PushStamina        bool
	PushAnnouncement   bool
	GachaSource        string
	SimulateEnabled    bool
	SimulateGroups     []string
	DailyLimitLimited  int
	DailyLimitStandard int
	DailyLimitWeapon   int
	AutoSign           bool
	AutoSignCron       string
	AdminUsers         []string
}

func defaultConfig() config {
	return config{
		APIKey:             "",
		BackendBaseURL:     "https://end-api.shallow.ink",
		UseWikiStrategy:    true,
		PushStamina:        true,
		PushAnnouncement:   true,
		GachaSource:        "backend_api",
		SimulateEnabled:    true,
		DailyLimitLimited:  0,
		DailyLimitStandard: 0,
		DailyLimitWeapon:   0,
		AutoSign:           true,
		AutoSignCron:       "0 0 1 * * ?",
	}
}

func configSchema() sdkSchema {
	return sdkSchema{
		Type: "object",
		Properties: map[string]sdkSchema{
			"apiKey":                 {Type: "string", Description: "终末地协议终端 API 秘钥（https://end.shallow.ink）", Default: json.RawMessage(`""`)},
			"backendBaseUrl":         {Type: "string", Description: "统一后端地址", Default: json.RawMessage(`"https://end-api.shallow.ink"`)},
			"useWikiStrategy":        {Type: "boolean", Description: "是否启用 Wiki 干员攻略", Default: json.RawMessage(`true`)},
			"pushStamina":            {Type: "boolean", Description: "理智订阅推送开关（需要宿主定时任务支持）", Default: json.RawMessage(`true`)},
			"pushAnnouncement":       {Type: "boolean", Description: "公告推送开关（需要宿主定时任务支持）", Default: json.RawMessage(`true`)},
			"gachaSource":            {Type: "string", Description: "卡池信息来源", Default: json.RawMessage(`"backend_api"`), Enum: []any{"backend_api", "local_file"}},
			"simulateEnabled":        {Type: "boolean", Description: "是否启用模拟抽卡", Default: json.RawMessage(`true`)},
			"simulateGroupWhitelist": {Type: "array", Description: "模拟抽卡群白名单，留空表示所有群可用", Items: &sdkSchema{Type: "string"}},
			"dailyLimitLimited":      {Type: "integer", Description: "限定池每日次数，0 表示不限制", Default: json.RawMessage(`0`)},
			"dailyLimitStandard":     {Type: "integer", Description: "常驻池每日次数，0 表示不限制", Default: json.RawMessage(`0`)},
			"dailyLimitWeapon":       {Type: "integer", Description: "武器池每日次数，0 表示不限制", Default: json.RawMessage(`0`)},
			"autoSign":               {Type: "boolean", Description: "自动签到开关（需要宿主定时任务支持）", Default: json.RawMessage(`true`)},
			"autoSignCron":           {Type: "string", Description: "自动签到 cron 表达式", Default: json.RawMessage(`"0 0 1 * * ?"`)},
			"adminUsers":             {Type: "array", Description: "管理员 QQ/用户 ID 列表，用于全员签到、重置抽卡、上传攻略", Items: &sdkSchema{Type: "string"}},
		},
	}
}

func decodeConfig(raw map[string]any) config {
	cfg := defaultConfig()
	if raw == nil {
		return cfg
	}
	readString := func(key string, dst *string) {
		if value, ok := raw[key]; ok {
			if v, ok := value.(string); ok && strings.TrimSpace(v) != "" {
				*dst = strings.TrimSpace(v)
			}
		}
	}
	readBool := func(key string, dst *bool) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case bool:
				*dst = v
			case string:
				if parsed, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
					*dst = parsed
				}
			}
		}
	}
	readInt := func(key string, dst *int) {
		if value, ok := raw[key]; ok {
			switch v := value.(type) {
			case float64:
				*dst = int(v)
			case int:
				*dst = v
			case string:
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					*dst = n
				}
			}
		}
	}
	readList := func(key string, dst *[]string) {
		value, ok := raw[key]
		if !ok {
			return
		}
		switch v := value.(type) {
		case []string:
			*dst = v
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				} else if number, ok := item.(float64); ok {
					out = append(out, strconv.Itoa(int(number)))
				}
			}
			*dst = out
		case string:
			for _, item := range strings.Split(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					*dst = append(*dst, item)
				}
			}
		}
	}
	readString("apiKey", &cfg.APIKey)
	readString("backendBaseUrl", &cfg.BackendBaseURL)
	readBool("useWikiStrategy", &cfg.UseWikiStrategy)
	readBool("pushStamina", &cfg.PushStamina)
	readBool("pushAnnouncement", &cfg.PushAnnouncement)
	readString("gachaSource", &cfg.GachaSource)
	readBool("simulateEnabled", &cfg.SimulateEnabled)
	readList("simulateGroupWhitelist", &cfg.SimulateGroups)
	readInt("dailyLimitLimited", &cfg.DailyLimitLimited)
	readInt("dailyLimitStandard", &cfg.DailyLimitStandard)
	readInt("dailyLimitWeapon", &cfg.DailyLimitWeapon)
	readBool("autoSign", &cfg.AutoSign)
	readString("autoSignCron", &cfg.AutoSignCron)
	readList("adminUsers", &cfg.AdminUsers)
	return cfg
}

func (cfg config) isAdmin(userID string) bool {
	for _, id := range cfg.AdminUsers {
		if id == userID {
			return true
		}
	}
	return false
}

func (cfg config) simulateAllowed(groupID string) bool {
	if !cfg.SimulateEnabled {
		return false
	}
	if len(cfg.SimulateGroups) == 0 {
		return true
	}
	for _, id := range cfg.SimulateGroups {
		if id == groupID {
			return true
		}
	}
	return false
}

// sdkSchema 在本包内别名，避免各处重复写 sdk.Schema。
type sdkSchema = sdk.Schema

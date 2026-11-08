// Package weather 天气查询：多日天气与当日天气。
// 行为对齐上游 koishi-plugin-weather-qq-rainbowsky：
// 城市数据内嵌 cityid.txt / adcode.txt，文案逐字一致。
package weather

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

//go:embed cityid.txt
var cityidData string

//go:embed adcode.txt
var adcodeData string

// 接口地址；测试可替换。
var (
	primaryEndpoint   = "http://t.weather.itboy.net/api/weather/city/"
	secondaryEndpoint = "https://www.haotechs.cn/ljh-wx/weather"
	client            = &http.Client{Timeout: 20 * time.Second}
)

const (
	// configDefaultDatenumber 是多日天气默认天数。
	configDefaultDatenumber = "defaultDatenumber"
	// defaultDatenumber 与上游默认值一致。
	defaultDatenumber = 3
	maxResponseBytes  = 4 << 20
)

var (
	multiDayTrigger = regexp.MustCompile(`^(?:weather-qq-rainbowsky/weather-未来天气|weather-未来天气|未来天气)\s*([\s\S]*)$`)
	todayTrigger    = regexp.MustCompile(`^(?:weather-qq-rainbowsky/weather-当日天气|weather-当日天气|当日天气)\s*([\s\S]*)$`)

	cityOnce sync.Once
	cityMap  map[string]string

	adcodeOnce  sync.Once
	adcodeList  []adcodeEntry
	adcodeIndex map[string]int
)

type adcodeEntry struct {
	name   string
	adcode string
}

// Definition 订阅「未来天气」与「当日天气」。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "weather",
		Name:        "天气查询",
		Version:     "1.0.0",
		Description: "发送「weather-未来天气 城市」查询多日天气，或「weather-当日天气 城市」查询当日天气",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "weather", Trigger: "weather-未来天气"},
	},
		sdk.WithConfig(sdk.Schema{
			Type: "object",
			Properties: map[string]sdk.Schema{
				configDefaultDatenumber: {
					Type:        "number",
					Description: "多日天气查询时返回的天气天数，1 到 15 的正整数",
					Default:     json.RawMessage(`3`),
				},
			},
		}))
}

// Handle 分发天气指令。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if m := sdk.MatchRegexp(text, multiDayTrigger); m != nil {
		return c.Reply(ctx, queryForecast(ctx, strings.TrimSpace(m[1]), c.Config))
	}
	if m := sdk.MatchRegexp(text, todayTrigger); m != nil {
		return c.Reply(ctx, queryToday(ctx, strings.TrimSpace(m[1])))
	}
	return nil
}

// cityIDs 解析 cityid.txt，格式为 城市id=城市名。
func cityIDs() map[string]string {
	cityOnce.Do(func() {
		cityMap = make(map[string]string)
		for _, line := range strings.Split(cityidData, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			cityMap[parts[1]] = parts[0]
		}
	})
	return cityMap
}

// adcodes 解析 adcode.txt，保留插入顺序，重复名称就地更新。
func adcodes() []adcodeEntry {
	adcodeOnce.Do(func() {
		adcodeIndex = make(map[string]int)
		for _, line := range strings.Split(adcodeData, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				continue
			}
			name, code := parts[0], parts[1]
			if idx, ok := adcodeIndex[name]; ok {
				adcodeList[idx].adcode = code
				continue
			}
			adcodeIndex[name] = len(adcodeList)
			adcodeList = append(adcodeList, adcodeEntry{name: name, adcode: code})
		}
	})
	return adcodeList
}

// findAdcode 与上游一致：按插入顺序查找包含关键词的城市，最后一个匹配生效。
func findAdcode(cityName string) string {
	if cityName == "" {
		return ""
	}
	found := ""
	for _, entry := range adcodes() {
		if strings.Contains(entry.name, cityName) {
			found = entry.adcode
		}
	}
	return found
}

// queryForecast 调用 itboy 接口返回多日天气文本。
func queryForecast(ctx context.Context, city string, config map[string]any) string {
	cityid, ok := cityIDs()[city]
	if !ok {
		return "无法找到该城市的cityid，请输入正确的城市名称。"
	}
	body, status, err := get(ctx, primaryEndpoint+url.PathEscape(cityid))
	if err != nil {
		return "查询天气信息时出错: " + err.Error() + "，请稍后重试。"
	}
	if status != http.StatusOK {
		return "获取天气信息失败，请检查输入的城市名称是否正确。"
	}
	var payload forecastResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "查询天气信息时出错: " + err.Error() + "，请稍后重试。"
	}

	days := dayCount(config)
	var b strings.Builder
	fmt.Fprintf(&b, "地区：%s\n (更新时间：%s)\n", payload.CityInfo.City, payload.CityInfo.UpdateTime)
	limit := len(payload.Data.Forecast)
	if days < limit {
		limit = days
	}
	if limit < 0 {
		limit = 0
	}
	for i := 0; i < limit; i++ {
		forecast := payload.Data.Forecast[i]
		b.WriteString("☁️==================\n")
		fmt.Fprintf(&b, "日期：%s (周%s)\n", forecast.YMD, strings.Replace(forecast.Week, "星期", "", 1))
		fmt.Fprintf(&b, "天气：%s\n", forecast.Type)
		fmt.Fprintf(&b, "温度：%s - %s\n", strings.Replace(forecast.Low, "低温 ", "", 1), strings.Replace(forecast.High, "高温 ", "", 1))
		fmt.Fprintf(&b, "风向：%s\n", forecast.FX)
		fmt.Fprintf(&b, "风力：%s\n", forecast.FL)
	}
	return b.String()
}

type forecastResponse struct {
	CityInfo struct {
		City       string `json:"city"`
		UpdateTime string `json:"updateTime"`
	} `json:"cityInfo"`
	Data struct {
		Forecast []struct {
			YMD  string `json:"ymd"`
			Week string `json:"week"`
			Type string `json:"type"`
			Low  string `json:"low"`
			High string `json:"high"`
			FX   string `json:"fx"`
			FL   string `json:"fl"`
		} `json:"forecast"`
	} `json:"data"`
}

// queryToday 调用 haotechs 接口返回当日天气文本。
func queryToday(ctx context.Context, city string) string {
	adcode := findAdcode(city)
	if adcode == "" {
		return "无法找到该城市的adcode，请输入正确的城市名称。"
	}
	body, status, err := get(ctx, secondaryEndpoint+"?adcode="+url.QueryEscape(adcode))
	if err != nil {
		return "查询天气信息时出错，请稍后重试。"
	}
	if status != http.StatusOK {
		return "查询天气信息时出错，服务器返回状态码：" + fmt.Sprint(status)
	}
	var payload todayResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "查询天气信息时出错，请稍后重试。"
	}
	if payload.Code != 0 || payload.Message != "操作成功！" || payload.Result == nil {
		return "获取天气信息失败，请检查输入的城市名称是否正确。"
	}
	r := payload.Result
	var b strings.Builder
	fmt.Fprintf(&b, "地区：%s%s\n", r.Province, r.City)
	b.WriteString("报告时间：\n")
	fmt.Fprintf(&b, "%s\n", r.ReportTime)
	b.WriteString("☁️==================\n")
	fmt.Fprintf(&b, "天气：%s\n", r.Weather)
	fmt.Fprintf(&b, "温度：%v℃\n", r.Temperature)
	fmt.Fprintf(&b, "风向：%s风\n", r.WindDirection)
	fmt.Fprintf(&b, "风力：%v级\n", r.WindPower)
	fmt.Fprintf(&b, "湿度：%v%%", r.Humidity)
	return b.String()
}

type todayResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  *struct {
		Province      string `json:"province"`
		City          string `json:"city"`
		Weather       string `json:"weather"`
		Temperature   any    `json:"temperature"`
		WindDirection string `json:"winddirection"`
		WindPower     any    `json:"windpower"`
		Humidity      any    `json:"humidity"`
		ReportTime    string `json:"reporttime"`
	} `json:"result"`
}

// get 发起 GET 请求并返回响应体与状态码。
func get(ctx context.Context, address string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusOK && len(body) == 0 {
		return nil, resp.StatusCode, errors.New("响应内容为空")
	}
	return body, resp.StatusCode, nil
}

// dayCount 读取配置天数，0 或缺失时回落到默认值（对齐 `config.defaultDatenumber || 3`）。
func dayCount(config map[string]any) int {
	switch v := config[configDefaultDatenumber].(type) {
	case int:
		if v == 0 {
			return defaultDatenumber
		}
		return v
	case int64:
		if v == 0 {
			return defaultDatenumber
		}
		return int(v)
	case float64:
		if v == 0 {
			return defaultDatenumber
		}
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil || n == 0 {
			return defaultDatenumber
		}
		return int(n)
	}
	return defaultDatenumber
}

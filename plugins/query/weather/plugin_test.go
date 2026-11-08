package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot-plugins/internal/plugintest"
	"shizukubot/sdk"
)

func TestDefinitionRegistersCommands(t *testing.T) {
	commands, err := plugintest.Commands(Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"weather"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

type testEnv struct {
	replies []sdk.Reply
}

func (e *testEnv) run(t *testing.T, text string, cfg map[string]any) {
	t.Helper()
	e.replies = nil
	ev := sdk.Event{Message: text, Reply: func(_ context.Context, r sdk.Reply) error {
		e.replies = append(e.replies, r)
		return nil
	}}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: cfg}); err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) text(t *testing.T) string {
	t.Helper()
	if len(e.replies) == 0 {
		t.Fatalf("no replies")
	}
	return e.replies[len(e.replies)-1].Text
}

func usePrimary(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	old := primaryEndpoint
	primaryEndpoint = ts.URL + "/api/weather/city/"
	t.Cleanup(func() {
		primaryEndpoint = old
		ts.Close()
	})
	return ts
}

func useSecondary(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	old := secondaryEndpoint
	secondaryEndpoint = ts.URL
	t.Cleanup(func() {
		secondaryEndpoint = old
		ts.Close()
	})
	return ts
}

func TestForecast(t *testing.T) {
	usePrimary(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/101010100") {
			t.Errorf("path = %q, want cityid 101010100", r.URL.Path)
		}
		w.Write([]byte(`{"cityInfo":{"city":"北京","updateTime":"2026-11-08 10:00"},"data":{"forecast":[
{"ymd":"2026-11-08","week":"星期日","type":"晴","low":"低温 5℃","high":"高温 15℃","fx":"北风","fl":"<3级"},
{"ymd":"2026-11-09","week":"星期一","type":"多云","low":"低温 6℃","high":"高温 16℃","fx":"南风","fl":"3级"}
]}}`))
	})
	e := &testEnv{}
	e.run(t, "weather-未来天气 北京", nil)
	want := "地区：北京\n (更新时间：2026-11-08 10:00)\n" +
		"☁️==================\n日期：2026-11-08 (周日)\n天气：晴\n温度：5℃ - 15℃\n风向：北风\n风力：<3级\n" +
		"☁️==================\n日期：2026-11-09 (周一)\n天气：多云\n温度：6℃ - 16℃\n风向：南风\n风力：3级\n"
	if got := e.text(t); got != want {
		t.Fatalf("forecast reply =\n%q\nwant\n%q", got, want)
	}
}

func TestForecastDayLimit(t *testing.T) {
	usePrimary(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cityInfo":{"city":"北京","updateTime":"t"},"data":{"forecast":[
{"ymd":"d1","week":"星期一","type":"晴","low":"低温 1℃","high":"高温 2℃","fx":"风","fl":"1级"},
{"ymd":"d2","week":"星期二","type":"晴","low":"低温 1℃","high":"高温 2℃","fx":"风","fl":"1级"},
{"ymd":"d3","week":"星期三","type":"晴","low":"低温 1℃","high":"高温 2℃","fx":"风","fl":"1级"}
]}}`))
	})
	e := &testEnv{}
	e.run(t, "weather-未来天气 北京", map[string]any{"defaultDatenumber": 1})
	got := e.text(t)
	if strings.Contains(got, "d2") || strings.Contains(got, "d3") {
		t.Fatalf("day limit not applied: %q", got)
	}
	if !strings.Contains(got, "d1") {
		t.Fatalf("missing first day: %q", got)
	}
}

func TestForecastUnknownCity(t *testing.T) {
	e := &testEnv{}
	e.run(t, "weather-未来天气 不存在的城市名", nil)
	if got := e.text(t); got != "无法找到该城市的cityid，请输入正确的城市名称。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestForecastHTTPError(t *testing.T) {
	usePrimary(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	e := &testEnv{}
	e.run(t, "weather-未来天气 北京", nil)
	if got := e.text(t); got != "获取天气信息失败，请检查输入的城市名称是否正确。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestToday(t *testing.T) {
	useSecondary(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("adcode"); got != "110000" {
			t.Errorf("adcode = %q, want 110000", got)
		}
		w.Write([]byte(`{"code":0,"message":"操作成功！","result":{"province":"北京","city":"北京市","weather":"晴","temperature":12,"winddirection":"北","windpower":"3","humidity":"40","reporttime":"2026-11-08 10:20"}}`))
	})
	e := &testEnv{}
	e.run(t, "weather-当日天气 北京", nil)
	want := "地区：北京北京市\n报告时间：\n2026-11-08 10:20\n☁️==================\n天气：晴\n温度：12℃\n风向：北风\n风力：3级\n湿度：40%"
	if got := e.text(t); got != want {
		t.Fatalf("today reply =\n%q\nwant\n%q", got, want)
	}
}

func TestTodayUnknownCity(t *testing.T) {
	e := &testEnv{}
	e.run(t, "weather-当日天气 不存在的城市名", nil)
	if got := e.text(t); got != "无法找到该城市的adcode，请输入正确的城市名称。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestTodayBadResponse(t *testing.T) {
	useSecondary(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":1,"message":"失败","result":null}`))
	})
	e := &testEnv{}
	e.run(t, "weather-当日天气 北京", nil)
	if got := e.text(t); got != "获取天气信息失败，请检查输入的城市名称是否正确。" {
		t.Fatalf("reply = %q", got)
	}
}

func TestTodayStatusError(t *testing.T) {
	useSecondary(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	e := &testEnv{}
	e.run(t, "weather-当日天气 北京", nil)
	if got := e.text(t); got != "查询天气信息时出错，服务器返回状态码：502" {
		t.Fatalf("reply = %q", got)
	}
}

func TestNoTrigger(t *testing.T) {
	e := &testEnv{}
	e.run(t, "天气 北京", nil)
	if len(e.replies) != 0 {
		t.Fatalf("expected no replies, got %+v", e.replies)
	}
}

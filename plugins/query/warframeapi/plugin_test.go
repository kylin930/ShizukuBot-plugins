package warframeapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shizukubot/sdk"
)

const sample = `{
  "cetusCycle": {"isDay": true, "timeLeft": "1h 20m"},
  "vallisCycle": {"isWarm": false, "timeLeft": "5m"},
  "cambionCycle": {"active": "vome", "timeLeft": "40m"},
  "alerts": [{
    "active": true,
    "eta": "2h",
    "mission": {"node": "E Prime", "type": "Extermination", "minEnemyLevel": 1, "maxEnemyLevel": 5,
      "reward": {"asString": "1000 现金"}}
  }],
  "arbitration": {"node": "Mot", "type": "Survival", "enemy": "Corpus", "expiry": "2030-01-01T00:00:00Z"},
  "dailyDeals": [{"item": "催化剂蓝图", "originalPrice": 100, "salePrice": 50, "total": 1000, "sold": 300, "eta": "3h"}]
}`

type capture struct{ texts []string }

func run(t *testing.T, message string) capture {
	t.Helper()
	var got capture
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got.texts = append(got.texts, r.Text)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

func stub(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sample))
	}))
	t.Cleanup(server.Close)
	old := worldStateURL
	worldStateURL = server.URL
	t.Cleanup(func() { worldStateURL = old })
}

func TestPlains(t *testing.T) {
	stub(t)
	cases := map[string]string{
		"地球平原时间":  "地球平原: 白天, 下次更新: 1h 20m",
		"金星平原时间":  "金星平原: 寒冷, 下次更新: 5m",
		"火卫二平原时间": "火卫二平原: vome, 下次更新: 40m",
	}
	for msg, want := range cases {
		got := run(t, msg)
		if len(got.texts) != 1 || got.texts[0] != want {
			t.Fatalf("%s => %q", msg, got.texts)
		}
	}
}

func TestPlainNotExist(t *testing.T) {
	stub(t)
	got := run(t, "木星平原时间")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "平原不存在") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestAlerts(t *testing.T) {
	stub(t)
	got := run(t, "wf警报")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "E Prime") ||
		!strings.Contains(got.texts[0], "1000 现金") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestArbitration(t *testing.T) {
	stub(t)
	got := run(t, "wf仲裁")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "Mot") ||
		!strings.Contains(got.texts[0], "阵营: Corpus") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestDeals(t *testing.T) {
	stub(t)
	got := run(t, "wf每日特惠")
	if len(got.texts) != 1 || !strings.Contains(got.texts[0], "催化剂蓝图") ||
		!strings.Contains(got.texts[0], "100→50") {
		t.Fatalf("texts = %q", got.texts)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "wf"); len(got.texts) != 0 {
		t.Fatalf("unexpected reply %q", got.texts)
	}
}

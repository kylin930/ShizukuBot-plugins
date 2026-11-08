package ygotrade

import (
	"context"
	"errors"
	"fmt"
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
	for _, name := range []string{"ygotrade"} {
		if commands[name] == nil {
			t.Fatalf("command %q not registered: %v", name, commands)
		}
	}
}

const priceJSON = `{"data":[
 {"id":11,"name_cn":"青眼白龙","number":"QCCP-JP001","rarity":"20SER","min_price":"88.00","quantity":"2","image_url":"https://img.example/11.jpg"},
 {"id":12,"name_cn":"青眼白龙","number":"QCCP-JP001","rarity":"PSER","min_price":"12.50","quantity":"5","image_url":"https://img.example/12.jpg"},
 {"id":13,"name_cn":"青眼白龙","number":"QCCP-JP001","rarity":"SER","min_price":"33","quantity":"1","image_url":"https://img.example/13.jpg"}
]}`

const storeJSON = `{"data":[
 {"id":9001,"seller_user_id":42,"seller_username":"卡牌小店","seller_city":"上海","seller_credit_rank":"A","quantity":"3","min_price":"15.00","rarity":"20SER","card_version_image":"https://img.example/1.jpg"}
]}`

func run(t *testing.T, message string) []sdk.Reply {
	t.Helper()
	var got []sdk.Reply
	c := sdk.Call{Event: sdk.Event{Message: message, Reply: func(_ context.Context, r sdk.Reply) error {
		got = append(got, r)
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return got
}

// stub 用本地服务替换集换社接口，测试结束自动还原。
func stub(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	oldCard, oldStore, oldBase := rarityTradeURL, storeTradeURL, detailBase
	rarityTradeURL = server.URL + "/api/market/search/match-product?game_key=ygo&game_sub_key=ocg&page=1&keyword="
	storeTradeURL = server.URL + "/api/market/card-versions/products?game_key=ygo&game_sub_key=ocg&page=1&condition=1&card_version_id="
	detailBase = "https://jhs.example/product/"
	t.Cleanup(func() { rarityTradeURL, storeTradeURL, detailBase = oldCard, oldStore, oldBase })
}

func TestUsage(t *testing.T) {
	for _, message := range []string{"查卡价", "查卡店"} {
		got := run(t, message)
		if len(got) != 1 || !strings.Contains(got[0].Text, "用法") {
			t.Fatalf("%s => %q", message, got)
		}
	}
}

func TestPrice(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		if keyword := r.URL.Query().Get("keyword"); keyword != "青眼白龙" {
			t.Errorf("keyword = %q", keyword)
		}
		_, _ = w.Write([]byte(priceJSON))
	})
	got := run(t, "查卡价 青眼白龙")
	// 一条文本汇总 + 三张卡图。
	if len(got) != 4 {
		t.Fatalf("replies = %q", got)
	}
	text := got[0].Text
	for _, want := range []string{
		"卡名：青眼白龙", "稀有度：20SER", "当前最低价：¥88.00",
		"在售数量：2", "卡序：QCCP-JP001", "编号：11",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("reply %q missing %q", text, want)
		}
	}
	// 价格应按升序排列：12.50 → 33 → 88.00，卡图顺序与文本一致。
	low := strings.Index(text, "¥12.50")
	mid := strings.Index(text, "¥33")
	high := strings.Index(text, "¥88.00")
	if low < 0 || mid < 0 || high < 0 || !(low < mid && mid < high) {
		t.Fatalf("reply not sorted by price: %q", text)
	}
	wantImages := []string{
		"https://img.example/12.jpg",
		"https://img.example/13.jpg",
		"https://img.example/11.jpg",
	}
	for i, want := range wantImages {
		if got[i+1].Kind != "image" || got[i+1].URL != want {
			t.Fatalf("image %d = %+v, want %q", i, got[i+1], want)
		}
	}
}

func TestPriceShowsAllItems(t *testing.T) {
	stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
		 {"id":1,"name_cn":"卡","rarity":"N","min_price":"1"},
		 {"id":2,"name_cn":"卡","rarity":"N","min_price":"2"},
		 {"id":3,"name_cn":"卡","rarity":"N","min_price":"3"},
		 {"id":4,"name_cn":"卡","rarity":"N","min_price":"4"},
		 {"id":5,"name_cn":"卡","rarity":"N","min_price":"5"},
		 {"id":6,"name_cn":"卡","rarity":"N","min_price":"6"}
		]}`))
	})
	got := run(t, "查卡价 卡")
	if len(got) == 0 || !strings.Contains(got[0].Text, "共 6 条在售") {
		t.Fatalf("replies = %q", got)
	}
	for i := 1; i <= 6; i++ {
		if !strings.Contains(got[0].Text, fmt.Sprintf("¥%d", i)) {
			t.Fatalf("reply %q missing item %d", got[0].Text, i)
		}
	}
}

func TestPriceRarityFilter(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		if rarity := r.URL.Query().Get("rarity"); rarity != "20SER" {
			t.Errorf("rarity = %q", rarity)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":11,"name_cn":"青眼白龙","rarity":"20SER","min_price":"9.90","quantity":"1"}]}`))
	})
	got := run(t, "查卡价 青眼白龙 -r 20SER")
	if len(got) != 1 || !strings.Contains(got[0].Text, "稀有度：20SER") ||
		!strings.Contains(got[0].Text, "共 1 条在售") {
		t.Fatalf("replies = %q", got)
	}
}

func TestStore(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/market/search/match-product":
			_, _ = w.Write([]byte(priceJSON))
		case "/api/market/card-versions/products":
			if id := r.URL.Query().Get("card_version_id"); id != "11" {
				t.Errorf("card_version_id = %q", id)
			}
			_, _ = w.Write([]byte(storeJSON))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	got := run(t, "查卡店 青眼白龙")
	if len(got) != 2 || got[0].Kind != "markdown" {
		t.Fatalf("replies = %q", got)
	}
	for _, want := range []string{
		"卖家名：卡牌小店", "价格：¥15.00", "卖家ID：42", "地区：上海",
		"链接：[查看商品](https://jhs.example/product/9001)",
	} {
		if !strings.Contains(got[0].Text, want) {
			t.Fatalf("reply %q missing %q", got[0].Text, want)
		}
	}
	if strings.Contains(got[0].Text, "品相：") {
		t.Fatalf("卡店不应再把稀有度当品相: %q", got[0].Text)
	}
	if got[1].Kind != "image" || got[1].URL != "https://img.example/1.jpg" {
		t.Fatalf("image = %+v", got[1])
	}
}

func TestStoreShowsAllItems(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/market/search/match-product" {
			_, _ = w.Write([]byte(priceJSON))
			return
		}
		_, _ = w.Write([]byte(`{"data":[
		 {"id":1,"seller_user_id":1,"seller_username":"店1","min_price":"1","card_version_image":"https://img.example/s1.jpg"},
		 {"id":2,"seller_user_id":2,"seller_username":"店2","min_price":"2","card_version_image":"https://img.example/s2.jpg"},
		 {"id":3,"seller_user_id":3,"seller_username":"店3","min_price":"3","card_version_image":"https://img.example/s3.jpg"},
		 {"id":4,"seller_user_id":4,"seller_username":"店4","min_price":"4","card_version_image":"https://img.example/s4.jpg"},
		 {"id":5,"seller_user_id":5,"seller_username":"店5","min_price":"5","card_version_image":"https://img.example/s5.jpg"},
		 {"id":6,"seller_user_id":6,"seller_username":"店6","min_price":"6","card_version_image":"https://img.example/s6.jpg"}
		]}`))
	})
	got := run(t, "查卡店 青眼白龙")
	if len(got) != 7 || !strings.Contains(got[0].Text, "共 6 家在售卡店") {
		t.Fatalf("replies = %q", got)
	}
	for i := 1; i <= 6; i++ {
		if !strings.Contains(got[0].Text, fmt.Sprintf("卖家名：店%d", i)) {
			t.Fatalf("reply %q missing store %d", got[0].Text, i)
		}
	}
}

func TestStorePlainFallback(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/market/search/match-product" {
			_, _ = w.Write([]byte(priceJSON))
			return
		}
		_, _ = w.Write([]byte(storeJSON))
	})
	var got string
	var images []string
	c := sdk.Call{Event: sdk.Event{Message: "查卡店 青眼白龙", Reply: func(_ context.Context, r sdk.Reply) error {
		if r.Kind == "markdown" {
			return errors.New("markdown unsupported")
		}
		if r.Kind == "image" {
			images = append(images, r.URL)
			return nil
		}
		got = r.Text
		return nil
	}}}
	if err := Handle(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "链接：查看商品 (https://jhs.example/product/9001)") {
		t.Fatalf("reply = %q", got)
	}
	if len(images) != 1 || images[0] != "https://img.example/1.jpg" {
		t.Fatalf("images = %q", images)
	}
}

func TestUpstreamError(t *testing.T) {
	stub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	got := run(t, "查卡价 青眼白龙")
	if len(got) != 1 || !strings.Contains(got[0].Text, "查询失败") {
		t.Fatalf("replies = %q", got)
	}
}

func TestNoResult(t *testing.T) {
	stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	if got := run(t, "查卡价 青眼白龙"); len(got) != 1 || !strings.Contains(got[0].Text, "没有找到相关卡片") {
		t.Fatalf("replies = %q", got)
	}
	if got := run(t, "查卡店 青眼白龙"); len(got) != 1 || !strings.Contains(got[0].Text, "没有找到") {
		t.Fatalf("replies = %q", got)
	}
}

func TestIgnoresOtherText(t *testing.T) {
	if got := run(t, "青眼白龙"); len(got) != 0 {
		t.Fatalf("unexpected replies %q", got)
	}
}

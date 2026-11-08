package gloryofkings

import (
	"bytes"
	"reflect"
	"testing"
	"text/template"
)

// fillSlices 把结构体里的切片填一个零元素，确保 {{range}} 分支会被真正执行，
// 从而暴露模板里写错的字段名（text/template 在不执行的分支里不会报错）。
func fillSlices(value reflect.Value, depth int) {
	if depth > 6 || !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Ptr:
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
		}
		fillSlices(value.Elem(), depth+1)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if !value.Field(i).CanSet() {
				continue
			}
			fillSlices(value.Field(i), depth+1)
		}
	case reflect.Slice:
		elem := reflect.New(value.Type().Elem()).Elem()
		fillSlices(elem, depth+1)
		value.Set(reflect.Append(value, elem))
	case reflect.String:
		value.SetString("x")
	case reflect.Bool:
		value.SetBool(true)
	}
}

func TestP2TemplatesExecute(t *testing.T) {
	cases := []struct {
		name string
		tpl  *template.Template
		view any
	}{
		{"SeasonPage", tplSeasonPage, seasonPageView{}},
		{"PeakPerformance", tplPeakPage, peakPageView{}},
		{"AllSeasonPerformance", tplAllSeason, allSeasonView{}},
		{"ScoreTrend", tplScoreTrend, scoreTrendView{}},
		{"RankTrend", tplRankTrend, rankTrendView{}},
		{"MyHeroList", tplMyHeroList, myHeroView{}},
		{"HeroList", tplHeroList, heroListView{}},
		{"SkinWall", tplSkinWall, skinWallView{}},
		{"SkinMissing", tplSkinMissing, skinMissingView{}},
		{"HeroSkin", tplHeroSkin, heroSkinView{}},
		{"CampSign", tplCampSign, signPageView{}},
		{"CampExchange", tplCampExchange, exchangePageView{}},
		{"CampPrivacy", tplCampPrivacy, privacyPageView{}},
		{"CampOnline", tplCampOnline, onlinePageView{}},
		{"CampBattleDetail", tplBattleDetail, battleDetailView{}},
	}
	for _, item := range cases {
		value := reflect.New(reflect.TypeOf(item.view))
		fillSlices(value.Elem(), 0)
		var buffer bytes.Buffer
		if err := item.tpl.Execute(&buffer, value.Elem().Interface()); err != nil {
			t.Errorf("%s: %v", item.name, err)
			continue
		}
		if buffer.Len() == 0 {
			t.Errorf("%s: 输出为空", item.name)
		}
	}
}

package toutai

import (
	"math/rand"
	"strconv"
	"strings"
)

// 中国投胎的基准出生人口，上游按这四个数固定总盘。
const (
	chinaBirthPopulation    = 12123210
	hongKongBirthPopulation = 33200
	macauBirthPopulation    = 3712
	taiwanBirthPopulation   = 137413
	totalPopulation         = chinaBirthPopulation + hongKongBirthPopulation + macauBirthPopulation + taiwanBirthPopulation
)

var (
	orderKeys    = []string{"one", "two", "three", "four", "fivePlus"}
	categoryKeys = []string{"town", "city", "countryside"}
	genderKeys   = []string{"male", "female"}
)

// birthsOf 是一个国家的年出生人数（人口 × 出生率）；数据缺失记 0。
func birthsOf(country worldBirthrate) float64 {
	if country.BirthRate == nil {
		return 0
	}
	births := country.Population * *country.BirthRate
	if births > 0 {
		return births
	}
	return 0
}

// simulateWorld 按年出生人数加权抽取国家。
func simulateWorld(list []worldBirthrate, rnd *rand.Rand) (worldBirthrate, bool) {
	total := 0.0
	for _, country := range list {
		total += birthsOf(country)
	}
	if total <= 0 {
		return worldBirthrate{}, false
	}
	remaining := rnd.Float64() * total
	var last *worldBirthrate
	for i := range list {
		births := birthsOf(list[i])
		if births == 0 {
			continue
		}
		last = &list[i]
		remaining -= births
		if remaining < 0 {
			return list[i], true
		}
	}
	if last == nil {
		return worldBirthrate{}, false
	}
	return *last, true
}

// simulateChina 按各省分城乡、胎次、性别的出生人数加权抽取。
func simulateChina(d *dataset, rnd *rand.Rand) birthResultInChina {
	randomNumber := rnd.Float64() * totalPopulation
	cumulative := 0.0
	for _, region := range d.birthDetailed {
		if region.Name == "national" {
			continue
		}
		for _, category := range categoryKeys {
			var group map[string]map[string]float64
			switch category {
			case "town":
				group = region.Town
			case "city":
				group = region.City
			default:
				group = region.Countryside
			}
			for _, order := range orderKeys {
				for _, gender := range genderKeys {
					population := group[order][gender]
					if !isSpecialProvince(region.Name) {
						population *= 10
					}
					cumulative += population
					if cumulative > randomNumber {
						return birthResultInChina{
							ID:          region.ID,
							Province:    region.DisplayName,
							Gender:      gender,
							Category:    categoryText(category),
							Order:       orderTextValue(order),
							Probability: population / totalPopulation,
						}
					}
				}
			}
		}
	}
	return birthResultInChina{}
}

func isSpecialProvince(name string) bool {
	return name == "xiang_gang" || name == "ao_men" || name == "tai_wan"
}

func categoryText(category string) string {
	switch category {
	case "town":
		return "城镇"
	case "city":
		return "城市"
	default:
		return "乡村"
	}
}

func orderTextValue(order string) string {
	switch order {
	case "one":
		return "一"
	case "two":
		return "二"
	case "three":
		return "三"
	case "four":
		return "四"
	default:
		return "五及以上"
	}
}

// checkAlive 依据新生儿死亡率判断是否顺利降生。
func checkAlive(mortalityPercent float64, rnd *rand.Rand) bool {
	return !(rnd.Float64() < mortalityPercent/100)
}

// pickOne 随机取一则祝祷或哀辞。
func pickOne(list []string, rnd *rand.Rand) string {
	if len(list) == 0 {
		return ""
	}
	return list[rnd.Intn(len(list))]
}

// percentText 保留一位小数，去掉多余的 .0。
func percentText(count, total int) string {
	if total == 0 {
		return "0%"
	}
	value := float64(count) / float64(total) * 100
	var text string
	if value >= 10 {
		text = strconv.FormatFloat(value, 'f', 0, 64)
	} else {
		text = strconv.FormatFloat(value, 'f', 1, 64)
	}
	text = strings.TrimSuffix(text, ".0")
	return text + "%"
}

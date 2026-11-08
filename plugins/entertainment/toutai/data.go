// Package toutai 移植上游投胎模拟器插件：随机投胎、记录与排行榜。
package toutai

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed assets/*
var assetsFS embed.FS

const cardWidth = 820

// birthResultInChina 是一次中国投胎的落点。
type birthResultInChina struct {
	ID          int     `json:"id"`
	Order       string  `json:"order"`
	Index       int     `json:"index,omitempty"`
	Gender      string  `json:"gender"`
	Category    string  `json:"category"`
	Province    string  `json:"province"`
	Probability float64 `json:"probability"`
}

// birthResultInWorld 是一次世界投胎的落点。
type birthResultInWorld struct {
	Index         int       `json:"index,omitempty"`
	DictName      string    `json:"dictName"`
	DictContinent string    `json:"dictContinent"`
	Center        []float64 `json:"center"`
	Coordinate    []float64 `json:"coordinate"`
}

// demiseRecord 是世界投胎中的夭折记录。
type demiseRecord struct {
	Index         int    `json:"index,omitempty"`
	DictName      string `json:"dictName"`
	DictContinent string `json:"dictContinent"`
}

// record 是对应上游 toutai_records 表的一行。
type record struct {
	UserID                          string               `json:"userId"`
	Username                        string               `json:"username"`
	Timestamp                       int64                `json:"timestamp"`
	NumberOfStillbirthsInChina      int                  `json:"numberOfStillbirthsInChina"`
	NumberOfStillbirthsInWorld      int                  `json:"numberOfStillbirthsInWorld"`
	BirthResultsInChina             []birthResultInChina `json:"birthResultsInChina"`
	BirthResultsInWorld             []birthResultInWorld `json:"birthResultsInWorld"`
	UnfortunateDemiseRecordsInWorld []demiseRecord       `json:"unfortunateDemiseRecordsInWorld"`
}

// geoFeature 只保留画图与落点需要的字段。
type geoFeature struct {
	Properties struct {
		Name string    `json:"name"`
		CP   []float64 `json:"cp"`
	} `json:"properties"`
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

type geoCollection struct {
	Features []geoFeature `json:"features"`
}

// rings 返回要素的多边形环集合；Polygon 与 MultiPolygon 都展开为环数组。
func (f geoFeature) rings() [][][]float64 {
	switch f.Geometry.Type {
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &rings); err != nil {
			return nil
		}
		return rings
	case "MultiPolygon":
		var polygons [][][][]float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &polygons); err != nil {
			return nil
		}
		out := make([][][]float64, 0, len(polygons))
		for _, polygon := range polygons {
			out = append(out, polygon...)
		}
		return out
	default:
		return nil
	}
}

type country struct {
	NameEn     string    `json:"nameEn"`
	NameCn     string    `json:"nameCn"`
	Population float64   `json:"population"`
	BirthRate  *float64  `json:"birthRate"`
	Position   []float64 `json:"position"`
	Continent  string    `json:"continent"`
}

type worldBirthrate struct {
	Country    string   `json:"country"`
	Name       string   `json:"name"`
	Population float64  `json:"population"`
	BirthRate  *float64 `json:"birthRate"`
}

type birthRegion struct {
	ID          int                           `json:"id"`
	Name        string                        `json:"name"`
	DisplayName string                        `json:"displayName"`
	Town        map[string]map[string]float64 `json:"town"`
	City        map[string]map[string]float64 `json:"city"`
	Countryside map[string]map[string]float64 `json:"countryside"`
}

type dataset struct {
	china         geoCollection
	world         geoCollection
	worldData     map[string]country
	worldBirths   []worldBirthrate
	birthDetailed []birthRegion
	mortality     map[string]float64
}

var (
	loadOnce sync.Once
	loaded   *dataset
	loadErr  error
)

func loadDataset() (*dataset, error) {
	loadOnce.Do(func() {
		loaded, loadErr = readDataset()
	})
	return loaded, loadErr
}

func readDataset() (*dataset, error) {
	read := func(name string, v any) error {
		data, err := assetsFS.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	d := &dataset{}
	if err := read("China.json", &d.china); err != nil {
		return nil, err
	}
	if err := read("world.json", &d.world); err != nil {
		return nil, err
	}
	if err := read("worldData.json", &d.worldData); err != nil {
		return nil, err
	}
	if err := read("worldBirthrate.json", &d.worldBirths); err != nil {
		return nil, err
	}
	if err := read("birthrateDetailed.json", &d.birthDetailed); err != nil {
		return nil, err
	}
	if err := read("neonatalMortalityRate.json", &d.mortality); err != nil {
		return nil, err
	}
	return d, nil
}

// baseCSS 读取嵌入的样式表。
func baseCSS() string {
	data, err := assetsFS.ReadFile("assets/base.css")
	if err != nil {
		return ""
	}
	return string(data)
}

// totalProvinceCount 是命簿收录的省级行政区数量（national 为汇总行，不计）。
func (d *dataset) totalProvinceCount() int {
	count := 0
	for _, region := range d.birthDetailed {
		if region.Name != "national" {
			count++
		}
	}
	return count
}

var continentDict = map[string]string{
	"AF": "非洲",
	"EU": "欧洲",
	"AS": "亚洲",
	"OA": "大洋洲",
	"NA": "北美洲",
	"SA": "南美洲",
	"AN": "南极洲",
}

var continentOrder = []string{"非洲", "亚洲", "欧洲", "北美洲", "南美洲", "大洋洲", "南极洲"}

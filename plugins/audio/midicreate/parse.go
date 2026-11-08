package midicreate

import (
	"errors"
	"strconv"
	"strings"
)

// noteMap 与上游 midicreate 保持一致：音名到默认八度（第 5 级）的 MIDI 音高。
var noteMap = map[string]uint8{
	"C":  60,
	"Db": 61,
	"D":  62,
	"Eb": 63,
	"E":  64,
	"F":  65,
	"Gb": 66,
	"G":  67,
	"Ab": 68,
	"A":  69,
	"Bb": 70,
	"B":  71,
}

// ticks4th 是四分音符的 tick 数。上游使用零值 smf.MetricTicks，
// 该库约定 0 等价于 960 ticks/四分音符。
const ticks4th uint32 = 960

// noteEvent 是解析后的一个发音事件：restTicks 为它之前的休止，
// ticks 为音符自身时值。
type noteEvent struct {
	RestTicks uint32
	Ticks     uint32
	Note      uint8
}

// parseMusic 完整复用上游 mkMidi 的音符解析语义。
// 支持音名 C~B、#/b、八度数字、休止符 R、<n 时值记法，
// 空格与逗号均可作为分隔符。
func parseMusic(input string) ([]noteEvent, error) {
	k := strings.NewReplacer(" ", "", ",", "", "，", "").Replace(input)
	var (
		events    []noteEvent
		base      uint8
		level     uint8
		delay     uint32
		sleepFlag bool
	)
	lengthBytes := make([]byte, 0, 4)
	for i := 0; i < len(k); {
		base = 0
		level = 0
		sleepFlag = false
		lengthBytes = lengthBytes[:0]
		for {
			switch {
			case k[i] == 'R':
				sleepFlag = true
				i++
			case k[i] >= 'A' && k[i] <= 'G':
				base = noteMap[k[i:i+1]] % 12
				i++
			case k[i] == 'b':
				base--
				i++
			case k[i] == '#':
				base++
				i++
			case k[i] >= '0' && k[i] <= '9':
				level = level*10 + k[i] - '0'
				i++
			case k[i] == '<':
				i++
				for i < len(k) && (k[i] == '-' || (k[i] >= '0' && k[i] <= '9')) {
					lengthBytes = append(lengthBytes, k[i])
					i++
				}
			default:
				return nil, errors.New("无法解析第" + strconv.Itoa(i) + "个位置的" + string(k[i]) + "字符")
			}
			if i >= len(k) || (k[i] >= 'A' && k[i] <= 'G') || k[i] == 'R' {
				break
			}
		}
		length, _ := strconv.Atoi(string(lengthBytes))
		if sleepFlag {
			// 上游用赋值而非累加，连续休止符只保留最后一个。
			delay = ticksFor(length)
			continue
		}
		if level == 0 {
			level = 5
		}
		events = append(events, noteEvent{
			RestTicks: delay,
			Ticks:     ticksFor(length),
			Note:      o(base, level),
		})
		delay = 0
	}
	// 末尾休止符没有后续音符，上游同样丢弃。
	return events, nil
}

// ticksFor 把上游的时值参数换算成 tick 数：
// 非负数表示四分音符的 2 的幂倍，负数表示 2 的幂分之一。
func ticksFor(length int) uint32 {
	if length >= 0 {
		return ticks4th << uint(length)
	}
	return ticks4th / (1 << uint(-length))
}

// o 与上游一致，保留 uint8 溢出与仅回退一个八度的行为。
func o(base uint8, oct uint8) uint8 {
	if oct > 10 {
		oct = 10
	}
	if oct == 0 {
		return base
	}
	res := base + 12*oct
	if res > 127 {
		res -= 12
	}
	return res
}

// name 返回音级名；上游按音高模 12 查找，音级与音名一一对应。
func name(n uint8) string {
	for k, v := range noteMap {
		if v%12 == n%12 {
			return k
		}
	}
	return ""
}

// processOne 与上游一致，把用户回答解析为 MIDI 音高。
func processOne(note string) uint8 {
	k := strings.ReplaceAll(note, " ", "")
	var (
		base  uint8
		level uint8
	)
	for i := 0; i < len(k); i++ {
		switch {
		case k[i] >= 'A' && k[i] <= 'G':
			base = noteMap[k[i:i+1]] % 12
		case k[i] == 'b':
			base--
		case k[i] == '#':
			base++
		case k[i] >= '0' && k[i] <= '9':
			level = level*10 + k[i] - '0'
		}
	}
	if level == 0 {
		level = 5
	}
	return o(base, level)
}

// answerFor 生成与上游相同的答案文本，例如 60 -> C5。
func answerFor(target uint8) string {
	return name(target) + strconv.Itoa(int(target/12))
}

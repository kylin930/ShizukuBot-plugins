package midicreate

import (
	"encoding/binary"
	"math"
)

const (
	// sampleRate 与 WAV 输出格式：44100Hz、单声道、16-bit PCM。
	sampleRate = 44100
	// bpm 与上游 MetaTempo(72) 一致。
	bpm = 72.0
	// maxHarmonics 是加法合成使用的谐波数。
	maxHarmonics = 8
	// maxWAVBytes 是框架内联语音上限 5MiB，超出部分截断。
	maxWAVBytes = 5 << 20
	// amplitude 控制整体音量，避免叠加谐波后削波。
	amplitude = 0.55
)

// samplesPerTick 由 72 BPM 与 960 ticks/四分音符换算得出。
const samplesPerTick = sampleRate * 60.0 / (bpm * float64(ticks4th))

// waveform 是 GM 音色族的基波形态。
type waveform int

const (
	waveSine waveform = iota
	waveSquare
	waveSaw
	waveTriangle
	waveOrgan
	waveBell
	wavePulse
	waveBright
)

// family 是一组 GM 音色的共同合成参数。
type family struct {
	wave         waveform
	attack       float64
	decay        float64
	sustain      float64
	release      float64
	vibratoDepth float64
	vibratoRate  float64
	detune       float64
}

// families 对应 GM 的 16 个乐器族，每族 8 个音色。
var families = [16]family{
	{waveSaw, 0.004, 0.60, 0.25, 0.18, 0, 0, 0},                // 0-7 钢琴
	{waveBell, 0.001, 0.35, 0.05, 0.12, 0, 0, 0},               // 8-15 色彩打击乐
	{waveOrgan, 0.010, 0.05, 0.92, 0.06, 0, 0, 0},              // 16-23 风琴
	{waveSaw, 0.003, 0.45, 0.18, 0.12, 0, 0, 0},                // 24-31 吉他
	{waveSquare, 0.005, 0.30, 0.35, 0.10, 0, 0, 0},             // 32-39 贝斯
	{waveSaw, 0.090, 0.25, 0.80, 0.20, 0.004, 5.2, 0.0015},     // 40-47 弦乐
	{waveBright, 0.120, 0.30, 0.82, 0.28, 0.005, 5.8, 0.004},   // 48-55 合奏
	{waveBright, 0.030, 0.20, 0.75, 0.12, 0.002, 5.5, 0},       // 56-63 铜管
	{wavePulse, 0.020, 0.20, 0.70, 0.10, 0.004, 5.0, 0},        // 64-71 簧管
	{waveTriangle, 0.050, 0.15, 0.80, 0.15, 0.003, 5.6, 0.001}, // 72-79 吹管
	{waveSaw, 0.010, 0.25, 0.65, 0.10, 0.006, 6.0, 0.002},      // 80-87 合成主音
	{waveSine, 0.250, 0.35, 0.80, 0.40, 0.003, 4.5, 0.003},     // 88-95 合成铺底
	{waveBell, 0.020, 0.40, 0.40, 0.25, 0.006, 7.0, 0.002},     // 96-103 合成效果
	{wavePulse, 0.015, 0.30, 0.55, 0.12, 0.004, 5.3, 0},        // 104-111 民族乐器
	{waveBell, 0.001, 0.22, 0.08, 0.08, 0, 0, 0},               // 112-119 打击乐器
	{waveBright, 0.005, 0.50, 0.20, 0.20, 0.008, 8.0, 0.003},   // 120-127 音效
}

// voice 是单个 GM 音色的合成参数。
type voice struct {
	harmonics    [maxHarmonics]float64
	attack       float64
	decay        float64
	sustain      float64
	release      float64
	vibratoDepth float64
	vibratoRate  float64
	detune       float64
}

// baseHarmonics 返回各波形的基础谐波幅度。
func baseHarmonics(w waveform) [maxHarmonics]float64 {
	var h [maxHarmonics]float64
	switch w {
	case waveSine:
		h[0] = 1
	case waveSquare:
		for k := 1; k <= maxHarmonics; k += 2 {
			h[k-1] = 1 / float64(k)
		}
	case waveSaw:
		for k := 1; k <= maxHarmonics; k++ {
			h[k-1] = 1 / float64(k)
		}
	case waveTriangle:
		for k := 1; k <= maxHarmonics; k += 2 {
			h[k-1] = 1 / float64(k*k)
		}
	case waveOrgan:
		h = [maxHarmonics]float64{1, 0.8, 0.1, 0.5, 0, 0.25, 0, 0.1}
	case waveBell:
		h = [maxHarmonics]float64{1, 0.25, 0.55, 0.12, 0.35, 0.08, 0.22, 0.05}
	case wavePulse:
		h = [maxHarmonics]float64{1, 0.6, 0.1, 0.35, 0.05, 0.2, 0.02, 0.12}
	case waveBright:
		h = [maxHarmonics]float64{1, 0.75, 0.55, 0.45, 0.35, 0.28, 0.22, 0.18}
	}
	return h
}

// gmVoices 在初始化时把 128 个 GM 音色映射到波形、谐波与包络组合。
var gmVoices = buildVoices()

func buildVoices() [128]voice {
	var out [128]voice
	for p := 0; p < 128; p++ {
		fam := families[p/8]
		variant := p % 8
		h := baseHarmonics(fam.wave)
		brightness := 0.5 + 0.14*float64(variant)
		sum := 0.0
		for k := 0; k < maxHarmonics; k++ {
			h[k] *= math.Pow(brightness, float64(k))
			sum += math.Abs(h[k])
		}
		if sum > 0 {
			for k := range h {
				h[k] /= sum
			}
		}
		v := voice{
			harmonics:    h,
			attack:       math.Max(fam.attack*(0.7+0.12*float64(variant)), 0.001),
			decay:        math.Max(fam.decay*(0.8+0.08*float64(variant)), 0.001),
			sustain:      clamp01(fam.sustain + 0.03*float64(variant-4)),
			release:      math.Max(fam.release*(0.6+0.15*float64(variant)), 0.001),
			vibratoDepth: fam.vibratoDepth * (0.6 + 0.12*float64(variant)),
			vibratoRate:  fam.vibratoRate + 0.15*float64(variant),
			detune:       fam.detune * (0.5 + 0.2*float64(variant)),
		}
		out[p] = v
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// voiceFor 返回校验后的音色参数，非法编号回退到上游默认的 40（小提琴）。
func voiceFor(timbre int) voice {
	if timbre < 0 || timbre > 127 {
		timbre = 40
	}
	return gmVoices[timbre]
}

// synthesize 按解析结果合成 16-bit 单声道 WAV。
func synthesize(events []noteEvent, timbre int) []byte {
	v := voiceFor(timbre)
	maxSamples := (maxWAVBytes - 44) / 2
	out := make([]int16, 0, maxSamples)
	for _, ev := range events {
		rest := samplesForTicks(ev.RestTicks)
		for i := 0; i < rest && len(out) < maxSamples; i++ {
			out = append(out, 0)
		}
		dur := samplesForTicks(ev.Ticks)
		if dur <= 0 {
			dur = 1
		}
		if len(out)+dur > maxSamples {
			dur = maxSamples - len(out)
		}
		if dur <= 0 {
			break
		}
		out = renderNote(out, ev.Note, dur, v)
	}
	return encodeWAV(out)
}

// synthesizeNote 合成单个四分音符，用于听音练习。
func synthesizeNote(note uint8, timbre int) []byte {
	return synthesize([]noteEvent{{Ticks: ticks4th, Note: note}}, timbre)
}

func samplesForTicks(ticks uint32) int {
	return int(math.Round(float64(ticks) * samplesPerTick))
}

// renderNote 把单个音符追加到 out，返回扩容后的切片。
func renderNote(out []int16, note uint8, durSamples int, v voice) []int16 {
	durSec := float64(durSamples) / sampleRate
	a, d, r := v.attack, v.decay, v.release
	// 短音符按比例压缩包络，避免释放段盖住整个音符。
	if total := a + d + r; total > durSec*0.8 {
		scale := durSec * 0.8 / total
		a *= scale
		d *= scale
		r *= scale
	}
	freq := 440.0 * math.Pow(2, (float64(note)-69)/12)
	step := freq / sampleRate
	stepDetuned := freq * (1 + v.detune) / sampleRate
	var phase, phaseDetuned float64
	for i := 0; i < durSamples; i++ {
		t := float64(i) / sampleRate
		vib := 1.0
		if v.vibratoDepth > 0 {
			vib += v.vibratoDepth * math.Sin(2*math.Pi*v.vibratoRate*t)
		}
		phase = wrapPhase(phase + step*vib)
		s := oscillator(&v.harmonics, phase)
		if v.detune > 0 {
			phaseDetuned = wrapPhase(phaseDetuned + stepDetuned*vib)
			s = 0.5*s + 0.5*oscillator(&v.harmonics, phaseDetuned)
		}
		val := s * envelope(t, durSec, a, d, r, v.sustain) * amplitude
		if val > 1 {
			val = 1
		} else if val < -1 {
			val = -1
		}
		out = append(out, int16(val*32767))
	}
	return out
}

func wrapPhase(p float64) float64 {
	return p - math.Floor(p)
}

// oscillator 用加法合成计算一个采样。
func oscillator(h *[maxHarmonics]float64, phase float64) float64 {
	twoPi := 2 * math.Pi
	sum := 0.0
	for k := 0; k < maxHarmonics; k++ {
		if h[k] == 0 {
			continue
		}
		sum += h[k] * math.Sin(twoPi*float64(k+1)*phase)
	}
	return sum
}

// envelope 计算音符内的 ADSR 包络。
func envelope(t, dur, attack, decay, release, sustain float64) float64 {
	switch {
	case t < attack:
		return t / attack
	case t < attack+decay:
		return 1 - (1-sustain)*(t-attack)/decay
	case t < dur-release:
		return sustain
	default:
		rel := (dur - t) / release
		if rel < 0 {
			return 0
		}
		return sustain * rel
	}
}

// encodeWAV 写出标准 RIFF/WAVE 头与 PCM 数据。
func encodeWAV(samples []int16) []byte {
	dataLen := len(samples) * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[44+2*i:], uint16(s))
	}
	return buf
}

package midicreate

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestParseMusicPitchAndOctave(t *testing.T) {
	notes := func(input string) []uint8 {
		t.Helper()
		events, err := parseMusic(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		out := make([]uint8, len(events))
		for i, ev := range events {
			out[i] = ev.Note
		}
		return out
	}

	cases := []struct {
		input string
		want  []uint8
	}{
		{"C D E F G A B", []uint8{60, 62, 64, 65, 67, 69, 71}},
		{"C,D，E", []uint8{60, 62, 64}},
		{"C4", []uint8{48}},
		{"C6", []uint8{72}},
		{"C#5", []uint8{61}},
		{"Db5", []uint8{61}},
		{"Bb5", []uint8{70}},
		{"Cb5", []uint8{59}},
		{"C12", []uint8{120}},
		{"G4", []uint8{55}},
		{"E7", []uint8{88}},
	}
	for _, tc := range cases {
		got := notes(tc.input)
		if !equalUint8(got, tc.want) {
			t.Fatalf("parse %q = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseMusicDurationsAndRests(t *testing.T) {
	events, err := parseMusic("C<1 D<-1 E<2 F<-2")
	if err != nil {
		t.Fatal(err)
	}
	wantTicks := []uint32{1920, 480, 3840, 240}
	if len(events) != len(wantTicks) {
		t.Fatalf("events = %d", len(events))
	}
	for i, want := range wantTicks {
		if events[i].Ticks != want {
			t.Fatalf("event %d ticks = %d, want %d", i, events[i].Ticks, want)
		}
	}

	events, err = parseMusic("R C")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RestTicks != 960 {
		t.Fatalf("R C = %+v", events)
	}

	// 连续休止符沿用上游的覆盖语义，只保留最后一个。
	events, err = parseMusic("R R C")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RestTicks != 960 {
		t.Fatalf("R R C = %+v", events)
	}

	events, err = parseMusic("C R D")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].RestTicks != 960 || events[1].Note != 62 {
		t.Fatalf("C R D = %+v", events)
	}

	events, err = parseMusic("C R")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("trailing rest should be dropped: %+v", events)
	}

	events, err = parseMusic("R")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("R should produce no notes: %+v", events)
	}
}

func TestParseMusicInvalid(t *testing.T) {
	events, err := parseMusic("")
	if err != nil || len(events) != 0 {
		t.Fatalf("empty input: %v %+v", err, events)
	}

	if _, err := parseMusic("X"); err == nil || !strings.Contains(err.Error(), "无法解析第0个位置的X字符") {
		t.Fatalf("X error = %v", err)
	}
	if _, err := parseMusic("C?"); err == nil || !strings.Contains(err.Error(), "第1个位置的?字符") {
		t.Fatalf("C? error = %v", err)
	}
	if _, err := parseMusic("c"); err == nil || !strings.Contains(err.Error(), "第0个位置的c字符") {
		t.Fatalf("c error = %v", err)
	}
}

func TestOAndProcessOneMatchUpstream(t *testing.T) {
	if got := o(0, 5); got != 60 {
		t.Fatalf("o(0,5) = %d", got)
	}
	if got := o(11, 10); got != 119 {
		t.Fatalf("o(11,10) = %d", got)
	}
	// Cb 令 uint8 下溢到 255，255+60 再回绕为 59，与上游一致。
	if got := o(255, 5); got != 59 {
		t.Fatalf("o(255,5) = %d", got)
	}
	if got := o(7, 0); got != 7 {
		t.Fatalf("o(7,0) = %d", got)
	}

	cases := map[string]uint8{
		"C":   60,
		"C5":  60,
		"B":   71,
		"B7":  95,
		"C#6": 73,
		"Db5": 61,
		"Cb5": 59,
		"":    60,
	}
	for input, want := range cases {
		if got := processOne(input); got != want {
			t.Fatalf("processOne(%q) = %d, want %d", input, got, want)
		}
	}
	if got := answerFor(55); got != "G4" {
		t.Fatalf("answerFor(55) = %q", got)
	}
	if got := answerFor(60); got != "C5" {
		t.Fatalf("answerFor(60) = %q", got)
	}
}

func TestSynthesizeDurationAndWAV(t *testing.T) {
	single, err := parseMusic("C")
	if err != nil {
		t.Fatal(err)
	}
	quarter := float64(samplesForTicks(ticks4th)) / sampleRate
	approx(t, checkWAV(t, synthesize(single, 40)), quarter, "quarter note")

	half, _ := parseMusic("C<1")
	approx(t, checkWAV(t, synthesize(half, 40)), quarter*2, "half note")

	withRest, _ := parseMusic("C R D")
	approx(t, checkWAV(t, synthesize(withRest, 40)), quarter*3, "note rest note")

	data := synthesize(single, 40)
	hasSignal := false
	for i := 44; i+1 < len(data); i += 2 {
		if binary.LittleEndian.Uint16(data[i:i+2]) != 0 {
			hasSignal = true
			break
		}
	}
	if !hasSignal {
		t.Fatal("audio contains only silence")
	}
}

func TestSynthesizeTimbresDiffer(t *testing.T) {
	events, err := parseMusic("C")
	if err != nil {
		t.Fatal(err)
	}
	base := synthesize(events, 0)
	if bytes.Equal(base, synthesize(events, 40)) {
		t.Fatal("timbre 0 and 40 are identical")
	}
	if bytes.Equal(base, synthesize(events, 80)) {
		t.Fatal("timbre 0 and 80 are identical")
	}

	// 同一乐器族内 8 个音色也应听感不同。
	seen := map[string]bool{}
	for timbre := 40; timbre < 48; timbre++ {
		key := string(synthesize(events, timbre))
		if seen[key] {
			t.Fatalf("timbre %d duplicates another timbre", timbre)
		}
		seen[key] = true
	}
}

func TestVoiceTable(t *testing.T) {
	if len(gmVoices) != 128 {
		t.Fatalf("voices = %d", len(gmVoices))
	}
	for i, v := range gmVoices {
		sum := 0.0
		for _, h := range v.harmonics {
			sum += h
		}
		if sum <= 0 {
			t.Fatalf("timbre %d has no harmonics", i)
		}
		if v.attack <= 0 || v.decay <= 0 || v.release <= 0 {
			t.Fatalf("timbre %d has invalid envelope: %+v", i, v)
		}
	}
}

func equalUint8(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

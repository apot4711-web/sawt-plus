package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func pcm16(samples ...int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}

func TestApplyGain(t *testing.T) {
	b := pcm16(1000, -1000, 30000, -30000)
	ApplyGain(b, 2)
	got := []int16{
		int16(binary.LittleEndian.Uint16(b[0:])),
		int16(binary.LittleEndian.Uint16(b[2:])),
		int16(binary.LittleEndian.Uint16(b[4:])),
		int16(binary.LittleEndian.Uint16(b[6:])),
	}
	want := []int16{2000, -2000, math.MaxInt16, math.MinInt16}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRMS(t *testing.T) {
	if got := RMS(pcm16(0, 0, 0, 0)); got != 0 {
		t.Fatalf("silence RMS = %f", got)
	}
	got := RMS(pcm16(32767, -32768))
	if got < 0.99 || got > 1 {
		t.Fatalf("full scale RMS = %f", got)
	}
}

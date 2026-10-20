package audio

import (
	"encoding/binary"
	"math"
)

// ApplyGain scales 16-bit little-endian PCM in place. Values are clipped.
// A gain of 1 leaves the buffer untouched.
func ApplyGain(pcm []byte, gain float64) {
	if gain == 1 || gain <= 0 || len(pcm) < 2 {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int32(int16(binary.LittleEndian.Uint16(pcm[i:])))
		v := int32(math.Round(float64(s) * gain))
		if v > math.MaxInt16 {
			v = math.MaxInt16
		} else if v < math.MinInt16 {
			v = math.MinInt16
		}
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(v)))
	}
}

// RMS returns the root mean square level of 16-bit mono/stereo PCM,
// normalized to 0..1.
func RMS(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		sum += s * s
	}
	return math.Sqrt(sum/float64(n)) / 32768.0
}

// Silence returns zeroed 16-bit PCM of the given duration.
func Silence(rate, channels, frameMS int) []byte {
	n := rate * frameMS / 1000 * channels * 2
	return make([]byte, n)
}

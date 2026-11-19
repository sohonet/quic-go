package bbr

import (
	"math"
	"math/bits"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
)

const (
	infBandwidth = Bandwidth(math.MaxUint64)
)

// Bandwidth of a connection
type Bandwidth uint64

const (
	// BitsPerSecond is 1 bit per second
	BitsPerSecond Bandwidth = 1
	// BytesPerSecond is 1 byte per second
	BytesPerSecond = 8 * BitsPerSecond
)

// BandwidthFromDelta calculates the bandwidth from a number of bytes and a time delta
func BandwidthFromDelta(bytes congestion.ByteCount, delta time.Duration) Bandwidth {
	if bytes <= 0 || delta <= 0 {
		return 0
	}
	return Bandwidth(saturatingMulDiv(uint64(bytes), uint64(time.Second)*8, uint64(delta)))
}

// saturatingMulDiv keeps the intermediate product at 128 bits. Delivery-rate
// calculations can overflow 64 bits even when their final result fits easily.
func saturatingMulDiv(a, b, divisor uint64) uint64 {
	high, low := bits.Mul64(a, b)
	if divisor == 0 || high >= divisor {
		return math.MaxUint64
	}
	quotient, _ := bits.Div64(high, low, divisor)
	return quotient
}

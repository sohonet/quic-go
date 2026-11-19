package bbr

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/stretchr/testify/require"
)

// A 35 Mbps stream cannot make useful progress with a four-packet window while
// ProbeRTT drains the path. Keep the probe, but cap it at half the estimated BDP.
func TestProbeRTTUsesBoundedHalfBDP(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, 1200, ProfileStandard)
	now := monotime.Now()
	b.minRtt = 80 * time.Millisecond
	b.minRttTimestamp = now.Add(-11 * time.Second)
	b.maxBandwidth.Reset(50_000_000*BitsPerSecond, 0)
	b.mode = bbrModeProbeBw
	b.isAtFullBandwidth = true
	b.bytesInFlight = 100_000

	expired := b.maybeUpdateMinRtt(now, 120*time.Millisecond)
	require.True(t, expired)
	b.maybeEnterOrExitProbeRtt(now, true, expired)
	require.Equal(t, bbrMode(bbrModeProbeRtt), b.mode)
	require.Equal(t, congestion.ByteCount(250_000), b.GetCongestionWindow(), "save the BDP before RTT expiry raises the RTT estimate")

	b.maxBandwidth.Reset(100_000_000*BitsPerSecond, 1)
	require.Equal(t, congestion.ByteCount(250_000), b.GetCongestionWindow(), "probe cannot inflate its saved cap")
	b.maxBandwidth.Reset(20_000_000*BitsPerSecond, 2)
	require.Equal(t, congestion.ByteCount(150_000), b.GetCongestionWindow(), "probe still follows a lower bandwidth estimate")
	b.maxBandwidth.Reset(1_000*BitsPerSecond, 3)
	require.Equal(t, b.minCongestionWindow, b.GetCongestionWindow(), "retain the packet-count floor")

	b.maybeEnterOrExitProbeRtt(now.Add(199*time.Millisecond), true, false)
	require.Equal(t, bbrMode(bbrModeProbeRtt), b.mode)
	b.maybeEnterOrExitProbeRtt(now.Add(200*time.Millisecond), true, false)
	require.Equal(t, bbrMode(bbrModeProbeBw), b.mode, "retain the probe duration and round-trip completion requirement")
}

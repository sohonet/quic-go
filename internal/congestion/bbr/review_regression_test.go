package bbr

import (
	"math"
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestBandwidthConversionsAfterLongIdle(t *testing.T) {
	require.EqualValues(t, 1_250_000_000, bdpFromRttAndBandwidth(time.Second, 10_000_000_000))
	require.EqualValues(t, 1_250_000_000, bytesFromBandwidthAndTimeDelta(1_000_000_000, 10*time.Second))
	require.EqualValues(t, 10_000_000_000, BandwidthFromDelta(12_500_000_000, 10*time.Second))
	require.Equal(t, 10*time.Second, timeDeltaFromBytesAndBandwidth(12_500_000_000, 10_000_000_000))
	require.EqualValues(t, math.MaxInt64, bytesFromBandwidthAndTimeDelta(infBandwidth, time.Duration(math.MaxInt64)))
	require.Equal(t, time.Duration(math.MaxInt64), timeDeltaFromBytesAndBandwidth(math.MaxInt64, 1))
}

func TestInitialPacerUsesConfiguredDatagramSize(t *testing.T) {
	for _, size := range []congestion.ByteCount{1200, 1400} {
		b := NewBbrSender(DefaultClock{}, size, ProfileStandard)
		b.pacingRate = 800_000 // 100 kB/s: the ten-packet burst floor dominates.
		require.Equal(t, 10*size, b.pacer.Budget(monotime.Now()))
	}
}

func TestTimerOnlyLossEntersAndExtendsRecovery(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, 1200, ProfileStandard)
	rtt := utils.NewRTTStats()
	rtt.UpdateRTT(20*time.Millisecond, 0)
	b.SetRTTStatsProvider(rtt)
	now := monotime.Now()
	for pn := congestion.PacketNumber(0); pn < 30; pn++ {
		b.OnPacketSent(now, congestion.ByteCount(pn)*1200, pn, 1200, true)
	}
	b.isAtFullBandwidth = true
	b.mode = bbrModeProbeBw
	b.OnCongestionEventEx(congestion.CongestionEvent{Time: now.Add(25 * time.Millisecond), PriorInFlight: 36000, BytesInFlight: 34800, FirstInFlight: 1, LostPackets: []congestion.LostPacketInfo{{PacketNumber: 0, BytesLost: 1200}}})
	require.Equal(t, bbrRecoveryState(bbrRecoveryStateConservation), b.recoveryState)
	require.EqualValues(t, 29, b.endRecoveryAt)
	require.EqualValues(t, 34800, b.recoveryWindow)
	b.OnPacketSent(now.Add(26*time.Millisecond), 34800, 30, 1200, true)
	b.OnCongestionEventEx(congestion.CongestionEvent{Time: now.Add(30 * time.Millisecond), PriorInFlight: 36000, BytesInFlight: 34800, FirstInFlight: 2, LostPackets: []congestion.LostPacketInfo{{PacketNumber: 1, BytesLost: 1200}}})
	require.EqualValues(t, 30, b.endRecoveryAt)
	require.Equal(t, bbrRecoveryState(bbrRecoveryStateConservation), b.recoveryState)
}

package bbr

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestSamplerRetainsOutstandingAcrossSkippedNumbers(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, 1200, ProfileStandard)
	rtt := utils.NewRTTStats()
	rtt.UpdateRTT(20*time.Millisecond, 0)
	b.SetRTTStatsProvider(rtt)
	now := monotime.Now()
	for i, pn := range []congestion.PacketNumber{10, 12, 13} {
		b.OnPacketSent(now, congestion.ByteCount(i*1200), pn, 1200, true)
	}
	b.OnCongestionEventEx(congestion.CongestionEvent{PriorInFlight: 3600, BytesInFlight: 2400, FirstInFlight: 10, Time: now.Add(20 * time.Millisecond), AckedPackets: []congestion.AckedPacketInfo{{PacketNumber: 13, BytesAcked: 1200}}})
	require.NotNil(t, b.sampler.connectionStateMap.GetEntry(10), "packet 11 was skipped: packet 10 is only two sent packets behind 13 and remains outstanding")
	b.OnCongestionEventEx(congestion.CongestionEvent{PriorInFlight: 2400, BytesInFlight: 1200, FirstInFlight: 12, Time: now.Add(21 * time.Millisecond), AckedPackets: []congestion.AckedPacketInfo{{PacketNumber: 10, BytesAcked: 1200}}})
	require.Equal(t, congestion.ByteCount(2400), b.sampler.TotalBytesAcked())
}

func TestSamplerRetiresProbeWithoutCongestionLoss(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, 1200, ProfileStandard)
	rtt := utils.NewRTTStats()
	rtt.UpdateRTT(20*time.Millisecond, 0)
	b.SetRTTStatsProvider(rtt)
	now := monotime.Now()
	b.OnPacketSent(now, 0, 10, 1300, true)
	b.OnCongestionEventEx(congestion.CongestionEvent{Time: now.Add(time.Millisecond), PriorInFlight: 1300, FirstInFlight: 11, NeuteredPackets: []congestion.PacketNumber{10}})
	require.Zero(t, b.bytesInFlight)
	require.Zero(t, b.sampler.TotalBytesLost())
	require.Equal(t, congestion.ByteCount(1300), b.sampler.totalBytesNeutered)
	require.Nil(t, b.sampler.connectionStateMap.GetEntry(10))
}

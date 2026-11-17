package ackhandler

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/utils"
	"github.com/quic-go/quic-go/internal/wire"
	"github.com/stretchr/testify/require"
)

func newBBRTestHandler(enabled bool) *sentPacketHandler {
	rtt := utils.NewRTTStats()
	rtt.UpdateRTT(20*time.Millisecond, 0)
	return NewSentPacketHandler(0, 1200, rtt, &utils.ConnectionStats{}, true, true, nil,
		protocol.PerspectiveServer, nil, utils.DefaultLogger, false, enabled).(*sentPacketHandler)
}

func TestBBRActivationAndMigration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := newBBRTestHandler(enabled)
		require.Nil(t, h.bbr, "handshake keeps Reno and separate packet-number spaces")
		if enabled {
			require.Nil(t, h.ecnTracker, "do not send ECN marks with a controller lacking ECN feedback")
		} else {
			require.NotNil(t, h.ecnTracker, "default controller preserves ECN")
		}
		h.SetMaxDatagramSize(1400)
		now := monotime.Now()
		h.DropPackets(protocol.EncryptionInitial, now)
		require.Nil(t, h.bbr)
		h.DropPackets(protocol.EncryptionHandshake, now)
		if !enabled {
			require.Nil(t, h.bbr)
			continue
		}
		require.NotNil(t, h.bbr)
		require.Equal(t, protocol.ByteCount(32*1400), h.congestion.GetCongestionWindow())
		first := h.bbr
		h.DropPackets(protocol.EncryptionHandshake, now)
		require.Same(t, first, h.bbr, "duplicate key drop cannot reset the controller")
		var packets packetTracker
		pn := h.PopPacketNumber(protocol.Encryption1RTT)
		h.SentPacket(now, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1400, false, false)
		h.DropPackets(protocol.EncryptionHandshake, now)
		require.Same(t, first, h.bbr, "duplicate key drop preserves active samples")
		require.EqualValues(t, 1400, h.bbrBytesInFlight)
		h.MigratedPath(now.Add(time.Second), 1200)
		require.NotSame(t, first, h.bbr, "path model and sampler cannot cross migration")
		require.Equal(t, protocol.ByteCount(32*1200), h.congestion.GetCongestionWindow())
		require.Zero(t, h.bytesInFlight)
		require.Equal(t, []protocol.PacketNumber{pn}, packets.Lost)
	}
}

type recordingBBR struct {
	congestion.BatchedSendAlgorithm
	events    []congestion.CongestionEvent
	sentPrior []protocol.ByteCount
}

func (r *recordingBBR) OnPacketSent(at monotime.Time, prior protocol.ByteCount, pn protocol.PacketNumber, size protocol.ByteCount, eliciting bool) {
	r.sentPrior = append(r.sentPrior, prior)
	r.BatchedSendAlgorithm.OnPacketSent(at, prior, pn, size, eliciting)
}

func (r *recordingBBR) OnCongestionEventEx(event congestion.CongestionEvent) {
	copyEvent := event
	copyEvent.AckedPackets = append([]congestion.AckedPacketInfo(nil), event.AckedPackets...)
	copyEvent.LostPackets = append([]congestion.LostPacketInfo(nil), event.LostPackets...)
	copyEvent.NeuteredPackets = append([]protocol.PacketNumber(nil), event.NeuteredPackets...)
	r.events = append(r.events, copyEvent)
	r.BatchedSendAlgorithm.OnCongestionEventEx(event)
}

func TestBBRReceivesACKAndTimerLossBatches(t *testing.T) {
	h := newBBRTestHandler(true)
	now := monotime.Now()
	h.DropPackets(protocol.EncryptionInitial, now)
	h.DropPackets(protocol.EncryptionHandshake, now)
	r := &recordingBBR{BatchedSendAlgorithm: h.bbr}
	h.bbr = r
	h.congestion = r
	var packets packetTracker
	send := func(at monotime.Time) protocol.PacketNumber {
		pn := h.PopPacketNumber(protocol.Encryption1RTT)
		h.SentPacket(at, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1200, false, false)
		return pn
	}
	first := send(now)
	second := send(now.Add(time.Millisecond))
	require.Equal(t, []protocol.ByteCount{0, 1200}, r.sentPrior, "sampler needs the in-flight total before each send")
	_, err := h.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(second)}, protocol.Encryption1RTT, now.Add(21*time.Millisecond))
	require.NoError(t, err)
	require.Len(t, r.events, 1)
	require.Equal(t, protocol.ByteCount(2400), r.events[0].PriorInFlight)
	require.Equal(t, []congestion.AckedPacketInfo{{PacketNumber: second, BytesAcked: 1200, ReceivedTime: now.Add(21 * time.Millisecond)}}, r.events[0].AckedPackets)
	require.Empty(t, r.events[0].LostPackets)
	deadline := h.GetLossDetectionTimeout()
	require.False(t, deadline.IsZero())
	require.NoError(t, h.OnLossDetectionTimeout(deadline))
	require.Len(t, r.events, 2)
	require.Equal(t, protocol.ByteCount(1200), r.events[1].PriorInFlight)
	require.Empty(t, r.events[1].AckedPackets)
	require.Equal(t, []congestion.LostPacketInfo{{PacketNumber: first, BytesLost: 1200}}, r.events[1].LostPackets)
	require.EqualValues(t, 1, h.connStats.PacketsLost.Load())
	require.EqualValues(t, 1200, h.connStats.BytesLost.Load())
	require.Zero(t, h.bytesInFlight)

	// A later ACK simultaneously acknowledges the new tail and declares the
	// preceding packets lost by packet threshold. The sampler needs one batch.
	start := now.Add(time.Second)
	var numbers []protocol.PacketNumber
	for i := 0; i < 5; i++ {
		numbers = append(numbers, send(start))
	}
	_, err = h.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(numbers[4])}, protocol.Encryption1RTT, start.Add(20*time.Millisecond))
	require.NoError(t, err)
	require.Len(t, r.events, 3)
	require.Equal(t, protocol.ByteCount(6000), r.events[2].PriorInFlight)
	require.Len(t, r.events[2].AckedPackets, 1)
	require.GreaterOrEqual(t, len(r.events[2].LostPackets), 2)
	require.Equal(t, numbers[4], r.events[2].AckedPackets[0].PacketNumber)
}

func TestBBRMTUProbeRetirementIsNotCongestionLoss(t *testing.T) {
	h := newBBRTestHandler(true)
	now := monotime.Now()
	h.DropPackets(protocol.EncryptionInitial, now)
	h.DropPackets(protocol.EncryptionHandshake, now)
	r := &recordingBBR{BatchedSendAlgorithm: h.bbr}
	h.bbr, h.congestion = r, r
	var packets packetTracker
	probe := h.PopPacketNumber(protocol.Encryption1RTT)
	h.SentPacket(now, probe, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(probe)}, protocol.Encryption1RTT, protocol.ECNNon, 1300, true, false)
	regular := h.PopPacketNumber(protocol.Encryption1RTT)
	h.SentPacket(now, regular, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(regular)}, protocol.Encryption1RTT, protocol.ECNNon, 1200, false, false)
	data := h.PopPacketNumber(protocol.Encryption1RTT)
	h.SentPacket(now.Add(time.Millisecond), data, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(data)}, protocol.Encryption1RTT, protocol.ECNNon, 1200, false, false)
	_, err := h.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(data)}, protocol.Encryption1RTT, now.Add(21*time.Millisecond))
	require.NoError(t, err)
	require.Len(t, r.events, 1)
	require.NoError(t, h.OnLossDetectionTimeout(h.GetLossDetectionTimeout()))
	require.Zero(t, h.bytesInFlight)
	require.Len(t, r.events, 2, "probe retirement must reach the sampler even without data losses or ACKs")
	require.Equal(t, []congestion.LostPacketInfo{{PacketNumber: regular, BytesLost: 1200}}, r.events[1].LostPackets, "MTU discovery loss is not congestion")
	require.Equal(t, h.bytesInFlight, r.events[1].BytesInFlight, "sampler must also account for retired probe bytes")
	require.Equal(t, []protocol.PacketNumber{probe}, r.events[1].NeuteredPackets)
	require.EqualValues(t, 1, h.connStats.PacketsLost.Load())
}

func TestBBRSkipsPacketsSentBeforeActivation(t *testing.T) {
	for _, oldPacketLost := range []bool{false, true} {
		h := newBBRTestHandler(true)
		now := monotime.Now()
		var packets packetTracker
		send := func(at monotime.Time) protocol.PacketNumber {
			pn := h.PopPacketNumber(protocol.Encryption1RTT)
			h.SentPacket(at, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1200, false, false)
			return pn
		}
		old := send(now)
		h.DropPackets(protocol.EncryptionInitial, now)
		h.DropPackets(protocol.EncryptionHandshake, now)
		r := &recordingBBR{BatchedSendAlgorithm: h.bbr}
		h.bbr, h.congestion = r, r
		current := send(now.Add(time.Millisecond))
		require.Equal(t, []protocol.ByteCount{0}, r.sentPrior)
		require.EqualValues(t, 2400, h.bytesInFlight)
		ack := current
		if !oldPacketLost {
			ack = old
		}
		_, err := h.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(ack)}, protocol.Encryption1RTT, now.Add(21*time.Millisecond))
		require.NoError(t, err)
		if !oldPacketLost {
			require.Empty(t, r.events, "a packet sent under Reno is not a BBR delivery sample")
			require.EqualValues(t, 1200, h.bbrBytesInFlight)
		} else {
			require.Len(t, r.events, 1)
			require.EqualValues(t, 1200, r.events[0].PriorInFlight)
			require.Zero(t, r.events[0].BytesInFlight)
			require.NoError(t, h.OnLossDetectionTimeout(h.GetLossDetectionTimeout()))
			require.Len(t, r.events, 1, "a pre-activation loss cannot corrupt BBR's sampler totals")
			require.EqualValues(t, 1, h.connStats.PacketsLost.Load())
		}
	}
}

func TestBBRTracksSkippedNumbersAndProbeRetirement(t *testing.T) {
	h := newBBRTestHandler(true)
	now := monotime.Now()
	h.DropPackets(protocol.EncryptionInitial, now)
	h.DropPackets(protocol.EncryptionHandshake, now)
	r := &recordingBBR{BatchedSendAlgorithm: h.bbr}
	h.bbr, h.congestion = r, r
	var packets packetTracker
	send := func(pn protocol.PacketNumber) {
		h.SentPacket(now, pn, protocol.InvalidPacketNumber, nil, []Frame{packets.NewPingFrame(pn)}, protocol.Encryption1RTT, protocol.ECNNon, 1200, false, false)
	}
	send(10)
	h.appDataPackets.history.SkippedPacket(11)
	send(12)
	send(13)
	_, err := h.ReceivedAck(&wire.AckFrame{AckRanges: ackRanges(13)}, protocol.Encryption1RTT, now.Add(20*time.Millisecond))
	require.NoError(t, err)
	require.Len(t, r.events, 1)
	require.EqualValues(t, 10, r.events[0].FirstInFlight)
	require.EqualValues(t, 2400, r.events[0].BytesInFlight)
	require.Empty(t, r.events[0].LostPackets)
	require.True(t, h.QueueProbePacket(protocol.Encryption1RTT))
	require.Len(t, r.events, 2)
	require.Equal(t, []protocol.PacketNumber{10}, r.events[1].NeuteredPackets)
	require.EqualValues(t, 12, r.events[1].FirstInFlight)
	require.EqualValues(t, 1200, r.events[1].BytesInFlight)
	require.Empty(t, r.events[1].LostPackets)
	require.Zero(t, h.connStats.PacketsLost.Load())
}

package congestion

import (
	"time"

	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/protocol"
)

// The aliases keep the imported controller on the transport's native units.
type ByteCount = protocol.ByteCount
type PacketNumber = protocol.PacketNumber

const (
	InitialPacketSize          = protocol.InitialPacketSize
	MaxPacketBufferSize        = protocol.MaxPacketBufferSize
	MinInitialPacketSize       = protocol.MinInitialPacketSize
	MaxCongestionWindowPackets = protocol.MaxCongestionWindowPackets
	PacketsPerConnectionID     = protocol.PacketsPerConnectionID
	MinPacingDelay             = time.Millisecond
)

type AckedPacketInfo struct {
	PacketNumber PacketNumber
	BytesAcked   ByteCount
	ReceivedTime monotime.Time
}

type LostPacketInfo struct {
	PacketNumber PacketNumber
	BytesLost    ByteCount
}

type RTTStatsProvider interface {
	MinRTT() time.Duration
}

// CongestionEvent describes one recovery update for packets sent by this controller.
// FirstInFlight is the earliest still-tracked packet, or largestSent+1 if none.
// NeuteredPackets were retired without indicating congestion (MTU or PTO probes).
type CongestionEvent struct {
	Time            monotime.Time
	PriorInFlight   ByteCount
	BytesInFlight   ByteCount
	FirstInFlight   PacketNumber
	AckedPackets    []AckedPacketInfo
	LostPackets     []LostPacketInfo
	NeuteredPackets []PacketNumber
}

// BatchedSendAlgorithm observes all ACKs and losses from one recovery event.
// A loss-only timer event has an empty acknowledged slice.
type BatchedSendAlgorithm interface {
	SendAlgorithmWithDebugInfos
	SetRTTStatsProvider(RTTStatsProvider)
	OnCongestionEventEx(CongestionEvent)
	// OnApplicationLimited records exhaustion of sendable data, not pacing or congestion blocking.
	OnApplicationLimited()
}

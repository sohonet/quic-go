package bbr

import (
	"testing"
	"time"

	"github.com/quic-go/quic-go/internal/congestion"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestApplicationLimitedQuietTrafficDoesNotEndStartup(t *testing.T) {
	for _, notify := range []bool{false, true} {
		b := NewBbrSender(DefaultClock{}, 1280, ProfileStandard)
		rtt := utils.NewRTTStats()
		rtt.UpdateRTT(55*time.Millisecond, 0)
		b.SetRTTStatsProvider(rtt)
		start := monotime.Now()
		const size = congestion.ByteCount(1280)
		var pn congestion.PacketNumber
		for round := 1; round <= 30; round++ {
			sent := start.Add(time.Duration(round) * 100 * time.Millisecond)
			var acked []congestion.AckedPacketInfo
			for packet := 0; packet < 8; packet++ {
				pn++
				b.OnPacketSent(sent.Add(time.Duration(packet)*200*time.Microsecond), congestion.ByteCount(packet)*size, pn, size, true)
				acked = append(acked, congestion.AckedPacketInfo{PacketNumber: pn, BytesAcked: size})
			}
			if notify {
				b.OnApplicationLimited()
			}
			b.OnCongestionEventEx(congestion.CongestionEvent{
				Time: sent.Add(55 * time.Millisecond), PriorInFlight: 8 * size, FirstInFlight: pn + 1,
				AckedPackets: acked,
			})
		}
		if notify {
			require.False(t, b.isAtFullBandwidth, "low offered traffic cannot establish the path's full bandwidth")
			require.Equal(t, bbrMode(bbrModeStartup), b.mode)
			quietBandwidth := b.bandwidthEstimate()
			// Resume a larger offered load without exhaustion notifications. ACK-only
			// packets must not prevent tracked delivery samples from ending the phase.
			for round := 31; round <= 50; round++ {
				sent := start.Add(time.Duration(round) * 100 * time.Millisecond)
				pn++
				b.OnPacketSent(sent, 0, pn, 32, false)
				if round == 31 {
					// The exhaustion boundary can itself be an untracked ACK-only packet.
					b.OnApplicationLimited()
				}
				var acked []congestion.AckedPacketInfo
				for packet := 0; packet < 32; packet++ {
					pn++
					b.OnPacketSent(sent.Add(time.Duration(packet+1)*200*time.Microsecond), congestion.ByteCount(packet)*size, pn, size, true)
					acked = append(acked, congestion.AckedPacketInfo{PacketNumber: pn, BytesAcked: size})
				}
				b.OnCongestionEventEx(congestion.CongestionEvent{
					Time: sent.Add(55 * time.Millisecond), PriorInFlight: 32 * size, FirstInFlight: pn + 1, AckedPackets: acked,
				})
			}
			require.False(t, b.lastSampleIsAppLimited)
			require.Greater(t, b.bandwidthEstimate(), quietBandwidth)
			require.True(t, b.isAtFullBandwidth, "a sustained plateau must still terminate startup")
		} else {
			require.True(t, b.isAtFullBandwidth, "control reproduces premature exit without notification")
		}
	}
}

func TestApplicationLimitedStartupStillExitsOnExcessiveLoss(t *testing.T) {
	for _, appLimited := range []bool{false, true} {
		for _, estimate := range []Bandwidth{1000000, 2000000} {
			b := NewBbrSender(DefaultClock{}, 1280, ProfileStandard)
			b.lastSampleIsAppLimited = appLimited
			b.bandwidthAtLastRound = 1000000
			b.maxBandwidth.Update(estimate, 0)
			b.numLossEventsInRound = defaultStartupFullLossCount
			b.bytesLostInRound = 2560
			state := sendTimeState{isValid: true, bytesInFlight: 64000}
			b.checkIfFullBandwidthReached(&state)
			require.True(t, b.isAtFullBandwidth, "loss remains authoritative even for an application-limited sample")
		}
	}
}

func TestSetMaxDatagramSizeRescalesPacketSizedWindows(t *testing.T) {
	const oldMaxDatagramSize = congestion.ByteCount(1000)
	const newMaxDatagramSize = congestion.ByteCount(1400)
	const initialCongestionWindowPackets = congestion.ByteCount(20)
	const maxCongestionWindowPackets = congestion.ByteCount(80)

	b := newBbrSender(
		DefaultClock{},
		oldMaxDatagramSize,
		initialCongestionWindowPackets*oldMaxDatagramSize,
		maxCongestionWindowPackets*oldMaxDatagramSize,
		ProfileStandard,
	)
	b.congestionWindow = b.initialCongestionWindow

	b.SetMaxDatagramSize(newMaxDatagramSize)

	require.Equal(t, initialCongestionWindowPackets*newMaxDatagramSize, b.initialCongestionWindow)
	require.Equal(t, maxCongestionWindowPackets*newMaxDatagramSize, b.maxCongestionWindow)
	require.Equal(t, minCongestionWindowPackets*newMaxDatagramSize, b.minCongestionWindow)
	require.Equal(t, initialCongestionWindowPackets*newMaxDatagramSize, b.congestionWindow)
}

func TestSetMaxDatagramSizeClampsCongestionWindow(t *testing.T) {
	const oldMaxDatagramSize = congestion.ByteCount(1000)
	const newMaxDatagramSize = congestion.ByteCount(1400)

	b := NewBbrSender(DefaultClock{}, oldMaxDatagramSize, ProfileStandard)
	b.congestionWindow = b.minCongestionWindow + oldMaxDatagramSize
	b.recoveryWindow = b.minCongestionWindow + oldMaxDatagramSize

	b.SetMaxDatagramSize(newMaxDatagramSize)

	require.Equal(t, b.minCongestionWindow, b.congestionWindow)
	require.Equal(t, b.minCongestionWindow, b.recoveryWindow)
}

func TestNewBbrSenderAppliesProfiles(t *testing.T) {
	testCases := []struct {
		name                                string
		profile                             Profile
		highGain                            float64
		highCwndGain                        float64
		congestionWindowGainConstant        float64
		numStartupRtts                      int64
		drainToTarget                       bool
		detectOvershooting                  bool
		bytesLostMultiplier                 uint8
		enableAckAggregationDuringStartup   bool
		expireAckAggregationInStartup       bool
		enableOverestimateAvoidance         bool
		reduceExtraAckedOnBandwidthIncrease bool
	}{
		{
			name:                         "standard",
			profile:                      ProfileStandard,
			highGain:                     defaultHighGain,
			highCwndGain:                 derivedHighCWNDGain,
			congestionWindowGainConstant: 2.0,
			numStartupRtts:               roundTripsWithoutGrowthBeforeExitingStartup,
			bytesLostMultiplier:          2,
		},
		{
			name:                                "conservative",
			profile:                             ProfileConservative,
			highGain:                            2.25,
			highCwndGain:                        1.75,
			congestionWindowGainConstant:        1.75,
			numStartupRtts:                      2,
			drainToTarget:                       true,
			detectOvershooting:                  true,
			bytesLostMultiplier:                 1,
			enableOverestimateAvoidance:         true,
			reduceExtraAckedOnBandwidthIncrease: true,
		},
		{
			name:                              "aggressive",
			profile:                           ProfileAggressive,
			highGain:                          3.0,
			highCwndGain:                      2.25,
			congestionWindowGainConstant:      2.5,
			numStartupRtts:                    4,
			bytesLostMultiplier:               2,
			enableAckAggregationDuringStartup: true,
			expireAckAggregationInStartup:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBbrSender(DefaultClock{}, congestion.InitialPacketSize, tc.profile)
			require.Equal(t, tc.profile, b.profile)
			require.Equal(t, tc.highGain, b.highGain)
			require.Equal(t, tc.highCwndGain, b.highCwndGain)
			require.Equal(t, tc.congestionWindowGainConstant, b.congestionWindowGainConstant)
			require.Equal(t, tc.numStartupRtts, b.numStartupRtts)
			require.Equal(t, tc.drainToTarget, b.drainToTarget)
			require.Equal(t, tc.detectOvershooting, b.detectOvershooting)
			require.Equal(t, tc.bytesLostMultiplier, b.bytesLostMultiplierWhileDetectingOvershooting)
			require.Equal(t, tc.enableAckAggregationDuringStartup, b.enableAckAggregationDuringStartup)
			require.Equal(t, tc.expireAckAggregationInStartup, b.expireAckAggregationInStartup)
			require.Equal(t, tc.enableOverestimateAvoidance, b.sampler.IsOverestimateAvoidanceEnabled())
			require.Equal(t, tc.reduceExtraAckedOnBandwidthIncrease, b.sampler.maxAckHeightTracker.reduceExtraAckedOnBandwidthIncrease)
			require.Equal(t, b.highGain, b.pacingGain)
			require.Equal(t, b.highCwndGain, b.congestionWindowGain)
		})
	}
}

func TestParseProfile(t *testing.T) {
	profile, err := ParseProfile("")
	require.NoError(t, err)
	require.Equal(t, ProfileStandard, profile)

	profile, err = ParseProfile("Aggressive")
	require.NoError(t, err)
	require.Equal(t, ProfileAggressive, profile)

	_, err = ParseProfile("turbo")
	require.EqualError(t, err, `unsupported BBR profile "turbo"`)
}

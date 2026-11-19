package quic

import (
	"testing"

	"github.com/quic-go/quic-go/internal/ackhandler"
	mockackhandler "github.com/quic-go/quic-go/internal/mocks/ackhandler"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type applicationLimitedPacketHandler struct {
	ackhandler.SentPacketHandler
	notifications int
}

func (h *applicationLimitedPacketHandler) OnApplicationLimited() { h.notifications++ }

func TestConnectionApplicationLimitedOnlyWhenSendableDataExhausted(t *testing.T) {
	for _, gso := range []bool{false, true} {
		for _, stop := range []string{"empty", "exhausted", "pacing", "congestion", "sendQueue"} {
			name := "datagrams/" + stop
			if gso {
				name = "gso/" + stop
			}
			t.Run(name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				sph := mockackhandler.NewMockSentPacketHandler(ctrl)
				observer := &applicationLimitedPacketHandler{SentPacketHandler: sph}
				sender := NewMockSender(ctrl)
				tc := newServerTestConnection(t, ctrl, nil, gso,
					connectionOptSentPacketHandler(observer), connectionOptSender(sender), connectionOptHandshakeConfirmed())
				sph.EXPECT().ECNMode(true).Return(protocol.ECNNon).AnyTimes()
				mode := ackhandler.SendAny
				if stop == "pacing" {
					mode = ackhandler.SendPacingLimited
				}
				if stop == "congestion" {
					mode = ackhandler.SendAck
				}
				sph.EXPECT().SendMode(gomock.Any()).Return(mode).AnyTimes()
				sph.EXPECT().TimeUntilSend().Return(monotime.Now()).AnyTimes()
				sender.EXPECT().WouldBlock().Return(stop == "sendQueue").AnyTimes()
				if stop != "empty" {
					sph.EXPECT().SentPacket(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any())
					tc.packer.EXPECT().AppendPacket(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
						func(buf *packetBuffer, size protocol.ByteCount, _ monotime.Time, _ protocol.Version) (shortHeaderPacket, error) {
							if stop == "sendQueue" {
								size = 1
							}
							buf.Data = append(buf.Data, make([]byte, int(size))...)
							return shortHeaderPacket{Length: size}, nil
						})
					sender.EXPECT().Send(gomock.Any(), gomock.Any(), protocol.ECNNon).Do(
						func(buf *packetBuffer, _ uint16, _ protocol.ECN) { buf.Release() })
				}
				if stop == "empty" || stop == "exhausted" {
					tc.packer.EXPECT().AppendPacket(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(shortHeaderPacket{}, errNothingToPack)
				}
				var err error
				if gso {
					err = tc.conn.sendPacketsWithGSO(monotime.Now())
				} else {
					err = tc.conn.sendPacketsWithoutGSO(monotime.Now())
				}
				require.NoError(t, err)
				expected := 0
				if stop == "empty" || stop == "exhausted" {
					expected = 1
				}
				require.Equal(t, expected, observer.notifications)
			})
		}
	}
}

# BBR provenance and integration

Imported from https://github.com/apernet/hysteria, commit
`c2b181e0b0e72b077e4b0b7297735af14d1f9929`, directories
`core/internal/congestion/bbr` and `core/internal/congestion/common/pacer.go`.
The upstream core licence is preserved in `LICENSE.hysteria` and also covers
the imported pacer in `pacing/`. This is the older Chromium-derived BBR
implementation used by Hysteria; it is not BBRv3.

Local changes:

- Import paths and congestion types use quic-go's internal protocol units.
- The filter uses standard-library `cmp.Ordered` and a local numeric constraint
  instead of adding an `x/exp` dependency.
- ProbeRTT uses half the estimated bandwidth-delay product, bounded by the BDP
  saved before refreshing an expired RTT, with the original packet floor. The
  ten-second expiry and 200 ms probe duration remain. See
  https://datatracker.ietf.org/doc/html/draft-ietf-ccwg-bbr-07#section-5.3.4.
- The send callback receives bytes in flight before the send. Recovery batches
  carry the actual remaining flight and earliest tracked packet, replacing the
  imported packet-number cleanup estimate. MTU losses and PTO retirements remove
  sampler state without reporting congestion loss. Packets sent before BBR
  activation are excluded from its delivery model.
- Regression tests cover these accounting transitions, skipped packet numbers,
  delayed pre-activation ACKs/losses and migration.
- Bandwidth/time conversions use overflow-safe intermediate arithmetic; loss-only
  timer events enter/extend recovery; the pacer starts with the configured packet
  size. These fixes to the imported controller have focused regressions.
- A regression test covers the saved bound, decreased-rate bound, packet floor
  and probe duration. No diagnostic force-probe trigger is included.

`Config.EnableBBR` is opt-in. Reno handles the handshake; BBR starts on the
connection goroutine when handshake packet-number spaces are dropped. No public
runtime controller-replacement API is added. ACK/loss recovery events are batched
for the sampler, including loss-only timer events. Migration starts a fresh path
model and sampler. Disabled configurations retain existing Reno behaviour.

This controller does not consume ECN feedback, so opting into it disables ECN
marking for the connection, including the handshake. This is explicit fallback
to non-ECN traffic, not ignoring congestion marks on ECT packets.

The imported profile definitions remain for provenance and their upstream tests;
the transport selects only the standard profile. Public configuration does not
expose the other profiles.

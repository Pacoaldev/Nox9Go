//go:build !linux

package tray

// No L2 on this platform: neither Windows nor macOS offers an unprivileged
// "who owns this listening socket" query that x/sys exposes.
//
// DecideStop still fails closed for the in-process token path (a claim is not
// an identity — §5.2). Actions.Stop then takes the CLI fallback on these
// platforms: shell out to the sibling `nox9go stop`, the same way Start
// already shells out to `nox9go start`. The child process owns the credential;
// the tray never puts it on the wire (§C3).
//
// §9 of the tray spec records the upgrade: a UDS / named-pipe control channel
// lets the kernel attest the peer, which solves all three platforms without
// unsafe code. It needs a gateway change and its own review.

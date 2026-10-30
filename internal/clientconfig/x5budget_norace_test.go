//go:build !race

package clientconfig

import "time"

// x5Budget is the §7.4 X5 ceiling for a 64 KB file. The race detector slows
// every memory access by roughly an order of magnitude, so the assertion would
// measure instrumentation, not the patcher; x5budget_race_test.go relaxes it.
//
// TestPatchScalesLinearly still uses a ratio (not a wall-clock ceiling) on
// plain `go test`. Under -race the ratio itself is warped by instrumentation,
// so that test skips via patchscale_race_test.go rather than widening the
// budget until a real quadratic would pass.
const x5Budget = 200 * time.Millisecond

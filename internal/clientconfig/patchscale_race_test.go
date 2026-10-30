//go:build race

package clientconfig

// patchScaleUnderRace skips TestPatchScalesLinearly when the race detector is
// on. That test compares ns/op across a 4× sample to prove linear (~4×) not
// quadratic (~16×) cost; race instrumentation warps the ratio (CI saw 8.37×
// against an 8× budget with no patcher change), so the measurement would be of
// the detector, not the algorithm. Plain `go test` still runs the check.
const patchScaleUnderRace = true

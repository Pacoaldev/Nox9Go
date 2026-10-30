//go:build !race

package clientconfig

// patchScaleUnderRace is false outside the race detector: TestPatchScalesLinearly
// runs its ns/op ratio check on a plain `go test`.
const patchScaleUnderRace = false

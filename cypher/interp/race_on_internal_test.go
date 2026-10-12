//go:build race

package interp

// raceEnabled is true under the race detector, which makes the large
// differential tests 15-20 times slower.
const raceEnabled = true

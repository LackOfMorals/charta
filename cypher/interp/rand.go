package interp

import (
	"fmt"
	mrand "math/rand"
)

func rand01() float64 { return mrand.Float64() }

func randomUUID() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(mrand.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

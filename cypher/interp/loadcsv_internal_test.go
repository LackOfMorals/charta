package interp

import "testing"

func TestReservedDeviceNames(t *testing.T) {
	for _, n := range []string{"CON", "con.csv", "Nul", "COM1", "lpt9.txt", "aux.", "PRN"} {
		if !isReservedDeviceName(n) {
			t.Errorf("%q should be reserved", n)
		}
	}
	for _, n := range []string{"data.csv", "COM0", "COM10", "console.csv", "x.con"} {
		if isReservedDeviceName(n) {
			t.Errorf("%q should not be reserved", n)
		}
	}
}

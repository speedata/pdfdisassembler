package crypt

import "testing"

// New must reject an /Encrypt /Length whose derived key size (Length/8) falls
// outside [1, 16] — the RC4/AESV2 file key is sliced from a 16-byte MD5 digest,
// so a hostile large or negative /Length would slice out of range and panic.
func TestNewRejectsHostileKeyLength(t *testing.T) {
	for _, length := range []int{136, 256, 4096, -8} {
		base := Params{
			V:          2,
			R:          3,
			Length:     length,
			OwnerEntry: make([]byte, 32),
			UserEntry:  make([]byte, 32),
			ID0:        make([]byte, 16),
		}
		if _, err := New(base, nil); err == nil {
			t.Fatalf("Length=%d: expected error, got nil", length)
		}
	}
}

package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"testing"
)

// deriveRC4Key mirrors computeRC4Key's derivation (without the /U validation),
// so a test can compute the matching /U for an empty-password fixture.
func deriveRC4Key(p Params, password []byte) []byte {
	pad := padPassword(password)
	h := md5.New()
	h.Write(pad)
	h.Write(p.OwnerEntry)
	h.Write([]byte{
		byte(uint32(p.P)), byte(uint32(p.P) >> 8),
		byte(uint32(p.P) >> 16), byte(uint32(p.P) >> 24),
	})
	h.Write(p.ID0)
	if p.R >= 4 && !p.EncryptMeta {
		h.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	sum := h.Sum(nil)
	keyLen := p.Length / 8
	if keyLen == 0 {
		keyLen = 5
	}
	if p.R >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(sum[:keyLen])
			sum = s[:]
		}
	}
	key := make([]byte, keyLen)
	copy(key, sum[:keyLen])
	return key
}

// New must reconstruct the V2/V4 file key from a correct empty-password /U.
func TestNewV2V4KeyDerivationRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		V, R, bits int
	}{
		{"V2R2", 2, 2, 40},
		{"V2R3", 2, 3, 128},
		{"V4R4", 4, 4, 128},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			password := []byte{}
			p := Params{
				V: tc.V, R: tc.R, Length: tc.bits,
				OwnerEntry:  bytes.Repeat([]byte{0x5a}, 32),
				ID0:         bytes.Repeat([]byte{0x7c}, 16),
				P:           -3904,
				EncryptMeta: true,
				StmF:        "StdCF", StrF: "StdCF",
				CryptFilters: map[string]string{"StdCF": "V2"},
			}
			key := deriveRC4Key(p, password)
			u, err := computeU(p, key)
			if err != nil {
				t.Fatalf("computeU: %v", err)
			}
			p.UserEntry = u
			h, err := New(p, password)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if !bytes.Equal(h.FileKey, key) {
				t.Fatalf("file key mismatch:\n got %x\nwant %x", h.FileKey, key)
			}
		})
	}
}

// A wrong /U must be rejected, not accepted with a garbage key.
func TestNewV2RejectsWrongUserEntry(t *testing.T) {
	p := Params{
		V: 2, R: 3, Length: 128,
		OwnerEntry: bytes.Repeat([]byte{0x5a}, 32),
		ID0:        bytes.Repeat([]byte{0x7c}, 16),
		UserEntry:  bytes.Repeat([]byte{0x00}, 32), // not the real /U
	}
	if _, err := New(p, []byte{}); err == nil {
		t.Fatal("expected password-incorrect error, got nil")
	}
}

// aesCBCEncryptRaw is the inverse of v5DecryptKey: CBC-encrypt block-aligned
// data with a zero-prepend-free layout.
func aesCBCEncryptRaw(t *testing.T, key, iv, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	out := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plaintext)
	return out
}

// computeV5Key must recover the AES-256 file key from a correct empty-password
// /U, /UE for both R=5 (SHA-256) and R=6 (the iterated r6Hash).
func TestComputeV5KeyRoundTrip(t *testing.T) {
	for _, R := range []int{5, 6} {
		t.Run(map[int]string{5: "R5", 6: "R6"}[R], func(t *testing.T) {
			password := []byte("user-pw")
			fileKey := bytes.Repeat([]byte{0x42}, 32)
			uVS := bytes.Repeat([]byte{0x01}, 8)
			uKS := bytes.Repeat([]byte{0x02}, 8)

			uValHash, err := v5Hash(password, uVS, nil, R)
			if err != nil {
				t.Fatalf("v5Hash(validation): %v", err)
			}
			kHash, err := v5Hash(password, uKS, nil, R)
			if err != nil {
				t.Fatalf("v5Hash(key): %v", err)
			}
			ue := aesCBCEncryptRaw(t, kHash, make([]byte, aes.BlockSize), fileKey)

			userEntry := append(append(append([]byte{}, uValHash...), uVS...), uKS...)
			p := Params{
				V: 5, R: R,
				UserEntry:  userEntry,            // 48 bytes
				OwnerEntry: make([]byte, 48),     // present but unused (user path matches first)
				UE:         ue,                   // 32 bytes
				OE:         make([]byte, 32),
			}
			key, err := computeV5Key(p, password)
			if err != nil {
				t.Fatalf("computeV5Key: %v", err)
			}
			if !bytes.Equal(key, fileKey) {
				t.Fatalf("V5 key mismatch:\n got %x\nwant %x", key, fileKey)
			}
		})
	}
}

// New must map each V4 /CF crypt-filter method to a cipher and reject unknown
// ones, for a valid empty-password setup.
func TestNewV4CryptFilterMethods(t *testing.T) {
	cases := []struct {
		cfm     string
		wantErr bool
	}{
		{"V2", false}, {"AESV2", false}, {"AESV3", false}, {"None", false}, {"Bogus", true},
	}
	for _, tc := range cases {
		t.Run(tc.cfm, func(t *testing.T) {
			p := Params{
				V: 4, R: 4, Length: 128,
				OwnerEntry:  bytes.Repeat([]byte{0x5a}, 32),
				ID0:         bytes.Repeat([]byte{0x7c}, 16),
				P:           -3904,
				EncryptMeta: true,
				StmF:        "StdCF", StrF: "StdCF",
				CryptFilters: map[string]string{"StdCF": tc.cfm},
			}
			key := deriveRC4Key(p, nil)
			u, err := computeU(p, key)
			if err != nil {
				t.Fatalf("computeU: %v", err)
			}
			p.UserEntry = u
			_, err = New(p, nil)
			if tc.wantErr != (err != nil) {
				t.Fatalf("CFM %q: wantErr=%v, got err=%v", tc.cfm, tc.wantErr, err)
			}
		})
	}
}

// computeV5Key must reject short /U, /O, /UE, /OE entries rather than slicing
// out of range.
func TestComputeV5KeyRejectsShortEntries(t *testing.T) {
	cases := map[string]Params{
		"short_user":  {V: 5, R: 6, UserEntry: make([]byte, 47), OwnerEntry: make([]byte, 48), UE: make([]byte, 32), OE: make([]byte, 32)},
		"short_owner": {V: 5, R: 6, UserEntry: make([]byte, 48), OwnerEntry: make([]byte, 47), UE: make([]byte, 32), OE: make([]byte, 32)},
		"short_ue":    {V: 5, R: 6, UserEntry: make([]byte, 48), OwnerEntry: make([]byte, 48), UE: make([]byte, 31), OE: make([]byte, 32)},
		"short_oe":    {V: 5, R: 6, UserEntry: make([]byte, 48), OwnerEntry: make([]byte, 48), UE: make([]byte, 32), OE: make([]byte, 31)},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := computeV5Key(p, []byte{}); err == nil {
				t.Fatal("expected error for short entry, got nil")
			}
		})
	}
}

package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"testing"
)

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

// fixedIV is a deterministic 16-byte IV for reproducible AES test vectors.
var fixedIV = []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// aesCBCEncryptStream is the inverse of aesCBCDecrypt: PKCS#7-pad, CBC-encrypt,
// and prepend the IV, producing a blob the handler should decrypt back.
func aesCBCEncryptStream(t *testing.T, key, iv, plaintext []byte) []byte {
	t.Helper()
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte{}, plaintext...), bytes.Repeat([]byte{byte(padLen)}, padLen)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	body := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(body, padded)
	return append(append([]byte{}, iv...), body...)
}

func TestDecryptRC4RoundTrip(t *testing.T) {
	h := &Handler{FileKey: bytes.Repeat([]byte{0x33}, 16), StreamAlg: AlgRC4, StringAlg: AlgRC4}
	plaintext := []byte("the quick brown fox / RC4")
	// RC4 is symmetric: decrypting plaintext yields ciphertext.
	ct, err := h.DecryptStream(plaintext, 12, 0, "")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := h.DecryptStream(ct, 12, 0, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: %q", got)
	}
	// Per-object keying: the same bytes under a different object number must
	// not decrypt to the plaintext.
	if other, _ := h.DecryptStream(ct, 99, 0, ""); bytes.Equal(other, plaintext) {
		t.Fatal("ciphertext decrypted under wrong object number")
	}
}

func TestDecryptAES128RoundTrip(t *testing.T) {
	h := &Handler{FileKey: bytes.Repeat([]byte{0x11}, 16), StreamAlg: AlgAES128, StringAlg: AlgAES128}
	plaintext := []byte("attachment bytes under AESV2")
	key := h.objKeyRC4orAES(7, 0, true)
	ct := aesCBCEncryptStream(t, key, fixedIV, plaintext)
	got, err := h.DecryptStream(ct, 7, 0, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: %q", got)
	}
}

func TestDecryptAES256RoundTrip(t *testing.T) {
	// V5/AESV3 keys streams directly with the file key (no per-object key).
	h := &Handler{FileKey: bytes.Repeat([]byte{0x22}, 32), StreamAlg: AlgAES256, StringAlg: AlgAES256}
	plaintext := []byte("AES-256 stream content for V5")
	ct := aesCBCEncryptStream(t, h.FileKey, fixedIV, plaintext)
	got, err := h.DecryptString(ct, 5, 0)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: %q", got)
	}
}

// Attacker-supplied AES blobs (too short for the IV, not block-aligned, empty)
// must surface an error or empty output — never panic.
func TestDecryptAESMalformedNoPanic(t *testing.T) {
	h := &Handler{FileKey: bytes.Repeat([]byte{0x11}, 16), StreamAlg: AlgAES128}
	cases := map[string][]byte{
		"empty":           {},
		"shorter_than_iv": make([]byte, aes.BlockSize-1),
		"iv_only":         make([]byte, aes.BlockSize),
		"unaligned_body":  make([]byte, aes.BlockSize+aes.BlockSize-1),
		"one_byte":        {0x00},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			// Must not panic; result is ignored, the point is robustness.
			_, _ = h.DecryptStream(data, 1, 0, "")
		})
	}
}

func TestDecryptIdentityPassthrough(t *testing.T) {
	h := &Handler{StreamAlg: AlgIdentity, StringAlg: AlgIdentity}
	data := []byte{0xde, 0xad, 0xbe, 0xef}
	got, err := h.DecryptStream(data, 1, 0, "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("identity altered data")
	}
}

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
				UserEntry:  userEntry,        // 48 bytes
				OwnerEntry: make([]byte, 48), // present but unused (user path matches first)
				UE:         ue,               // 32 bytes
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

// Owner-password path. Unlike the user path, the owner hash mixes in the
// 48-byte /U entry (§7.6.4.4.10) — so userEntry here must be well-formed.
func TestComputeV5KeyOwnerPath(t *testing.T) {
	const R = 6
	password := []byte("owner-pw")
	fileKey := bytes.Repeat([]byte{0x37}, 32)

	// All-zero validation hash (first 32 bytes) can never equal v5Hash output,
	// forcing the user path to miss so the owner path is taken.
	uVS := bytes.Repeat([]byte{0x11}, 8)
	uKS := bytes.Repeat([]byte{0x22}, 8)
	userEntry := append(append(make([]byte, 32), uVS...), uKS...) // 48 bytes

	oVS := bytes.Repeat([]byte{0x33}, 8)
	oKS := bytes.Repeat([]byte{0x44}, 8)
	oValHash, err := v5Hash(password, oVS, userEntry, R)
	if err != nil {
		t.Fatalf("v5Hash(owner validation): %v", err)
	}
	oKeyHash, err := v5Hash(password, oKS, userEntry, R)
	if err != nil {
		t.Fatalf("v5Hash(owner key): %v", err)
	}
	oe := aesCBCEncryptRaw(t, oKeyHash, make([]byte, aes.BlockSize), fileKey)
	ownerEntry := append(append(append([]byte{}, oValHash...), oVS...), oKS...)

	p := Params{
		V: 5, R: R,
		UserEntry:  userEntry,
		OwnerEntry: ownerEntry,
		UE:         make([]byte, 32), // present but never reached
		OE:         oe,
	}
	key, err := computeV5Key(p, password)
	if err != nil {
		t.Fatalf("computeV5Key: %v", err)
	}
	if !bytes.Equal(key, fileKey) {
		t.Fatalf("owner-path key mismatch:\n got %x\nwant %x", key, fileKey)
	}
}

func TestComputeV5KeyWrongPassword(t *testing.T) {
	p := Params{
		V: 5, R: 6,
		UserEntry:  bytes.Repeat([]byte{0x01}, 48),
		OwnerEntry: bytes.Repeat([]byte{0x02}, 48),
		UE:         bytes.Repeat([]byte{0x03}, 32),
		OE:         bytes.Repeat([]byte{0x04}, 32),
	}
	overlong := bytes.Repeat([]byte{'z'}, 200) // > 127: exercises the spec truncation
	if _, err := computeV5Key(p, overlong); err == nil {
		t.Fatal("expected an error for a non-matching password, got nil")
	}
}

// New must reject an unsupported /V rather than returning a zero handler.
func TestNewUnsupportedVersion(t *testing.T) {
	for _, v := range []int{0, 3, 99} {
		if _, err := New(Params{V: v}, nil); err == nil {
			t.Errorf("New(/V %d) should error", v)
		}
	}
}

// New drives the /V 5 branch end to end (AES-256 key derivation + algorithm
// selection), not just computeV5Key in isolation.
func TestNewV5(t *testing.T) {
	const R = 6
	password := []byte("v5-user")
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

	h, err := New(Params{
		V: 5, R: R,
		UserEntry:  userEntry,
		OwnerEntry: make([]byte, 48),
		UE:         ue,
		OE:         make([]byte, 32),
	}, password)
	if err != nil {
		t.Fatalf("New(V5): %v", err)
	}
	if !bytes.Equal(h.FileKey, fileKey) {
		t.Fatal("V5 file key mismatch")
	}
	if h.StreamAlg != AlgAES256 {
		t.Errorf("StreamAlg = %v, want AlgAES256", h.StreamAlg)
	}
}

// A per-stream Identity crypt filter overrides the default algorithm and passes
// the bytes through unchanged.
func TestDecryptStreamIdentityOverride(t *testing.T) {
	h := &Handler{StreamAlg: AlgRC4, FileKey: bytes.Repeat([]byte{1}, 16)}
	data := []byte("plaintext")
	out, err := h.DecryptStream(data, 1, 0, "Identity")
	if err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("Identity override = %q, want %q", out, data)
	}
}

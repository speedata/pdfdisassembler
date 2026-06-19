package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

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
		"empty":        {},
		"shorter_than_iv": make([]byte, aes.BlockSize-1),
		"iv_only":      make([]byte, aes.BlockSize),
		"unaligned_body": make([]byte, aes.BlockSize+aes.BlockSize-1),
		"one_byte":     {0x00},
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

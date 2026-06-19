// Package crypt implements the PDF /Standard security handler for
// versions V2 (RC4), V4 (RC4 or AES-128) and V5 (AES-256, PDF 1.7
// Extension 3 and PDF 2.0).
//
// Only password-based access (the user password "empty string" path
// included) is supported. Public-key encryption (/Adobe.PubSec) is
// out of scope.
package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
)

// Algorithm identifies a stream/string cipher.
type Algorithm int

const (
	AlgRC4 Algorithm = iota + 1
	AlgAES128
	AlgAES256
	AlgIdentity
)

// Handler holds the state needed to decrypt strings and streams in a PDF
// once a password has been validated.
type Handler struct {
	V          int        // /V version
	R          int        // /R revision
	Length     int        // key length in bits (V2/V4)
	FileKey    []byte     // file encryption key (computed from password)
	StringAlg  Algorithm  // algorithm for strings
	StreamAlg  Algorithm  // algorithm for streams
	EmbedAlg   Algorithm  // algorithm for embedded files
	CryptFilts map[string]filterDef
	StmF       string // default stream filter (V4)
	StrF       string // default string filter (V4)
	EFF        string // embedded-file filter (V4)
}

type filterDef struct {
	CFM Algorithm
}

// Params is the inputs needed to instantiate a Handler from the PDF
// /Encrypt dict.
type Params struct {
	V             int
	R             int
	Length        int
	P             int32
	OwnerEntry    []byte // /O
	UserEntry     []byte // /U
	OE            []byte // /OE (V5)
	UE            []byte // /UE (V5)
	Perms         []byte // /Perms (V5)
	ID0           []byte // first element of /ID
	EncryptMeta   bool
	StmF          string
	StrF          string
	EFF           string
	CryptFilters  map[string]string // name → CFM
}

// New tries to instantiate a Handler given the encryption parameters and
// the user password (empty string is the most common case).
func New(p Params, password []byte) (*Handler, error) {
	h := &Handler{
		V:           p.V,
		R:           p.R,
		Length:      p.Length,
		StmF:        p.StmF,
		StrF:        p.StrF,
		EFF:         p.EFF,
		CryptFilts:  map[string]filterDef{},
	}
	for name, cfm := range p.CryptFilters {
		alg, err := algFromCFM(cfm)
		if err != nil {
			return nil, err
		}
		h.CryptFilts[name] = filterDef{CFM: alg}
	}

	switch p.V {
	case 1, 2:
		h.StringAlg = AlgRC4
		h.StreamAlg = AlgRC4
		h.EmbedAlg = AlgRC4
		key, err := computeRC4Key(p, password)
		if err != nil {
			return nil, err
		}
		h.FileKey = key
	case 4:
		// V4 introduces /CF, /StmF, /StrF for per-stream/string cipher.
		h.StringAlg = h.algFor(p.StrF)
		h.StreamAlg = h.algFor(p.StmF)
		h.EmbedAlg = h.algFor(p.EFF)
		key, err := computeRC4Key(p, password)
		if err != nil {
			return nil, err
		}
		h.FileKey = key
	case 5:
		h.StringAlg = AlgAES256
		h.StreamAlg = AlgAES256
		h.EmbedAlg = AlgAES256
		key, err := computeV5Key(p, password)
		if err != nil {
			return nil, err
		}
		h.FileKey = key
	default:
		return nil, fmt.Errorf("crypt: unsupported /V %d", p.V)
	}
	return h, nil
}

func (h *Handler) algFor(filterName string) Algorithm {
	if filterName == "" || filterName == "Identity" {
		return AlgIdentity
	}
	if def, ok := h.CryptFilts[filterName]; ok {
		return def.CFM
	}
	return AlgIdentity
}

func algFromCFM(cfm string) (Algorithm, error) {
	switch cfm {
	case "V2":
		return AlgRC4, nil
	case "AESV2":
		return AlgAES128, nil
	case "AESV3":
		return AlgAES256, nil
	case "None":
		return AlgIdentity, nil
	}
	return 0, fmt.Errorf("crypt: unknown /CFM %q", cfm)
}

// DecryptString decrypts a string using the configured string algorithm
// and object identity.
func (h *Handler) DecryptString(data []byte, objNum, objGen int) ([]byte, error) {
	return h.decrypt(data, objNum, objGen, h.StringAlg)
}

// DecryptStream decrypts a stream. cryptFilterName, if non-empty, overrides
// the default stream algorithm (V4 streams can carry an inline /Filter
// chain containing /Crypt with parameters).
func (h *Handler) DecryptStream(data []byte, objNum, objGen int, cryptFilterName string) ([]byte, error) {
	alg := h.StreamAlg
	if cryptFilterName != "" {
		alg = h.algFor(cryptFilterName)
	}
	return h.decrypt(data, objNum, objGen, alg)
}

func (h *Handler) decrypt(data []byte, objNum, objGen int, alg Algorithm) ([]byte, error) {
	switch alg {
	case AlgIdentity:
		return data, nil
	case AlgRC4:
		key := h.objKeyRC4orAES(objNum, objGen, false)
		out := make([]byte, len(data))
		c, _ := rc4.NewCipher(key)
		c.XORKeyStream(out, data)
		return out, nil
	case AlgAES128:
		key := h.objKeyRC4orAES(objNum, objGen, true)
		return aesCBCDecrypt(key, data)
	case AlgAES256:
		return aesCBCDecrypt(h.FileKey, data)
	}
	return nil, fmt.Errorf("crypt: unknown algorithm %d", alg)
}

// objKeyRC4orAES derives the per-object encryption key for V2/V4.
//
// PDF 32000-1:2008 §7.6.2: object key = MD5(fileKey || lo3(objNum) ||
// lo2(objGen) || (for AES) "sAlT"). Truncate to min(len(fileKey)+5, 16).
func (h *Handler) objKeyRC4orAES(objNum, objGen int, aes bool) []byte {
	buf := make([]byte, 0, len(h.FileKey)+9)
	buf = append(buf, h.FileKey...)
	buf = append(buf,
		byte(objNum),
		byte(objNum>>8),
		byte(objNum>>16),
		byte(objGen),
		byte(objGen>>8),
	)
	if aes {
		buf = append(buf, 's', 'A', 'l', 'T')
	}
	sum := md5.Sum(buf)
	n := len(h.FileKey) + 5
	if n > 16 {
		n = 16
	}
	return sum[:n]
}

// aesCBCDecrypt unwraps AES/CBC/PKCS#7 with a 16-byte IV prepended.
func aesCBCDecrypt(key, data []byte) ([]byte, error) {
	if len(data) < aes.BlockSize {
		return nil, errors.New("crypt: AES data shorter than IV")
	}
	iv := data[:aes.BlockSize]
	body := data[aes.BlockSize:]
	if len(body)%aes.BlockSize != 0 {
		return nil, errors.New("crypt: AES body not block-aligned")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, len(body))
	mode.CryptBlocks(out, body)
	// Strip PKCS#7 padding.
	if len(out) == 0 {
		return out, nil
	}
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize {
		return out, nil // tolerate broken padding
	}
	if pad > len(out) {
		return out, nil
	}
	return out[:len(out)-pad], nil
}

// computeRC4Key implements PDF 32000-1:2008 algorithm 2 for the file key
// (V1/V2/V4 with RC4 or AESV2). The user password is the input; the empty
// string is the default.
func computeRC4Key(p Params, password []byte) ([]byte, error) {
	pad := padPassword(password)
	h := md5.New()
	h.Write(pad)
	h.Write(p.OwnerEntry)
	pBytes := []byte{
		byte(uint32(p.P)),
		byte(uint32(p.P) >> 8),
		byte(uint32(p.P) >> 16),
		byte(uint32(p.P) >> 24),
	}
	h.Write(pBytes)
	h.Write(p.ID0)
	if p.R >= 4 && !p.EncryptMeta {
		h.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	sum := h.Sum(nil)
	keyLen := p.Length / 8
	if keyLen == 0 {
		keyLen = 5 // V1 default
	}
	// /Length is attacker-controlled; the key is sliced from a 16-byte MD5
	// digest, so anything outside [1, md5.Size] would slice/make out of range.
	if keyLen < 1 || keyLen > md5.Size {
		return nil, fmt.Errorf("crypt: invalid key length %d bits", p.Length)
	}
	if p.R >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(sum[:keyLen])
			sum = s[:]
		}
	}
	key := make([]byte, keyLen)
	copy(key, sum[:keyLen])

	// Validate password by computing U and comparing.
	uExpected, err := computeU(p, key)
	if err != nil {
		return nil, err
	}
	if !validU(uExpected, p.UserEntry, p.R) {
		return nil, errors.New("crypt: password incorrect (V2/V4)")
	}
	return key, nil
}

var passPad = []byte{
	0x28, 0xbf, 0x4e, 0x5e, 0x4e, 0x75, 0x8a, 0x41,
	0x64, 0x00, 0x4e, 0x56, 0xff, 0xfa, 0x01, 0x08,
	0x2e, 0x2e, 0x00, 0xb6, 0xd0, 0x68, 0x3e, 0x80,
	0x2f, 0x0c, 0xa9, 0xfe, 0x64, 0x53, 0x69, 0x7a,
}

func padPassword(p []byte) []byte {
	if len(p) >= 32 {
		return p[:32]
	}
	out := make([]byte, 32)
	copy(out, p)
	copy(out[len(p):], passPad)
	return out
}

func computeU(p Params, key []byte) ([]byte, error) {
	if p.R == 2 {
		out := make([]byte, 32)
		c, _ := rc4.NewCipher(key)
		c.XORKeyStream(out, passPad)
		return out, nil
	}
	// R >= 3.
	h := md5.New()
	h.Write(passPad)
	h.Write(p.ID0)
	digest := h.Sum(nil)
	out := make([]byte, 16)
	c, _ := rc4.NewCipher(key)
	c.XORKeyStream(out, digest)
	for i := 1; i <= 19; i++ {
		tweaked := make([]byte, len(key))
		for j, b := range key {
			tweaked[j] = b ^ byte(i)
		}
		c2, _ := rc4.NewCipher(tweaked)
		c2.XORKeyStream(out, out)
	}
	final := make([]byte, 32)
	copy(final, out)
	// Trailing bytes are arbitrary per spec; pad with zeros.
	return final, nil
}

func validU(expected, actual []byte, r int) bool {
	if r == 2 {
		return bytes.Equal(expected, actual)
	}
	if len(actual) < 16 {
		return false
	}
	return bytes.Equal(expected[:16], actual[:16])
}

// computeV5Key implements PDF 32000-2:2020 §7.6.4 / PDF 1.7 Extension 3
// §3.5.2 — the AES-256 key derivation.
func computeV5Key(p Params, password []byte) ([]byte, error) {
	if len(p.UserEntry) < 48 || len(p.OwnerEntry) < 48 {
		return nil, errors.New("crypt: V5 entries too short")
	}
	if len(p.UE) < 32 || len(p.OE) < 32 {
		return nil, errors.New("crypt: V5 /UE or /OE missing")
	}
	// Limit password length to 127 bytes per spec.
	if len(password) > 127 {
		password = password[:127]
	}
	uValHash := p.UserEntry[:32]
	uVS := p.UserEntry[32:40]
	uKS := p.UserEntry[40:48]
	oValHash := p.OwnerEntry[:32]
	oVS := p.OwnerEntry[32:40]
	oKS := p.OwnerEntry[40:48]
	_ = uValHash
	_ = oValHash

	// Try user password first.
	if hash, err := v5Hash(password, uVS, nil, p.R); err == nil && bytes.Equal(hash, uValHash) {
		kHash, err := v5Hash(password, uKS, nil, p.R)
		if err != nil {
			return nil, err
		}
		return v5DecryptKey(kHash, p.UE)
	}
	// Try owner password.
	if hash, err := v5Hash(password, oVS, p.UserEntry[:48], p.R); err == nil && bytes.Equal(hash, oValHash) {
		kHash, err := v5Hash(password, oKS, p.UserEntry[:48], p.R)
		if err != nil {
			return nil, err
		}
		return v5DecryptKey(kHash, p.OE)
	}
	return nil, errors.New("crypt: password incorrect (V5)")
}

func v5DecryptKey(kHash, encryptedKey []byte) ([]byte, error) {
	if len(kHash) != 32 || len(encryptedKey) != 32 {
		return nil, errors.New("crypt: V5 key derivation: wrong sizes")
	}
	block, err := aes.NewCipher(kHash)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, aes.BlockSize)
	mode := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, 32)
	mode.CryptBlocks(out, encryptedKey)
	return out, nil
}

// v5Hash implements the PDF 2.0 / R=6 password hashing function. For R=5
// (PDF 1.7 Ext.3) it's just SHA-256.
func v5Hash(password, salt, userKey []byte, R int) ([]byte, error) {
	switch R {
	case 5:
		h := sha256.New()
		h.Write(password)
		h.Write(salt)
		h.Write(userKey)
		return h.Sum(nil), nil
	case 6:
		return r6Hash(password, salt, userKey)
	}
	return nil, fmt.Errorf("crypt: unsupported R=%d", R)
}

// r6Hash is the iterated AES-128 hash from PDF 2.0 §7.6.4.3.4.
func r6Hash(password, salt, userKey []byte) ([]byte, error) {
	h := sha256.New()
	h.Write(password)
	h.Write(salt)
	h.Write(userKey)
	K := h.Sum(nil)
	round := 0
	for {
		K1 := make([]byte, 0, 64*(len(password)+len(K)+len(userKey)))
		for i := 0; i < 64; i++ {
			K1 = append(K1, password...)
			K1 = append(K1, K...)
			K1 = append(K1, userKey...)
		}
		if len(K) < 32 {
			return nil, errors.New("r6Hash: short K")
		}
		block, err := aes.NewCipher(K[:16])
		if err != nil {
			return nil, err
		}
		mode := cipher.NewCBCEncrypter(block, K[16:32])
		E := make([]byte, len(K1))
		mode.CryptBlocks(E, K1)

		// Treat first 16 bytes as big-endian int and take mod 3.
		sum := 0
		for i := 0; i < 16; i++ {
			sum = (sum*256 + int(E[i])) % 3
		}
		switch sum {
		case 0:
			s := sha256.Sum256(E)
			K = s[:]
		case 1:
			s := sha512.Sum384(E)
			K = s[:]
		case 2:
			s := sha512.Sum512(E)
			K = s[:]
		}
		round++
		if round >= 64 && int(E[len(E)-1]) <= round-32 {
			return K[:32], nil
		}
		if round > 1000 {
			return nil, errors.New("r6Hash: too many rounds")
		}
	}
}

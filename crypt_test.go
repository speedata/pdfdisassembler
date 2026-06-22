package pdfdisassembler

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// buildEncryptedPDF constructs a PDF secured with the /Standard handler
// (V2/R3 RC4) whose /Encrypt dict declares the given /Length in bits. /O and
// /U are 32-byte placeholders; the empty-password key derivation runs during
// Open regardless of whether they validate.
func buildEncryptedPDF(t *testing.T, length int) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, 4) // index 1..3

	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")

	o := strings.Repeat("ab", 32) // 32 bytes, hex-encoded
	u := strings.Repeat("cd", 32)
	offsets[3] = off()
	fmt.Fprintf(&buf,
		"3 0 obj\n<< /Filter /Standard /V 2 /R 3 /Length %d /O <%s> /U <%s> /P -44 >>\nendobj\n",
		length, o, u)

	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	id := "<00112233445566778899aabbccddeeff>"
	fmt.Fprintf(&buf,
		"trailer\n<< /Size 4 /Root 1 0 R /Encrypt 3 0 R /ID [%s %s] >>\n", id, id)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

// A malicious /Encrypt dict can declare a /Length whose key size exceeds the
// 16-byte MD5 digest (or is negative). Open must surface an error, not panic.
func TestEncryptHostileKeyLengthNoPanic(t *testing.T) {
	for _, length := range []int{256, 4096, -8} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			data := buildEncryptedPDF(t, length)
			if _, err := Open(bytes.NewReader(data)); err == nil {
				t.Fatal("expected an error for hostile /Length, got nil")
			}
		})
	}
}

// stdPassPad is the 32-byte padding string from PDF 32000-1:2008 algorithm 2,
// used to build an empty-password V2/R3 fixture.
var stdPassPad = []byte{
	0x28, 0xbf, 0x4e, 0x5e, 0x4e, 0x75, 0x8a, 0x41,
	0x64, 0x00, 0x4e, 0x56, 0xff, 0xfa, 0x01, 0x08,
	0x2e, 0x2e, 0x00, 0xb6, 0xd0, 0x68, 0x3e, 0x80,
	0x2f, 0x0c, 0xa9, 0xfe, 0x64, 0x53, 0x69, 0x7a,
}

// emptyPwRC4Key derives the V2/R3 file key for the empty user password.
func emptyPwRC4Key(owner, id0 []byte, p int32, bits int) []byte {
	h := md5.New()
	h.Write(stdPassPad)
	h.Write(owner)
	h.Write([]byte{byte(uint32(p)), byte(uint32(p) >> 8), byte(uint32(p) >> 16), byte(uint32(p) >> 24)})
	h.Write(id0)
	sum := h.Sum(nil)
	keyLen := bits / 8
	for i := 0; i < 50; i++ {
		s := md5.Sum(sum[:keyLen])
		sum = s[:]
	}
	key := make([]byte, keyLen)
	copy(key, sum[:keyLen])
	return key
}

// emptyPwU computes the /U value (algorithm 5, R>=3) for the empty password,
// so Open's password validation accepts the fixture.
func emptyPwU(key, id0 []byte) []byte {
	h := md5.New()
	h.Write(stdPassPad)
	h.Write(id0)
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
	u := make([]byte, 32)
	copy(u, out)
	return u
}

// objKeyRC4 derives the per-object RC4 key (algorithm 1).
func objKeyRC4(fileKey []byte, num, gen int) []byte {
	buf := append([]byte{}, fileKey...)
	buf = append(buf, byte(num), byte(num>>8), byte(num>>16), byte(gen), byte(gen>>8))
	sum := md5.Sum(buf)
	n := len(fileKey) + 5
	if n > 16 {
		n = 16
	}
	return sum[:n]
}

func rc4Crypt(key, data []byte) []byte {
	out := make([]byte, len(data))
	c, _ := rc4.NewCipher(key)
	c.XORKeyStream(out, data)
	return out
}

// assembleEncryptedPDF builds a classical-xref PDF — catalog (1), pages (2),
// the given /Encrypt dict body (3), and an already-encrypted stream (4) — with
// the trailer wired to /Encrypt 3 0 R and /ID [id0 id0].
func assembleEncryptedPDF(encryptBody string, encStream, id0 []byte) []byte {
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 5) // 1..4
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprintf(&buf, "3 0 obj\n%s\nendobj\n", encryptBody)
	offsets[4] = off()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n", len(encStream))
	buf.Write(encStream)
	fmt.Fprint(&buf, "\nendstream\nendobj\n")

	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 5\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	id := hex.EncodeToString(id0)
	fmt.Fprintf(&buf,
		"trailer\n<< /Size 5 /Root 1 0 R /Encrypt 3 0 R /ID [<%s> <%s>] >>\n", id, id)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

// buildRC4EncryptedStreamPDF builds a V2/R3 RC4-encrypted PDF (empty password)
// whose object 4 is a stream carrying RC4-encrypted plaintext.
func buildRC4EncryptedStreamPDF(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	owner := bytes.Repeat([]byte{0x5a}, 32)
	id0 := bytes.Repeat([]byte{0x7c}, 16)
	const bits = 128
	var p int32 = -44
	fileKey := emptyPwRC4Key(owner, id0, p, bits)
	u := emptyPwU(fileKey, id0)
	enc := rc4Crypt(objKeyRC4(fileKey, 4, 0), plaintext)
	body := fmt.Sprintf("<< /Filter /Standard /V 2 /R 3 /Length %d /O <%s> /U <%s> /P %d >>",
		bits, hex.EncodeToString(owner), hex.EncodeToString(u), p)
	return assembleEncryptedPDF(body, enc, id0)
}

// buildV4RC4EncryptedStreamPDF builds a V4/R4 PDF whose StdCF crypt filter uses
// CFM /V2 (RC4) — same empty-password key derivation as V2/R3, reached through
// the V4 /CF + /StmF parsing path.
func buildV4RC4EncryptedStreamPDF(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	owner := bytes.Repeat([]byte{0x5a}, 32)
	id0 := bytes.Repeat([]byte{0x7c}, 16)
	const bits = 128
	var p int32 = -44
	fileKey := emptyPwRC4Key(owner, id0, p, bits)
	u := emptyPwU(fileKey, id0)
	enc := rc4Crypt(objKeyRC4(fileKey, 4, 0), plaintext)
	body := fmt.Sprintf("<< /Filter /Standard /V 4 /R 4 /Length %d /O <%s> /U <%s> /P %d "+
		"/CF << /StdCF << /CFM /V2 /Length 16 >> >> /StmF /StdCF /StrF /StdCF /EncryptMetadata true >>",
		bits, hex.EncodeToString(owner), hex.EncodeToString(u), p)
	return assembleEncryptedPDF(body, enc, id0)
}

// Open must accept an RC4-encrypted PDF secured with the empty user password
// and decrypt its stream content end-to-end.
func TestOpenDecryptsRC4Stream(t *testing.T) {
	plaintext := []byte("BT (top secret invoice) Tj ET")
	data := buildRC4EncryptedStreamPDF(t, plaintext)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, err := r.DecodeStream(Reference{Number: 4, Generation: 0})
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypted stream mismatch:\n got %q\nwant %q", got, plaintext)
	}
}

// The V4 path resolves the stream cipher through /CF + /StmF rather than /V
// directly, so it must be exercised end-to-end too.
func TestOpenDecryptsV4RC4Stream(t *testing.T) {
	plaintext := []byte("BT (V4 crypt filter) Tj ET")
	data := buildV4RC4EncryptedStreamPDF(t, plaintext)
	r, err := Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, err := r.DecodeStream(Reference{Number: 4, Generation: 0})
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("V4 decrypted stream mismatch:\n got %q\nwant %q", got, plaintext)
	}
}

// buildPDFWithEncryptObj wraps an arbitrary /Encrypt dict body as object 3 of a
// classical-xref PDF, with the trailer pointing /Encrypt at it.
func buildPDFWithEncryptObj(t *testing.T, encryptBody string) []byte {
	t.Helper()
	var buf bytes.Buffer
	off := func() int { return buf.Len() }
	fmt.Fprint(&buf, "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 4)
	offsets[1] = off()
	fmt.Fprint(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = off()
	fmt.Fprint(&buf, "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n")
	offsets[3] = off()
	fmt.Fprintf(&buf, "3 0 obj\n%s\nendobj\n", encryptBody)
	xrefOff := off()
	fmt.Fprint(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "%010d %05d f \n", 0, 65535)
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&buf, "%010d %05d n \n", offsets[i], 0)
	}
	id := "<00112233445566778899aabbccddeeff>"
	fmt.Fprintf(&buf, "trailer\n<< /Size 4 /Root 1 0 R /Encrypt 3 0 R /ID [%s %s] >>\n", id, id)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

// Only the /Standard security handler is supported; any other /Filter must be
// rejected rather than silently treated as unencrypted.
func TestEncryptNonStandardFilterRejected(t *testing.T) {
	data := buildPDFWithEncryptObj(t, "<< /Filter /FooSecurity /V 2 /R 3 /Length 128 >>")
	if _, err := Open(bytes.NewReader(data)); err == nil {
		t.Fatal("expected an error for a non-Standard /Filter, got nil")
	}
}

// Malformed /Encrypt dictionaries (wrong type, missing fields, short V5 entries)
// must surface as errors during Open, never panics.
func TestEncryptMalformedNoPanic(t *testing.T) {
	cases := map[string]string{
		"encrypt not a dict": "42",
		"missing V and R":    "<< /Filter /Standard >>",
		"short O and U":      "<< /Filter /Standard /V 2 /R 3 /Length 128 /O <00> /U <00> /P 0 >>",
		"v5 short entries":   "<< /Filter /Standard /V 5 /R 6 /Length 256 /O <00> /U <00> /OE <00> /UE <00> /Perms <00> /P 0 >>",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(bytes.NewReader(buildPDFWithEncryptObj(t, body))); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

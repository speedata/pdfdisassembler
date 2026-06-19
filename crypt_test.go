package pdfdisassembler

import (
	"bytes"
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

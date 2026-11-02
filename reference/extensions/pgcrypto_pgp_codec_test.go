package reference_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// Upstream-authored REL_18_6 contrib/pgcrypto/sql/pgp-decrypt.sql, aes128
// fixture (password foobar). This is not claimed to be GnuPG-generated.
const pgpVector64 = "jA0EBwMCci97v0Q6Z0Zg0kQBsVf5Oe3iC+FBzUmuMV9KxmAyOMyjCc/5i8f1Eest" +
	"UTAsG35A1vYs02VARKzGz6xI2UHwFUirP+brPBg3Ee7muOx8pA=="
const pgpProfile = "cipher-algo=aes128,compress-algo=0,s2k-mode=3,s2k-digest-algo=sha1,s2k-count=65536"

func pgpVector(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(pgpVector64)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type pgpRFCPacket struct {
	tag     byte
	body    []byte
	partial bool
}

// Independent RFC 4880 section 4.2 reader. Declared lengths never determine an
// allocation: every chunk is checked against bytes already supplied by the test
// or public SQL output. Context lengths consume the caller's bounded region;
// only the authenticated inner caller enables them, never outer framing.
// PostgreSQL's release-specific MAX_CHUNK is not an RFC grammar restriction.
func pgpRFCRead(input []byte, contextLength bool) (pgpRFCPacket, []byte, error) {
	var p pgpRFCPacket
	if len(input) == 0 || input[0]&128 == 0 {
		return p, nil, fmt.Errorf("short packet header")
	}
	h := input[0]
	input = input[1:]
	if h&64 == 0 {
		p.tag = (h >> 2) & 15
		if h&3 == 3 {
			if !contextLength {
				return p, nil, fmt.Errorf("outer context length")
			}
			p.body = input
			return p, nil, nil
		}
		n := 1 << (h & 3)
		if len(input) < n {
			return p, nil, fmt.Errorf("old packet length")
		}
		var size uint64
		for _, b := range input[:n] {
			size = size<<8 | uint64(b)
		}
		input = input[n:]
		if size > uint64(len(input)) {
			return p, nil, fmt.Errorf("old packet truncated")
		}
		p.body = input[:int(size)]
		return p, input[int(size):], nil
	}
	p.tag = h & 63
	for {
		if len(input) == 0 {
			return p, nil, fmt.Errorf("missing terminal length")
		}
		b := input[0]
		input = input[1:]
		var size uint64
		partial := false
		switch {
		case b < 192:
			size = uint64(b)
		case b < 224:
			if len(input) == 0 {
				return p, nil, fmt.Errorf("short two octet length")
			}
			size = (uint64(b)-192)*256 + uint64(input[0]) + 192
			input = input[1:]
		case b == 255:
			if len(input) < 4 {
				return p, nil, fmt.Errorf("short five octet length")
			}
			size = uint64(binary.BigEndian.Uint32(input[:4]))
			input = input[4:]
		default:
			size = uint64(1) << (b & 31)
			partial = true
			p.partial = true
		}
		if size > uint64(len(input)) {
			return p, nil, fmt.Errorf("packet body truncated")
		}
		if !p.partial {
			p.body = input[:int(size)]
		} else {
			p.body = append(p.body, input[:int(size)]...)
		}
		input = input[int(size):]
		if !partial {
			return p, input, nil
		}
	}
}

func pgpRFCLength(n int, form string) []byte {
	switch form {
	case "five":
		return []byte{255, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		if n < 192 {
			return []byte{byte(n)}
		}
		if n < 8384 {
			n -= 192
			return []byte{byte(n>>8) + 192, byte(n)}
		}
		return pgpRFCLength(n, "five")
	}
}

func pgpRFCFrame(tag byte, body []byte, form string) []byte {
	if (form == "old1" || form == "old2" || form == "old4" || form == "context") && tag > 15 {
		panic("old packet tag exceeds four bits")
	}
	var out []byte
	switch form {
	case "old1":
		if len(body) > 255 {
			panic("test old1 length")
		}
		out = []byte{128 | tag<<2, byte(len(body))}
	case "old2":
		if len(body) > 65535 {
			panic("test old2 length")
		}
		out = []byte{129 | tag<<2, byte(len(body) >> 8), byte(len(body))}
	case "old4":
		out = []byte{130 | tag<<2, byte(len(body) >> 24), byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	case "context":
		out = []byte{131 | tag<<2}
	case "partial24":
		return pgpRFCPartialFrame(tag, body, 24, len(body)/(1<<24))
	case "partial-terminal":
		return pgpRFCPartialFrame(tag, body, 14, 1)
	case "partial":
		out = []byte{192 | tag}
		for len(body) >= 16384 {
			out = append(out, 238)
			out = append(out, body[:16384]...)
			body = body[16384:]
		}
		out = append(out, pgpRFCLength(len(body), "")...)
	default:
		out = append([]byte{192 | tag}, pgpRFCLength(len(body), form)...)
	}
	return append(out, body...)
}

// Author explicit partial chunks, then one definite terminal chunk. This also
// authors release-invalid (but RFC-valid) sizes for the independent oracle.
func pgpRFCPartialFrame(tag byte, body []byte, exponent byte, chunks int) []byte {
	out := []byte{192 | tag}
	size := 1 << exponent
	for range chunks {
		out = append(out, 224+exponent)
		out = append(out, body[:size]...)
		body = body[size:]
	}
	out = append(out, pgpRFCLength(len(body), "")...)
	return append(out, body...)
}

func pgpRFCCount(c byte) int { return (16 + int(c&15)) << ((c >> 4) + 6) }

// RFC 4880 section 3.7.1.3: count bytes, not digest invocations. AES128
// requires only the first SHA1 block, so no generalized production key helper.
func pgpRFCKey(password string, salt []byte, count int) []byte {
	unit := append(append([]byte(nil), salt...), []byte(password)...)
	defer clear(unit)
	if count < len(unit) {
		count = len(unit)
	}
	h := sha1.New()
	for count > 0 {
		n := min(count, len(unit))
		h.Write(unit[:n])
		count -= n
	}
	sum := h.Sum(nil)
	key := append([]byte(nil), sum[:16]...)
	clear(sum)
	return key
}

type pgpRFCDecoded struct {
	text            string
	count           int
	encodedCount    byte
	outerPartial    bool
	literalBodySize int
	literalFormat   byte
	filenameLength  int
	literalPackets  int
}

// Authenticate complete CFB plaintext before interpreting any inner packet.
// Never include key, plaintext or password in returned diagnostics.
func pgpRFCDecode(packet []byte, password string) (pgpRFCDecoded, error) {
	var result pgpRFCDecoded
	keyPacket, rest, err := pgpRFCRead(packet, false)
	if err != nil {
		return result, err
	}
	if keyPacket.tag != 3 || len(keyPacket.body) != 13 || !bytes.Equal(keyPacket.body[:4], []byte{4, 7, 3, 2}) {
		return result, fmt.Errorf("not v4 AES128 iterated SHA1 SKESK")
	}
	result.encodedCount = keyPacket.body[12]
	result.count = pgpRFCCount(result.encodedCount)
	data, tail, err := pgpRFCRead(rest, false)
	if err != nil {
		return result, err
	}
	if data.tag != 18 || len(tail) != 0 || len(data.body) < 1+18+22 || data.body[0] != 1 {
		return result, fmt.Errorf("not complete SEIPD v1")
	}
	result.outerPartial = data.partial
	key := pgpRFCKey(password, keyPacket.body[4:12], result.count)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return result, err
	}
	plain := make([]byte, len(data.body)-1)
	defer clear(plain)
	cipher.NewCFBDecrypter(block, make([]byte, 16)).XORKeyStream(plain, data.body[1:])
	end := len(plain) - 20
	sum := sha1.Sum(plain[:end])
	if subtle.ConstantTimeCompare(plain[14:16], plain[16:18]) != 1 ||
		!bytes.Equal(plain[end-2:end], []byte{211, 20}) || subtle.ConstantTimeCompare(sum[:], plain[end:]) != 1 {
		return result, fmt.Errorf("prefix or MDC authentication failed")
	}
	inner := plain[18 : end-2]
	var text strings.Builder
	for len(inner) > 0 {
		literal, trailing, err := pgpRFCRead(inner, true)
		if err != nil {
			return result, err
		}
		if literal.tag != 11 || len(literal.body) < 2 ||
			(literal.body[0] != 't' && literal.body[0] != 'u') {
			clear(literal.body)
			return result, fmt.Errorf("not a text literal")
		}
		offset := 2 + int(literal.body[1]) + 4
		if offset > len(literal.body) {
			clear(literal.body)
			return result, fmt.Errorf("truncated literal filename or timestamp")
		}
		if result.literalPackets == 0 {
			result.literalFormat = literal.body[0]
			result.filenameLength = int(literal.body[1])
		}
		result.literalPackets++
		result.literalBodySize += len(literal.body)
		text.Write(literal.body[offset:])
		clear(literal.body)
		inner = trailing
	}
	if result.literalPackets == 0 {
		return result, fmt.Errorf("missing text literal")
	}
	result.text = text.String()
	return result, nil
}

// Deterministic salt/prefix are test-vector material ONLY. Packet authoring
// uses independent RFC algorithms, never either backend's encrypt function.
func pgpRFCBuild(password string, inner []byte, form string, corrupt string) []byte {
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	keyBody := append([]byte{4, 7, 3, 2}, salt...)
	keyBody = append(keyBody, 0)
	key := pgpRFCKey(password, salt, 1024)
	defer clear(key)
	prefix := []byte{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 35, 36}
	if corrupt == "prefix" {
		prefix[17] ^= 1
	}
	plain := append(prefix, inner...)
	defer func() { clear(plain) }()
	if corrupt != "missing_mdc" {
		plain = append(plain, 211, 20)
		sum := sha1.Sum(plain)
		plain = append(plain, sum[:]...)
		if corrupt == "mdc" {
			plain[len(plain)-1] ^= 1
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	body := make([]byte, len(plain)+1)
	body[0] = 1
	cipher.NewCFBEncrypter(block, make([]byte, 16)).XORKeyStream(body[1:], plain)
	keyForm := "old1"
	if form == "old1" || form == "old2" || form == "old4" {
		keyForm, form = form, ""
	}
	return append(pgpRFCFrame(3, keyBody, keyForm), pgpRFCFrame(18, body, form)...)
}

func pgpRFCLiteral(text, form string, format byte) []byte {
	body := append([]byte{format, 0, 0, 0, 0, 0}, []byte(text)...)
	return pgpRFCFrame(11, body, form)
}

func pgpRFCAnchor(t *testing.T) {
	t.Helper()
	d, err := pgpRFCDecode(pgpVector(t), "foobar")
	if err != nil || d.text != "Secret message." || d.encodedCount != 96 {
		t.Fatal("upstream RFC anchor failed", err)
	}
}

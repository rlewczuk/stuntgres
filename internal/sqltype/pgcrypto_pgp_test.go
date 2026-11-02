package sqltype

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

const pgpUnitVector = "jA0EBwMCci97v0Q6Z0Zg0kQBsVf5Oe3iC+FBzUmuMV9KxmAyOMyjCc/5i8f1EestUTAsG35A1vYs02VARKzGz6xI2UHwFUirP+brPBg3Ee7muOx8pA=="

func pgpUnitError(t *testing.T, err error, message string) {
	t.Helper()
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: message}) {
		t.Fatal("PGP diagnostic", err)
	}
}

func TestPgcryptoPGPPacketCodec(t *testing.T) {
	ctx := context.Background()
	if pgpPacketSize(-1) != -1 || pgpPacketSize(int(^uint(0)>>1)) != -1 {
		t.Fatal("packet output size overflow accepted")
	}
	for _, tc := range []struct {
		wire []byte
		size int
	}{
		{[]byte{140, 1, 42}, 1}, {[]byte{141, 0, 1, 42}, 1}, {[]byte{142, 0, 0, 0, 1, 42}, 1},
		{[]byte{195, 1, 42}, 1}, {append([]byte{195, 192, 0}, make([]byte, 192)...), 192},
		{[]byte{195, 255, 0, 0, 0, 1, 42}, 1},
	} {
		p, rest, err := pgpReadPacket(ctx, pgpInput{data: tc.wire}, pgpOuter)
		if err != nil || p.tag != pgpSKESK || p.size != tc.size || rest.len() != 0 {
			t.Fatal("packet header", p.size, err)
		}
		p2, rest, err := pgpReadPacket(ctx, pgpInput{text: string(tc.wire)}, pgpOuter)
		if err != nil || p2.tag != p.tag || p2.size != p.size || rest.len() != 0 {
			t.Fatal("immutable packet header", err)
		}
	}
	partial := []byte{210, 238}
	partial = append(partial, bytes.Repeat([]byte{17}, 16384)...)
	partial = append(partial, 238)
	partial = append(partial, bytes.Repeat([]byte{23}, 16384)...)
	partial = append(partial, 1, 31)
	p, rest, err := pgpReadPacket(ctx, pgpInput{data: partial}, pgpOuter)
	if err != nil || !p.partial || p.size != 32769 || rest.len() != 0 {
		t.Fatal("partial packet", err)
	}
	out := make([]byte, p.size)
	if err := p.copyTo(ctx, out); err != nil || out[0] != 17 || out[16383] != 17 || out[16384] != 23 || out[32768] != 31 {
		t.Fatal("partial chunks", err)
	}
	for _, wire := range [][]byte{{}, {195}, {195, 192}, {195, 255, 255, 255, 255, 255}, {143, 1}, {140, 2, 1}, partial[:len(partial)-2]} {
		_, _, err := pgpReadPacket(ctx, pgpInput{data: wire}, pgpOuter)
		pgpUnitError(t, err, "Wrong key or corrupt data")
	}
	for _, n := range []int{0, 191, 192, 8383, 8384, 16383, 16384, 32768} {
		out := make([]byte, pgpPacketSize(n))
		err := pgpWritePacket(ctx, out, pgpLiteral, n, func(dst []byte) error {
			for i := range dst {
				dst[i] = 91
			}
			return nil
		})
		if err != nil || out[0] != 203 {
			t.Fatal("packet writer", err)
		}
		if n >= 16384 && out[1] != 238 {
			t.Fatal("missing 16KiB partial chunk")
		}
		if n == 191 && out[1] != 191 || n == 192 && (out[1] != 192 || out[2] != 0) || n == 8383 && (out[1] != 223 || out[2] != 255) || n == 8384 && (out[1] != 255 || out[3] != 0 || out[4] != 32 || out[5] != 192) {
			t.Fatal("new length encoding", n)
		}
		p, rest, err := pgpReadPacket(ctx, pgpInput{data: out}, pgpAuthenticatedInner)
		if err != nil || p.size != n || rest.len() != 0 {
			t.Fatal("writer consumption", n, err)
		}
	}
}

func TestPgcryptoPGPS2K(t *testing.T) {
	for _, tc := range []struct {
		count   int
		encoded byte
		rounded int
	}{{1024, 0, 1024}, {1025, 1, 1088}, {65536, 96, 65536}, {65011712, 255, 65011712}} {
		if got := pgpS2KEncode(tc.count, 255); got != tc.encoded || pgpS2KCount(got) != tc.rounded {
			t.Fatal("S2K byte count", tc, got)
		}
	}
	for _, b := range []byte{0, 31, 32, 255} {
		got := pgpS2KEncode(-1, b)
		if got != 96+(b&31) {
			t.Fatal("random S2K count", got)
		}
	}
	// Expected SHA1 is independently assembled from RFC salt||password
	// repetitions, including a password longer than a whole count.
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, password := range []string{"foobar", strings.Repeat("p", 1100), strings.Repeat("q", 5000)} {
		unit := append(append([]byte(nil), salt...), []byte(password)...)
		input := bytes.Repeat(unit, (max(1024, len(unit))+len(unit)-1)/len(unit))[:max(1024, len(unit))]
		want := sha1.Sum(input)
		got, err := pgpS2KKey(context.Background(), password, salt, 0)
		if err != nil || !bytes.Equal(got[:], want[:16]) {
			t.Fatal("iterated salted key", err)
		}
		clear(unit)
		clear(input)
		clear(got[:])
	}
}

func TestPgcryptoPGPCFB(t *testing.T) {
	packet, err := base64.StdEncoding.DecodeString(pgpUnitVector)
	if err != nil {
		t.Fatal(err)
	}
	text, err := pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: string(packet), password: "foobar"})
	if err != nil || text != "Secret message." {
		t.Fatal("upstream AES128 CFB/MDC vector", err)
	}
	packet[len(packet)-1] ^= 1
	text, err = pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: string(packet), password: "foobar"})
	if text != "" {
		t.Fatal("corrupt CFB plaintext published")
	}
	pgpUnitError(t, err, "Wrong key or corrupt data")
}

func TestPgcryptoPGPOptions(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{"", "cipher-algo=AES128,s2k-cipher-algo=aes,s2k-count=1025,", "cipher-algo=aes256,cipher-algo=aes128", "s2k-count=1024junk,compress-algo=0\r,ignore-cipher-failure=-1"} {
		o, err := pgpParseOptions(ctx, raw)
		if err != nil || o.encryptionProfile() != nil {
			t.Fatal("supported options", raw, err)
		}
	}
	for _, raw := range []string{" ", "s2k-mode=0,s2k-count=1024", "s2k-count=1023,s2k-count=1024", "compress-level=10", "cipher-algo=aes128;s2k-mode=3", "unknown=1"} {
		_, err := pgpParseOptions(ctx, raw)
		pgpUnitError(t, err, "Illegal argument to function")
	}
	for _, raw := range []string{"s2k-cipher-algo=aes256", "cipher-algo=aes192", "compress-algo=1", "s2k-mode=0", "disable-mdc=1", "convert-crlf=1", "unicode-mode=1", "sess-key=1", "s2k-digest-algo=sha256"} {
		o, err := pgpParseOptions(ctx, raw)
		if err != nil || o.encryptionProfile() == nil {
			t.Fatal("unavailable effective profile silently accepted", raw, err)
		}
	}
	for raw, want := range map[string]int{"2147483647": 2147483647, "-2147483648": -2147483648, "nonnumeric": 0} {
		got, err := pgpAtoi(ctx, raw)
		if err != nil || got != want {
			t.Fatal("C integer option interpretation", raw, err)
		}
	}
	canceled := &pgpCancelContext{Context: ctx, at: 3}
	if _, err := pgpParseOptions(canceled, "s2k-count="+strings.Repeat("1", 16384)); !errors.Is(err, context.Canceled) {
		t.Fatal("option token cancellation", err)
	}
}

// A test-only author builds encrypted bytes without production framing/S2K.
func pgpUnitAuthored(inner []byte, badMDC bool) string {
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	unit := append(append([]byte(nil), salt...), []byte("foobar")...)
	input := bytes.Repeat(unit, 74)[:1024]
	digest := sha1.Sum(input)
	block, err := aes.NewCipher(digest[:16])
	if err != nil {
		panic(err)
	}
	plain := append(make([]byte, 18), inner...)
	plain = append(plain, 211, 20)
	sum := sha1.Sum(plain)
	plain = append(plain, sum[:]...)
	if badMDC {
		plain[len(plain)-1] ^= 1
	}
	out := append([]byte{140, 13, 4, 7, 3, 2}, salt...)
	out = append(out, 0, 210, byte(len(plain)+1), 1)
	start := len(out)
	out = append(out, make([]byte, len(plain))...)
	cipher.NewCFBEncrypter(block, make([]byte, 16)).XORKeyStream(out[start:], plain)
	clear(plain)
	clear(input)
	clear(unit)
	clear(digest[:])
	return string(out)
}

func TestPgcryptoPGPSecurity(t *testing.T) {
	for _, tc := range []struct {
		inner   []byte
		message string
	}{
		{[]byte{203, 7, 'b', 0, 0, 0, 0, 0, 'x'}, "Not text data"},
		{[]byte{203, 7, 'x', 0, 0, 0, 0, 0, 'x'}, "Not text data"},
		{[]byte{203, 7, 0, 0, 0, 0, 0, 0, 'x'}, "Not text data"},
		{[]byte{203, 7, 255, 0, 0, 0, 0, 0, 'x'}, "Not text data"},
		{[]byte{203, 7, 't', 0, 0, 0, 0, 0, 'a', 203, 7, 'x', 0, 0, 0, 0, 0, 'b'}, "Not text data"},
		{[]byte{203, 7, 'x', 0, 0, 0, 0, 0, 'a', 203, 7, 't', 0, 0, 0, 0, 0, 'b'}, "Not text data"},
		{[]byte{203, 7, 'x', 0, 0, 0, 0, 0, 'a', 203, 3, 't', 0, 0}, "Wrong key or corrupt data"},
		{[]byte{203, 6, 'x', 255, 0, 0, 0, 0}, "Wrong key or corrupt data"},
		{[]byte{203, 3, 'x', 0, 0}, "Wrong key or corrupt data"},
		{[]byte{200, 2, 3, 0}, "Unsupported compression algorithm"},
		{[]byte{203, 6, 't', 255, 0, 0, 0, 0}, "Wrong key or corrupt data"},
	} {
		for _, bad := range []bool{false, true} {
			text, err := pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: pgpUnitAuthored(tc.inner, bad), password: "foobar"})
			if text != "" {
				t.Fatal("failed authenticated packet published text")
			}
			want := tc.message
			if bad {
				want = "Wrong key or corrupt data"
			}
			pgpUnitError(t, err, want)
		}
	}
	// Authenticated literal filename/timestamp are framing, not returned text.
	inner, _ := hex.DecodeString("cb087401780000000079")
	text, err := pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: pgpUnitAuthored(inner, false), password: "foobar"})
	if err != nil || text != "y" {
		t.Fatal("bounded authenticated filename", err)
	}
}

type pgpCancelContext struct {
	context.Context
	polls, at int
}

func (c *pgpCancelContext) Err() error {
	c.polls++
	if c.polls >= c.at {
		return context.Canceled
	}
	return nil
}

func TestPgcryptoPGPEntropy(t *testing.T) {
	o, _ := pgpParseOptions(context.Background(), "s2k-count=1024")
	call := PgcryptoPGPInvocation{input: "text", password: "foobar", options: o}
	var borrowed []byte
	output, err := PgcryptoPGPEncrypt(context.Background(), call, func(dst []byte) error {
		borrowed = dst
		for i := range dst {
			dst[i] = byte(i)
		}
		return nil
	})
	if err != nil || output == "" || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("entropy scratch retained", err)
	}
	failure := errors.New("entropy unavailable")
	_, err = PgcryptoPGPEncrypt(context.Background(), call, func(dst []byte) error {
		borrowed = dst
		for i := range dst {
			dst[i] = 255
		}
		return failure
	})
	var sqlerr *sqlstate.Error
	if !errors.As(err, &sqlerr) || sqlerr.Code != "XX000" || sqlerr.Message != "could not generate a random number" || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("entropy failure cleanup", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = PgcryptoPGPEncrypt(ctx, call, func(dst []byte) error { cancel(); return failure })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("entropy cancellation ownership", err)
	}
	for _, at := range []int{1, 2, 3, 10} {
		ctx := &pgpCancelContext{Context: context.Background(), at: at}
		key, err := pgpS2KKey(ctx, "foobar", make([]byte, 8), 255)
		if !errors.Is(err, context.Canceled) || key != ([16]byte{}) {
			t.Fatal("S2K cancellation published key", err)
		}
	}
	vector, decodeErr := base64.StdEncoding.DecodeString(pgpUnitVector)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	for _, at := range []int{1, 3, 10} {
		ctx := &pgpCancelContext{Context: context.Background(), at: at}
		text, err := pgpDecrypt(ctx, PgcryptoPGPInvocation{input: string(vector), password: "foobar"})
		if !errors.Is(err, context.Canceled) || text != "" {
			t.Fatal("decrypt cancellation published plaintext", err)
		}
	}
	ctx2 := &pgpCancelContext{Context: context.Background(), at: 10}
	call.input = strings.Repeat("p", 65536)
	output, err = PgcryptoPGPEncrypt(ctx2, call, func(dst []byte) error { return nil })
	if !errors.Is(err, context.Canceled) || output != "" {
		t.Fatal("packet work cancellation published output", err)
	}
}

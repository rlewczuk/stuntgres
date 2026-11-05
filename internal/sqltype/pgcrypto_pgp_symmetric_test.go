package sqltype

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPgcryptoPGPCompression(t *testing.T) {
	input := bytes.Repeat([]byte("independent ZIP and ZLIB literal bytes\r\n"), 1000)
	for _, algorithm := range []byte{1, 2} {
		for _, level := range []int{1, 6, 9} {
			var independent bytes.Buffer
			var writer io.WriteCloser
			if algorithm == 1 {
				writer, _ = flate.NewWriter(&independent, level)
			} else {
				writer, _ = zlib.NewWriterLevel(&independent, level)
			}
			if _, err := writer.Write(input); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			var got pgpScratch
			if err := pgpDecompress(context.Background(), &got, independent.Bytes(), algorithm, nil); err != nil || !bytes.Equal(got.data, input) {
				t.Fatal("independent compressed input", algorithm, err)
			}
			owned := got.data[:cap(got.data)]
			got.clear()
			if !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatal("decompressed owner retained plaintext")
			}
			for _, suffix := range []int{1, 16384} {
				extended := append(append([]byte(nil), independent.Bytes()...), make([]byte, suffix)...)
				err := pgpDecompress(context.Background(), &got, extended, algorithm, nil)
				if suffix == 1 {
					if err != nil || !bytes.Equal(got.data, input) {
						t.Fatal("buffered stream suffix", err)
					}
				} else {
					pgpUnitError(t, err, "Wrong key or corrupt data")
				}
				got.clear()
			}
			if err := pgpCompress(context.Background(), &got, input, pgpCompression(algorithm), level); err != nil {
				t.Fatal(err)
			}
			var reader io.ReadCloser
			if algorithm == 1 {
				reader = flate.NewReader(bytes.NewReader(got.data))
			} else {
				var err error
				reader, err = zlib.NewReader(bytes.NewReader(got.data))
				if err != nil {
					t.Fatal(err)
				}
			}
			decoded, err := io.ReadAll(reader)
			closed := reader.Close()
			if err != nil || closed != nil || !bytes.Equal(decoded, input) {
				t.Fatal("independent compression decode", err, closed)
			}
			got.clear()
			truncated := independent.Bytes()[:len(independent.Bytes())-2]
			err = pgpDecompress(context.Background(), &got, truncated, algorithm, nil)
			pgpUnitError(t, err, "Wrong key or corrupt data")
			got.clear()
			if algorithm == 2 {
				damaged := append([]byte(nil), independent.Bytes()...)
				damaged[len(damaged)-1] ^= 1
				err = pgpDecompress(context.Background(), &got, damaged, algorithm, nil)
				pgpUnitError(t, err, "Wrong key or corrupt data")
				got.clear()
			}
			canceled := &pgpCancelContext{Context: context.Background(), at: 3}
			if err = pgpDecompress(canceled, &got, independent.Bytes(), algorithm, nil); !errors.Is(err, context.Canceled) {
				t.Fatal("decompression cancellation", err)
			}
			owned = got.data[:cap(got.data)]
			got.clear()
			if !bytes.Equal(owned, make([]byte, len(owned))) {
				t.Fatal("canceled decompression scratch")
			}
			canceled = &pgpCancelContext{Context: context.Background(), at: 3}
			if err = pgpCompress(canceled, &got, input, pgpCompression(algorithm), level); !errors.Is(err, context.Canceled) {
				t.Fatal("compression cancellation", err)
			}
			got.clear()
		}
	}
}

func TestPgcryptoPGPConversion(t *testing.T) {
	for _, input := range []string{"", "\n", "\r", "\r\n", "\r\r\n", "1\n2\n3\r\n", strings.Repeat("a", 16377) + "\r\n"} {
		for _, binary := range []bool{false, true} {
			o, _ := pgpParseOptions(context.Background(), "convert-crlf=1,unicode-mode=1")
			packet, err := pgpMakeLiteral(context.Background(), PgcryptoPGPInvocation{input: input, options: o, binary: binary})
			if err != nil {
				t.Fatal(err)
			}
			p, tail, err := pgpReadPacket(context.Background(), pgpInput{data: packet}, pgpDecryptedInner)
			if err != nil || tail.len() != 0 {
				t.Fatal(err)
			}
			body := make([]byte, p.size)
			if err = p.copyTo(context.Background(), body); err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(input, "\n", "\r\n")
			format := byte('u')
			if binary {
				want = input
				format = 'b'
			}
			if body[0] != format || string(body[6:]) != want {
				t.Fatal("literal format/conversion")
			}
			got, err := pgpReadInner(context.Background(), packet, binary, true, false)
			if err != nil || got != input {
				t.Fatal("conversion roundtrip", err)
			}
		}
	}
	// Conversion state belongs to one literal: CR and LF across two literals
	// remain separate. Structural validity precedes Not-text/UTF8 diagnostics.
	input := []byte{203, 7, 't', 0, 0, 0, 0, 0, '\r', 203, 7, 'u', 0, 0, 0, 0, 0, '\n'}
	if got, err := pgpReadInner(context.Background(), input, false, true, false); err != nil || got != "\r\n" {
		t.Fatal("per-literal CRLF state", err)
	}
	for _, format := range []byte{'b', 'x', 0, 255} {
		wire := []byte{203, 7, format, 0, 0, 0, 0, 0, 255}
		if got, err := pgpReadInner(context.Background(), append([]byte(nil), wire...), true, true, false); err != nil || got != "\xff" {
			t.Fatal("bytea literal formats", err)
		}
		got, err := pgpReadInner(context.Background(), append([]byte(nil), wire...), false, true, false)
		if got != "" {
			t.Fatal("nontext published")
		}
		pgpUnitError(t, err, "Not text data")
	}
}

func pgpUnitSession(inner []byte, algorithm byte, keyLength int) string {
	original := pgpUnitAuthored(inner, false)
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	unit := append(append([]byte(nil), salt...), []byte("foobar")...)
	input := bytes.Repeat(unit, 74)[:1024]
	digest := sha1.Sum(input)
	key := append([]byte{algorithm}, digest[:min(keyLength, 16)]...)
	if keyLength > 16 {
		key = append(key, make([]byte, keyLength-16)...)
	}
	block, _ := aes.NewCipher(digest[:16])
	cipher.NewCFBEncrypter(block, make([]byte, 16)).XORKeyStream(key, key)
	out := append([]byte{140, byte(13 + len(key))}, []byte(original[2:15])...)
	out = append(out, key...)
	out = append(out, original[15:]...)
	return string(out)
}
func TestPgcryptoPGPSessionKey(t *testing.T) {
	literal := []byte{203, 7, 't', 0, 0, 0, 0, 0, 'X'}
	for _, v := range []struct {
		algorithm byte
		size      int
		valid     bool
	}{{7, 16, true}, {127, 16, false}, {7, 15, false}, {9, 16, false}, {9, 32, false}} {
		// The last case has a valid AES256 session header but encrypts data with
		// AES128, so must fail full MDC rather than expose a semantic result.
		text, err := pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: pgpUnitSession(literal, v.algorithm, v.size), password: "foobar"})
		if v.valid {
			if err != nil || text != "X" {
				t.Fatal("independently wrapped session", err)
			}
		} else {
			if text != "" {
				t.Fatal("invalid session published")
			}
			pgpUnitError(t, err, "Wrong key or corrupt data")
		}
	}
}

func TestPgcryptoPGPSecretOwnership(t *testing.T) {
	// These are actual retired backing arrays and schedule objects, not
	// reconstructed replacement identities or claims about opaque AES internals.
	var scratch pgpScratch
	if err := scratch.append([]byte("owned plaintext")); err != nil {
		t.Fatal(err)
	}
	retired := scratch.data[:cap(scratch.data)]
	if err := scratch.append(bytes.Repeat([]byte{'S'}, 100)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retired, make([]byte, len(retired))) {
		t.Fatal("retired scratch backing retained secret")
	}
	owned := scratch.data[:cap(scratch.data)]
	scratch.clear()
	if scratch.data != nil || !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("scratch release")
	}
	for _, id := range []pgpCipher{3, 4} {
		block, err := pgpNewBlock(id, bytes.Repeat([]byte{173}, 16))
		if err != nil {
			t.Fatal(err)
		}
		bf, cast := block.bf, block.cast
		block.clear()
		if block.Block != nil || bf != nil && *bf != (pgpBlowfishBlock{}) || cast != nil && *cast != (pgpCAST5{}) {
			t.Fatal("private cipher schedule retained")
		}
	}
	for _, raw := range []string{"cipher-algo=cast5,s2k-digest-algo=md5,s2k-count=1024,sess-key=1,compress-algo=1", "cipher-algo=bf,s2k-digest-algo=sha256,s2k-count=1024,disable-mdc=1", "cipher-algo=aes256,s2k-cipher-algo=aes128,sess-key=1,s2k-count=1024,compress-algo=2"} {
		o, err := pgpParseOptions(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		call := PgcryptoPGPInvocation{input: strings.Repeat("sensitive", 100), password: "owned password", options: o, binary: true}
		healthy := &pgpCancelContext{Context: context.Background(), at: int(^uint(0) >> 1)}
		var borrowed []byte
		entropy := func(dst []byte) error {
			borrowed = dst
			for i := range dst {
				dst[i] = byte(i + 1)
			}
			return nil
		}
		output, err := PgcryptoPGPEncrypt(healthy, call, entropy)
		if err != nil || output == "" {
			t.Fatal("healthy owner path", err)
		}
		if !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
			t.Fatal("success retained entropy")
		}
		polls := healthy.polls
		failed, err := PgcryptoPGPEncrypt(context.Background(), call, func(dst []byte) error {
			borrowed = dst
			for i := range dst {
				dst[i] = 255
			}
			return errors.New("entropy unavailable")
		})
		if err == nil || failed != "" || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
			t.Fatal("failed profile entropy ownership", err)
		}
		for at := 1; at <= polls; at++ {
			borrowed = nil
			ctx := &pgpCancelContext{Context: context.Background(), at: at}
			got, err := PgcryptoPGPEncrypt(ctx, call, entropy)
			if !errors.Is(err, context.Canceled) || got != "" || !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
				t.Fatal("canceled encryption owner/publication", at, err)
			}
		}
		decrypt := PgcryptoPGPInvocation{input: output, password: call.password, binary: true}
		healthy = &pgpCancelContext{Context: context.Background(), at: int(^uint(0) >> 1)}
		if got, err := pgpDecrypt(healthy, decrypt); err != nil || got != call.input {
			t.Fatal("healthy decrypt owner", err)
		}
		for at := 1; at <= healthy.polls; at++ {
			ctx := &pgpCancelContext{Context: context.Background(), at: at}
			if got, err := pgpDecrypt(ctx, decrypt); !errors.Is(err, context.Canceled) || got != "" {
				t.Fatal("canceled decrypt publication", at, err)
			}
		}
	}
}

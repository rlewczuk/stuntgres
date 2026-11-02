package sqltype

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"time"
)

// PgcryptoPGPInvocation borrows immutable typed operands for one evaluation.
// It must never be retained in a plan, installed definition, or entropy source.
type PgcryptoPGPInvocation struct {
	input, password string
	options         pgpOptions
}

func (p PgcryptoPlan) PreparePGP(ctx context.Context, values ...Value) (PgcryptoPGPInvocation, bool, error) {
	var call PgcryptoPGPInvocation
	if p.name != "pgp_sym_encrypt" && p.name != "pgp_sym_decrypt" {
		return call, false, fmt.Errorf("invalid PGP plan")
	}
	if err := ctx.Err(); err != nil {
		return call, false, err
	}
	null, err := p.validateValues(values)
	if err != nil || null {
		return call, null, err
	}
	var raw string
	if p.count == 3 {
		raw, err = values[2].Text()
		if err != nil {
			return call, false, err
		}
	}
	call.options, err = pgpParseOptions(ctx, raw)
	if err != nil {
		return call, false, err
	}
	call.password, err = values[1].Text()
	if err != nil {
		return call, false, err
	}
	if len(call.password) == 0 {
		return call, false, pgpArgument()
	}
	if p.name == "pgp_sym_encrypt" {
		call.input, err = values[0].Text()
		if err == nil {
			err = call.options.encryptionProfile()
		}
	} else {
		call.input, err = values[0].Binary()
		if err == nil && call.options.convertCRLF {
			err = pgpCapability()
		}
	}
	return call, false, err
}

// PgcryptoPGPEncrypt obtains execution entropy only for a prepared non-NULL
// invocation. Key, prefix, digest and plaintext are owned scratch, cleared on
// every exit. AES state is invocation-local and never serialized or retained.
func PgcryptoPGPEncrypt(ctx context.Context, call PgcryptoPGPInvocation, entropy func([]byte) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := call.options.encryptionProfile(); err != nil {
		return "", err
	}
	if call.password == "" {
		return "", pgpArgument()
	}
	maxInt := int(^uint(0) >> 1)
	if len(call.input) > maxInt-6 {
		return "", pgcryptoError("54000", "OpenPGP packet is too large")
	}
	literalSize := pgpPacketSize(6 + len(call.input))
	if literalSize < 0 || literalSize > maxInt-41 {
		return "", pgcryptoError("54000", "OpenPGP packet is too large")
	}
	plainSize := 18 + literalSize + 2 + sha1.Size
	dataSize := 1 + plainSize
	encodedSize := pgpPacketSize(dataSize)
	if encodedSize < 0 || encodedSize > maxInt-15 {
		return "", pgcryptoError("54000", "OpenPGP packet is too large")
	}
	if entropy == nil {
		return "", fmt.Errorf("missing pgcrypto entropy context")
	}
	var random [25]byte // salt8, encoded-count random byte, random AES prefix16.
	defer clear(random[:])
	if err := entropy(random[:]); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return "", canceled
		}
		return "", pgcryptoError("XX000", "could not generate a random number")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded := pgpS2KEncode(call.options.count, random[8])
	key, err := pgpS2KKey(ctx, call.password, random[:8], encoded)
	if err != nil {
		return "", err
	}
	defer clear(key[:])
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	plain := make([]byte, plainSize)
	defer clear(plain)
	copy(plain[:16], random[9:])
	plain[16], plain[17] = plain[14], plain[15]
	var header [6]byte
	header[0] = 't'
	binary.BigEndian.PutUint32(header[2:], uint32(time.Now().Unix()))
	written := 0
	err = pgpWritePacket(ctx, plain[18:18+literalSize], pgpLiteral, 6+len(call.input), func(dst []byte) error {
		if written < len(header) {
			n := copy(dst, header[written:])
			dst = dst[n:]
			written += n
		}
		if len(dst) > 0 {
			n := copy(dst, call.input[written-6:])
			written += n
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	end := len(plain) - sha1.Size
	plain[end-2], plain[end-1] = 211, 20
	sum, err := pgpSHA1Sum(ctx, plain[:end])
	if err != nil {
		return "", err
	}
	copy(plain[end:], sum[:])
	clear(sum[:])
	out := make([]byte, 15+encodedSize)
	out[0], out[1] = 195, 13
	copy(out[2:6], []byte{4, 7, 3, 2})
	copy(out[6:14], random[:8])
	out[14] = encoded
	stream := cipher.NewCFBEncrypter(block, make([]byte, aes.BlockSize))
	position := 0
	first := true
	err = pgpWritePacket(ctx, out[15:], pgpSEIPD, dataSize, func(dst []byte) error {
		if first {
			dst[0] = 1
			dst = dst[1:]
			first = false
		}
		for len(dst) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(4096, len(dst))
			stream.XORKeyStream(dst[:n], plain[position:position+n])
			position += n
			dst = dst[n:]
		}
		return nil
	})
	if err != nil {
		clear(out)
		return "", err
	}
	if err := ctx.Err(); err != nil {
		clear(out)
		return "", err
	}
	return string(out), nil
}

func pgpDecrypt(ctx context.Context, call PgcryptoPGPInvocation) (string, error) {
	keyPacket, rest, err := pgpReadPacket(ctx, pgpInput{text: call.input}, pgpOuter)
	if err != nil {
		return "", err
	}
	if keyPacket.tag != pgpSKESK || keyPacket.size < 4 {
		return "", pgpCorrupt()
	}
	// S2K mode is an externally visible pre-authentication framing diagnostic.
	var keyBody [13]byte
	if keyPacket.size > len(keyBody) {
		if keyPacket.size == 30 {
			return "", pgpCapability()
		} // separate AES128 session key
		return "", pgpCorrupt()
	}
	if err := keyPacket.copyTo(ctx, keyBody[:keyPacket.size]); err != nil {
		return "", err
	}
	if keyBody[0] != 4 {
		return "", pgpCorrupt()
	}
	if keyBody[1] != byte(pgpAES128) {
		return "", pgpError("Unsupported cipher algorithm")
	}
	if keyBody[2] != 0 && keyBody[2] != 1 && keyBody[2] != 3 {
		return "", pgpError("Bad S2K mode")
	}
	if keyBody[2] != 3 {
		return "", pgpCapability()
	}
	if keyBody[3] != byte(pgpSHA1) {
		switch pgpDigest(keyBody[3]) {
		case 1, 3, 8, 9, 10:
			return "", pgpError("Unsupported digest algorithm")
		default:
			return "", pgpCorrupt()
		}
	}
	if keyPacket.size != 13 {
		return "", pgpCorrupt()
	}
	data, tail, err := pgpReadPacket(ctx, rest, pgpOuter)
	if err != nil {
		return "", err
	}
	if data.tag == pgpEncrypted {
		return "", pgpCapability()
	}
	if data.tag != pgpSEIPD || tail.len() != 0 || data.size < 1+18+2+sha1.Size {
		return "", pgpCorrupt()
	}
	plain := make([]byte, data.size)
	defer clear(plain)
	if err := data.copyTo(ctx, plain); err != nil {
		return "", err
	}
	if plain[0] != 1 {
		return "", pgpCorrupt()
	}
	plain = plain[1:]
	key, err := pgpS2KKey(ctx, call.password, keyBody[4:12], keyBody[12])
	if err != nil {
		return "", err
	}
	defer clear(key[:])
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	stream := cipher.NewCFBDecrypter(block, make([]byte, aes.BlockSize))
	for pos := 0; pos < len(plain); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(4096, len(plain)-pos)
		stream.XORKeyStream(plain[pos:pos+n], plain[pos:pos+n])
		pos += n
	}
	end := len(plain) - sha1.Size
	sum, err := pgpSHA1Sum(ctx, plain[:end])
	if err != nil {
		return "", err
	}
	prefixOK := subtle.ConstantTimeCompare(plain[14:16], plain[16:18])
	mdcOK := subtle.ConstantTimeCompare(sum[:], plain[end:])
	markerOK := subtle.ConstantTimeByteEq(plain[end-2], 211) & subtle.ConstantTimeByteEq(plain[end-1], 20)
	clear(sum[:])
	if prefixOK&mdcOK&markerOK != 1 {
		return "", pgpCorrupt()
	}
	// Only authenticated bytes may influence literal/compression diagnostics.
	return pgpReadText(ctx, plain[18:end-2])
}

func pgpReadText(ctx context.Context, authenticated []byte) (string, error) {
	remaining := authenticated
	written := 0
	gotLiteral, nonText := false, false
	for len(remaining) > 0 {
		packet, tail, err := pgpReadPacket(ctx, pgpInput{data: remaining}, pgpAuthenticatedInner)
		if err != nil {
			return "", err
		}
		if packet.tag == pgpCompressed {
			if gotLiteral || tail.len() != 0 {
				return "", pgpCorrupt()
			}
			return "", pgpCompressionError(packet)
		}
		if packet.tag != pgpLiteral || packet.size < 6 {
			return "", pgpCorrupt()
		}
		body := packet.body.data
		if packet.partial {
			// Compact only this already-parsed packet. Neither its body nor
			// the accumulated payload can reach the following unread packet.
			body = remaining[:packet.size]
			if err := packet.copyTo(ctx, body); err != nil {
				return "", err
			}
		}
		start := 2 + int(body[1]) + 4
		if start > len(body) {
			return "", pgpCorrupt()
		}
		gotLiteral = true
		nonText = nonText || (body[0] != 't' && body[0] != 'u')
		for pos := start; pos < len(body); {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			n := min(4096, len(body)-pos)
			copy(authenticated[written:written+n], body[pos:pos+n])
			written += n
			pos += n
		}
		remaining = tail.data
	}
	if !gotLiteral {
		return "", pgpCorrupt()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// A later structural failure defeats any earlier nontext flag. MDC
	// authentication already preceded this entire interpretation/compaction.
	if nonText {
		return "", pgpError("Not text data")
	}
	text := string(authenticated[:written])
	if err := binaryTextValid(ctx, text); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return text, nil
}

func pgpCompressionError(packet pgpPacket) error {
	if packet.size < 1 {
		return pgpCorrupt()
	}
	// Compression is interpreted only after full MDC authentication. The
	// checked packet boundary is the shared hook for later ZIP/ZLIB leaves.
	return pgpError("Unsupported compression algorithm")
}

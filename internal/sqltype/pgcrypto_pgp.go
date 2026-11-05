package sqltype

import (
	"context"
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
	binary          bool
}

func (p PgcryptoPlan) PreparePGP(ctx context.Context, values ...Value) (PgcryptoPGPInvocation, bool, error) {
	var call PgcryptoPGPInvocation
	switch p.name {
	case "pgp_sym_encrypt", "pgp_sym_decrypt", "pgp_sym_encrypt_bytea", "pgp_sym_decrypt_bytea":
	default:
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
	call.binary = p.name == "pgp_sym_encrypt_bytea" || p.name == "pgp_sym_decrypt_bytea"
	if p.name == "pgp_sym_encrypt" {
		call.input, err = values[0].Text()
	} else {
		call.input, err = values[0].Binary()
	}
	if err == nil && (p.name == "pgp_sym_encrypt" || p.name == "pgp_sym_encrypt_bytea") {
		err = call.options.encryptionProfile()
	}
	return call, false, err
}

func pgpFrame(ctx context.Context, tag pgpTag, data []byte) ([]byte, error) {
	size := pgpPacketSize(len(data))
	if size < 0 {
		return nil, pgcryptoError("54000", "OpenPGP packet is too large")
	}
	out := make([]byte, size)
	pos := 0
	err := pgpWritePacket(ctx, out, tag, len(data), func(dst []byte) error { pos += copy(dst, data[pos:]); return nil })
	if err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}

// PgcryptoPGPEncrypt uses only invocation-owned mutable secrets and obtains
// execution entropy after strict NULL, option and password validation.
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
	if entropy == nil {
		return "", fmt.Errorf("missing pgcrypto entropy context")
	}
	o := call.options
	wrap := o.s2kCipher
	if wrap == 0 {
		wrap = o.cipher
	}
	keySize, _ := pgpCipherSize(wrap)
	dataCipher := o.cipher
	dataSize, blockSize := pgpCipherSize(dataCipher)
	// OpenSSL's generic AES engine chooses strength by the derived key length.
	if !o.sessionKey && dataCipher >= 7 && dataCipher <= 9 {
		dataSize = keySize
		switch keySize {
		case 16:
			dataCipher = 7
		case 24:
			dataCipher = 8
		case 32:
			dataCipher = 9
		}
	}
	var random [57]byte
	defer clear(random[:])
	randomSize := 9 + blockSize
	if o.sessionKey {
		randomSize += dataSize
	}
	if err := entropy(random[:randomSize]); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return "", canceled
		}
		return "", pgcryptoError("XX000", "could not generate a random number")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	encoded := pgpS2KEncode(o.count, random[8])
	var key [32]byte
	defer clear(key[:])
	if err := pgpS2KKey(ctx, key[:keySize], call.password, random[:8], o.mode, o.digest, encoded); err != nil {
		return "", err
	}
	var skesk [46]byte
	defer clear(skesk[:])
	skesk[0], skesk[1], skesk[2], skesk[3] = 4, byte(wrap), byte(o.mode), byte(o.digest)
	skeskSize := 4
	if o.mode != 0 {
		copy(skesk[4:12], random[:8])
		skeskSize = 12
	}
	if o.mode == 3 {
		skesk[12] = encoded
		skeskSize = 13
	}
	dataKey := key[:keySize]
	if o.sessionKey {
		dataKey = random[9+blockSize : randomSize]
		wrapped := skesk[skeskSize : skeskSize+1+dataSize]
		wrapped[0] = byte(dataCipher)
		copy(wrapped[1:], dataKey)
		block, err := pgpNewBlock(wrap, key[:keySize])
		if err != nil {
			return "", err
		}
		err = pgpCFB(ctx, block.Block, wrapped, false, false)
		block.clear()
		if err != nil {
			return "", err
		}
		skeskSize += 1 + dataSize
	}
	literal, err := pgpMakeLiteral(ctx, call)
	if err != nil {
		return "", err
	}
	defer clear(literal)
	inner := literal
	var compressed pgpScratch
	defer compressed.clear()
	var compressedPacket []byte
	if o.compression != 0 && o.level != 0 {
		if err = compressed.append([]byte{byte(o.compression)}); err != nil {
			return "", err
		}
		if err = pgpCompress(ctx, &compressed, literal, o.compression, o.level); err != nil {
			return "", err
		}
		compressedPacket, err = pgpFrame(ctx, pgpCompressed, compressed.data)
		if err != nil {
			return "", err
		}
		defer clear(compressedPacket)
		inner = compressedPacket
	}
	extra := blockSize + 2
	if !o.disableMDC {
		extra += 2 + sha1.Size
	}
	if len(inner) > int(^uint(0)>>1)-extra {
		return "", pgcryptoError("54000", "OpenPGP packet is too large")
	}
	plain := make([]byte, extra+len(inner))
	defer clear(plain)
	copy(plain, random[9:9+blockSize])
	plain[blockSize], plain[blockSize+1] = plain[blockSize-2], plain[blockSize-1]
	copy(plain[blockSize+2:], inner)
	if !o.disableMDC {
		end := len(plain) - sha1.Size
		plain[end-2], plain[end-1] = 211, 20
		sum, e := pgpSHA1Sum(ctx, plain[:end])
		if e != nil {
			return "", e
		}
		copy(plain[end:], sum[:])
		clear(sum[:])
	}
	block, err := pgpNewBlock(dataCipher, dataKey)
	if err != nil {
		return "", err
	}
	err = pgpCFB(ctx, block.Block, plain, false, o.disableMDC)
	block.clear()
	if err != nil {
		return "", err
	}
	tag := pgpSEIPD
	version := 1
	if o.disableMDC {
		tag = pgpEncrypted
		version = 0
	}
	dataPacketSize := pgpPacketSize(len(plain) + version)
	keyPacketSize := pgpPacketSize(skeskSize)
	if dataPacketSize < 0 || keyPacketSize > int(^uint(0)>>1)-dataPacketSize {
		return "", pgcryptoError("54000", "OpenPGP packet is too large")
	}
	out := make([]byte, keyPacketSize+dataPacketSize)
	defer clear(out)
	pos := 0
	err = pgpWritePacket(ctx, out[:keyPacketSize], pgpSKESK, skeskSize, func(dst []byte) error { pos += copy(dst, skesk[pos:skeskSize]); return nil })
	if err != nil {
		return "", err
	}
	pos = 0
	err = pgpWritePacket(ctx, out[keyPacketSize:], tag, len(plain)+version, func(dst []byte) error {
		if version != 0 {
			dst[0] = 1
			dst = dst[1:]
			version = 0
		}
		pos += copy(dst, plain[pos:])
		return nil
	})
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return string(out), nil
}

func pgpDecrypt(ctx context.Context, call PgcryptoPGPInvocation) (string, error) {
	keyPacket, rest, err := pgpReadPacket(ctx, pgpInput{text: call.input}, pgpOuter)
	if err != nil {
		return "", err
	}
	if keyPacket.tag != pgpSKESK || keyPacket.size < 4 || keyPacket.size > 46 {
		return "", pgpCorrupt()
	}
	var header [46]byte
	defer clear(header[:])
	if err = keyPacket.copyTo(ctx, header[:keyPacket.size]); err != nil {
		return "", err
	}
	if header[0] != 4 {
		return "", pgpCorrupt()
	}
	wrap := pgpCipher(header[1])
	keySize, _ := pgpCipherSize(wrap)
	if keySize == 0 {
		return "", pgpError("Unsupported cipher algorithm")
	}
	mode := int(header[2])
	offset := 4
	if mode != 0 && mode != 1 && mode != 3 {
		return "", pgpError("Bad S2K mode")
	}
	if mode != 0 {
		offset = 12
	}
	if mode == 3 {
		offset = 13
	}
	if keyPacket.size < offset {
		return "", pgpCorrupt()
	}
	var key [32]byte
	defer clear(key[:])
	encoded := byte(0)
	if mode == 3 {
		encoded = header[12]
	}
	if err = pgpS2KKey(ctx, key[:keySize], call.password, header[4:12], mode, pgpDigest(header[3]), encoded); err != nil {
		return "", err
	}
	dataCipher := wrap
	dataKey := key[:keySize]
	if keyPacket.size > offset {
		wrapped := header[offset:keyPacket.size]
		block, e := pgpNewBlock(wrap, dataKey)
		if e != nil {
			return "", e
		}
		err = pgpCFB(ctx, block.Block, wrapped, true, false)
		block.clear()
		if err != nil {
			return "", err
		}
		dataCipher = pgpCipher(wrapped[0])
		size, _ := pgpCipherSize(dataCipher)
		if size == 0 || len(wrapped) != size+1 {
			return "", pgpCorrupt()
		}
		dataKey = wrapped[1:]
	}
	data, tail, err := pgpReadPacket(ctx, rest, pgpNoMDCOuter)
	if err != nil {
		return "", err
	}
	noMDC := data.tag == pgpEncrypted
	if (!noMDC && data.tag != pgpSEIPD) || tail.len() != 0 {
		return "", pgpCorrupt()
	}
	_, blockSize := pgpCipherSize(dataCipher)
	minimum := blockSize + 2
	if !noMDC {
		minimum += 1 + 2 + sha1.Size
	}
	if data.size < minimum {
		return "", pgpCorrupt()
	}
	storage := make([]byte, data.size)
	defer clear(storage)
	if err = data.copyTo(ctx, storage); err != nil {
		return "", err
	}
	plain := storage
	if !noMDC {
		if plain[0] != 1 {
			return "", pgpCorrupt()
		}
		plain = plain[1:]
	}
	block, err := pgpNewBlock(dataCipher, dataKey)
	if err != nil {
		return "", err
	}
	err = pgpCFB(ctx, block.Block, plain, true, noMDC)
	block.clear()
	if err != nil {
		return "", err
	}
	prefixOK := subtle.ConstantTimeCompare(plain[blockSize-2:blockSize], plain[blockSize:blockSize+2])
	end := len(plain)
	if !noMDC {
		end -= sha1.Size
		sum, e := pgpSHA1Sum(ctx, plain[:end])
		if e != nil {
			return "", e
		}
		valid := subtle.ConstantTimeCompare(sum[:], plain[end:]) & subtle.ConstantTimeByteEq(plain[end-2], 211) & subtle.ConstantTimeByteEq(plain[end-1], 20)
		clear(sum[:])
		if valid&prefixOK != 1 {
			return "", pgpCorrupt()
		}
		end -= 2
	} else if prefixOK != 1 {
		return "", pgpCorrupt()
	}
	// Authentication precedes every sensitive literal/compression diagnostic.
	return pgpReadInner(ctx, plain[blockSize+2:end], call.binary, call.options.convertCRLF, noMDC)
}

func pgpMakeLiteral(ctx context.Context, call PgcryptoPGPInvocation) ([]byte, error) {
	count := len(call.input)
	if call.options.convertCRLF && !call.binary {
		for i := range len(call.input) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if call.input[i] == '\n' {
				if count == int(^uint(0)>>1) {
					return nil, pgcryptoError("54000", "OpenPGP packet is too large")
				}
				count++
			}
		}
	}
	if count > int(^uint(0)>>1)-6 {
		return nil, pgcryptoError("54000", "OpenPGP packet is too large")
	}
	size := pgpPacketSize(count + 6)
	if size < 0 {
		return nil, pgcryptoError("54000", "OpenPGP packet is too large")
	}
	out := make([]byte, size)
	var header [6]byte
	header[0] = 't'
	if call.binary {
		header[0] = 'b'
	} else if call.options.unicode {
		header[0] = 'u'
	}
	binary.BigEndian.PutUint32(header[2:], uint32(time.Now().Unix()))
	pos, input := 0, 0
	pendingLF := false
	err := pgpWritePacket(ctx, out, pgpLiteral, count+6, func(dst []byte) error {
		for i := range dst {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if pos < 6 {
				dst[i] = header[pos]
				pos++
				continue
			}
			if pendingLF {
				dst[i] = '\n'
				pendingLF = false
				input++
				continue
			}
			c := call.input[input]
			if c == '\n' && call.options.convertCRLF && !call.binary {
				dst[i] = '\r'
				pendingLF = true
			} else {
				dst[i] = c
				input++
			}
		}
		return nil
	})
	if err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}

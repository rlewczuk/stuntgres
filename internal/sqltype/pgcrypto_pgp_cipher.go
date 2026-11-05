package sqltype

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"encoding/binary"
)

func pgpCipherSize(id pgpCipher) (key, block int) {
	switch id {
	case 2:
		return 24, 8
	case 3, 4:
		return 16, 8
	case 7:
		return 16, 16
	case 8:
		return 24, 16
	case 9:
		return 32, 16
	}
	return 0, 0
}

// The owner releases opaque standard-library schedules and explicitly erases
// the mutable schedules we own. No schedule survives this invocation.
type pgpBlock struct {
	cipher.Block
	bf   *pgpBlowfishBlock
	cast *pgpCAST5
}

func (b *pgpBlock) clear() {
	if b.bf != nil {
		*b.bf = pgpBlowfishBlock{}
	}
	if b.cast != nil {
		*b.cast = pgpCAST5{}
	}
	*b = pgpBlock{}
}
func pgpNewBlock(id pgpCipher, key []byte) (pgpBlock, error) {
	var b pgpBlock
	size, _ := pgpCipherSize(id)
	if size == 0 {
		return b, pgpError("Unsupported cipher algorithm")
	}
	// Mixed selectors without a session key pass the S2K key's actual length to
	// the data provider (REL_18_6 openssl.c), not the nominal data key length.
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return b, pgpCorrupt()
	}
	var err error
	switch id {
	case 2:
		var padded [24]byte
		copy(padded[:], key)
		b.Block, err = des.NewTripleDESCipher(padded[:])
		clear(padded[:])
	case 3:
		b.cast = new(pgpCAST5)
		b.cast.keySchedule(key[:16]) // OpenSSL CAST_set_key truncates at128 bits.
		b.Block = b.cast
	case 4:
		b.bf = &pgpBlowfishBlock{state: pgcryptoBlowfishInitial}
		var words [8]uint32
		count := len(key) / 4
		for i := range count {
			words[i] = binary.BigEndian.Uint32(key[i*4:])
		}
		b.bf.state.expand(words[:count], nil)
		clear(words[:])
		b.Block = b.bf
	default:
		b.Block, err = aes.NewCipher(key)
	}
	return b, err
}

type pgpBlowfishBlock struct{ state pgcryptoBlowfishState }

func (*pgpBlowfishBlock) BlockSize() int { return 8 }
func (b *pgpBlowfishBlock) Encrypt(dst, src []byte) {
	l, r := b.state.encrypt(binary.BigEndian.Uint32(src[:4]), binary.BigEndian.Uint32(src[4:8]))
	binary.BigEndian.PutUint32(dst[:4], l)
	binary.BigEndian.PutUint32(dst[4:8], r)
}
func (b *pgpBlowfishBlock) Decrypt(dst, src []byte) {
	l, r := b.state.decrypt(binary.BigEndian.Uint32(src[:4]), binary.BigEndian.Uint32(src[4:8]))
	binary.BigEndian.PutUint32(dst[:4], l)
	binary.BigEndian.PutUint32(dst[4:8], r)
}

// CFB operates in place. Tag9 resynchronizes after the block+2 prefix;
// SKESK and tag18 use ordinary full-block CFB with a zero IV.
func pgpCFB(ctx context.Context, block cipher.Block, data []byte, decrypt, resync bool) error {
	var iv [16]byte
	defer clear(iv[:])
	size := block.BlockSize()
	run := func(data []byte, feedback []byte) error {
		var stream cipher.Stream
		if decrypt {
			stream = cipher.NewCFBDecrypter(block, feedback)
		} else {
			stream = cipher.NewCFBEncrypter(block, feedback)
		}
		for pos := 0; pos < len(data); {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(4096, len(data)-pos)
			stream.XORKeyStream(data[pos:pos+n], data[pos:pos+n])
			pos += n
		}
		return ctx.Err()
	}
	if !resync {
		return run(data, iv[:size])
	}
	if len(data) < size+2 {
		return pgpCorrupt()
	}
	if decrypt {
		copy(iv[:size], data[2:size+2])
	}
	var zero [16]byte
	if err := run(data[:size+2], zero[:size]); err != nil {
		return err
	}
	if !decrypt {
		copy(iv[:size], data[2:size+2])
	}
	return run(data[size+2:], iv[:size])
}

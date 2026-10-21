package sqltype

import (
	"context"
	"encoding/binary"
	"strings"
)

const pgcryptoBFBlockSize = 8

// Raw BF cycles every unsigned octet (including NUL) without bcrypt's
// terminating-NUL or historical signed-byte packing. OpenSSL clips at56 bytes;
// its empty key has the same all-zero words as one zero octet.
func pgcryptoBFKey(raw string) (words [18]uint32) {
	raw = raw[:min(len(raw), 56)]
	if len(raw) == 0 {
		return words
	}
	position := 0
	for i := range words {
		for range 4 {
			words[i] = words[i]<<8 | uint32(raw[position])
			position++
			if position == len(raw) {
				position = 0
			}
		}
	}
	return words
}

func (s *pgcryptoBlowfishState) encryptBlock(dst, src *[pgcryptoBFBlockSize]byte) {
	left, right := s.encrypt(binary.BigEndian.Uint32(src[:4]), binary.BigEndian.Uint32(src[4:]))
	binary.BigEndian.PutUint32(dst[:4], left)
	binary.BigEndian.PutUint32(dst[4:], right)
}

func (s *pgcryptoBlowfishState) decryptBlock(dst, src *[pgcryptoBFBlockSize]byte) {
	left, right := s.decrypt(binary.BigEndian.Uint32(src[:4]), binary.BigEndian.Uint32(src[4:]))
	binary.BigEndian.PutUint32(dst[:4], left)
	binary.BigEndian.PutUint32(dst[4:], right)
}

// The shared raw entrypoint has checked context and parsed the type once.
// Every frame owns its schedule, feedback and fixed block scratch. The builder
// owns the only payload-sized allocation; no padded input copy or block boxing.
func pgcryptoBF(ctx context.Context, function, data, rawKey, rawIV string, options pgcryptoRawOptions) (string, error) {
	decrypt := function == "decrypt" || function == "decrypt_iv"
	stream := options.mode == pgcryptoModeCFB64
	if !stream && (len(data)%pgcryptoBFBlockSize != 0 && (decrypt || !options.padding) || decrypt && options.padding && len(data) == 0) {
		return "", pgcryptoRawFailure(function, decrypt)
	}
	if len(data) == 0 && (stream || !options.padding) {
		// Nothing to transform or pad; still observe final publication.
		return "", ctx.Err()
	}
	key := pgcryptoBFKey(rawKey)
	state := pgcryptoBlowfishInitial
	state.expand(key[:], nil)
	var chain, input, result [pgcryptoBFBlockSize]byte
	if options.mode != pgcryptoModeECB {
		copy(chain[:], rawIV)
	}
	length := len(data)
	if !stream && !decrypt && options.padding {
		length += pgcryptoBFBlockSize - len(data)%pgcryptoBFBlockSize
	}
	var output strings.Builder
	output.Grow(length)
	for offset := 0; offset < length; offset += pgcryptoBFBlockSize {
		// No more than64KiB of data work between context observations.
		if offset%65536 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		end := min(offset+pgcryptoBFBlockSize, len(data))
		n := end - offset
		if stream {
			// CFB64 encrypts feedback in BOTH directions. Feedback always
			// receives ciphertext; valid pad settings do not affect length.
			state.encryptBlock(&result, &chain)
			for i := range n {
				octet := data[offset+i]
				input[i] = octet ^ result[i]
				if decrypt {
					chain[i] = octet
				} else {
					chain[i] = input[i]
				}
			}
			output.Write(input[:n])
			continue
		}
		copy(input[:], data[offset:end])
		if !decrypt && n < pgcryptoBFBlockSize {
			for i := n; i < pgcryptoBFBlockSize; i++ {
				input[i] = byte(pgcryptoBFBlockSize - n)
			}
		}
		if decrypt {
			state.decryptBlock(&result, &input)
			if options.mode == pgcryptoModeCBC {
				for i := range result {
					result[i] ^= chain[i]
				}
				chain = input
			}
		} else {
			if options.mode == pgcryptoModeCBC {
				for i := range input {
					input[i] ^= chain[i]
				}
			}
			state.encryptBlock(&result, &input)
			if options.mode == pgcryptoModeCBC {
				chain = result
			}
		}
		n = pgcryptoBFBlockSize
		if decrypt && options.padding && offset+pgcryptoBFBlockSize == length {
			var valid bool
			n, valid = pgcryptoRawUnpad(result[:])
			if !valid {
				return "", pgcryptoRawFailure(function, decrypt)
			}
		}
		output.Write(result[:n])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return output.String(), nil
}

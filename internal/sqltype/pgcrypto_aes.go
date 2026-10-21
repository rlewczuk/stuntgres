package sqltype

import (
	"context"
	"crypto/aes"
	"strings"
)

func pgcryptoAESKey(raw string) (key [32]byte, size int) {
	size = 16
	if len(raw) > 16 {
		size = 24
	}
	if len(raw) > 24 {
		size = 32
	}
	copy(key[:], raw)
	return key, size
}

func pgcryptoAESIV(raw string) (iv [aes.BlockSize]byte) {
	copy(iv[:], raw)
	return iv
}

// Input octets remain immutable strings. Scratch and chaining state are bounded;
// the builder owns the one full result allocation, without padded-input staging.
func pgcryptoAES(ctx context.Context, function, data, rawKey, rawIV string, options pgcryptoRawOptions) (string, error) {
	decrypt := function == "decrypt" || function == "decrypt_iv"
	if len(data)%aes.BlockSize != 0 && (decrypt || !options.padding) || decrypt && options.padding && len(data) == 0 {
		return "", pgcryptoRawFailure(function, decrypt)
	}
	key, keySize := pgcryptoAESKey(rawKey)
	block, err := aes.NewCipher(key[:keySize])
	if err != nil {
		return "", err
	}
	var chain [aes.BlockSize]byte
	if options.mode == pgcryptoModeCBC {
		chain = pgcryptoAESIV(rawIV)
	}
	length := len(data)
	if !decrypt && options.padding {
		length += aes.BlockSize - len(data)%aes.BlockSize
	}
	var output strings.Builder
	output.Grow(length)
	var input, result [aes.BlockSize]byte
	for offset := 0; offset < length; offset += aes.BlockSize {
		// At most64KiB of block work between cancellation observations.
		if offset%65536 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		end := min(offset+aes.BlockSize, len(data))
		n := copy(input[:], data[offset:end])
		if !decrypt && n < aes.BlockSize {
			for i := n; i < aes.BlockSize; i++ {
				input[i] = byte(aes.BlockSize - n)
			}
		}
		if decrypt {
			block.Decrypt(result[:], input[:])
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
			block.Encrypt(result[:], input[:])
			if options.mode == pgcryptoModeCBC {
				chain = result
			}
		}
		n = aes.BlockSize
		if decrypt && options.padding && offset+aes.BlockSize == length {
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

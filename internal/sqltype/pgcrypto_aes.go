package sqltype

import (
	"context"
	"crypto/aes"
	"fmt"
	"strings"
)

type pgcryptoAESOptions struct {
	ecb         bool
	padding     bool
	unsupported bool
}

// The normalized complete type is also the diagnostic name. Options are parsed
// before cipher lookup, and only the final pad value participates in admission.
func parsePgcryptoAES(name string) (pgcryptoAESOptions, error) {
	options := pgcryptoAESOptions{padding: true}
	cipherName, rest, _ := strings.Cut(name, "/")
	pad := "pkcs"
	failure := func(reason string) (pgcryptoAESOptions, error) {
		return pgcryptoAESOptions{}, pgcryptoError("22023", fmt.Sprintf("Cannot use \"%s\": %s", name, reason))
	}
	for rest != "" {
		var option string
		option, rest, _ = strings.Cut(rest, "/")
		if option == "" {
			continue
		}
		key, value, colon := strings.Cut(option, ":")
		if !colon {
			return failure("Badly formatted type")
		}
		if key != "pad" {
			return failure("Unknown option")
		}
		pad = value
	}
	switch cipherName {
	case "aes", "aes-cbc", "rijndael", "rijndael-cbc":
	case "aes-ecb", "rijndael-ecb":
		options.ecb = true
	case "aes-cfb", "rijndael-cfb", "bf", "blowfish", "bf-cbc", "bf-ecb", "bf-cfb", "blowfish-cbc", "blowfish-ecb", "blowfish-cfb", "des", "des-cbc", "des-ecb", "3des", "3des-cbc", "3des-ecb", "des3-cbc", "des3-ecb", "cast5", "cast5-cbc", "cast5-ecb":
		options.unsupported = true
	default:
		return failure("No such cipher algorithm")
	}
	switch pad {
	case "pkcs":
	case "none":
		options.padding = false
	default:
		return failure("No such cipher algorithm")
	}
	return options, nil
}

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

// Return the unpadded final-block length only after checking the complete run.
func pgcryptoAESUnpad(block *[aes.BlockSize]byte) (int, bool) {
	pad := int(block[aes.BlockSize-1])
	if pad < 1 || pad > aes.BlockSize {
		return 0, false
	}
	for _, value := range block[aes.BlockSize-pad:] {
		if value != byte(pad) {
			return 0, false
		}
	}
	return aes.BlockSize - pad, true
}

// Input octets remain immutable strings. Scratch and chaining state are bounded;
// the builder owns the one full result allocation, without padded-input staging.
func pgcryptoAES(ctx context.Context, function, data, rawKey, rawIV, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	options, err := parsePgcryptoAES(name)
	if err != nil {
		return "", err
	}
	if options.unsupported {
		return "", pgcryptoError("0A000", function+": unsupported cipher algorithm")
	}
	decrypt := function == "decrypt" || function == "decrypt_iv"
	failure := func() (string, error) {
		message := "Encryption failed"
		if decrypt {
			message = "Decryption failed"
		}
		return "", pgcryptoError("39000", function+" error: "+message)
	}
	if len(data)%aes.BlockSize != 0 && (decrypt || !options.padding) || decrypt && options.padding && len(data) == 0 {
		return failure()
	}
	key, keySize := pgcryptoAESKey(rawKey)
	block, err := aes.NewCipher(key[:keySize])
	if err != nil {
		return "", err
	}
	var chain [aes.BlockSize]byte
	if !options.ecb {
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
			if !options.ecb {
				for i := range result {
					result[i] ^= chain[i]
				}
				chain = input
			}
		} else {
			if !options.ecb {
				for i := range input {
					input[i] ^= chain[i]
				}
			}
			block.Encrypt(result[:], input[:])
			if !options.ecb {
				chain = result
			}
		}
		n = aes.BlockSize
		if decrypt && options.padding && offset+aes.BlockSize == length {
			var valid bool
			n, valid = pgcryptoAESUnpad(&result)
			if !valid {
				return failure()
			}
		}
		output.Write(result[:n])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return output.String(), nil
}

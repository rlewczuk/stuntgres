package sqltype

import (
	"context"
	"fmt"
	"strings"
)

type pgcryptoRawCipher uint8

const (
	pgcryptoCipherAES pgcryptoRawCipher = iota
	pgcryptoCipherBF
	pgcryptoCipherExcluded
)

type pgcryptoRawMode uint8

const (
	pgcryptoModeCBC pgcryptoRawMode = iota
	pgcryptoModeECB
	pgcryptoModeCFB64
)

type pgcryptoRawOptions struct {
	cipher  pgcryptoRawCipher
	mode    pgcryptoRawMode
	padding bool
}

// The normalized complete type is also the diagnostic name. Every option is
// parsed before cipher lookup; only the final pad value participates in admission.
func parsePgcryptoRaw(name string) (pgcryptoRawOptions, error) {
	options := pgcryptoRawOptions{padding: true}
	cipherName, rest, _ := strings.Cut(name, "/")
	pad := "pkcs"
	failure := func(reason string) (pgcryptoRawOptions, error) {
		return pgcryptoRawOptions{}, pgcryptoError("22023", fmt.Sprintf("Cannot use \"%s\": %s", name, reason))
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
		options.mode = pgcryptoModeECB
	case "bf", "blowfish", "bf-cbc", "blowfish-cbc":
		options.cipher = pgcryptoCipherBF
	case "bf-ecb", "blowfish-ecb":
		options.cipher, options.mode = pgcryptoCipherBF, pgcryptoModeECB
	case "bf-cfb", "blowfish-cfb":
		options.cipher, options.mode = pgcryptoCipherBF, pgcryptoModeCFB64
	case "aes-cfb", "rijndael-cfb", "des", "des-cbc", "des-ecb", "3des", "3des-cbc", "3des-ecb", "des3-cbc", "des3-ecb", "cast5", "cast5-cbc", "cast5-ecb":
		options.cipher = pgcryptoCipherExcluded
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

func pgcryptoRaw(ctx context.Context, function, data, key, iv, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	options, err := parsePgcryptoRaw(name)
	if err != nil {
		return "", err
	}
	switch options.cipher {
	case pgcryptoCipherAES:
		return pgcryptoAES(ctx, function, data, key, iv, options)
	case pgcryptoCipherBF:
		return pgcryptoBF(ctx, function, data, key, iv, options)
	default:
		return "", pgcryptoError("0A000", function+": unsupported cipher algorithm")
	}
}

func pgcryptoRawFailure(function string, decrypt bool) error {
	message := "Encryption failed"
	if decrypt {
		message = "Decryption failed"
	}
	return pgcryptoError("39000", function+" error: "+message)
}

// Return the unpadded final-block length only after checking the complete run.
// Bytes before the run are data, even when they resemble an invalid pad byte.
func pgcryptoRawUnpad(block []byte) (int, bool) {
	pad := int(block[len(block)-1])
	if pad < 1 || pad > len(block) {
		return 0, false
	}
	for _, value := range block[len(block)-pad:] {
		if value != byte(pad) {
			return 0, false
		}
	}
	return len(block) - pad, true
}

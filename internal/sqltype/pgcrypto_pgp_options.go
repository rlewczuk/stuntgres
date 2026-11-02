package sqltype

import (
	"context"

	"stuntgres/internal/sqlstate"
)

type pgpCipher uint8
type pgpDigest uint8
type pgpCompression uint8

const pgpAES128 pgpCipher = 7
const pgpSHA1 pgpDigest = 2

// Parsed options are invocation-local. Recognized algorithms are distinct from
// executable algorithms; later symmetric implementations extend this dispatch.
type pgpOptions struct {
	cipher, s2kCipher                                                 pgpCipher
	digest                                                            pgpDigest
	mode, count                                                       int
	compression                                                       pgpCompression
	level                                                             int
	disableMDC, sessionKey, unicode, convertCRLF, ignoreCipherFailure bool
}

func pgpError(message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: message}
}

func pgpCorrupt() error  { return pgpError("Wrong key or corrupt data") }
func pgpArgument() error { return pgpError("Illegal argument to function") }
func pgpCapability() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "requested OpenPGP operation is not supported"}
}

func pgpEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		c := a[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != b[i] {
			return false
		}
	}
	return true
}

func pgpCipherName(name string) (pgpCipher, error) {
	for _, entry := range [...]struct {
		name string
		code pgpCipher
	}{
		{"3des", 2}, {"cast5", 3}, {"bf", 4}, {"blowfish", 4},
		{"aes", 7}, {"aes128", 7}, {"aes192", 8}, {"aes256", 9}, {"twofish", 10},
	} {
		if pgpEqual(name, entry.name) {
			return entry.code, nil
		}
	}
	return 0, pgpError("Unsupported cipher algorithm")
}

func pgpDigestName(name string) (pgpDigest, error) {
	for _, entry := range [...]struct {
		name string
		code pgpDigest
	}{
		{"md5", 1}, {"sha1", 2}, {"sha-1", 2}, {"ripemd160", 3},
		{"sha256", 8}, {"sha384", 9}, {"sha512", 10},
	} {
		if pgpEqual(name, entry.name) {
			return entry.code, nil
		}
	}
	return 0, pgpError("Unsupported digest algorithm")
}

func pgpSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

// getword's separators and whitespace are deliberately ASCII and narrow.
func pgpWord(ctx context.Context, raw string, offset int) (string, int, error) {
	for offset < len(raw) && pgpSpace(raw[offset]) {
		if offset&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", offset, err
			}
		}
		offset++
	}
	start := offset
	if offset < len(raw) && (raw[offset] == '=' || raw[offset] == ',') {
		offset++
	} else {
		for offset < len(raw) && !pgpSpace(raw[offset]) && raw[offset] != '=' && raw[offset] != ',' {
			if offset&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return "", offset, err
				}
			}
			offset++
		}
	}
	word := raw[start:offset]
	for offset < len(raw) && pgpSpace(raw[offset]) {
		if offset&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", offset, err
			}
		}
		offset++
	}
	return word, offset, nil
}

// PostgreSQL's setters use C atoi, not a whole-token integer parser. Model the
// release's LP64 strtol-to-int narrowing; no platform overflow promise is exposed.
func pgpAtoi(ctx context.Context, raw string) (int, error) {
	i := 0
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r' || raw[i] == '\v' || raw[i] == '\f') {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		i++
	}
	negative := false
	if i < len(raw) && (raw[i] == '-' || raw[i] == '+') {
		negative = raw[i] == '-'
		i++
	}
	var n uint64
	limit := uint64(1<<63 - 1)
	if negative {
		limit++
	}
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		d := uint64(raw[i] - '0')
		if n > (limit-d)/10 {
			n = limit
		} else {
			n = n*10 + d
		}
		i++
	}
	if negative {
		return int(int32(uint32(0) - uint32(n))), nil
	}
	return int(int32(uint32(n))), nil
}

func pgpParseOptions(ctx context.Context, raw string) (pgpOptions, error) {
	o := pgpOptions{cipher: pgpAES128, digest: pgpSHA1, mode: 3, count: -1, level: 6}
	for pos := 0; pos < len(raw); {
		if err := ctx.Err(); err != nil {
			return o, err
		}
		key, next, err := pgpWord(ctx, raw, pos)
		if err != nil {
			return o, err
		}
		if next >= len(raw) || raw[next] != '=' {
			return o, pgpArgument()
		}
		value, next, err := pgpWord(ctx, raw, next+1)
		if err != nil {
			return o, err
		}
		if next < len(raw) {
			if raw[next] != ',' {
				return o, pgpArgument()
			}
			next++
		}
		if key == "" || value == "" {
			return o, pgpArgument()
		}
		n, err := pgpAtoi(ctx, value)
		if err != nil {
			return o, err
		}
		switch {
		case pgpEqual(key, "cipher-algo"):
			o.cipher, err = pgpCipherName(value)
		case pgpEqual(key, "s2k-cipher-algo"):
			o.s2kCipher, err = pgpCipherName(value)
		case pgpEqual(key, "s2k-digest-algo"):
			o.digest, err = pgpDigestName(value)
		case pgpEqual(key, "s2k-mode"):
			if n != 0 && n != 1 && n != 3 {
				return o, pgpArgument()
			}
			o.mode = n
		case pgpEqual(key, "s2k-count"):
			if o.mode != 3 || n < 1024 || n > 65011712 {
				return o, pgpArgument()
			}
			o.count = n
		case pgpEqual(key, "compress-algo"):
			if n < 0 || n > 3 {
				return o, pgpArgument()
			}
			o.compression = pgpCompression(n)
		case pgpEqual(key, "compress-level"):
			if n < 0 || n > 9 {
				return o, pgpArgument()
			}
			o.level = n
		case pgpEqual(key, "disable-mdc"):
			o.disableMDC = n != 0
		case pgpEqual(key, "sess-key"):
			o.sessionKey = n != 0
		case pgpEqual(key, "unicode-mode"):
			o.unicode = n != 0
		case pgpEqual(key, "convert-crlf"):
			o.convertCRLF = n != 0
		case pgpEqual(key, "ignore-cipher-failure"):
			o.ignoreCipherFailure = n != 0
		case pgpEqual(key, "debug"), pgpEqual(key, "expect-cipher-algo"), pgpEqual(key, "expect-disable-mdc"),
			pgpEqual(key, "expect-sess-key"), pgpEqual(key, "expect-s2k-mode"), pgpEqual(key, "expect-s2k-count"),
			pgpEqual(key, "expect-s2k-digest-algo"), pgpEqual(key, "expect-s2k-cipher-algo"),
			pgpEqual(key, "expect-compress-algo"), pgpEqual(key, "expect-unicode-mode"):
			return o, pgpCapability()
		default:
			return o, pgpArgument()
		}
		if err != nil {
			return o, err
		}
		pos = next
	}
	return o, nil
}

func (o pgpOptions) encryptionProfile() error {
	if o.cipher != pgpAES128 || (o.s2kCipher != 0 && o.s2kCipher != pgpAES128) {
		return pgpError("Unsupported cipher algorithm")
	}
	if o.digest != pgpSHA1 {
		return pgpError("Unsupported digest algorithm")
	}
	if o.compression != 0 {
		return pgpError("Unsupported compression algorithm")
	}
	if o.mode != 3 || o.disableMDC || o.sessionKey || o.unicode || o.convertCRLF {
		return pgpCapability()
	}
	return nil
}

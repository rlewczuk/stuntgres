package sqltype

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// PostgreSQL REL_18_6 crypt-sha.c follows the public-domain Drepper algorithm,
// but distinguishes decoded salt from the raw prefix used by digest B and DS.
// Keep both views: dropping leading dollars changes real PostgreSQL hashes.
type pgcryptoSHASetting struct {
	id     byte
	rounds uint32
	custom bool
	salt   [16]byte
	size   int
	raw    string
}

func pgcryptoSHAParse(setting string, notice func(sqlstate.Error)) (pgcryptoSHASetting, error) {
	p := pgcryptoSHASetting{id: setting[1], rounds: 5000}
	raw := setting[3:] // Called only for the exact dispatch prefixes $5$/$6$.
	if strings.HasPrefix(raw, "rounds=") {
		n, end := pgcryptoSHARounds(raw[7:])
		if end == len(raw)-7 || raw[7+end] != '$' {
			return pgcryptoSHASetting{}, pgcryptoError("42601", "could not parse salt options")
		}
		raw = raw[8+end:]
		var message string
		if n < 1000 {
			if notice != nil {
				message = fmt.Sprintf("rounds=%d is below supported value (1000), using 1000 instead", n)
			}
			n = 1000
		} else if n > 999999999 {
			if notice != nil {
				message = fmt.Sprintf("rounds=%d exceeds maximum supported value (999999999), using 999999999 instead", n)
			}
			n = 999999999
		}
		if message != "" {
			notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: message})
		}
		p.rounds, p.custom = uint32(n), true
	}
	if raw != "" {
		// Release checks the ENTIRE suffix before inspecting each of at most16
		// raw bytes. Hoist identical searches, preserving diagnostic precedence.
		if strings.Contains(raw, "$5$") || strings.Contains(raw, "$6$") {
			return pgcryptoSHASetting{}, pgcryptoError("XX000", "bogus magic byte found in salt string")
		}
		if strings.Contains(raw, "rounds=") {
			return pgcryptoSHASetting{}, pgcryptoError("XX000", "invalid rounds option specified in salt string")
		}
	}
	for i := range min(16, len(raw)) {
		b := raw[i]
		if b == '$' {
			if p.size > 0 {
				break
			}
			continue
		}
		if strings.IndexByte(pgcryptoCryptAlphabet, b) < 0 {
			_, size := utf8.DecodeRuneInString(raw[i:])
			return pgcryptoSHASetting{}, pgcryptoError("22023", fmt.Sprintf("invalid character in salt string: \"%s\"", raw[i:i+size]))
		}
		p.salt[p.size] = b
		p.size++
	}
	p.raw = raw[:p.size]
	return p, nil
}

// Match the pinned64-bit C strtol(base10) end pointer and saturation, then
// PostgreSQL strtoint's int32 narrowing. This is deliberately NOT SQL INT4 input.
func pgcryptoSHARounds(s string) (int32, int) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] >= '\t' && s[i] <= '\r') {
		i++
	}
	negative := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		negative = s[i] == '-'
		i++
	}
	start := i
	limit := uint64(1<<63) - 1
	if negative {
		limit++
	}
	var value uint64
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		digit := uint64(s[i] - '0')
		if value > (limit-digit)/10 {
			value = limit
		} else {
			value = value*10 + digit
		}
		i++
	}
	if i == start {
		return 0, 0
	}
	if negative {
		return int32(-value), i
	}
	return int32(value), i
}

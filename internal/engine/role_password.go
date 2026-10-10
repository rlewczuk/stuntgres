package engine

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

type passwordNotice uint8

const (
	passwordNoNotice passwordNotice = iota
	passwordEmptyNotice
	passwordMD5Notice
)

func (n passwordNotice) emit(op *Operation) {
	switch n {
	case passwordEmptyNotice:
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "empty string is not a valid password, clearing password"})
	case passwordMD5Notice:
		op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01P01", Message: "setting an MD5-encrypted password", Detail: "MD5 password support is deprecated and will be removed in a future release of PostgreSQL.", Hint: "Refer to the PostgreSQL documentation for details about migrating to another password type."})
	}
}

type roleSCRAM struct {
	iterations           int
	salt, stored, server []byte
}

func parseRoleSCRAM(s string) (roleSCRAM, bool) {
	var out roleSCRAM
	rest, ok := strings.CutPrefix(s, "SCRAM-SHA-256$")
	if !ok {
		return out, false
	}
	iterations, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return out, false
	}
	salt, rest, ok := strings.Cut(rest, "$")
	if !ok {
		return out, false
	}
	stored, server, ok := strings.Cut(rest, ":")
	if !ok {
		return out, false
	}
	// Release strtol permits leading ASCII whitespace, a sign, leading zeroes,
	// and nonpositive counts. Classification is separate from key generation.
	originalIterations := iterations
	iterations = strings.TrimLeft(iterations, " \t\r\n\v\f")
	if originalIterations != "" && iterations == "" {
		return out, false
	}
	var n int64
	var err error
	if iterations != "" {
		n, err = strconv.ParseInt(iterations, 10, 64)
	}
	if err != nil {
		return out, false
	}
	out.iterations = int(int32(n))
	decode := func(s string) ([]byte, error) {
		s = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, s)
		return base64.StdEncoding.DecodeString(s)
	}
	out.salt, err = decode(salt)
	if err != nil {
		return out, false
	}
	out.stored, err = decode(stored)
	if err != nil || len(out.stored) != 32 {
		return out, false
	}
	out.server, err = decode(server)
	if err != nil || len(out.server) != 32 {
		return out, false
	}
	return out, true
}
func roleMD5(s string) bool {
	if len(s) != 35 || !strings.HasPrefix(s, "md5") {
		return false
	}
	for _, c := range s[3:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func roleHMAC(key []byte, message string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return h.Sum(nil)
}
func roleSCRAMSaltedPassword(ctx context.Context, password string, salt []byte, iterations int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// PostgreSQL's Hi computes U1 even when a preencoded count is <=0.
	// Reuse the keyed hash and fixed buffers so attacker-selected iteration
	// counts remain interruptible without workers or per-iteration allocation.
	h := hmac.New(sha256.New, []byte(password))
	var u, salted [sha256.Size]byte
	block := [4]byte{0, 0, 0, 1}
	h.Write(salt)
	h.Write(block[:])
	h.Sum(u[:0])
	salted = u
	for i := 1; i < iterations; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.Reset()
		h.Write(u[:])
		h.Sum(u[:0])
		for j := range salted {
			salted[j] ^= u[j]
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return salted[:], nil
}
func roleSCRAMKeys(ctx context.Context, password string, salt []byte, iterations int) ([]byte, []byte, error) {
	salted, err := roleSCRAMSaltedPassword(ctx, password, salt, iterations)
	if err != nil {
		return nil, nil, err
	}
	client := roleHMAC(salted, "Client Key")
	stored := sha256.Sum256(client)
	return stored[:], roleHMAC(salted, "Server Key"), nil
}
func prepareRolePassword(ctx context.Context, name, password string) (string, passwordNotice, error) {
	if err := ctx.Err(); err != nil {
		return "", passwordNoNotice, err
	}
	if password == "" {
		return "", passwordEmptyNotice, nil
	}
	md5Encoded := roleMD5(password)
	scram, scramEncoded := parseRoleSCRAM(password)
	if md5Encoded || scramEncoded {
		if len(password) > 512 {
			return "", passwordNoNotice, roleDiagnostic("54000", "encrypted password is too long", "Encrypted passwords must be no longer than 512 bytes.")
		}
		empty := false
		if md5Encoded {
			sum := md5.Sum([]byte(name))
			empty = password == "md5"+hex.EncodeToString(sum[:])
		} else {
			salted, err := roleSCRAMSaltedPassword(ctx, "", scram.salt, scram.iterations)
			if err != nil {
				return "", passwordNoNotice, err
			}
			empty = hmac.Equal(roleHMAC(salted, "Server Key"), scram.server)
		}
		if empty {
			return "", passwordEmptyNotice, nil
		}
		if md5Encoded {
			return password, passwordMD5Notice, nil
		}
		return password, passwordNoNotice, nil
	}
	prepared := roleSASLprep(password)
	var salt [16]byte
	if _, err := io.ReadFull(rand.Reader, salt[:]); err != nil {
		return "", passwordNoNotice, fmt.Errorf("stuntgres: generating role password salt: %w", err)
	}
	stored, server, err := roleSCRAMKeys(ctx, prepared, salt[:], 4096)
	if err != nil {
		return "", passwordNoNotice, err
	}
	return "SCRAM-SHA-256$4096:" + base64.StdEncoding.EncodeToString(salt[:]) + "$" + base64.StdEncoding.EncodeToString(stored) + ":" + base64.StdEncoding.EncodeToString(server), passwordNoNotice, nil
}

func saslInRanges(r rune, ranges []rune) bool {
	lo, hi := 0, len(ranges)/2
	for lo < hi {
		mid := lo + (hi-lo)/2
		if r < ranges[2*mid] {
			hi = mid
		} else if r > ranges[2*mid+1] {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

// REL_18_0 checks mapped input, not normalized output. Failure uses original
// bytes, including invalid UTF8. Its pure-ASCII fast path includes controls.
func roleSASLprep(password string) string {
	ascii := true
	for i := range len(password) {
		if password[i] >= 128 {
			ascii = false
			break
		}
	}
	if ascii || !utf8.ValidString(password) {
		return password
	}
	mapped := make([]rune, 0, utf8.RuneCountInString(password))
	rtl := false
	left := false
	for _, r := range password {
		if saslInRanges(r, sasl_non_ascii_space_ranges[:]) {
			r = ' '
		} else if saslInRanges(r, sasl_commonly_mapped_to_nothing_ranges[:]) {
			continue
		}
		if saslInRanges(r, sasl_prohibited_output_ranges[:]) || saslInRanges(r, sasl_unassigned_codepoint_ranges[:]) {
			return password
		}
		rtl = rtl || saslInRanges(r, sasl_RandALCat_codepoint_ranges[:])
		left = left || saslInRanges(r, sasl_LCat_codepoint_ranges[:])
		mapped = append(mapped, r)
	}
	if len(mapped) == 0 || rtl && (left || !saslInRanges(mapped[0], sasl_RandALCat_codepoint_ranges[:]) || !saslInRanges(mapped[len(mapped)-1], sasl_RandALCat_codepoint_ranges[:])) {
		return password
	}
	result, err := unicode16.Normalize(context.Background(), string(mapped), unicode16.NFKC)
	if err != nil {
		return password
	}
	return result
}

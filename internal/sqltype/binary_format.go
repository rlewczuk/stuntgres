package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// Algorithms and error precedence follow PostgreSQL REL_18_0 encode.c:
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/encode.c
// Portions derived from PostgreSQL, Copyright (c) 2001-2025 PostgreSQL Global
// Development Group. The full upstream grant/disclaimer is in pgencoding18/LICENSE.
const binaryFormatLimit uint64 = (1 << 30) - 5 // MaxAllocSize - VARHDRSZ
const binaryBase64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func binaryFormatName(name, expected string) bool {
	if len(name) != len(expected) {
		return false
	}
	for i := range name {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != expected[i] {
			return false
		}
	}
	return true
}

func binaryBase64Digit(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+':
		return 62
	case c == '/':
		return 63
	}
	return -1
}

func binaryFormatSize(size uint64, encode bool) error {
	if size <= binaryFormatLimit {
		return nil
	}
	direction := "decoding"
	if encode {
		direction = "encoding"
	}
	return failure("54000", "result of "+direction+" conversion is too large")
}

func binaryFormat(ctx context.Context, raw, format string, encode bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	kind := ""
	for _, candidate := range [...]string{"hex", "escape", "base64"} {
		if binaryFormatName(format, candidate) {
			kind = candidate
			break
		}
	}
	if kind == "" {
		return "", failure("22023", fmt.Sprintf("unrecognized encoding: \"%s\"", format))
	}
	n := uint64(len(raw))
	estimate := n
	switch kind {
	case "hex":
		if encode {
			estimate = n * 2
		} else {
			estimate = n / 2
		}
	case "base64":
		if encode {
			estimate = (n+2)/3*4 + n/57
		} else {
			estimate = n * 3 / 4
		}
	}
	if err := binaryFormatSize(estimate, encode); err != nil {
		return "", err
	}
	// Escape length must be checked before allocating, including malformed escapes.
	// All formats use a counting pass to allocate only the actual output, not the
	// release decoder's deliberately loose upper bound for whitespace-heavy input.
	size, err := binaryFormatPass(ctx, raw, kind, encode, nil)
	if err != nil {
		return "", err
	}
	if err := binaryFormatSize(uint64(size), encode); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	if _, err := binaryFormatPass(ctx, raw, kind, encode, &out); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func binaryFormatSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func binaryFormatPass(ctx context.Context, raw, kind string, encode bool, out *strings.Builder) (int, error) {
	size := 0
	emit := func(c byte) {
		size++
		if out != nil {
			out.WriteByte(c)
		}
	}
	next := 0
	check := func(i int) error {
		if i >= next {
			next = i + 4096
			return ctx.Err()
		}
		return nil
	}
	if !encode {
		switch kind {
		case "hex":
			for i := 0; i < len(raw); {
				if err := check(i); err != nil {
					return 0, err
				}
				if binaryFormatSpace(raw[i]) {
					i++
					continue
				}
				hi, err := hexNibble(raw[i:])
				if err != nil {
					return 0, err
				}
				i++
				if i == len(raw) {
					return 0, failure("22023", "invalid hexadecimal data: odd number of digits")
				}
				lo, err := hexNibble(raw[i:])
				if err != nil {
					return 0, err
				}
				i++
				emit(hi<<4 | lo)
			}
		case "escape":
			for i := 0; i < len(raw); i++ {
				if err := check(i); err != nil {
					return 0, err
				}
				if raw[i] != '\\' {
					emit(raw[i])
					continue
				}
				if i+1 < len(raw) && raw[i+1] == '\\' {
					emit('\\')
					i++
					continue
				}
				if i+3 >= len(raw) || raw[i+1] < '0' || raw[i+1] > '3' || raw[i+2] < '0' || raw[i+2] > '7' || raw[i+3] < '0' || raw[i+3] > '7' {
					return 0, failure("22P02", "invalid input syntax for type bytea")
				}
				emit((raw[i+1]-'0')<<6 | (raw[i+2]-'0')<<3 | (raw[i+3] - '0'))
				i += 3
			}
		case "base64":
			var buf uint32
			pos, end := 0, 0
			for i := range len(raw) {
				if err := check(i); err != nil {
					return 0, err
				}
				c := raw[i]
				if binaryFormatSpace(c) {
					continue
				}
				b := 0
				if c == '=' {
					if end == 0 {
						if pos == 2 {
							end = 1
						} else if pos == 3 {
							end = 2
						} else {
							return 0, failure("22023", "unexpected \"=\" while decoding base64 sequence")
						}
					}
				} else {
					b = binaryBase64Digit(c)
					if b < 0 {
						_, width := utf8.DecodeRuneInString(raw[i:])
						return 0, failure("22023", fmt.Sprintf("invalid symbol \"%s\" found while decoding base64 sequence", raw[i:i+width]))
					}
				}
				buf = buf<<6 + uint32(b)
				pos++
				if pos == 4 {
					emit(byte(buf >> 16))
					if end == 0 || end > 1 {
						emit(byte(buf >> 8))
					}
					if end == 0 || end > 2 {
						emit(byte(buf))
					}
					buf, pos = 0, 0
				}
			}
			if pos != 0 {
				return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid base64 end sequence", Hint: "Input data is missing padding, is truncated, or is otherwise corrupted."}
			}
		}
	} else {
		const hex = "0123456789abcdef"
		for i := 0; i < len(raw); i++ {
			if err := check(i); err != nil {
				return 0, err
			}
			c := raw[i]
			switch kind {
			case "hex":
				emit(hex[c>>4])
				emit(hex[c&15])
			case "escape":
				if c == 0 || c >= 128 {
					emit('\\')
					emit('0' + (c >> 6))
					emit('0' + (c >> 3 & 7))
					emit('0' + (c & 7))
				} else if c == '\\' {
					emit('\\')
					emit('\\')
				} else {
					emit(c)
				}
			case "base64":
				// Emit whole triples; a partial final quartet never receives a linefeed.
				remain := len(raw) - i
				buf := uint32(c) << 16
				if remain > 1 {
					buf |= uint32(raw[i+1]) << 8
				}
				if remain > 2 {
					buf |= uint32(raw[i+2])
				}
				emit(binaryBase64Alphabet[buf>>18&63])
				emit(binaryBase64Alphabet[buf>>12&63])
				if remain > 1 {
					emit(binaryBase64Alphabet[buf>>6&63])
				} else {
					emit('=')
				}
				if remain > 2 {
					emit(binaryBase64Alphabet[buf&63])
				} else {
					emit('=')
				}
				if remain >= 3 && (i+3)%57 == 0 {
					emit('\n')
				}
				i += min(3, remain) - 1
			}
		}
	}
	return size, ctx.Err()
}

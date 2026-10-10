// Package pgencoding18 implements immutable PostgreSQL REL_18_0 encoding data
// and character algorithms. Numeric identities are the release pg_enc enum.
// Sources: data/src/common/wchar.c and data/src/include/mb/pg_wchar.h.
package pgencoding18

import "unicode/utf8"

func between(b byte, lo, hi byte) bool { return b >= lo && b <= hi }

// Length is PostgreSQL mblen, including its diagnostic width for invalid leads.
func Length(id int, s string) int {
	c := s[0]
	switch id {
	case 1, 3, 5, 40:
		if c == 0x8e {
			return 2
		}
		if c == 0x8f {
			return 3
		}
		if c >= 128 {
			return 2
		}
	case 2:
		if c >= 128 {
			return 2
		}
	case 4:
		if c == 0x8e {
			return 4
		}
		if c == 0x8f {
			return 3
		}
		if c >= 128 {
			return 2
		}
	case 6:
		if c&0xe0 == 0xc0 {
			return 2
		}
		if c&0xf0 == 0xe0 {
			return 3
		}
		if c&0xf8 == 0xf0 {
			return 4
		}
	case 7:
		if between(c, 0x81, 0x8d) {
			return 2
		}
		if between(c, 0x90, 0x9b) {
			return 3
		}
		if c == 0x9c || c == 0x9d {
			return 4
		}
	case 35, 41:
		if c >= 128 && !between(c, 0xa1, 0xdf) {
			return 2
		}
	case 36, 37, 38:
		if c >= 128 {
			return 2
		}
	case 39:
		if c >= 128 {
			if len(s) > 1 && between(s[1], 0x30, 0x39) {
				return 4
			}
			return 2
		}
	}
	return 1
}

// Verify returns the release verifier width, or -1 for an invalid character.
// NUL is rejected here as by pg_verify_mbstr_len's callers, even when the
// underlying single-byte verifychar accepts every byte.
func Verify(id int, s string) int {
	c := s[0]
	if c == 0 {
		return -1
	}
	n := Length(id, s)
	if len(s) < n {
		return -1
	}
	switch id {
	case 1, 5:
		if c == 0x8e {
			if !between(s[1], 0xa1, 0xdf) {
				return -1
			}
		} else if c == 0x8f {
			if !between(s[1], 0xa1, 0xfe) || !between(s[2], 0xa1, 0xfe) {
				return -1
			}
		} else if c >= 128 {
			if !between(c, 0xa1, 0xfe) || !between(s[1], 0xa1, 0xfe) {
				return -1
			}
		}
	case 2, 3:
		// EUC_CN has the EUC_KR verifier, not the generic EUC mblen.
		if c >= 128 {
			if len(s) < 2 || !between(c, 0xa1, 0xfe) || !between(s[1], 0xa1, 0xfe) {
				return -1
			}
			return 2
		}
	case 4:
		if c == 0x8f {
			return -1
		}
		if c == 0x8e {
			if !between(s[1], 0xa1, 0xa7) || !between(s[2], 0xa1, 0xfe) || !between(s[3], 0xa1, 0xfe) {
				return -1
			}
		} else if c >= 128 && !between(s[1], 0xa1, 0xfe) {
			return -1
		}
	case 6:
		r, w := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && w == 1 {
			return -1
		}
		return w
	case 7:
		for i := 1; i < n; i++ {
			if s[i] < 128 {
				return -1
			}
		}
	case 35, 41:
		if n == 2 && (!(between(c, 0x81, 0x9f) || between(c, 0xe0, 0xfc)) || !(between(s[1], 0x40, 0x7e) || between(s[1], 0x80, 0xfc))) {
			return -1
		}
	case 36, 37, 38:
		if n == 2 && (s[1] == 0 || c == 0x8d && s[1] == 0x20) {
			return -1
		}
	case 39:
		if c < 128 {
			return 1
		}
		if n == 4 {
			if !between(c, 0x81, 0xfe) || !between(s[2], 0x81, 0xfe) || !between(s[3], 0x30, 0x39) {
				return -1
			}
		} else if !between(c, 0x81, 0xfe) || !(between(s[1], 0x40, 0x7e) || between(s[1], 0x80, 0xfe)) {
			return -1
		}
	case 40:
		for i := 1; i < n; i++ {
			if !between(s[i], 0xa1, 0xfe) {
				return -1
			}
		}
	}
	return n
}

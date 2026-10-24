package sqltype

import "fmt"

// scanIsn follows REL_18_6 string2ean's raw lookahead. In particular a
// separator after a short check digit does not count as an end-of-number.
func scanIsn(text, target string, digits *[14]byte) (n int, family string, magic, valid bool, err error) {
	invalid := func() error { return failure("22P02", fmt.Sprintf("invalid input syntax for %s number: \"%s\"", target, text)) }
	valid = true
	for i := range len(text) {
		c := text[i]
		last := i+1 == len(text) || text[i+1] == '!'
		digit := c >= '0' && c <= '9'
		if c == '?' && last { magic, digit = true, true }
		switch {
		case n == 0 && (c == 'M' || c == 'm'):
			if family != "" { err = invalid(); return }; family, c = "ISMN", 'M'
		case n == 7 && (digit || c == 'X' || c == 'x') && last:
			if family != "" { err = invalid(); return }; family = "ISSN"
		case n == 9 && (digit || c == 'X' || c == 'x') && last:
			if family != "" && family != "ISMN" { err = invalid(); return }; if family == "" { family = "ISBN" }
		case n == 11 && digit && last:
			if family != "" { err = invalid(); return }; family = "UPC"
		case c == '-' || c == ' ': continue
		case c == '!' && i+1 == len(text):
			if !magic { valid = false }; magic = true; continue
		case !digit: err = invalid(); return
		}
		digits[n] = c; n++
		if n > 13 { err = failure("22003", fmt.Sprintf("value \"%s\" is out of range for %s type", text, target)); return }
	}
	switch n {
	case 13: if family != "" { err = invalid() }
	case 12: if family != "UPC" { err = invalid() }
	case 10: if family != "ISBN" && family != "ISMN" { err = invalid() }
	case 8: if family != "" && family != "ISSN" { err = invalid() }; family = "ISSN"
	default: err = invalid()
	}
	return
}

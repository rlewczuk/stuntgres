package functions

import "context"

/*
Metaphone, ported to bounded Go buffers and byte lookaround on 2026-10-15.
Derived from PostgreSQL REL_18_0 contrib/fuzzystrmatch/fuzzystrmatch.c.
Copyright (c) 2001-2025, PostgreSQL Global Development Group
ALL RIGHTS RESERVED;
Modified for PostgreSQL by Joe Conway.
Based on CPAN's "Text-Metaphone-1.96" by Michael G Schwern <schwern@pobox.com>.
Metaphone was originally created by Lawrence Philips and presented in article
in "Computer Language" December 1990 issue.

Permission to use, copy, modify, and distribute this software and its
 documentation for any purpose, without fee, and without a written agreement
is hereby granted, provided that the above copyright notice and this
paragraph and the following two paragraphs appear in all copies.

IN NO EVENT SHALL THE AUTHORS OR DISTRIBUTORS BE LIABLE TO ANY PARTY FOR
DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING
LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
DOCUMENTATION, EVEN IF THE AUTHOR OR DISTRIBUTORS HAVE BEEN ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.

THE AUTHORS AND DISTRIBUTORS SPECIFICALLY DISCLAIM ANY WARRANTIES,
INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
AND FITNESS FOR A PARTICULAR PURPOSE.  THE SOFTWARE PROVIDED HEREUNDER IS
ON AN "AS IS" BASIS, AND THE AUTHOR AND DISTRIBUTORS HAS NO OBLIGATIONS TO
PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.
*/

func phoneticUpper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}
func phoneticAlpha(c byte) bool { return c >= 'A' && c <= 'Z' }
func metaphoneFlags(c byte) byte {
	const codes = "\x01\x10\x04\x10\x09\x02\x04\x10\x09\x02\x00\x02\x02\x02\x01\x04\x00\x02\x04\x04\x01\x00\x00\x00\x08\x00"
	if !phoneticAlpha(c) {
		return 0
	}
	return codes[c-'A']
}

// MetaphoneCode implements the nontraditional release variant in the C locale.
// Byte positions (including punctuation) are deliberately not pre-cleaned.
func MetaphoneCode(ctx context.Context, source string, limit int32) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source) == 0 {
		return "", nil
	}
	if len(source) > 255 {
		return "", sqlError("22023", "argument exceeds the maximum length of 255 bytes")
	}
	if limit > 255 {
		return "", sqlError("22023", "output exceeds the maximum length of 255 bytes")
	}
	if limit <= 0 {
		return "", sqlError("2200F", "output cannot be empty string")
	}
	at := func(i int) byte {
		if i < 0 || i >= len(source) {
			return 0
		}
		return phoneticUpper(source[i])
	}
	var output [255]byte
	n := 0
	add := func(c byte) {
		if n < int(limit) {
			output[n] = c
			n++
		}
	}
	i := 0
	for i < len(source) && !phoneticAlpha(at(i)) {
		i++
	}
	if i == len(source) {
		return "", nil
	}
	switch at(i) {
	case 'A':
		if at(i+1) == 'E' {
			add('E')
			i += 2
		} else {
			add('A')
			i++
		}
	case 'G', 'K', 'P':
		if at(i+1) == 'N' {
			add('N')
			i += 2
		}
	case 'W':
		if at(i+1) == 'H' || at(i+1) == 'R' {
			add(at(i + 1))
			i += 2
		} else if metaphoneFlags(at(i+1))&1 != 0 {
			add('W')
			i += 2
		}
	case 'X':
		add('S')
		i++
	case 'E', 'I', 'O', 'U':
		add(at(i))
		i++
	}
	for ; i < len(source) && n < int(limit); i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		c, prev, next, after := at(i), at(i-1), at(i+1), at(i+2)
		if !phoneticAlpha(c) || c == prev && c != 'C' {
			continue
		}
		switch c {
		case 'B':
			if prev != 'M' {
				add('B')
			}
		case 'C':
			if metaphoneFlags(next)&8 != 0 {
				if next == 'I' && after == 'A' {
					add('X')
				} else if prev != 'S' {
					add('S')
				}
			} else if next == 'H' {
				if after == 'R' || prev == 'S' {
					add('K')
				} else {
					add('X')
				}
				i++
			} else {
				add('K')
			}
		case 'D':
			if next == 'G' && metaphoneFlags(after)&8 != 0 {
				add('J')
				i++
			} else {
				add('T')
			}
		case 'G':
			if next == 'H' {
				if metaphoneFlags(at(i-3))&16 == 0 && at(i-4) != 'H' {
					add('F')
					i++
				}
			} else if next == 'N' {
				if phoneticAlpha(after) && !(after == 'E' && at(i+3) == 'D') {
					add('K')
				}
			} else if metaphoneFlags(next)&8 != 0 && prev != 'G' {
				add('J')
			} else {
				add('K')
			}
		case 'H':
			if metaphoneFlags(next)&1 != 0 && metaphoneFlags(prev)&4 == 0 {
				add('H')
			}
		case 'K':
			if prev != 'C' {
				add('K')
			}
		case 'P':
			if next == 'H' {
				add('F')
			} else {
				add('P')
			}
		case 'Q':
			add('K')
		case 'S':
			if next == 'I' && (after == 'O' || after == 'A') {
				add('X')
			} else if next == 'H' {
				add('X')
				i++
			} else if next == 'C' && after == 'H' && at(i+3) == 'W' {
				add('X')
				i += 2
			} else {
				add('S')
			}
		case 'T':
			if next == 'I' && (after == 'O' || after == 'A') {
				add('X')
			} else if next == 'H' {
				add('0')
				i++
			} else {
				add('T')
			}
		case 'V':
			add('F')
		case 'W', 'Y':
			if metaphoneFlags(next)&1 != 0 {
				add(c)
			}
		case 'X':
			add('K')
			add('S')
		case 'Z':
			add('S')
		case 'F', 'J', 'L', 'M', 'N', 'R':
			add(c)
		}
	}
	return string(output[:n]), nil
}

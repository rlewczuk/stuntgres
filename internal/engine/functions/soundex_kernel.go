package functions

import "context"

/*
Soundex, adapted from PostgreSQL REL_18_0 contrib/fuzzystrmatch/fuzzystrmatch.c.
Copyright (c) 2001-2025, PostgreSQL Global Development Group

Permission to use, copy, modify, and distribute this software and its
 documentation for any purpose, without fee, and without a written agreement
is hereby granted, provided that the above copyright notice and this
paragraph and the following two paragraphs appear in all copies.

IN NO EVENT SHALL THE POSTGRESQL GLOBAL DEVELOPMENT GROUP BE LIABLE TO ANY
PARTY FOR DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES,
INCLUDING LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
DOCUMENTATION, EVEN IF THE POSTGRESQL GLOBAL DEVELOPMENT GROUP HAS BEEN
ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

THE POSTGRESQL GLOBAL DEVELOPMENT GROUP SPECIFICALLY DISCLAIMS ANY
WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE. THE SOFTWARE
PROVIDED HEREUNDER IS ON AN "AS IS" BASIS, AND THE POSTGRESQL GLOBAL
DEVELOPMENT GROUP HAS NO OBLIGATIONS TO PROVIDE MAINTENANCE, SUPPORT,
UPDATES, ENHANCEMENTS, OR MODIFICATIONS.
*/

func soundexDigit(c byte) byte {
	c = phoneticUpper(c)
	if phoneticAlpha(c) {
		return "01230120022455012623010202"[c-'A']
	}
	return c
}

// soundexCode preserves original-byte adjacency, including raw digit collisions.
// The zero buffer represents no ASCII letter, not the padded code "0000".
func soundexCode(ctx context.Context, source string) ([4]byte, error) {
	var out [4]byte
	if err := ctx.Err(); err != nil {
		return out, err
	}
	i := 0
	for ; i < len(source); i++ {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return [4]byte{}, err
			}
		}
		if c := phoneticUpper(source[i]); phoneticAlpha(c) {
			out[0] = c
			break
		}
	}
	if i == len(source) {
		return out, nil
	}
	n := 1
	for i++; i < len(source) && n < len(out); i++ {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return [4]byte{}, err
			}
		}
		c := phoneticUpper(source[i])
		if phoneticAlpha(c) {
			code := soundexDigit(c)
			if code != '0' && code != soundexDigit(source[i-1]) {
				out[n] = code
				n++
			}
		}
	}
	for ; n < len(out); n++ {
		out[n] = '0'
	}
	return out, nil
}

func SoundexCode(ctx context.Context, source string) (string, error) {
	code, err := soundexCode(ctx, source)
	if err != nil || code[0] == 0 {
		return "", err
	}
	return string(code[:]), nil
}

func SoundexDifference(ctx context.Context, left, right string) (int32, error) {
	a, err := soundexCode(ctx, left)
	if err != nil {
		return 0, err
	}
	b, err := soundexCode(ctx, right)
	if err != nil {
		return 0, err
	}
	var score int32
	for i := range a {
		if a[i] == b[i] {
			score++
		}
	}
	return score, nil
}

package sqltype

/*
 * Go adaptation of PostgreSQL REL_18_0 regc_lex.c. Lexical modes,
 * incremental parse order and syntax diagnostics are retained for the shared
 * PostgreSQL regex compiler and JSONPath syntax-validation adapter.
 *
 * Copyright (c) 1998, 1999 Henry Spencer. All rights reserved.
 *
 * Development of this software was funded, in part, by Cray Research Inc.,
 * UUNET Communications Services Inc., Sun Microsystems Inc., and Scriptics
 * Corporation, none of whom are responsible for the results. The author
 * thanks all of them.
 *
 * Redistribution and use in source and binary forms -- with or without
 * modification -- are permitted for any purpose, provided that
 * redistributions in source form retain this entire copyright notice and
 * indicate the origin and nature of any modifications.
 *
 * I'd appreciate being given credit for this package in the documentation
 * of software which uses it, but that is not a requirement.
 *
 * THIS SOFTWARE IS PROVIDED ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES,
 * INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY
 * AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL
 * HENRY SPENCER BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
 * EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
 * PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS;
 * OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
 * WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
 * OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF
 * ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
 */
import (
	"context"
	"strings"
	"unicode/utf8"
)

const (
	pgRegexARE uint8 = iota
	pgRegexERE
	pgRegexBRE
	pgRegexQuote
)

type pgRegexToken struct {
	kind, name    string
	code          uint32
	capture, look bool
	lazy          bool
	lo, hi        int
	fixed         bool
}
type pgRegexScanner struct {
	ctx                                         context.Context
	text                                        string
	pos                                         int
	mode                                        uint8
	expanded                                    bool
	previous                                    string
	captures                                    int
	caseInsensitive, newlineStop, newlineAnchor bool
	boundToken                                  pgRegexToken
}

func regexASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func (s *pgRegexScanner) prefix() error {
	if s.mode == pgRegexQuote {
		return nil
	}
	if strings.HasPrefix(s.text, "***") && len(s.text) >= 4 {
		switch s.text[3] {
		case '?':
			return pgRegexCompileError("invalid regexp (reg version 0.8)")
		case '=':
			s.mode = pgRegexQuote
			s.expanded, s.newlineStop, s.newlineAnchor = false, false, false
			s.pos = 4
			return nil
		case ':':
			s.pos = 4
			s.mode = pgRegexARE
		default:
			return pgRegexCompileError("quantifier operand invalid")
		}
	}
	if s.mode == pgRegexARE && strings.HasPrefix(s.text[s.pos:], "(?") && len(s.text)-s.pos >= 3 && regexASCIIAlpha(s.text[s.pos+2]) {
		s.pos += 2
		for s.pos < len(s.text) && regexASCIIAlpha(s.text[s.pos]) {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			switch s.text[s.pos] {
			case 'b':
				s.mode = pgRegexBRE
			case 'e':
				s.mode = pgRegexERE
			case 'q':
				s.mode = pgRegexQuote
			case 'x':
				s.expanded = true
			case 't':
				s.expanded = false
			case 'c':
				s.caseInsensitive = false
			case 'i':
				s.caseInsensitive = true
			case 'm', 'n':
				s.newlineStop, s.newlineAnchor = true, true
			case 'p':
				s.newlineStop, s.newlineAnchor = true, false
			case 'w':
				s.newlineStop, s.newlineAnchor = false, true
			case 's':
				s.newlineStop, s.newlineAnchor = false, false
			default:
				return pgRegexCompileError("invalid embedded option")
			}
			s.pos++
		}
		if s.pos == len(s.text) || s.text[s.pos] != ')' {
			return pgRegexCompileError("invalid embedded option")
		}
		s.pos++
	}
	return nil
}
func (s *pgRegexScanner) skip() error {
	if !s.expanded {
		return s.ctx.Err()
	}
	for s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		c := s.text[s.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			s.pos++
			continue
		}
		if c == '#' {
			for s.pos < len(s.text) && s.text[s.pos] != '\n' {
				if err := s.ctx.Err(); err != nil {
					return err
				}
				s.pos++
			}
			continue
		}
		break
	}
	return nil
}
func (s *pgRegexScanner) next() (pgRegexToken, error) {
	for {
		if err := s.skip(); err != nil {
			return pgRegexToken{}, err
		}
		if s.pos == len(s.text) {
			return pgRegexToken{kind: "eof"}, nil
		}
		previous := s.previous
		r, width := utf8.DecodeRuneInString(s.text[s.pos:])
		s.pos += width
		t := pgRegexToken{kind: "plain", code: uint32(r)}
		if s.mode == pgRegexBRE {
			switch r {
			case '*':
				if previous != "empty" && previous != "open" && previous != "caret" {
					t.kind = "quant"
				}
			case '^':
				if previous == "empty" || previous == "open" {
					t.kind = "constraint"
					s.previous = "caret"
					return t, nil
				}
			case '$':
				if err := s.skip(); err != nil {
					return t, err
				}
				if s.pos == len(s.text) || strings.HasPrefix(s.text[s.pos:], `\)`) {
					t.kind = "constraint"
				}
			case '.':
				t.kind = "any"
			case '[':
				t.kind = "bracket"
				if strings.HasPrefix(s.text[s.pos:], "[:<:]]") || strings.HasPrefix(s.text[s.pos:], "[:>:]]") {
					s.pos += 6
					t.code = uint32(s.text[s.pos-4])
					t.kind = "constraint"
				}
			case '\\':
				if s.pos == len(s.text) {
					return t, pgRegexCompileError(`invalid escape \ sequence`)
				}
				c, w := utf8.DecodeRuneInString(s.text[s.pos:])
				s.pos += w
				t.code = uint32(c)
				switch c {
				case '(':
					t.kind = "open"
					t.capture = true
				case ')':
					t.kind = "close"
				case '{':
					t.kind = "bound"
				case '<', '>':
					t.kind = "constraint"
				default:
					if c >= '1' && c <= '9' {
						t.kind = "ref"
						t.code = uint32(c - '0')
					}
				}
			}
		} else {
			switch r {
			case '.':
				t.kind = "any"
			case '|':
				t.kind = "alt"
			case '*', '+', '?':
				t.kind = "quant"
				if s.mode == pgRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
					s.pos++
					t.lazy = true
				}
			case '{':
				if err := s.skip(); err != nil {
					return t, err
				}
				if s.pos < len(s.text) && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
					t.kind = "bound"
				}
			case '(':
				t.kind = "open"
				t.capture = true
				if s.mode == pgRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
					s.pos++
					if s.pos == len(s.text) {
						return t, pgRegexCompileError("quantifier operand invalid")
					}
					c := s.text[s.pos]
					s.pos++
					t.capture = false
					switch c {
					case ':':
					case '=', '!':
						t.look = true
						t.code = uint32(c)
					case '<':
						if s.pos == len(s.text) || (s.text[s.pos] != '=' && s.text[s.pos] != '!') {
							return t, pgRegexCompileError("quantifier operand invalid")
						}
						t.code = uint32(s.text[s.pos]) + 256
						s.pos++
						t.look = true
					case '#':
						for s.pos < len(s.text) && s.text[s.pos] != ')' {
							if err := s.ctx.Err(); err != nil {
								return t, err
							}
							s.pos++
						}
						if s.pos < len(s.text) {
							s.pos++
						}
						continue
					default:
						return t, pgRegexCompileError("quantifier operand invalid")
					}
				}
			case ')':
				t.kind = "close"
			case '^', '$':
				t.kind = "constraint"
			case '[':
				t.kind = "bracket"
				if strings.HasPrefix(s.text[s.pos:], "[:<:]]") || strings.HasPrefix(s.text[s.pos:], "[:>:]]") {
					s.pos += 6
					t.code = uint32(s.text[s.pos-4])
					t.kind = "constraint"
				}
			case '\\':
				var err error
				t, err = s.escape(false)
				if err != nil {
					return t, err
				}
			}
		}
		s.previous = t.kind
		return t, nil
	}
}
func (s *pgRegexScanner) digits(base uint64, min, max int) (uint32, error) {
	var v uint32
	n := 0
	for n < max && s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		d, ok := digit(s.text[s.pos], base)
		if !ok {
			break
		}
		v = v*uint32(base) + uint32(d)
		s.pos++
		n++
	}
	if n < min {
		return 0, pgRegexCompileError(`invalid escape \ sequence`)
	}
	return v, nil
}
func (s *pgRegexScanner) escape(bracket bool) (pgRegexToken, error) {
	t := pgRegexToken{kind: "plain"}
	if s.pos == len(s.text) {
		return t, pgRegexCompileError(`invalid escape \ sequence`)
	}
	c, w := utf8.DecodeRuneInString(s.text[s.pos:])
	s.pos += w
	t.code = uint32(c)
	if s.mode != pgRegexARE {
		return t, nil
	}
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
		return t, nil
	}
	var err error
	switch c {
	case 'a':
		t.code = 7
	case 'b':
		t.code = 8
	case 'B':
		t.code = '\\'
	case 'c':
		if s.pos == len(s.text) {
			return t, pgRegexCompileError(`invalid escape \ sequence`)
		}
		r, width := utf8.DecodeRuneInString(s.text[s.pos:])
		s.pos += width
		t.code = uint32(r) & 31
	case 'e':
		t.code = 27
	case 'f':
		t.code = 12
	case 'n':
		t.code = 10
	case 'r':
		t.code = 13
	case 't':
		t.code = 9
	case 'v':
		t.code = 11
	case 'd', 'D', 's', 'S', 'w', 'W':
		t.kind = "class"
	case 'A', 'Z', 'm', 'M', 'y', 'Y':
		t.kind = "constraint"
	case 'u':
		t.code, err = s.digits(16, 4, 4)
	case 'U':
		t.code, err = s.digits(16, 8, 8)
	case 'x':
		t.code, err = s.digits(16, 1, 255)
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		start := s.pos - w
		if c != '0' {
			s.pos = start
			v, e := s.digits(10, 1, 255)
			if e != nil {
				return t, e
			}
			if s.pos == start+1 || int32(v) > 0 && uint64(v) <= uint64(s.captures) {
				t.kind = "ref"
				t.code = v
				break
			}
		}
		s.pos = start
		t.code, err = s.digits(8, 1, 3)
		if t.code > 255 {
			s.pos--
			t.code >>= 3
		}
	default:
		return t, pgRegexCompileError(`invalid escape \ sequence`)
	}
	if err != nil {
		return t, err
	}
	if (c == 'u' || c == 'U' || c == 'x') && t.code > 0x7ffffffe {
		return t, pgRegexCompileError(`invalid escape \ sequence`)
	}
	if bracket && t.kind != "plain" && t.kind != "class" {
		return t, pgRegexCompileError(`invalid escape \ sequence`)
	}
	return t, nil
}
func (s *pgRegexScanner) bound() error {
	number := func() (int, error) {
		n := 0
		for {
			if err := s.skip(); err != nil {
				return 0, err
			}
			if s.pos == len(s.text) {
				return 0, pgRegexCompileError("braces {} not balanced")
			}
			c := s.text[s.pos]
			if c < '0' || c > '9' {
				break
			}
			// scannum acquires one token after each consumed digit, but
			// stops accumulating at DUPMAX. A pending digit then fails
			// without reading past it; EOF acquisition above still wins
			// after a final consumed digit (for example, 256 versus 2550).
			if n >= 255 {
				return 0, pgRegexCompileError("invalid repetition count(s)")
			}
			n = n*10 + int(c-'0')
			s.pos++
		}
		// BRE enters bound mode even without digits; scannum starts at zero.
		// ARE/ERE admission still requires a digit in next().
		if n > 255 {
			return 0, pgRegexCompileError("invalid repetition count(s)")
		}
		return n, nil
	}
	min, err := number()
	if err != nil {
		return err
	}
	max := min
	fixed := true
	if s.text[s.pos] == ',' {
		fixed = false
		s.pos++
		if err := s.skip(); err != nil {
			return err
		}
		if s.pos == len(s.text) {
			return pgRegexCompileError("braces {} not balanced")
		}
		max = 256
		if s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
			max, err = number()
			if err != nil {
				return err
			}
		}
	}
	if min > max {
		return pgRegexCompileError("invalid repetition count(s)")
	}
	if err := s.skip(); err != nil {
		return err
	}
	if s.pos == len(s.text) {
		return pgRegexCompileError("braces {} not balanced")
	}
	s.boundToken = pgRegexToken{lo: min, hi: max, fixed: fixed}
	if max == 256 {
		s.boundToken.hi = -1
	}
	if s.mode == pgRegexBRE {
		if !strings.HasPrefix(s.text[s.pos:], `\}`) {
			return pgRegexCompileError("invalid repetition count(s)")
		}
		s.pos += 2
	} else {
		if s.text[s.pos] != '}' {
			return pgRegexCompileError("invalid repetition count(s)")
		}
		s.pos++
		if s.mode == pgRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
			s.pos++
			s.boundToken.lazy = true
		}
	}
	s.previous = "quant"
	return nil
}

// bracketPart consumes the current item and obtains exactly one lexical
// lookahead before validating it, as scanplain/brackpart do in REL_18_0.
// A named lookahead remains only an opener: its contents and semantics belong
// to the next parser iteration, not to validation of the current item.
func (s *pgRegexScanner) bracketPart(t pgRegexToken, first *bool) (pgRegexToken, pgRegexToken, error) {
	named := t.kind == "classname" || t.kind == "collating" || t.kind == "equivalence"
	if named {
		delimiter := byte(t.code)
		start := s.pos
		for s.pos < len(s.text) && !(s.text[s.pos] == delimiter && s.pos+1 < len(s.text) && s.text[s.pos+1] == ']') {
			if err := s.ctx.Err(); err != nil {
				return t, pgRegexToken{}, err
			}
			s.pos++
		}
		if s.pos == len(s.text) {
			return t, pgRegexToken{}, pgRegexCompileError("brackets [] not balanced")
		}
		t.name = s.text[start:s.pos]
		s.pos += 2
	}
	lookahead, err := s.bracketToken(first)
	if err != nil {
		return t, lookahead, err
	}
	switch t.kind {
	case "classname":
		switch t.name {
		case "alnum", "alpha", "ascii", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper", "xdigit", "word":
		default:
			return t, lookahead, pgRegexCompileError("invalid character class")
		}
	case "collating", "equivalence":
		code, ok := pgRegexElement(t.name)
		if !ok {
			return t, lookahead, pgRegexCompileError("invalid collating element")
		}
		t.code = code
		// Retain collating origin: unlike a plain atom, even a singleton
		// collating range keeps its original scalar in case-independent mode.
	}
	return t, lookahead, nil
}
func (s *pgRegexScanner) bracketToken(first *bool) (pgRegexToken, error) {
	t := pgRegexToken{kind: "plain"}
	if err := s.ctx.Err(); err != nil {
		return t, err
	}
	if s.pos == len(s.text) {
		return t, pgRegexCompileError("brackets [] not balanced")
	}
	r, width := utf8.DecodeRuneInString(s.text[s.pos:])
	s.pos += width
	t.code = uint32(r)
	wasFirst := *first
	*first = false
	switch r {
	case ']':
		if !wasFirst {
			t.kind = "end"
		}
	case '-':
		if !wasFirst && (s.pos == len(s.text) || s.text[s.pos] != ']') {
			t.kind = "range"
		}
	case '\\':
		if s.mode == pgRegexARE {
			return s.escape(true)
		}
	case '[':
		if s.pos == len(s.text) {
			return t, pgRegexCompileError("brackets [] not balanced")
		}
		c := s.text[s.pos]
		if c != ':' && c != '.' && c != '=' {
			return t, nil
		}
		s.pos++
		t.code = uint32(c)
		switch c {
		case ':':
			t.kind = "classname"
		case '.':
			t.kind = "collating"
		case '=':
			t.kind = "equivalence"
		}
	}
	return t, nil
}

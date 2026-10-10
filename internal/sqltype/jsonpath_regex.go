package sqltype

/*
 * Syntax-only Go adaptation of PostgreSQL REL_18_0 regc_lex.c and regcomp.c.
 * Automata construction and matching are intentionally not included. Lexical
 * modes, incremental parse order and syntax diagnostics are retained.
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
	jsonpathRegexARE uint8 = iota
	jsonpathRegexERE
	jsonpathRegexBRE
	jsonpathRegexQuote
)

type jsonpathRegexToken struct {
	kind, name    string
	code          uint32
	capture, look bool
}
type jsonpathRegexScanner struct {
	ctx      context.Context
	text     string
	pos      int
	mode     uint8
	expanded bool
	previous string
	captures int
}

func jsonpathRegexError(suffix string) error {
	return failure("2201B", "invalid regular expression: "+suffix)
}
func validateJSONPathRegex(ctx context.Context, pattern, flags string) (uint8, error) {
	var bits uint8
	for _, c := range flags {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		i := strings.IndexRune("ismxq", c)
		if i < 0 {
			return 0, jsonpathDetail("42601", "Unrecognized flag character \""+string(c)+"\" in LIKE_REGEX predicate.")
		}
		bits |= 1 << i
	}
	if bits&16 != 0 {
		return bits, ctx.Err()
	}
	if bits&8 != 0 {
		return 0, failure("0A000", `XQuery "x" flag (expanded regular expressions) is not implemented`)
	}
	s := jsonpathRegexScanner{ctx: ctx, text: pattern, previous: "empty"}
	if err := s.prefix(); err != nil {
		return 0, err
	}
	if s.mode == jsonpathRegexQuote {
		return bits, ctx.Err()
	}
	// Each group has its own current atom and inherits lookaround context.
	// Closed capture slots are retained even for zero repetitions.
	type group struct {
		capture                         int
		look, ownLook, atom, quantified bool
	}
	stack := []group{{}}
	closed := []bool{false}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		tok, err := s.next()
		if err != nil {
			return 0, err
		}
		g := &stack[len(stack)-1]
		switch tok.kind {
		case "eof":
			if len(stack) != 1 {
				return 0, jsonpathRegexError("parentheses () not balanced")
			}
			return bits, nil
		case "open":
			cap := 0
			if tok.capture && !g.look {
				s.captures++
				cap = s.captures
				closed = append(closed, false)
			}
			stack = append(stack, group{capture: cap, look: g.look || tok.look, ownLook: tok.look})
		case "close":
			if len(stack) == 1 {
				if s.mode != jsonpathRegexERE {
					return 0, jsonpathRegexError("parentheses () not balanced")
				}
				g.atom = true
				g.quantified = false
				continue
			}
			done := *g
			stack = stack[:len(stack)-1]
			if done.capture != 0 {
				closed[done.capture] = true
			}
			parent := &stack[len(stack)-1]
			parent.atom = !done.ownLook
			parent.quantified = false
		case "alt":
			g.atom = false
			g.quantified = false
		case "constraint":
			g.atom = false
			g.quantified = false
		case "quant", "bound":
			if !g.atom || g.quantified {
				return 0, jsonpathRegexError("quantifier operand invalid")
			}
			if tok.kind == "bound" {
				if err := s.bound(); err != nil {
					return 0, err
				}
			}
			g.quantified = true
		case "ref":
			if g.look || tok.code == 0 || int(tok.code) >= len(closed) || !closed[tok.code] {
				return 0, jsonpathRegexError("invalid backreference number")
			}
			g.atom = true
			g.quantified = false
		case "bracket":
			if err := s.bracket(); err != nil {
				return 0, err
			}
			g.atom = true
			g.quantified = false
		default:
			g.atom = true
			g.quantified = false
		}
	}
}
func regexASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func (s *jsonpathRegexScanner) prefix() error {
	if strings.HasPrefix(s.text, "***") && len(s.text) >= 4 {
		switch s.text[3] {
		case '?':
			return jsonpathRegexError("invalid regexp (reg version 0.8)")
		case '=':
			s.mode = jsonpathRegexQuote
			s.pos = 4
			return nil
		case ':':
			s.pos = 4
		default:
			return jsonpathRegexError("quantifier operand invalid")
		}
	}
	if strings.HasPrefix(s.text[s.pos:], "(?") && len(s.text)-s.pos >= 3 && regexASCIIAlpha(s.text[s.pos+2]) {
		s.pos += 2
		for s.pos < len(s.text) && regexASCIIAlpha(s.text[s.pos]) {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			switch s.text[s.pos] {
			case 'b':
				s.mode = jsonpathRegexBRE
			case 'e':
				s.mode = jsonpathRegexERE
			case 'q':
				s.mode = jsonpathRegexQuote
			case 'x':
				s.expanded = true
			case 't':
				s.expanded = false
			case 'c', 'i', 'm', 'n', 'p', 's', 'w':
			default:
				return jsonpathRegexError("invalid embedded option")
			}
			s.pos++
		}
		if s.pos == len(s.text) || s.text[s.pos] != ')' {
			return jsonpathRegexError("invalid embedded option")
		}
		s.pos++
	}
	return nil
}
func (s *jsonpathRegexScanner) skip() error {
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
func (s *jsonpathRegexScanner) next() (jsonpathRegexToken, error) {
	for {
		if err := s.skip(); err != nil {
			return jsonpathRegexToken{}, err
		}
		if s.pos == len(s.text) {
			return jsonpathRegexToken{kind: "eof"}, nil
		}
		previous := s.previous
		r, width := utf8.DecodeRuneInString(s.text[s.pos:])
		s.pos += width
		t := jsonpathRegexToken{kind: "plain", code: uint32(r)}
		if s.mode == jsonpathRegexBRE {
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
				t.kind = "plain"
			case '[':
				t.kind = "bracket"
				if strings.HasPrefix(s.text[s.pos:], "[:<:]]") || strings.HasPrefix(s.text[s.pos:], "[:>:]]") {
					s.pos += 6
					t.kind = "constraint"
				}
			case '\\':
				if s.pos == len(s.text) {
					return t, jsonpathRegexError(`invalid escape \ sequence`)
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
			case '|':
				t.kind = "alt"
			case '*', '+', '?':
				t.kind = "quant"
				if s.mode == jsonpathRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
					s.pos++
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
				if s.mode == jsonpathRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
					s.pos++
					if s.pos == len(s.text) {
						return t, jsonpathRegexError("quantifier operand invalid")
					}
					c := s.text[s.pos]
					s.pos++
					t.capture = false
					switch c {
					case ':':
					case '=', '!':
						t.look = true
					case '<':
						if s.pos == len(s.text) || (s.text[s.pos] != '=' && s.text[s.pos] != '!') {
							return t, jsonpathRegexError("quantifier operand invalid")
						}
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
						return t, jsonpathRegexError("quantifier operand invalid")
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
func (s *jsonpathRegexScanner) digits(base uint64, min, max int) (uint32, error) {
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
		return 0, jsonpathRegexError(`invalid escape \ sequence`)
	}
	return v, nil
}
func (s *jsonpathRegexScanner) escape(bracket bool) (jsonpathRegexToken, error) {
	t := jsonpathRegexToken{kind: "plain"}
	if s.pos == len(s.text) {
		return t, jsonpathRegexError(`invalid escape \ sequence`)
	}
	c, w := utf8.DecodeRuneInString(s.text[s.pos:])
	s.pos += w
	t.code = uint32(c)
	if s.mode != jsonpathRegexARE {
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
			return t, jsonpathRegexError(`invalid escape \ sequence`)
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
		return t, jsonpathRegexError(`invalid escape \ sequence`)
	}
	if err != nil {
		return t, err
	}
	if (c == 'u' || c == 'U' || c == 'x') && t.code > 0x7ffffffe {
		return t, jsonpathRegexError(`invalid escape \ sequence`)
	}
	if bracket && t.kind != "plain" && t.kind != "class" {
		return t, jsonpathRegexError(`invalid escape \ sequence`)
	}
	return t, nil
}
func (s *jsonpathRegexScanner) bound() error {
	number := func() (int, error) {
		n := 0
		for {
			if err := s.skip(); err != nil {
				return 0, err
			}
			if s.pos == len(s.text) {
				return 0, jsonpathRegexError("braces {} not balanced")
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
				return 0, jsonpathRegexError("invalid repetition count(s)")
			}
			n = n*10 + int(c-'0')
			s.pos++
		}
		// BRE enters bound mode even without digits; scannum starts at zero.
		// ARE/ERE admission still requires a digit in next().
		if n > 255 {
			return 0, jsonpathRegexError("invalid repetition count(s)")
		}
		return n, nil
	}
	min, err := number()
	if err != nil {
		return err
	}
	max := min
	if s.text[s.pos] == ',' {
		s.pos++
		if err := s.skip(); err != nil {
			return err
		}
		if s.pos == len(s.text) {
			return jsonpathRegexError("braces {} not balanced")
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
		return jsonpathRegexError("invalid repetition count(s)")
	}
	if err := s.skip(); err != nil {
		return err
	}
	if s.pos == len(s.text) {
		return jsonpathRegexError("braces {} not balanced")
	}
	if s.mode == jsonpathRegexBRE {
		if !strings.HasPrefix(s.text[s.pos:], `\}`) {
			return jsonpathRegexError("invalid repetition count(s)")
		}
		s.pos += 2
	} else {
		if s.text[s.pos] != '}' {
			return jsonpathRegexError("invalid repetition count(s)")
		}
		s.pos++
		if s.mode == jsonpathRegexARE && s.pos < len(s.text) && s.text[s.pos] == '?' {
			s.pos++
		}
	}
	s.previous = "quant"
	return nil
}
func (s *jsonpathRegexScanner) bracket() error {
	if s.pos < len(s.text) && s.text[s.pos] == '^' {
		s.pos++
	}
	first := true
	next := func() (jsonpathRegexToken, error) { return s.bracketToken(&first) }
	t, err := next()
	if err != nil {
		return err
	}
	for t.kind != "end" {
		if t.kind == "range" {
			return jsonpathRegexError("invalid character range")
		}
		left, lookahead, err := s.bracketPart(t, &first)
		if err != nil {
			return err
		}
		t = lookahead
		if left.kind != "plain" {
			continue
		}
		if t.kind == "range" {
			t, err = next()
			if err != nil {
				return err
			}
			if t.kind != "plain" && t.kind != "range" && t.kind != "collating" {
				return jsonpathRegexError("invalid character range")
			}
			right, lookahead, err := s.bracketPart(t, &first)
			if err != nil {
				return err
			}
			t = lookahead
			if left.code > right.code {
				return jsonpathRegexError("invalid character range")
			}
		}
	}
	return nil
}

// bracketPart consumes the current item and obtains exactly one lexical
// lookahead before validating it, as scanplain/brackpart do in REL_18_0.
// A named lookahead remains only an opener: its contents and semantics belong
// to the next parser iteration, not to validation of the current item.
func (s *jsonpathRegexScanner) bracketPart(t jsonpathRegexToken, first *bool) (jsonpathRegexToken, jsonpathRegexToken, error) {
	named := t.kind == "classname" || t.kind == "collating" || t.kind == "equivalence"
	if named {
		delimiter := byte(t.code)
		start := s.pos
		for s.pos < len(s.text) && !(s.text[s.pos] == delimiter && s.pos+1 < len(s.text) && s.text[s.pos+1] == ']') {
			if err := s.ctx.Err(); err != nil {
				return t, jsonpathRegexToken{}, err
			}
			s.pos++
		}
		if s.pos == len(s.text) {
			return t, jsonpathRegexToken{}, jsonpathRegexError("brackets [] not balanced")
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
			return t, lookahead, jsonpathRegexError("invalid character class")
		}
	case "collating", "equivalence":
		code, ok := jsonpathRegexElement(t.name)
		if !ok {
			return t, lookahead, jsonpathRegexError("invalid collating element")
		}
		t.code = code
		if t.kind == "collating" {
			t.kind = "plain"
		}
	}
	return t, lookahead, nil
}
func (s *jsonpathRegexScanner) bracketToken(first *bool) (jsonpathRegexToken, error) {
	t := jsonpathRegexToken{kind: "plain"}
	if err := s.ctx.Err(); err != nil {
		return t, err
	}
	if s.pos == len(s.text) {
		return t, jsonpathRegexError("brackets [] not balanced")
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
		if s.mode == jsonpathRegexARE {
			return s.escape(true)
		}
	case '[':
		if s.pos == len(s.text) {
			return t, jsonpathRegexError("brackets [] not balanced")
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

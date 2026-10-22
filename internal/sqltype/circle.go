package sqltype

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// circleDatum is the fixed, immutable PostgreSQL circle representation. It does
// not confer a POINT provider or a default ordering/equality capability.
type circleDatum struct{ x, y, radius float64 }

func circleOutput(v circleDatum, env OutputEnvironment) string {
	return "<(" + floatOutputWithEnvironment(v.x, Float8, env) + "," + floatOutputWithEnvironment(v.y, Float8, env) + ")," + floatOutputWithEnvironment(v.radius, Float8, env) + ">"
}

type circleScanner struct {
	text string
	pos  int
}

func (s *circleScanner) whitespace() {
	for s.pos < len(s.text) && space(s.text[s.pos]) {
		s.pos++
	}
}
func (s *circleScanner) take(c byte) bool {
	if s.pos < len(s.text) && s.text[s.pos] == c {
		s.pos++
		return true
	}
	return false
}
func circleSyntax(text string) error {
	return failure("22P02", fmt.Sprintf("invalid input syntax for type circle: \"%s\"", text))
}

// number consumes a strtod-style prefix, including its following ASCII space.
// Conversion precedes the caller's punctuation checks, preserving range-error
// precedence. The ordinary complete-scalar float8 input contract is unchanged.
func (s *circleScanner) number() (float64, error) {
	s.whitespace()
	start := s.pos
	i := start
	if i < len(s.text) && (s.text[i] == '+' || s.text[i] == '-') {
		i++
	}
	word := s.text[i:]
	special := 0
	if len(word) >= 8 && strings.EqualFold(word[:8], "infinity") {
		i += 8
		special = 1
	} else if len(word) >= 3 && strings.EqualFold(word[:3], "inf") {
		i += 3
		special = 1
	} else if len(word) >= 3 && strings.EqualFold(word[:3], "nan") {
		i += 3
		special = 2
		if i < len(s.text) && s.text[i] == '(' {
			end := i + 1
			for end < len(s.text) && (s.text[end] >= 'a' && s.text[end] <= 'z' || s.text[end] >= 'A' && s.text[end] <= 'Z' || s.text[end] >= '0' && s.text[end] <= '9' || s.text[end] == '_') {
				end++
			}
			if end < len(s.text) && s.text[end] == ')' {
				i = end + 1
			}
		}
	}
	if special != 0 {
		n := math.NaN()
		if special == 1 {
			n = math.Inf(1)
		}
		if start < len(s.text) && s.text[start] == '-' {
			n = math.Copysign(n, -1)
		}
		s.pos = i
		s.whitespace()
		return n, nil
	}
	hex := i+2 <= len(s.text) && s.text[i] == '0' && (s.text[i+1] == 'x' || s.text[i+1] == 'X')
	if hex {
		i += 2
	}
	digits, nonzero := 0, false
	scan := func() {
		for i < len(s.text) {
			c := s.text[i]
			valid := c >= '0' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
			if !valid {
				break
			}
			digits++
			nonzero = nonzero || c != '0'
			i++
		}
	}
	scan()
	if i < len(s.text) && s.text[i] == '.' {
		i++
		scan()
	}
	if digits == 0 {
		return 0, circleSyntax(s.text)
	}
	exponent := false
	if i < len(s.text) && (!hex && (s.text[i] == 'e' || s.text[i] == 'E') || hex && (s.text[i] == 'p' || s.text[i] == 'P')) {
		end := i
		i++
		if i < len(s.text) && (s.text[i] == '+' || s.text[i] == '-') {
			i++
		}
		begin := i
		for i < len(s.text) && s.text[i] >= '0' && s.text[i] <= '9' {
			i++
		}
		if i == begin {
			i = end
		} else {
			exponent = true
		}
	}
	token := s.text[start:i]
	parse := token
	if hex && !exponent {
		parse += "p0"
	}
	n, err := strconv.ParseFloat(parse, 64)
	if math.IsInf(n, 0) || n == 0 && nonzero {
		return 0, failure("22003", fmt.Sprintf("\"%s\" is out of range for type double precision", token))
	}
	if err != nil {
		return 0, circleSyntax(s.text)
	}
	s.pos = i
	s.whitespace()
	return n, nil
}

func (d *Descriptor) circleInput(text string) (Value, error) {
	s := circleScanner{text: text}
	s.whitespace()
	outer := s.take('<')
	if !outer && s.pos < len(text) && text[s.pos] == '(' {
		next := s.pos + 1
		for next < len(text) && space(text[next]) {
			next++
		}
		if next < len(text) && text[next] == '(' {
			outer = true
			s.pos = next
		}
	}
	s.whitespace()
	pair := s.take('(')
	x, err := s.number()
	if err != nil {
		return Value{}, err
	}
	if !s.take(',') {
		return Value{}, circleSyntax(text)
	}
	y, err := s.number()
	if err != nil {
		return Value{}, err
	}
	if pair {
		if !s.take(')') {
			return Value{}, circleSyntax(text)
		}
		s.whitespace()
	}
	s.take(',')
	radius, err := s.number()
	if err != nil {
		return Value{}, err
	}
	if radius < 0 {
		return Value{}, circleSyntax(text)
	}
	if outer {
		if !s.take(')') && !s.take('>') {
			return Value{}, circleSyntax(text)
		}
		s.whitespace()
	}
	if s.pos != len(text) {
		return Value{}, circleSyntax(text)
	}
	return Value{typ: d, circle: circleDatum{x, y, radius}}, nil
}

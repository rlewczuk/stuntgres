package sqltype

import (
	"context"
	"strings"
	"stuntgres/internal/sqlstate"
)

// identScanner is shared by the counting and materializing passes. It operates
// on UTF8 bytes, deliberately not Unicode letters or the truncating SQL lexer.
type identScanner struct {
	input                  string
	at                     int
	afterDot, done, strict bool
	work                   binarySearchWork
}

func identStart(c byte) bool {
	return c >= 128 || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
func identContinue(c byte) bool { return identStart(c) || c >= '0' && c <= '9' || c == '$' }

func (s *identScanner) failure(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `string is not a valid identifier: "` + s.input + `"`, Detail: detail}
}
func (s *identScanner) whitespace() error {
	for s.at < len(s.input) && space(s.input[s.at]) {
		if err := s.work.step(); err != nil {
			return err
		}
		s.at++
	}
	return nil
}

// next returns source boundaries and whether materialization must unescape or
// fold. Counting never allocates component strings.
func (s *identScanner) next() (start, end int, quoted, changed, present bool, err error) {
	if s.done {
		return
	}
	if err = s.whitespace(); err != nil {
		return
	}
	start = s.at
	if s.at < len(s.input) && s.input[s.at] == '"' {
		quoted = true
		s.at++
		start = s.at
		for {
			if err = s.work.step(); err != nil {
				return
			}
			if s.at == len(s.input) {
				err = s.failure("String has unclosed double quotes.")
				return
			}
			if s.input[s.at] != '"' {
				s.at++
				continue
			}
			if s.at+1 < len(s.input) && s.input[s.at+1] == '"' {
				changed = true
				s.at += 2
				continue
			}
			end = s.at
			s.at++
			break
		}
		if start == end {
			err = s.failure("Quoted identifier must not be empty.")
			return
		}
	} else if s.at < len(s.input) && identStart(s.input[s.at]) {
		for s.at < len(s.input) && identContinue(s.input[s.at]) {
			if err = s.work.step(); err != nil {
				return
			}
			changed = changed || s.input[s.at] >= 'A' && s.input[s.at] <= 'Z'
			s.at++
		}
		end = s.at
	} else {
		detail := ""
		if s.at < len(s.input) && s.input[s.at] == '.' {
			detail = `No valid identifier before ".".`
		} else if s.afterDot {
			detail = `No valid identifier after ".".`
		}
		err = s.failure(detail)
		return
	}
	if err = s.whitespace(); err != nil {
		return
	}
	if s.at < len(s.input) && s.input[s.at] == '.' {
		s.at++
		s.afterDot = true
	} else {
		if s.at < len(s.input) && s.strict {
			err = s.failure("")
			return
		}
		s.done = true
	}
	present = true
	return
}

func parseIdent(ctx context.Context, result *Descriptor, input string, strict bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	newScanner := func() identScanner {
		return identScanner{input: input, strict: strict, work: binarySearchWork{ctx: ctx, remaining: 4096}}
	}
	s := newScanner()
	count := 0
	for {
		_, _, _, _, present, err := s.next()
		if err != nil {
			return Value{}, err
		}
		if !present {
			break
		}
		if count == maxArrayItems {
			return Value{}, arraySizeFailure()
		}
		count++
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	elements := make([]Value, count)
	s = newScanner()
	for i := range elements {
		start, end, quoted, changed, _, err := s.next()
		if err != nil {
			return Value{}, err
		}
		part := input[start:end]
		if changed {
			var text strings.Builder
			text.Grow(len(part))
			for j := 0; j < len(part); j++ {
				if err := s.work.step(); err != nil {
					return Value{}, err
				}
				c := part[j]
				if quoted && c == '"' {
					j++
				} else if !quoted && c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				text.WriteByte(c)
			}
			part = text.String()
		}
		elements[i] = String(result.Element(), part)
	}
	return OwnedArrayValue(result, []ArrayDimension{{Lower: 1, Length: int32(count)}}, elements)
}

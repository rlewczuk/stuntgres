package parser

import (
	"strings"
	"stuntgres/internal/sqlstate"
)

// ParseRegprocedureName parses decoded type input, not a SQL call. Each complete
// argument is resolved before scanning the next, matching type-error precedence
// and avoiding an intermediate argument vector. Segments retain their own source.
func ParseRegprocedureName(input string, argument func(string) error) (ObjectName, error) {
	failure := func(code, message string) (ObjectName, error) {
		return ObjectName{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	}
	quoted, left := false, -1
	for i := 0; i < len(input); i++ {
		if input[i] == '"' {
			if quoted && i+1 < len(input) && input[i+1] == '"' {
				i++
				continue
			}
			quoted = !quoted
		} else if input[i] == '(' && !quoted {
			left = i
			break
		}
	}
	if left < 0 {
		return failure("22P02", "expected a left parenthesis")
	}
	name, err := ParseRegclassName(input[:left])
	if err != nil {
		return failure("42602", "invalid name syntax")
	}
	end := len(input)
	for end > left && space(input[end-1]) {
		end--
	}
	if end <= left+1 || input[end-1] != ')' {
		return failure("22P02", "expected a right parenthesis")
	}
	body := input[left+1 : end-1]
	if strings.Trim(body, " \t\n\r\f\v") == "" {
		return name, nil
	}
	start, parens, brackets, count := 0, 0, 0, 0
	quoted = false
	for i := 0; i <= len(body); i++ {
		if i == len(body) || body[i] == ',' && !quoted && parens == 0 && brackets == 0 {
			if quoted || parens != 0 || brackets != 0 {
				return failure("22P02", "improper type name")
			}
			segment := strings.Trim(body[start:i], " \t\n\r\f\v")
			if segment == "" {
				return failure("22P02", "expected a type name")
			}
			if count == 100 {
				return failure("54023", "too many arguments")
			}
			if err := argument(segment); err != nil {
				return ObjectName{}, err
			}
			count++
			start = i + 1
			continue
		}
		if body[i] == '"' {
			if quoted && i+1 < len(body) && body[i+1] == '"' {
				i++
				continue
			}
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch body[i] {
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		}
		if parens < 0 || brackets < 0 {
			return failure("22P02", "improper type name")
		}
	}
	return name, nil
}

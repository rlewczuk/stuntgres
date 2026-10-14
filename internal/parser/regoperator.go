package parser

import (
	"strings"
	"stuntgres/internal/sqlstate"
)

// ParseRegOperatorSignature validates the name before the closing delimiter,
// then delivers each operand before scanning the next. Callbacks own name and
// type resolution; their errors stop parsing without prevalidating later text.
func ParseRegOperatorSignature(text string, name func(string) error, operand func(string) error) error {
	fail := func(message string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message}
	}
	quoted, opener := false, -1
	for i := range len(text) {
		if text[i] == '"' {
			quoted = !quoted
		} else if !quoted && text[i] == '(' {
			opener = i
			break
		}
	}
	if opener < 0 {
		return fail("expected a left parenthesis")
	}
	if err := name(text[:opener]); err != nil {
		return err
	}
	end := len(strings.TrimRight(text, " \t\r\n\f\v")) - 1
	if end <= opener || text[end] != ')' {
		return fail("expected a right parenthesis")
	}
	body := text[opener+1 : end]
	hadComma := false
	for {
		body = strings.TrimLeft(body, " \t\r\n\f\v")
		if body == "" {
			if hadComma {
				return fail("expected a type name")
			}
			return nil
		}
		quoted, depth := false, 0
		i := 0
		for ; i < len(body); i++ {
			c := body[i]
			if c == '"' {
				quoted = !quoted
			} else if c == ',' && !quoted && depth == 0 {
				break
			} else if !quoted {
				switch c {
				case '(', '[':
					depth++
				case ')', ']':
					depth--
				}
			}
		}
		if quoted || depth != 0 {
			return fail("improper type name")
		}
		if err := operand(strings.TrimRight(body[:i], " \t\r\n\f\v")); err != nil {
			return err
		}
		hadComma = i < len(body)
		if hadComma {
			body = body[i+1:]
		} else {
			body = ""
		}
	}
}

package sepgsql

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type specRule struct {
	class, context string
	pattern        *regexp.Regexp
}

// compilePattern implements fnmatch(..., 0): wildcards span both dots and slashes.
// Regex is private, anchored and compiled once at registration, never per object.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("\\A(?:")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch c {
		case '*':
			b.WriteString("(?s:.*)")
		case '?':
			b.WriteString("(?s:.)")
		case '\\':
			i++
			if i == len(runes) {
				return nil, fmt.Errorf("trailing pattern escape")
			}
			b.WriteString(regexp.QuoteMeta(string(runes[i])))
		case '[':
			b.WriteByte('[')
			i++
			if i < len(runes) && (runes[i] == '!' || runes[i] == '^') {
				b.WriteByte('^')
				i++
			}
			start := i
			if i < len(runes) && runes[i] == ']' {
				b.WriteString("\\]")
				i++
			}
			for ; i < len(runes) && runes[i] != ']'; i++ {
				if runes[i] == '\\' {
					i++
					if i == len(runes) {
						return nil, fmt.Errorf("trailing bracket escape")
					}
					if runes[i] == '-' {
						// QuoteMeta leaves hyphens unescaped outside character classes.
						b.WriteString("\\-")
					} else {
						b.WriteString(regexp.QuoteMeta(string(runes[i])))
					}
				} else if runes[i] == '[' {
					b.WriteString("\\[")
				} else {
					b.WriteRune(runes[i])
				}
			}
			if i == len(runes) || i == start {
				return nil, fmt.Errorf("unterminated or empty pattern bracket")
			}
			b.WriteByte(']')
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(")\\z")
	return regexp.Compile(b.String())
}
func (r *Runtime) compileSpec(text string) ([]specRule, error) {
	var rules []specRule
	for lineNumber, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// libselabel's db backend reads three whitespace-delimited fields; a
		// backslash belongs to fnmatch, not shell quoting. Quoted SQL keys retain '"'.
		fields := strings.FieldsFunc(line, unicode.IsSpace)
		if len(fields) < 3 || len(fields) > 3 && !strings.HasPrefix(fields[3], "#") {
			return nil, fmt.Errorf("invalid spec line %d", lineNumber+1)
		}
		class := fields[0]
		if definition(class) == nil || class == "process" || !r.Valid(fields[2]) {
			return nil, fmt.Errorf("invalid spec class/context on line %d", lineNumber+1)
		}
		pattern, err := compilePattern(fields[1])
		if err != nil {
			return nil, fmt.Errorf("invalid spec pattern on line %d: %w", lineNumber+1, err)
		}
		rules = append(rules, specRule{class: class, context: fields[2], pattern: pattern})
	}
	return rules, nil
}
func (r *Runtime) HasSpec(name *string) bool {
	if name == nil {
		return true
	}
	_, ok := r.specs[*name]
	return ok
}

// InitialLabel returns the first matching same-class rule, with no implicit
// fallback. An absent match and an explicit unlabeled association differ.
func (r *Runtime) InitialLabel(name *string, class, key string) (string, bool) {
	spec := r.defaultSpec
	if name != nil {
		var ok bool
		spec, ok = r.specs[*name]
		if !ok {
			return "", false
		}
	}
	for _, rule := range spec {
		if rule.class == class && rule.pattern.MatchString(key) {
			return rule.context, true
		}
	}
	return "", false
}

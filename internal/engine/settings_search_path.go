package engine

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// parseSearchPath preserves raw setting text separately from decoded identifiers.
func parseSearchPath(input string) ([]string, error) {
	invalid := func() ([]string, error) {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "search_path": "%s"`, input), Detail: "List syntax is invalid."}
	}
	s := strings.Trim(input, " \t\r\n\f\v")
	var path []string
	for s != "" {
		var name string
		if s[0] == '"' {
			var b strings.Builder
			i := 1
			closed := false
			for i < len(s) {
				if s[i] == '"' {
					if i+1 < len(s) && s[i+1] == '"' {
						b.WriteByte('"')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				b.WriteByte(s[i])
				i++
			}
			if !closed {
				return invalid()
			}
			name = b.String()
			s = strings.Trim(s[i:], " \t\r\n\f\v")
		} else {
			i := 0
			for i < len(s) && s[i] != ',' && !strings.ContainsRune(" \t\r\n\f\v", rune(s[i])) {
				i++
			}
			if i == 0 {
				return invalid()
			}
			name = settingFold(s[:i])
			s = strings.Trim(s[i:], " \t\r\n\f\v")
		}
		if len(name) > 63 {
			n := 63
			for !utf8.RuneStart(name[n]) {
				n--
			}
			name = name[:n]
		}
		path = append(path, name)
		if s == "" {
			break
		}
		if s[0] != ',' {
			return invalid()
		}
		s = strings.Trim(s[1:], " \t\r\n\f\v")
		if s == "" {
			return invalid()
		}
	}
	return path, nil
}

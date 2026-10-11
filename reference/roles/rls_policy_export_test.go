package reference_test

import (
	"regexp"
	"strings"
	"unicode"
)

// These assertions concern the public SQL export, not implementation source.
// Basic independent field expectations supplement actual dependency probes;
// richer frozen expressions are replayed by Expressions and Controls.
func policyExportDefinition(s *membershipScenario, dump, name string) string {
	s.t.Helper()
	pattern := regexp.MustCompile(`(?is)CREATE\s+POLICY\s+"?` + regexp.QuoteMeta(name) + `"?\s+ON\s+[^;]+;`)
	definition := pattern.FindString(dump)
	if definition == "" {
		s.t.Fatal("named policy missing from export", name, dump)
	}
	return definition
}

// Read the ordered TO identifier vector, including duplicate targets, rather
// than finding a role name anywhere in the emitted statement.
func policyExportRoles(s *membershipScenario, dump, name string) []string {
	s.t.Helper()
	definition := policyExportDefinition(s, dump, name)
	clause := regexp.MustCompile(`(?is)\bTO\s+(.*?)(?:\s+USING\s*\(|\s+WITH\s+CHECK\s*\(|;\s*$)`).FindStringSubmatch(definition)
	if clause == nil {
		s.t.Fatal("explicit TO clause missing from export", definition)
	}
	identifier := regexp.MustCompile(`^\s*("(?:""|[^"])*"|[A-Za-z_][A-Za-z_0-9$]*)\s*(,|$)`)
	remaining := clause[1]
	var roles []string
	for {
		match := identifier.FindStringSubmatch(remaining)
		if match == nil {
			s.t.Fatal("invalid exported TO identifier vector", definition)
		}
		role := match[1]
		if strings.HasPrefix(role, `"`) {
			role = strings.ReplaceAll(role[1:len(role)-1], `""`, `"`)
		} else {
			role = strings.ToLower(role)
		}
		roles = append(roles, role)
		remaining = remaining[len(match[0]):]
		if match[2] == "" {
			return roles
		}
	}
}

func policyExportFields(s *membershipScenario, dump, name, role, using, check string) {
	s.t.Helper()
	definition := policyExportDefinition(s, dump, name)
	upper := strings.ToUpper(definition)
	usingAt, checkAt := strings.Index(upper, "USING"), strings.Index(upper, "WITH CHECK")
	compact := func(text string) string {
		return strings.ToLower(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == '(' || r == ')' || r == '"' {
				return -1
			}
			return r
		}, text))
	}
	if using == "" && usingAt >= 0 {
		s.t.Fatal("absent USING became stored expression", definition)
	}
	if check == "" && checkAt >= 0 {
		s.t.Fatal("absent CHECK became stored expression", definition)
	}
	if using != "" {
		if usingAt < 0 {
			s.t.Fatal("retained USING omitted", definition)
		}
		end := len(definition)
		if checkAt >= 0 {
			end = checkAt
		}
		if !strings.Contains(compact(definition[usingAt:end]), compact(using)) {
			s.t.Fatal("USING replacement/retention lost", using, definition)
		}
	}
	if check != "" {
		if checkAt < 0 || !strings.Contains(compact(definition[checkAt:]), compact(check)) {
			s.t.Fatal("CHECK replacement/retention lost", check, definition)
		}
	}
	if role != "public" && !strings.Contains(definition, role) {
		s.t.Fatal("retained target role omitted", role, definition)
	}
}

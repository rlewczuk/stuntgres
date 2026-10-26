package authorization

import (
	"fmt"
	"strings"
)

// ParseRelationPrivileges validates the complete comma list before returning
// ordinary and grant-option alternatives. Internal whitespace is significant.
func ParseRelationPrivileges(text string, allowed Privileges) (ordinary, options Privileges, err error) {
	for {
		token, rest, more := strings.Cut(text, ",")
		for len(token) > 0 && rolePrivilegeSpace(token[0]) {
			token = token[1:]
		}
		for len(token) > 0 && rolePrivilegeSpace(token[len(token)-1]) {
			token = token[:len(token)-1]
		}
		keyword := token
		option := false
		const suffix = " WITH GRANT OPTION"
		if len(keyword) >= len(suffix) && rolePrivilegeEqual(keyword[len(keyword)-len(suffix):], suffix) {
			keyword = keyword[:len(keyword)-len(suffix)]
			option = true
		}
		var bit Privileges
		for _, item := range [...]struct {
			name string
			bit  Privileges
		}{
			{"SELECT", SelectPrivilege}, {"INSERT", InsertPrivilege}, {"UPDATE", UpdatePrivilege},
			{"DELETE", DeletePrivilege}, {"TRUNCATE", TruncatePrivilege}, {"REFERENCES", ReferencesPrivilege},
			{"TRIGGER", TriggerPrivilege}, {"MAINTAIN", MaintainPrivilege}, {"USAGE", UsagePrivilege},
		} {
			if rolePrivilegeEqual(keyword, item.name) {
				bit = item.bit
				break
			}
		}
		if bit == 0 || bit&allowed == 0 {
			return 0, 0, sqlError("22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, token))
		}
		if option {
			options |= bit
		} else {
			ordinary |= bit
		}
		if !more {
			return ordinary, options, nil
		}
		text = rest
	}
}

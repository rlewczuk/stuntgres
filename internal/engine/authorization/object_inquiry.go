package authorization

import (
	"fmt"
	"strings"
)

// ParseObjectPrivileges validates the entire list before returning its two ANY
// alternatives. The inquiry grammar deliberately does not normalize whitespace.
func ParseObjectPrivileges(text, privilege string) (ordinary, options bool, err error) {
	for {
		token, rest, more := strings.Cut(text, ",")
		for len(token) > 0 && rolePrivilegeSpace(token[0]) { token = token[1:] }
		for len(token) > 0 && rolePrivilegeSpace(token[len(token)-1]) { token = token[:len(token)-1] }
		switch {
		case rolePrivilegeEqual(token, privilege):
			ordinary = true
		case len(token) == len(privilege)+len(" WITH GRANT OPTION") && rolePrivilegeEqual(token[:len(privilege)], privilege) && rolePrivilegeEqual(token[len(privilege):], " WITH GRANT OPTION"):
			options = true
		default:
			return false, false, sqlError("22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, token))
		}
		if !more { return ordinary, options, nil }
		text = rest
	}
}

package authorization

import (
	"fmt"
	"strings"
)

// ParseObjectPrivileges validates every list item and returns independent ANY
// masks for ordinary rights and grant options. Internal whitespace is exact.
func ParseObjectPrivileges(text string, allowed Privileges) (ordinary, options Privileges, err error) {
	for {
		token, rest, more := strings.Cut(text, ",")
		for len(token) > 0 && rolePrivilegeSpace(token[0]) {
			token = token[1:]
		}
		for len(token) > 0 && rolePrivilegeSpace(token[len(token)-1]) {
			token = token[:len(token)-1]
		}
		name := token
		grant := false
		const suffix = " WITH GRANT OPTION"
		if len(name) >= len(suffix) && rolePrivilegeEqual(name[len(name)-len(suffix):], suffix) {
			name = name[:len(name)-len(suffix)]
			grant = true
		}
		var mask Privileges
		switch {
		case rolePrivilegeEqual(name, "CREATE"):
			mask = CreatePrivilege
		case rolePrivilegeEqual(name, "CONNECT"):
			mask = ConnectPrivilege
		case rolePrivilegeEqual(name, "TEMPORARY"), rolePrivilegeEqual(name, "TEMP"):
			mask = TemporaryPrivilege
		case rolePrivilegeEqual(name, "USAGE"):
			mask = UsagePrivilege
		case rolePrivilegeEqual(name, "EXECUTE"):
			mask = ExecutePrivilege
		case rolePrivilegeEqual(name, "SELECT"):
			mask = SelectPrivilege
		case rolePrivilegeEqual(name, "UPDATE"):
			mask = UpdatePrivilege
		case rolePrivilegeEqual(name, "SET"):
			mask = SetPrivilege
		case rolePrivilegeEqual(name, "ALTER SYSTEM"):
			mask = AlterSystemPrivilege
		}
		if mask == 0 || mask&allowed == 0 {
			return 0, 0, sqlError("22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, token))
		}
		if grant {
			options |= mask
		} else {
			ordinary |= mask
		}
		if !more {
			return ordinary, options, nil
		}
		text = rest
	}
}

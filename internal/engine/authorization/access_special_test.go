package authorization

import (
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAccessSpecialPrivilegeTokens(t *testing.T) {
	for _, tc := range []struct {
		text                       string
		allowed, ordinary, options Privileges
	}{
		{" select ,UPDATE WITH GRANT OPTION", SelectPrivilege | UpdatePrivilege, SelectPrivilege, UpdatePrivilege},
		{"SET WITH GRANT OPTION,alter system", SetPrivilege | AlterSystemPrivilege, AlterSystemPrivilege, SetPrivilege},
		{"UPDATE WITH GRANT OPTION", SelectPrivilege | UpdatePrivilege, 0, UpdatePrivilege},
	} {
		a, b, err := ParseObjectPrivileges(tc.text, tc.allowed)
		if err != nil || a != tc.ordinary || b != tc.options {
			t.Fatal(tc.text, a, b, err)
		}
	}
	for _, tc := range []struct {
		text, bad string
		allowed   Privileges
	}{
		{"SELECT,", "", SelectPrivilege | UpdatePrivilege}, {"SET,bad", "bad", SetPrivilege | AlterSystemPrivilege},
		{"SET", "SET", SelectPrivilege | UpdatePrivilege}, {"UPDATE", "UPDATE", SetPrivilege | AlterSystemPrivilege},
		{"ALL", "ALL", SetPrivilege | AlterSystemPrivilege}, {"SET\tWITH GRANT OPTION", "SET\tWITH GRANT OPTION", SetPrivilege | AlterSystemPrivilege},
	} {
		_, _, err := ParseObjectPrivileges(tc.text, tc.allowed)
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "22023" || d.Message != `unrecognized privilege type: "`+tc.bad+`"` {
			t.Fatal(tc.text, err)
		}
	}
}

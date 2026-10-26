package authorization

import (
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAccessRelationsPrivilegeRequests(t *testing.T) {
	for _, tc := range []struct {
		text                       string
		allowed, ordinary, options Privileges
	}{
		{" select , UPDATE WITH GRANT OPTION ", TableGrantPrivileges, SelectPrivilege, UpdatePrivilege},
		{"MAINTAIN WITH GRANT OPTION,DELETE", TableGrantPrivileges, DeletePrivilege, MaintainPrivilege},
		{"REFERENCES,INSERT WITH GRANT OPTION", ColumnGrantPrivileges, ReferencesPrivilege, InsertPrivilege},
		{"USAGE,SELECT WITH GRANT OPTION", UsagePrivilege | SelectPrivilege | UpdatePrivilege, UsagePrivilege, SelectPrivilege},
	} {
		a, b, err := ParseRelationPrivileges(tc.text, tc.allowed)
		if err != nil || a != tc.ordinary || b != tc.options {
			t.Fatalf("%q: %v %v %v", tc.text, a, b, err)
		}
	}
	for _, tc := range []struct {
		text    string
		allowed Privileges
		token   string
	}{
		{"SELECT,bad", TableGrantPrivileges, "bad"}, {"SELECT,", TableGrantPrivileges, ""},
		{"ALL", TableGrantPrivileges, "ALL"}, {"", TableGrantPrivileges, ""},
		{"SELECT  WITH GRANT OPTION", TableGrantPrivileges, "SELECT  WITH GRANT OPTION"},
		{"SELECT\tWITH GRANT OPTION", TableGrantPrivileges, "SELECT\tWITH GRANT OPTION"},
		{"DELETE", ColumnGrantPrivileges, "DELETE"}, {"USAGE", TableGrantPrivileges, "USAGE"},
	} {
		_, _, err := ParseRelationPrivileges(tc.text, tc.allowed)
		e, ok := err.(*sqlstate.Error)
		if !ok || e.Code != "22023" || e.Message != `unrecognized privilege type: "`+tc.token+`"` {
			t.Fatalf("%q: %v", tc.text, err)
		}
	}
}

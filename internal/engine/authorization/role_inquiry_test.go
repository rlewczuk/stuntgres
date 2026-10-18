package authorization

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRolePrivilegesParse(t *testing.T) {
	for _, tc := range []struct {
		text string
		want RolePrivileges
	}{
		{"member", RoleMember}, {"usage,set", RoleUsage | RoleSet}, {"MEMBER,MEMBER", RoleMember},
		{"\t\n\v\f\r SeT \r", RoleSet}, {"USAGE WITH ADMIN OPTION", RoleAdmin},
		{"SET WITH GRANT OPTION,MEMBER", RoleAdmin | RoleMember},
	} {
		got, err := ParseRolePrivileges(tc.text)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %v %v want %v", tc.text, got, err, tc.want)
		}
	}
	for _, text := range []string{"", "MEMBER,", "MEMBER,,SET", "MEMBER,bAd", "ADMIN", "MEMBER  WITH ADMIN OPTION", "MEMBER\tWITH ADMIN OPTION", "\u00a0MEMBER", "ſET"} {
		mask, err := ParseRolePrivileges(text)
		var d *sqlstate.Error
		if mask != 0 || !errors.As(err, &d) || d.Code != "22023" || d.Position != "" {
			t.Fatalf("invalid token accepted: %q %v %v", text, mask, err)
		}
	}
}

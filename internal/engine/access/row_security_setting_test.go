package access

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestRowSecuritySettingGuard(t *testing.T) {
	f := newAccessFixture(t)
	root := f.record.Root
	for i, name := range []string{"owner", "reader", "member", "bypass", "bypass_member"} {
		var err error
		root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: f.owner.Principals.Principal(uint64(100 + i)), Name: name, Inherit: true, BypassRLS: name == "bypass"})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{"owner", "member"}, {"bypass", "bypass_member"}} {
		var err error
		root, _, _, err = authorization.MembershipCandidate(context.Background(), root.Read(f.bootstrap), authorization.RoleState{CurrentUser: f.bootstrap, SessionUser: f.bootstrap}, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: pair[0]}}, Members: []parser.RoleSpec{{Name: pair[1]}}}, nil, true, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	roles := root.Read(f.bootstrap)
	for _, forced := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for _, setting := range []bool{false, true} {
				for _, actor := range []string{"postgres", "owner", "reader", "member", "bypass", "bypass_member"} {
					d := &catalog.Relation{ObjectName: "t", Owner: roles.LookupRef("owner"), Security: catalog.RelationSecurity{RLSEnabled: enabled, RLSForced: forced}}
					owner := actor == "owner" || actor == "member"
					denied := !setting && enabled && actor != "postgres" && actor != "bypass" && (!owner || forced)
					err := RowSecuritySettingGuard(setting, roles, d, roles.LookupRef(actor))
					if !denied {
						if err != nil {
							t.Fatal(actor, setting, enabled, forced, err)
						}
						continue
					}
					want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `query would be affected by row-level security policy for table "t"`}
					if owner {
						want.Hint = "To disable the policy for the table's owner, use ALTER TABLE NO FORCE ROW LEVEL SECURITY."
					}
					if !reflect.DeepEqual(err, want) {
						t.Fatalf("actor=%s setting=%v enabled=%v forced=%v: %#v want %#v", actor, setting, enabled, forced, err, want)
					}
				}
			}
		}
	}
}

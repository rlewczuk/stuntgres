package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func grantUnitRoles() (PrincipalRef, PrincipalRef, PrincipalRef, PrincipalRef) {
	e := New()
	owner := e.roles.names["postgres"]
	a, b, reader := PrincipalRef{owner: e, id: 100001}, PrincipalRef{owner: e, id: 100002}, PrincipalRef{owner: e, id: 100003}
	for _, role := range []PrincipalRef{a, b, reader} {
		e.roles.roles[role] = RoleDescriptor{Ref: role, Inherit: true}
	}
	return owner, a, b, reader
}
func grantUnitEntry(source, target PrincipalRef, use, options Privileges) ACLEntry {
	return ACLEntry{Grantor: source, Grantee: ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}

func TestObjectGrantMasksAndDefaults(t *testing.T) {
	if tableGrantPrivileges != Privileges(1|2|16|32|64|128|256|512) || columnGrantPrivileges != Privileges(1|2|32|256) {
		t.Fatal("changed privilege encodings", tableGrantPrivileges, columnGrantPrivileges)
	}
	owner, a, _, _ := grantUnitRoles()
	absent := materializeRelationACL(ACLValue{}, owner)
	if !absent.Explicit || len(absent.Entries) != 1 || absent.Entries[0].GrantOptions != 0 || absent.Entries[0].Privileges != tableGrantPrivileges {
		t.Fatal(absent)
	}
	if aclRights(ACLValue{Explicit: true}, owner, []PrincipalRef{owner}, tableGrantPrivileges, false) != 0 {
		t.Fatal("explicit empty owner use")
	}
	source, options, sourceErr := objectGrantSource(context.Background(), owner, ACLValue{Explicit: true}, ACLValue{}, []PrincipalRef{a, owner}, false, tableGrantPrivileges)
	if sourceErr != nil || source != owner || options != tableGrantPrivileges {
		t.Fatal("inherited implicit options", source, options, sourceErr)
	}
	for _, item := range []parser.GrantPrivilege{{Name: "delete", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "maintain", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "trigger", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "truncate", Columns: []parser.Identifier{{Text: "a"}}}} {
		_, err := objectPrivilege(item)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" {
			t.Fatal(item, err)
		}
	}
	mask, err := objectPrivilege(parser.GrantPrivilege{All: true, Columns: []parser.Identifier{{Text: "a"}}})
	if err != nil || mask != columnGrantPrivileges {
		t.Fatal(mask, err)
	}
}

func TestObjectGrantSingleSourceAndImmutableOrigins(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	old := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(owner, b, UpdatePrivilege, UpdatePrivilege)}}
	source, available, sourceErr := objectGrantSource(context.Background(), owner, old, ACLValue{}, []PrincipalRef{reader, a, b}, false, SelectPrivilege|UpdatePrivilege)
	if sourceErr != nil || source != a || available != SelectPrivilege {
		t.Fatal("invented option union", source, available, sourceErr)
	}
	source, available, sourceErr = objectGrantSource(context.Background(), owner, old, ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, UpdatePrivilege, UpdatePrivilege)}}, []PrincipalRef{reader, a, b}, false, SelectPrivilege|UpdatePrivilege)
	if sourceErr != nil || source != a || available != SelectPrivilege|UpdatePrivilege {
		t.Fatal("same source table/column options", source, available, sourceErr)
	}
	before := slices.Clone(old.Entries)
	next, changed, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, old, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false)
	if err != nil || !changed || !slices.Equal(old.Entries, before) || next.Entries[2].Grantor != a {
		t.Fatal(next, changed, err, old)
	}
	next, _, err = changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, next, owner, b, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err = changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, next, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, true)
	if err != nil || aclRights(next, owner, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("independent origin removed", next, err)
	}
}

func TestObjectGrantPerACLCircularityAndRestrict(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	chain := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(a, b, SelectPrivilege, SelectPrivilege), grantUnitEntry(b, reader, SelectPrivilege, 0)}}
	_, changed, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, chain, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}
	if changed || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal(changed, err)
	}
	_, _, err = changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, chain, owner, b, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" {
		t.Fatal("circular backgrant", err)
	}
	alternate := ACLValue{Explicit: true, Entries: append(slices.Clone(chain.Entries), grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege))}
	if _, _, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, alternate, owner, b, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false); err != nil {
		t.Fatal("same ACL independent support", err)
	}
	// The table option is only a source-selection input. It cannot support a
	// column ACL's circularity check, nor impose a cross-ACL RESTRICT graph.
	table := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege)}}
	column := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(a, reader, SelectPrivilege, 0)}}
	if _, _, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, table, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true); err != nil {
		t.Fatal("invented cross-ACL dependency", err)
	}
	if aclRights(column, PrincipalRef{}, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("downstream column use lost")
	}
	if _, _, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, column, owner, a, []ACLGrantee{{Role: b}}, SelectPrivilege, true, false); err == nil {
		t.Fatal("table options used for column circularity")
	}
	alternate = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(b, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(a, reader, SelectPrivilege, 0)}}
	if _, _, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, alternate, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true); err != nil {
		t.Fatal("alternate origin support ignored", err)
	}
}

func TestObjectGrantACLNoopAndCancellation(t *testing.T) {
	owner, a, _, reader := grantUnitRoles()
	old := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege)}}
	next, changed, err := changeObjectACL(context.Background(), RoleRead{owner.owner, owner.owner.roles}, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false)
	if err != nil || changed || !slices.Equal(next.Entries, old.Entries) {
		t.Fatal(next, changed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	next, changed, err = changeObjectACL(ctx, RoleRead{owner.owner, owner.owner.roles}, old, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false)
	if !errors.Is(err, context.Canceled) || changed || !slices.Equal(next.Entries, old.Entries) {
		t.Fatal(next, changed, err)
	}
}

func TestObjectGrantInheritedAlternateSupport(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := RoleRead{owner.owner, owner.owner.roles}
	roles.state.edges = []Membership{{Role: b, Member: a, Grantor: owner, Inherit: true}}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(a, reader, SelectPrivilege, 0),
	}}
	next, changed, err := changeObjectACL(context.Background(), roles, acl, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true)
	if err != nil || !changed || aclRights(next, owner, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("inherited same-ACL alternate support lost", next, changed, err)
	}
	roles.state.edges[0].Inherit = false
	if _, _, err := changeObjectACL(context.Background(), roles, acl, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true); err == nil {
		t.Fatal("disabled inheritance supported dependent grants")
	}
}

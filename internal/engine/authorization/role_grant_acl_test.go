package authorization

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
	d := &Domain{}
	return d.Principal(10), d.Principal(100001), d.Principal(100002), d.Principal(100003)
}
func grantUnitRead(owner PrincipalRef) Read {
	root := Seed(owner.owner)
	for _, id := range []uint64{100001, 100002, 100003} {
		ref := owner.owner.Principal(id)
		root.roles[ref] = RoleDescriptor{Ref: ref, Inherit: true}
	}
	return root.Read(owner)
}
func grantUnitEntry(source, target PrincipalRef, use, options Privileges) ACLEntry {
	return ACLEntry{Grantor: source, Grantee: ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}

func TestObjectGrantMasksAndDefaults(t *testing.T) {
	if TableGrantPrivileges != Privileges(1|2|16|32|64|128|256|512) || ColumnGrantPrivileges != Privileges(1|2|32|256) {
		t.Fatal("changed privilege encodings", TableGrantPrivileges, ColumnGrantPrivileges)
	}
	owner, a, _, _ := grantUnitRoles()
	absent := MaterializeRelationACL(ACLValue{}, owner)
	if !absent.Explicit || len(absent.Entries) != 1 || absent.Entries[0].GrantOptions != 0 || absent.Entries[0].Privileges != TableGrantPrivileges {
		t.Fatal(absent)
	}
	if ACLRights(ACLValue{Explicit: true}, owner, []PrincipalRef{owner}, TableGrantPrivileges, false) != 0 {
		t.Fatal("explicit empty owner use")
	}
	source, options, sourceErr := ObjectGrantSource(context.Background(), owner, ACLValue{Explicit: true}, ACLValue{}, []PrincipalRef{a, owner}, false, TableGrantPrivileges)
	if sourceErr != nil || source != owner || options != TableGrantPrivileges {
		t.Fatal("inherited implicit options", source, options, sourceErr)
	}
	for _, item := range []parser.GrantPrivilege{{Name: "delete", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "maintain", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "trigger", Columns: []parser.Identifier{{Text: "a"}}}, {Name: "truncate", Columns: []parser.Identifier{{Text: "a"}}}} {
		_, err := ObjectPrivilege(item)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" {
			t.Fatal(item, err)
		}
	}
	mask, err := ObjectPrivilege(parser.GrantPrivilege{All: true, Columns: []parser.Identifier{{Text: "a"}}})
	if err != nil || mask != ColumnGrantPrivileges {
		t.Fatal(mask, err)
	}
}

func TestObjectGrantSingleSourceAndImmutableOrigins(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	old := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(owner, b, UpdatePrivilege, UpdatePrivilege)}}
	source, available, sourceErr := ObjectGrantSource(context.Background(), owner, old, ACLValue{}, []PrincipalRef{reader, a, b}, false, SelectPrivilege|UpdatePrivilege)
	if sourceErr != nil || source != a || available != SelectPrivilege {
		t.Fatal("invented option union", source, available, sourceErr)
	}
	source, available, sourceErr = ObjectGrantSource(context.Background(), owner, old, ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, UpdatePrivilege, UpdatePrivilege)}}, []PrincipalRef{reader, a, b}, false, SelectPrivilege|UpdatePrivilege)
	if sourceErr != nil || source != a || available != SelectPrivilege|UpdatePrivilege {
		t.Fatal("same source table/column options", source, available, sourceErr)
	}
	before := slices.Clone(old.Entries)
	next, changed, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), old, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false, false, false)
	if err != nil || !changed || !slices.Equal(old.Entries, before) || next.Entries[2].Grantor != a {
		t.Fatal(next, changed, err, old)
	}
	next, _, err = ChangeObjectACL(context.Background(), grantUnitRead(owner), next, owner, b, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err = ChangeObjectACL(context.Background(), grantUnitRead(owner), next, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, true, false, false)
	if err != nil || ACLRights(next, owner, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("independent origin removed", next, err)
	}
}

func TestObjectGrantPerACLCircularityAndRestrict(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	chain := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(a, b, SelectPrivilege, SelectPrivilege), grantUnitEntry(b, reader, SelectPrivilege, 0)}}
	_, changed, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), chain, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, false)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}
	if changed || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal(changed, err)
	}
	_, _, err = ChangeObjectACL(context.Background(), grantUnitRead(owner), chain, owner, b, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false, false, false)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0LP01" {
		t.Fatal("circular backgrant", err)
	}
	alternate := ACLValue{Explicit: true, Entries: append(slices.Clone(chain.Entries), grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege))}
	if _, _, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), alternate, owner, b, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false, false, false); err != nil {
		t.Fatal("same ACL independent support", err)
	}
	// The table option is only a source-selection input. It cannot support a
	// column ACL's circularity check, nor impose a cross-ACL RESTRICT graph.
	table := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege)}}
	column := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(a, reader, SelectPrivilege, 0)}}
	if _, _, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), table, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, false); err != nil {
		t.Fatal("invented cross-ACL dependency", err)
	}
	if ACLRights(column, PrincipalRef{}, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("downstream column use lost")
	}
	if _, _, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), column, owner, a, []ACLGrantee{{Role: b}}, SelectPrivilege, true, false, false, false); err == nil {
		t.Fatal("table options used for column circularity")
	}
	alternate = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(b, a, SelectPrivilege, SelectPrivilege), grantUnitEntry(a, reader, SelectPrivilege, 0)}}
	if _, _, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), alternate, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, false); err != nil {
		t.Fatal("alternate origin support ignored", err)
	}
}

func TestObjectGrantACLNoopAndCancellation(t *testing.T) {
	owner, a, _, reader := grantUnitRoles()
	old := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege)}}
	next, changed, err := ChangeObjectACL(context.Background(), grantUnitRead(owner), old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, true, false, false, false)
	if err != nil || changed || !slices.Equal(next.Entries, old.Entries) {
		t.Fatal(next, changed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	next, changed, err = ChangeObjectACL(ctx, grantUnitRead(owner), old, owner, a, []ACLGrantee{{Role: reader}}, SelectPrivilege, false, false, false, false)
	if !errors.Is(err, context.Canceled) || changed || !slices.Equal(next.Entries, old.Entries) {
		t.Fatal(next, changed, err)
	}
}

func TestObjectGrantInheritedAlternateSupport(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	roles.state.edges = []Membership{{Role: b, Member: a, Grantor: owner, Inherit: true}}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(a, reader, SelectPrivilege, 0),
	}}
	next, changed, err := ChangeObjectACL(context.Background(), roles, acl, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, false)
	if err != nil || !changed || ACLRights(next, owner, []PrincipalRef{reader}, SelectPrivilege, false) != SelectPrivilege {
		t.Fatal("inherited same-ACL alternate support lost", next, changed, err)
	}
	roles.state.edges[0].Inherit = false
	if _, _, err := ChangeObjectACL(context.Background(), roles, acl, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, false); err == nil {
		t.Fatal("disabled inheritance supported dependent grants")
	}
}

func TestObjectGrantSourceNearestFullMatch(t *testing.T) {
	owner, current, nearer, _ := grantUnitRoles()
	const requested = SelectPrivilege | UpdatePrivilege
	full := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, current, requested, requested)}}
	partial := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, current, SelectPrivilege, SelectPrivilege)}}
	for _, tc := range []struct {
		name          string
		table, column ACLValue
		principals    []PrincipalRef
		super         bool
		want          PrincipalRef
	}{
		{name: "current explicit full precedes inherited owner", table: full, principals: []PrincipalRef{current, owner}, want: current},
		{name: "current table and column options precede owner", table: partial, column: ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, current, UpdatePrivilege, UpdatePrivilege)}}, principals: []PrincipalRef{current, owner}, want: current},
		{name: "partial current loses to inherited full owner", table: partial, principals: []PrincipalRef{current, owner}, want: owner},
		{name: "nearer full source precedes inherited owner", table: ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, nearer, requested, requested)}}, principals: []PrincipalRef{current, nearer, owner}, want: nearer},
		{name: "current owner remains fast path", table: full, principals: []PrincipalRef{owner, current}, want: owner},
		{name: "actual superuser records owner origin", table: full, principals: []PrincipalRef{current, owner}, super: true, want: owner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retained := append([]PrincipalRef(nil), tc.principals...)
			source, options, err := ObjectGrantSource(context.Background(), owner, tc.table, tc.column, tc.principals, tc.super, requested)
			if err != nil || source != tc.want || options != requested || !slices.Equal(tc.principals, retained) {
				t.Fatal("canonical first full match", source, options, err, "want", tc.want, requested)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err = ObjectGrantSource(ctx, owner, tc.table, tc.column, tc.principals, tc.super, requested); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled grantor selection", err)
			}
		})
	}
}

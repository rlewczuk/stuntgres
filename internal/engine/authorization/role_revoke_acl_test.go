package authorization

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestObjectRevokeOptionOnly(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	old := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, SelectPrivilege|UpdatePrivilege, SelectPrivilege|UpdatePrivilege),
		grantUnitEntry(b, a, SelectPrivilege, 0),
	}}
	before := slices.Clone(old.Entries)
	next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, false)
	want := []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege|UpdatePrivilege, UpdatePrivilege), before[1]}
	if err != nil || !changed || !next.Explicit || !slices.Equal(next.Entries, want) || !slices.Equal(old.Entries, before) {
		t.Fatal("option-only changed use/origin/snapshot", next, changed, err, old)
	}
	quiet, changed, err := ChangeObjectACL(context.Background(), roles, next, owner, owner, []ACLGrantee{{Role: a}, {Role: reader}}, SelectPrivilege, false, true, true, true)
	if err != nil || changed || !slices.Equal(quiet.Entries, want) {
		t.Fatal("repeated or missing attributed options changed", quiet, changed, err)
	}
	plain, changed, err := ChangeObjectACL(context.Background(), roles, next, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, false, true)
	want[0] = grantUnitEntry(owner, a, UpdatePrivilege, UpdatePrivilege)
	if err != nil || !changed || !slices.Equal(plain.Entries, want) || ACLRights(plain, owner, []PrincipalRef{a}, TableGrantPrivileges, false) != SelectPrivilege|UpdatePrivilege {
		t.Fatal("plain removal lost independent origin", plain, changed, err)
	}
}

func TestObjectRevokeNoopDefaults(t *testing.T) {
	owner, a, _, _ := grantUnitRoles()
	roles := grantUnitRead(owner)
	public := ACLGrantee{Public: true}
	old := MaterializeRelationACL(ACLValue{}, owner)
	old.Entries = append(old.Entries, ACLEntry{Grantor: owner, Grantee: public, Privileges: SelectPrivilege})
	for _, grantee := range []ACLGrantee{public, {Role: owner}, {Role: a}} {
		next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{grantee}, SelectPrivilege, false, true, true, true)
		if err != nil || changed || !reflect.DeepEqual(next, old) {
			t.Fatal("effect-free option-only", grantee, next, changed, err)
		}
	}
	// Even explicitly stored owner options cannot support a cascade away from
	// the owner's implicit options. PUBLIC use is independent of delegation.
	old.Entries[0].GrantOptions = SelectPrivilege
	old.Entries = append(old.Entries, grantUnitEntry(owner, a, SelectPrivilege, 0))
	next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: owner}}, SelectPrivilege, false, true, true, true)
	if err != nil || !changed || next.Entries[0].Privileges != TableGrantPrivileges || next.Entries[0].GrantOptions != 0 || next.Entries[2].Privileges != SelectPrivilege {
		t.Fatal("owner implicit options lost", next, changed, err)
	}
	options, err := ScopedACLGrantOptions(context.Background(), roles, next, owner, owner, TableGrantPrivileges)
	if err != nil || options != TableGrantPrivileges || next.Entries[1].GrantOptions != 0 {
		t.Fatal("implicit owner/PUBLIC options", options, err, next)
	}
}

func TestObjectRevokeCascade(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	old := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(a, b, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(b, reader, SelectPrivilege, 0),
	}}
	before := slices.Clone(old.Entries)
	for _, optionOnly := range []bool{false, true} {
		next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, optionOnly, false)
		var diagnostic *sqlstate.Error
		wantError := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}
		if changed || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, wantError) || !reflect.DeepEqual(next, old) {
			t.Fatal("RESTRICT did not return original ACL", next, changed, err)
		}
		next, changed, err = ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, optionOnly, true)
		var want []ACLEntry
		if optionOnly {
			want = []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, 0)}
		}
		if err != nil || !changed || !slices.Equal(next.Entries, want) || !slices.Equal(old.Entries, before) {
			t.Fatal("recursive removal/pruning/snapshot", next, changed, err, old)
		}
	}
}

func TestObjectRevokeAlternateSupport(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	for _, options := range []Privileges{0, SelectPrivilege} {
		old := ACLValue{Explicit: true, Entries: []ACLEntry{
			grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
			grantUnitEntry(b, a, SelectPrivilege, options),
			grantUnitEntry(a, reader, SelectPrivilege, 0),
			grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege),
		}}
		next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
		want := []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, 0), old.Entries[1]}
		if options != 0 {
			want = append(want, old.Entries[2])
		}
		want = append(want, old.Entries[3])
		if err != nil || !changed || !slices.Equal(next.Entries, want) {
			t.Fatal("alternate ordinary use confused with options", next, changed, err)
		}
	}
	for _, inherit := range []bool{false, true} {
		roles.state.edges = []Membership{{Role: b, Member: a, Grantor: owner, Inherit: inherit}}
		old := ACLValue{Explicit: true, Entries: []ACLEntry{
			grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
			grantUnitEntry(owner, b, SelectPrivilege, SelectPrivilege),
			grantUnitEntry(a, reader, SelectPrivilege, 0),
		}}
		next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
		want := []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, 0), old.Entries[1]}
		if inherit {
			want = append(want, old.Entries[2])
		}
		if err != nil || !changed || !slices.Equal(next.Entries, want) {
			t.Fatal("INHERIT support", inherit, next, changed, err)
		}
	}
}

func TestObjectRevokeSelectiveBits(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	both := SelectPrivilege | UpdatePrivilege
	old := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, both, both),
		grantUnitEntry(a, b, both, both),
		grantUnitEntry(b, reader, both, 0),
		grantUnitEntry(owner, reader, SelectPrivilege, 0),
		{Grantor: a, Grantee: ACLGrantee{Public: true}, Privileges: both},
	}}
	before := slices.Clone(old.Entries)
	next, changed, err := ChangeObjectACL(context.Background(), roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
	want := []ACLEntry{
		grantUnitEntry(owner, a, both, UpdatePrivilege),
		grantUnitEntry(a, b, UpdatePrivilege, UpdatePrivilege),
		grantUnitEntry(b, reader, UpdatePrivilege, 0),
		before[3],
		{Grantor: a, Grantee: ACLGrantee{Public: true}, Privileges: UpdatePrivilege},
	}
	if err != nil || !changed || !slices.Equal(next.Entries, want) || !slices.Equal(old.Entries, before) {
		t.Fatal("selective bits/origins/PUBLIC/snapshot", next, changed, err)
	}
	for _, entry := range next.Entries {
		if entry.GrantOptions&^entry.Privileges != 0 || entry.Grantee.Public && entry.GrantOptions != 0 {
			t.Fatal("invalid ACL invariant", entry)
		}
	}
}

func TestObjectRevokeCancellation(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	old := ACLValue{Explicit: true, Entries: []ACLEntry{
		grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(a, b, SelectPrivilege, SelectPrivilege),
		grantUnitEntry(b, reader, SelectPrivilege, 0),
	}}
	before := slices.Clone(old.Entries)
	// Reuse a real cancelable-context fixture to visit every cooperative
	// checkpoint, including recursion after the private upstream mutation.
	ctx, cancel := context.WithCancel(context.Background())
	counter := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 1 << 30}
	_, changed, err := ChangeObjectACL(counter, roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
	cancel()
	if err != nil || !changed || counter.checks.Load() < 3 {
		t.Fatal("cascade calibration", changed, err, counter.checks.Load())
	}
	for at := int32(1); at <= counter.checks.Load(); at++ {
		ctx, cancel := context.WithCancel(context.Background())
		checkpoint := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		next, changed, err := ChangeObjectACL(checkpoint, roles, old, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
		cancel()
		if !errors.Is(err, context.Canceled) || changed || !reflect.DeepEqual(next, old) || !slices.Equal(old.Entries, before) {
			t.Fatal("canceled cascade published/mutated", at, next, changed, err, old)
		}
	}
}

func TestObjectRevokeOriginalTableSource(t *testing.T) {
	owner, a, b, reader := grantUnitRoles()
	roles := grantUnitRead(owner)
	table := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, SelectPrivilege, SelectPrivilege)}}
	column := ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(a, reader, SelectPrivilege, 0)}}
	next, changed, err := ChangeObjectACL(context.Background(), roles, table, owner, owner, []ACLGrantee{{Role: a}}, SelectPrivilege, false, true, true, true)
	if err != nil || !changed || next.Entries[0].GrantOptions != 0 {
		t.Fatal(next, changed, err)
	}
	source, available, err := ObjectGrantSource(context.Background(), owner, table, column, []PrincipalRef{a}, false, SelectPrivilege)
	if err != nil || source != a || available != SelectPrivilege || column.Entries[0].Privileges != SelectPrivilege {
		t.Fatal("original table selection or cross-ACL dependency", source, available, err, column)
	}
	_, available, err = ObjectGrantSource(context.Background(), owner, next, column, []PrincipalRef{a}, false, SelectPrivilege)
	if err != nil || available != 0 {
		t.Fatal("private table option loss not isolated", available, err)
	}
	// Independently chosen column grantor can beat the table grantor, while
	// the table's requested mask is not a union of inherited option sources.
	table = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, a, InsertPrivilege|UpdatePrivilege, InsertPrivilege|UpdatePrivilege)}}
	column = ACLValue{Explicit: true, Entries: []ACLEntry{grantUnitEntry(owner, b, ColumnGrantPrivileges, ColumnGrantPrivileges)}}
	principals := []PrincipalRef{reader, a, b}
	requested := SelectPrivilege | InsertPrivilege | UpdatePrivilege
	source, available, err = ObjectGrantSource(context.Background(), owner, table, ACLValue{}, principals, false, requested)
	if err != nil || source != a || available != InsertPrivilege|UpdatePrivilege {
		t.Fatal("table source", source, available, err)
	}
	source, available, err = ObjectGrantSource(context.Background(), owner, table, column, principals, false, requested)
	if err != nil || source != b || available != requested {
		t.Fatal("independent column source", source, available, err)
	}
}

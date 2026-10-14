package authorization

import (
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

type defaultUnitFixture struct{ owner, reader PrincipalRef }

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	owner, _, _, reader := grantUnitRoles()
	return defaultUnitFixture{owner, reader}
}
func defaultUnitReaderEntry(f defaultUnitFixture, privileges, options Privileges) ACLEntry {
	return ACLEntry{Grantor: f.owner, Grantee: ACLGrantee{Role: f.reader}, Privileges: privileges, GrantOptions: options}
}
func defaultUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" {
		t.Fatalf("diagnostic = %#v; want %s %q", err, code, message)
	}
}
func TestDefaultPrivilegesMasks(t *testing.T) {
	classes := []struct {
		name          string
		class         DefaultACLClass
		mask          Privileges
		invalid, kind string
	}{
		{"tables", DefaultTables, InsertPrivilege | SelectPrivilege | UpdatePrivilege | DeletePrivilege | TruncatePrivilege | ReferencesPrivilege | TriggerPrivilege | MaintainPrivilege, "usage", "relation"},
		{"sequences", DefaultSequences, SelectPrivilege | UpdatePrivilege | UsagePrivilege, "insert", "sequence"},
		{"functions", DefaultRoutines, ExecutePrivilege, "select", "function"},
		{"routines", DefaultRoutines, ExecutePrivilege, "usage", "function"},
		{"types", DefaultTypes, UsagePrivilege, "execute", "type"},
		{"schemas", DefaultSchemas, UsagePrivilege | CreatePrivilege, "select", "schema"},
		{"large objects", DefaultLargeObjects, SelectPrivilege | UpdatePrivilege, "usage", "large object"},
	}
	for _, tc := range classes {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultClass(tc.name); got != tc.class {
				t.Fatal("class", got)
			}
			if got := DefaultMask(tc.class); got != tc.mask {
				t.Fatal("mask", got, tc.mask)
			}
			mask, err := DefaultPrivilege(parser.GrantPrivilege{All: true}, tc.class)
			if err != nil || mask != tc.mask {
				t.Fatal("ALL", mask, err)
			}
			_, err = DefaultPrivilege(parser.GrantPrivilege{Name: tc.invalid}, tc.class)
			name := map[string]string{"usage": "USAGE", "insert": "INSERT", "select": "SELECT", "execute": "EXECUTE"}[tc.invalid]
			defaultUnitDiagnostic(t, err, "0LP01", "invalid privilege type "+name+" for "+tc.kind)
		})
	}
	_, err := DefaultPrivilege(parser.GrantPrivilege{Name: "bogus"}, DefaultTables)
	defaultUnitDiagnostic(t, err, "42601", `unrecognized privilege type "bogus"`)
	_, err = DefaultPrivilege(parser.GrantPrivilege{All: true, Columns: []parser.Identifier{{Text: "x"}}}, DefaultTables)
	defaultUnitDiagnostic(t, err, "0LP01", "default privileges cannot be set for columns")
	if DefaultClass("table") != 0 || DefaultMask(0) != 0 {
		t.Fatal("invalid class accepted")
	}
}

func TestDefaultPrivilegesBaselines(t *testing.T) {
	f := newDefaultUnitFixture(t)
	for _, class := range []DefaultACLClass{DefaultTables, DefaultSequences, DefaultRoutines, DefaultTypes, DefaultSchemas, DefaultLargeObjects} {
		baseline := DefaultBaseline(f.owner, class, false)
		if !baseline.Explicit || !MatchesDefaultBaseline(baseline, f.owner, class, false) {
			t.Fatal("baseline", class, baseline)
		}
		expectedCount := 1
		if class == DefaultTypes || class == DefaultRoutines {
			expectedCount = 2
		}
		if len(baseline.Entries) != expectedCount {
			t.Fatal("baseline origins", class, baseline)
		}
		for _, entry := range baseline.Entries {
			if entry.Grantor != f.owner || entry.GrantOptions != 0 || entry.Privileges != DefaultMask(class) || !entry.Grantee.Public && entry.Grantee.Role != f.owner {
				t.Fatal("baseline origin", entry)
			}
		}
		scoped := DefaultBaseline(f.owner, class, true)
		if !scoped.Explicit || len(scoped.Entries) != 0 {
			t.Fatal("scoped baseline", scoped)
		}
	}
}
func TestDefaultPrivilegesMerge(t *testing.T) {
	f := newDefaultUnitFixture(t)
	ownerEntry := ACLEntry{Grantor: f.owner, Grantee: ACLGrantee{Role: f.owner}, Privileges: InsertPrivilege}
	readerSelect := defaultUnitReaderEntry(f, SelectPrivilege, 0)
	readerInsert := defaultUnitReaderEntry(f, InsertPrivilege, InsertPrivilege)
	cases := []struct {
		name           string
		global, scoped *ACLValue
		want           ACLValue
	}{
		{"absent", nil, nil, ACLValue{}},
		{"empty release fallback", &ACLValue{Explicit: true}, nil, ACLValue{}},
		{"both empty", &ACLValue{Explicit: true}, &ACLValue{Explicit: true}, ACLValue{}},
		{"empty global nonempty scope", &ACLValue{Explicit: true}, &ACLValue{Explicit: true, Entries: []ACLEntry{readerSelect}}, ACLValue{Explicit: true, Entries: []ACLEntry{readerSelect}}},
		{"partial global retained", &ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry}}, nil, ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry}}},
		{"additive same origin", &ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry, readerSelect}}, &ACLValue{Explicit: true, Entries: []ACLEntry{readerInsert}}, ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry, defaultUnitReaderEntry(f, SelectPrivilege|InsertPrivilege, InsertPrivilege)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeGlobal, beforeScoped := ACLValue{}, ACLValue{}
			if tc.global != nil {
				beforeGlobal = ACLValue{Explicit: tc.global.Explicit, Entries: append([]ACLEntry(nil), tc.global.Entries...)}
			}
			if tc.scoped != nil {
				beforeScoped = ACLValue{Explicit: tc.scoped.Explicit, Entries: append([]ACLEntry(nil), tc.scoped.Entries...)}
			}
			got := CreationDefaultACL(f.owner, DefaultTables, tc.global, tc.scoped)
			if !EqualACL(got, tc.want) {
				t.Fatal("merge", got, tc.want)
			}
			if tc.global != nil && !reflect.DeepEqual(beforeGlobal, *tc.global) || tc.scoped != nil && !reflect.DeepEqual(beforeScoped, *tc.scoped) {
				t.Fatal("merge mutated source")
			}
		})
	}
	explicitEmpty := ACLValue{Explicit: true}
	if rights := ACLRights(explicitEmpty, f.owner, []PrincipalRef{f.owner}, SelectPrivilege, false); rights != 0 {
		t.Fatal("object empty ACL fell back", rights)
	}
	if rights := ACLRights(ACLValue{}, f.owner, []PrincipalRef{f.owner}, SelectPrivilege, false); rights != SelectPrivilege {
		t.Fatal("absent owner baseline", rights)
	}
	for _, class := range []DefaultACLClass{DefaultSequences, DefaultTypes, DefaultSchemas} {
		empty := ACLValue{Explicit: true}
		got := CreationDefaultACL(f.owner, class, &empty, nil)
		if class == DefaultTypes {
			if !MatchesDefaultBaseline(got, f.owner, class, false) {
				t.Fatal("empty type normalization", got)
			}
		} else if got.Explicit {
			t.Fatal("empty release normalization", class, got)
		}
	}
}

func TestDefaultSchemaCreationACLOrder(t *testing.T) {
	f := newDefaultUnitFixture(t)
	owner := ACLEntry{Grantor: f.owner, Grantee: ACLGrantee{Role: f.owner}, Privileges: UsagePrivilege | CreatePrivilege}
	public := ACLEntry{Grantor: f.owner, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege}
	reader := defaultUnitReaderEntry(f, CreatePrivilege, CreatePrivilege)
	global := ACLValue{Explicit: true, Entries: []ACLEntry{reader, owner, public}}
	before := append([]ACLEntry(nil), global.Entries...)
	got := CreationDefaultACL(f.owner, DefaultSchemas, &global, nil)
	roleEntries := []ACLEntry{owner, reader}
	if f.reader.ID() < f.owner.ID() {
		roleEntries[0], roleEntries[1] = reader, owner
	}
	want := ACLValue{Explicit: true, Entries: append([]ACLEntry{public}, roleEntries...)}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(global.Entries, before) {
		t.Fatal("schema order/options/refs/input immutability", got, want, global)
	}
	got.Entries[0].Privileges = CreatePrivilege
	if !reflect.DeepEqual(global.Entries, before) {
		t.Fatal("sorted result aliases immutable input")
	}
	for _, class := range []DefaultACLClass{DefaultTables, DefaultSequences, DefaultRoutines, DefaultTypes, DefaultLargeObjects} {
		mask := DefaultMask(class)
		record := ACLValue{Explicit: true, Entries: []ACLEntry{
			{Grantor: f.owner, Grantee: ACLGrantee{Role: f.reader}, Privileges: mask, GrantOptions: mask},
			{Grantor: f.owner, Grantee: ACLGrantee{Role: f.owner}, Privileges: mask},
			{Grantor: f.owner, Grantee: ACLGrantee{Public: true}, Privileges: mask},
		}}
		other := CreationDefaultACL(f.owner, class, &record, nil)
		if !reflect.DeepEqual(other, record) {
			t.Fatal("schema normalization changed other class", class, other)
		}
	}
	for _, record := range []*ACLValue{nil, {Explicit: true}, func() *ACLValue { v := DefaultBaseline(f.owner, DefaultSchemas, false); return &v }()} {
		if value := CreationDefaultACL(f.owner, DefaultSchemas, record, nil); value.Explicit || len(value.Entries) != 0 {
			t.Fatal("schema NULL/baseline normalization", value)
		}
	}
}

package engine

import (
	"reflect"
	"stuntgres/internal/parser"
	"testing"
)

func TestDefaultPrivilegesMasks(t *testing.T) {
	classes := []struct {
		name          string
		class         defaultACLClass
		mask          Privileges
		invalid, kind string
	}{
		{"tables", defaultTables, InsertPrivilege | SelectPrivilege | UpdatePrivilege | DeletePrivilege | TruncatePrivilege | ReferencesPrivilege | TriggerPrivilege | MaintainPrivilege, "usage", "relation"},
		{"sequences", defaultSequences, SelectPrivilege | UpdatePrivilege | UsagePrivilege, "insert", "sequence"},
		{"functions", defaultRoutines, ExecutePrivilege, "select", "function"},
		{"routines", defaultRoutines, ExecutePrivilege, "usage", "function"},
		{"types", defaultTypes, UsagePrivilege, "execute", "type"},
		{"schemas", defaultSchemas, UsagePrivilege | CreatePrivilege, "select", "schema"},
		{"large objects", defaultLargeObjects, SelectPrivilege | UpdatePrivilege, "usage", "large object"},
	}
	for _, tc := range classes {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultClass(tc.name); got != tc.class {
				t.Fatal("class", got)
			}
			if got := defaultMask(tc.class); got != tc.mask {
				t.Fatal("mask", got, tc.mask)
			}
			mask, err := defaultPrivilege(parser.GrantPrivilege{All: true}, tc.class)
			if err != nil || mask != tc.mask {
				t.Fatal("ALL", mask, err)
			}
			_, err = defaultPrivilege(parser.GrantPrivilege{Name: tc.invalid}, tc.class)
			name := map[string]string{"usage": "USAGE", "insert": "INSERT", "select": "SELECT", "execute": "EXECUTE"}[tc.invalid]
			defaultUnitDiagnostic(t, err, "0LP01", "invalid privilege type "+name+" for "+tc.kind)
		})
	}
	_, err := defaultPrivilege(parser.GrantPrivilege{Name: "bogus"}, defaultTables)
	defaultUnitDiagnostic(t, err, "42601", `unrecognized privilege type "bogus"`)
	_, err = defaultPrivilege(parser.GrantPrivilege{All: true, Columns: []parser.Identifier{{Text: "x"}}}, defaultTables)
	defaultUnitDiagnostic(t, err, "0LP01", "default privileges cannot be set for columns")
	if defaultClass("table") != 0 || defaultMask(0) != 0 {
		t.Fatal("invalid class accepted")
	}
}

func TestDefaultPrivilegesBaselines(t *testing.T) {
	f := newDefaultUnitFixture(t)
	for _, class := range []defaultACLClass{defaultTables, defaultSequences, defaultRoutines, defaultTypes, defaultSchemas, defaultLargeObjects} {
		baseline := defaultBaseline(f.owner, class, false)
		if !baseline.Explicit || !matchesDefaultBaseline(baseline, f.owner, class, false) {
			t.Fatal("baseline", class, baseline)
		}
		expectedCount := 1
		if class == defaultTypes || class == defaultRoutines {
			expectedCount = 2
		}
		if len(baseline.Entries) != expectedCount {
			t.Fatal("baseline origins", class, baseline)
		}
		for _, entry := range baseline.Entries {
			if entry.Grantor != f.owner || entry.GrantOptions != 0 || entry.Privileges != defaultMask(class) || !entry.Grantee.Public && entry.Grantee.Role != f.owner {
				t.Fatal("baseline origin", entry)
			}
		}
		scoped := defaultBaseline(f.owner, class, true)
		if !scoped.Explicit || len(scoped.Entries) != 0 {
			t.Fatal("scoped baseline", scoped)
		}
	}
	f.grant(t, "types", "usage", nil)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	table := defaultUnitTable(t, f, stage, "dependent", f.owner)
	row := stage.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(table.rowType)}].(*typeDescriptor)
	if !matchesDefaultBaseline(row.acl, f.owner, defaultTypes, false) || descriptorRoleDependencies(row, f.reader, nil) {
		t.Fatal("dependent rowtype consumed TYPES defaults")
	}
	array := stage.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}].(*typeDescriptor)
	if !matchesDefaultBaseline(array.acl, f.owner, defaultTypes, false) || descriptorRoleDependencies(array, f.reader, nil) {
		t.Fatal("dependent array consumed TYPES defaults")
	}
	// Only the independent base consumes TYPES: its generated array must not
	// acquire a second origin/dependency on the selected named recipient.
	composite := *table
	composite.address.ObjectID = 20020
	composite.name = "standalone"
	composite.rowType = 0
	composite.relationKind = compositeRelation
	f.e.mu.Lock()
	err := f.e.stageRowTypeLocked(stage, &composite)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	row = stage.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(composite.rowType)}].(*typeDescriptor)
	array = stage.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}].(*typeDescriptor)
	if !descriptorRoleDependencies(row, f.reader, nil) || descriptorRoleDependencies(array, f.reader, nil) || !matchesDefaultBaseline(array.acl, f.owner, defaultTypes, false) {
		t.Fatal("standalone/associated ACL distinction")
	}
}

func TestDefaultPrivilegesMerge(t *testing.T) {
	f := newDefaultUnitFixture(t)
	globalKey := defaultACLKey{creator: f.owner, class: defaultTables}
	scopedKey := defaultACLKey{creator: f.owner, schema: f.schema, class: defaultTables}
	ownerEntry := ACLEntry{Grantor: f.owner, Grantee: ACLGrantee{Role: f.owner}, Privileges: InsertPrivilege}
	readerSelect := defaultUnitReaderEntry(f, SelectPrivilege, 0)
	readerInsert := defaultUnitReaderEntry(f, InsertPrivilege, InsertPrivilege)
	cases := []struct {
		name           string
		global, scoped *defaultACLRecord
		want           ACLValue
	}{
		{"absent", nil, nil, ACLValue{}},
		{"empty release fallback", &defaultACLRecord{acl: ACLValue{Explicit: true}}, nil, ACLValue{}},
		{"both empty", &defaultACLRecord{acl: ACLValue{Explicit: true}}, &defaultACLRecord{acl: ACLValue{Explicit: true}}, ACLValue{}},
		{"empty global nonempty scope", &defaultACLRecord{acl: ACLValue{Explicit: true}}, &defaultACLRecord{acl: ACLValue{Explicit: true, Entries: []ACLEntry{readerSelect}}}, ACLValue{Explicit: true, Entries: []ACLEntry{readerSelect}}},
		{"partial global retained", &defaultACLRecord{acl: ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry}}}, nil, ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry}}},
		{"additive same origin", &defaultACLRecord{acl: ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry, readerSelect}}}, &defaultACLRecord{acl: ACLValue{Explicit: true, Entries: []ACLEntry{readerInsert}}}, ACLValue{Explicit: true, Entries: []ACLEntry{ownerEntry, defaultUnitReaderEntry(f, SelectPrivilege|InsertPrivilege, InsertPrivilege)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := cloneCatalog(f.e.state)
			state.defaultACLs = map[defaultACLKey]*defaultACLRecord{}
			if tc.global != nil {
				state.defaultACLs[globalKey] = tc.global
			}
			if tc.scoped != nil {
				state.defaultACLs[scopedKey] = tc.scoped
			}
			beforeGlobal, beforeScoped := ACLValue{}, ACLValue{}
			if tc.global != nil {
				beforeGlobal = ACLValue{Explicit: tc.global.acl.Explicit, Entries: append([]ACLEntry(nil), tc.global.acl.Entries...)}
			}
			if tc.scoped != nil {
				beforeScoped = ACLValue{Explicit: tc.scoped.acl.Explicit, Entries: append([]ACLEntry(nil), tc.scoped.acl.Entries...)}
			}
			got := creationDefaultACL(CatalogRead{f.e, state}, f.owner, f.schema, defaultTables)
			if !equalACL(got, tc.want) {
				t.Fatal("merge", got, tc.want)
			}
			if tc.global != nil && !reflect.DeepEqual(beforeGlobal, tc.global.acl) || tc.scoped != nil && !reflect.DeepEqual(beforeScoped, tc.scoped.acl) {
				t.Fatal("merge mutated source")
			}
		})
	}
	explicitEmpty := ACLValue{Explicit: true}
	if rights := aclRights(explicitEmpty, f.owner, []PrincipalRef{f.owner}, SelectPrivilege, false); rights != 0 {
		t.Fatal("object empty ACL fell back", rights)
	}
	if rights := aclRights(ACLValue{}, f.owner, []PrincipalRef{f.owner}, SelectPrivilege, false); rights != SelectPrivilege {
		t.Fatal("absent owner baseline", rights)
	}
	for _, class := range []defaultACLClass{defaultSequences, defaultTypes, defaultSchemas} {
		state := cloneCatalog(f.e.state)
		state.defaultACLs = map[defaultACLKey]*defaultACLRecord{{creator: f.owner, class: class}: {acl: ACLValue{Explicit: true}}}
		got := creationDefaultACL(CatalogRead{f.e, state}, f.owner, ObjectAddress{}, class)
		if class == defaultTypes {
			if !matchesDefaultBaseline(got, f.owner, class, false) {
				t.Fatal("empty type normalization", got)
			}
		} else if got.Explicit {
			t.Fatal("empty release normalization", class, got)
		}
	}
}

func TestDefaultPrivilegesStorage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := f.e.CatalogRead()
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		f.grant(t, class, "all", nil)
	}
	if len(f.e.state.defaultACLs) != 6 || len(old.state.defaultACLs) != 0 {
		t.Fatal("six stored classes/source immutability")
	}
	first := f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: defaultRoutines}]
	alias := defaultUnitStatement("routines", "all", []string{"default_owner"}, nil, []string{"default_reader"})
	alias.Revoke = true
	if err := f.apply(alias); err != nil {
		t.Fatal(err)
	}
	if len(f.e.state.defaultACLs) != 5 || first == nil || len(first.acl.Entries) != 3 {
		t.Fatal("routine alias/source mutation")
	}
	empty := defaultUnitStatement("large objects", "all", []string{"default_owner"}, nil, []string{"default_owner"})
	empty.Revoke = true
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	// Revoke the recipient too: stored global empty is not the baseline and its
	// creator dependency persists even without a creation provider.
	empty.Grantees = []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "default_reader"}}}
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	record := f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: defaultLargeObjects}]
	if record == nil || !record.acl.Explicit || len(record.acl.Entries) != 0 {
		t.Fatal("stored empty lost", record)
	}
	if err := validateDefaultACLs(f.e.Roles(), f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPrivilegesCreatorSelection(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.member)
	f.grant(t, "tables", "select", nil)
	view := f.e.CatalogRead()
	if creationDefaultACL(view, f.member, f.schema, defaultTables).Explicit {
		t.Fatal("inherited creator defaults applied")
	}
	ownerACL := creationDefaultACL(view, f.owner, f.schema, defaultTables)
	if !ownerACL.Explicit || aclRights(ownerACL, f.owner, []PrincipalRef{f.reader}, SelectPrivilege, false)&SelectPrivilege != SelectPrivilege {
		t.Fatal("selected owner defaults missing")
	}
	current := defaultUnitStatement("sequences", "usage", nil, nil, []string{"default_reader"})
	if err := f.apply(current); err != nil {
		t.Fatal(err)
	}
	if f.e.state.defaultACLs[defaultACLKey{creator: f.member, class: defaultSequences}] == nil || f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: defaultSequences}] != nil {
		t.Fatal("omitted creator not current")
	}
	roles := cloneRoles(f.e.roles)
	roles.edges = append([]Membership(nil), roles.edges...)
	for i := range roles.edges {
		if roles.edges[i].Member == f.member && roles.edges[i].Role == f.owner {
			roles.edges[i].Inherit = false
			roles.edges[i].Admin = true
		}
	}
	f.e.roles = roles
	err := f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"}))
	defaultUnitDiagnostic(t, err, "42501", "permission denied to change default privileges")
	f.actor(f.owner)
	if err = f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"})); err != nil {
		t.Fatal("selected SET target", err)
	}
}

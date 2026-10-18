package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestDefaultPrivilegesBaselines(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "types", "usage", nil)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	table := defaultUnitTable(t, f, stage, "dependent", f.owner)
	row := stage.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(table.rowType)}].(*typeDescriptor)
	if !authorization.MatchesDefaultBaseline(row.acl, f.owner, authorization.DefaultTypes, false) || descriptorRoleDependencies(row, f.reader, nil) {
		t.Fatal("dependent rowtype consumed TYPES defaults")
	}
	array := stage.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(row.array)}].(*typeDescriptor)
	if !authorization.MatchesDefaultBaseline(array.acl, f.owner, authorization.DefaultTypes, false) || descriptorRoleDependencies(array, f.reader, nil) {
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
	row = stage.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(composite.rowType)}].(*typeDescriptor)
	array = stage.descriptors[ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(row.array)}].(*typeDescriptor)
	if !descriptorRoleDependencies(row, f.reader, nil) || descriptorRoleDependencies(array, f.reader, nil) || !authorization.MatchesDefaultBaseline(array.acl, f.owner, authorization.DefaultTypes, false) {
		t.Fatal("standalone/associated ACL distinction")
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
	first := f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: authorization.DefaultRoutines}]
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
	record := f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: authorization.DefaultLargeObjects}]
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
	if creationDefaultACL(view, f.member, f.schema, authorization.DefaultTables).Explicit {
		t.Fatal("inherited creator defaults applied")
	}
	ownerACL := creationDefaultACL(view, f.owner, f.schema, authorization.DefaultTables)
	if !ownerACL.Explicit || authorization.ACLRights(ownerACL, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("selected owner defaults missing")
	}
	current := defaultUnitStatement("sequences", "usage", nil, nil, []string{"default_reader"})
	if err := f.apply(current); err != nil {
		t.Fatal(err)
	}
	if f.e.state.defaultACLs[defaultACLKey{creator: f.member, class: authorization.DefaultSequences}] == nil || f.e.state.defaultACLs[defaultACLKey{creator: f.owner, class: authorization.DefaultSequences}] != nil {
		t.Fatal("omitted creator not current")
	}
	roles := cloneRoleFixture(f.e.roles)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.member && edges[i].Role == f.owner {
			edges[i].Inherit = false
			edges[i].Admin = true
		}
	}
	setFixtureEdges(roles, edges)
	f.e.roles = roles
	err := f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"}))
	defaultUnitDiagnostic(t, err, "42501", "permission denied to change default privileges")
	f.actor(f.owner)
	if err = f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"})); err != nil {
		t.Fatal("selected SET target", err)
	}
}

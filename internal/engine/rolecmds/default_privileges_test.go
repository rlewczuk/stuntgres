package rolecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDefaultPrivilegesBaselines(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "types", "usage", nil)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	table := defaultUnitTable(t, f, stage, "dependent", f.owner)
	row := stage.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}).(*catalog.Type)
	if !authorization.MatchesDefaultBaseline(row.ACL, f.owner, authorization.DefaultTypes, false) || access.DescriptorRoleDependencies(row, f.reader, nil) {
		t.Fatal("dependent rowtype consumed TYPES defaults")
	}
	array := stage.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}).(*catalog.Type)
	if !authorization.MatchesDefaultBaseline(array.ACL, f.owner, authorization.DefaultTypes, false) || access.DescriptorRoleDependencies(array, f.reader, nil) {
		t.Fatal("dependent array consumed TYPES defaults")
	}
	// Only the independent base consumes TYPES: its generated array must not
	// acquire a second origin/dependency on the selected named recipient.
	composite := *table
	composite.ObjectAddress.ObjectID = 20020
	composite.ObjectName = "standalone"
	composite.RowType = 0
	composite.RelationKind = catalog.CompositeRelation
	f.e.Mu.Lock()
	err := f.e.StageRowTypeLocked(stage, &composite)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	row = stage.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(composite.RowType)}).(*catalog.Type)
	array = stage.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}).(*catalog.Type)
	if !access.DescriptorRoleDependencies(row, f.reader, nil) || access.DescriptorRoleDependencies(array, f.reader, nil) || !authorization.MatchesDefaultBaseline(array.ACL, f.owner, authorization.DefaultTypes, false) {
		t.Fatal("standalone/associated ACL distinction")
	}
}

func TestDefaultPrivilegesStorage(t *testing.T) {
	f := newDefaultUnitFixture(t)
	old := f.e.CatalogRead()
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		f.grant(t, class, "all", nil)
	}
	if f.e.Catalog.State.DefaultACLCount() != 6 || old.State().DefaultACLCount() != 0 {
		t.Fatal("six stored classes/source immutability")
	}
	first := f.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultRoutines})
	alias := defaultUnitStatement("routines", "all", []string{"default_owner"}, nil, []string{"default_reader"})
	alias.Revoke = true
	if err := f.apply(alias); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACLCount() != 5 || first == nil || len(first.ACL.Entries) != 3 {
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
	record := f.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultLargeObjects})
	if record == nil || !record.ACL.Explicit || len(record.ACL.Entries) != 0 {
		t.Fatal("stored empty lost", record)
	}
	if err := catalog.ValidateDefaultACLs(f.e.Roles(), f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPrivilegesCreatorSelection(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.member)
	f.grant(t, "tables", "select", nil)
	view := f.e.CatalogRead()
	if catalog.CreationDefaultACL(view, f.member, f.schema, authorization.DefaultTables).Explicit {
		t.Fatal("inherited creator defaults applied")
	}
	ownerACL := catalog.CreationDefaultACL(view, f.owner, f.schema, authorization.DefaultTables)
	if !ownerACL.Explicit || authorization.ACLRights(ownerACL, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege, false)&authorization.SelectPrivilege != authorization.SelectPrivilege {
		t.Fatal("selected owner defaults missing")
	}
	current := defaultUnitStatement("sequences", "usage", nil, nil, []string{"default_reader"})
	if err := f.apply(current); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: f.member, Class: authorization.DefaultSequences}) == nil || f.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultSequences}) != nil {
		t.Fatal("omitted creator not current")
	}
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.member && edges[i].Role == f.owner {
			edges[i].Inherit = false
			edges[i].Admin = true
		}
	}
	setFixtureEdges(roles, edges)
	f.e.RoleStore.PublishLocked(roles)
	err := f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"}))
	defaultUnitDiagnostic(t, err, "42501", "permission denied to change default privileges")
	f.actor(f.owner)
	if err = f.apply(defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"default_reader"})); err != nil {
		t.Fatal("selected SET target", err)
	}
}

// defaultUnitTable owns Engine.mu; callers must not hold it. Resolve the
// immutable stage-base carrier first, without a locking Engine.CatalogRead.
func defaultUnitTable(t *testing.T, f defaultUnitFixture, stage *engine.CatalogStage, name string, owner authorization.PrincipalRef) *catalog.Relation {
	t.Helper()
	typ, ok := stage.Base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("stage has no int4 carrier")
	}
	f.e.Mu.Lock()
	d, err := table.NewRelation(engine.DDLHost{E: f.e}, f.schema, name, owner, []catalog.Column{{Name: "x", Usage: typ.Usage()}}, nil)
	if err == nil {
		d.Security.ACL = stage.CreationACL(owner, f.schema, authorization.DefaultTables)
		err = f.e.StageRowTypeLocked(stage, d)
	}
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Create(d); err != nil {
		t.Fatal(err)
	}
	return d
}

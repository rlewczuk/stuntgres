package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgTablespaceUnitCapture(t *testing.T, e *Engine) (*catalog.Relation, *exec.Source) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1213}
	d, ok := e.Catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing pg_tablespace")
	}
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	source, err := captureCatalogSourceLocked(e, context.Background(), e.CatalogRead(), e.Roles(), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	return d, source
}

func pgTablespaceUnitRows(t *testing.T, source *exec.Source) {
	t.Helper()
	if len(source.Anonymous) != 2 {
		t.Fatal("builtin-only row bag", source.Anonymous)
	}
	for i, want := range []struct {
		oid  uint32
		name string
	}{{1663, "pg_default"}, {1664, "pg_global"}} {
		row := source.Anonymous[i]
		if len(row) != 5 {
			t.Fatal("bounded projection", row)
		}
		oid, err := row[0].OID()
		if err != nil || oid != want.oid {
			t.Fatal(oid, err)
		}
		name, err := row[1].Text()
		if err != nil || name != want.name {
			t.Fatal(name, err)
		}
		owner, err := row[2].OID()
		if err != nil || owner != 10 {
			t.Fatal("canonical owner", owner, err)
		}
		if !row[3].IsNull() || row[3].Type().ID() != sqltype.ACLItemArray {
			t.Fatal("typed ACL NULL", row[3])
		}
		if !row[4].IsNull() || row[4].Type().ID() != 1009 {
			t.Fatal("typed options NULL", row[4])
		}
	}
}

func TestPGTablespaceDescriptorCapture(t *testing.T) {
	e := New()
	d, source := pgTablespaceUnitCapture(t, e)
	pgTablespaceUnitRows(t, source)
	if d.Owner != e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID) || d.RowType == 0 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || !pgcatalog.SummaryTable(1213) {
		t.Fatal("catalog identity", d)
	}
	if !reflect.DeepEqual(d.Attributes, []int32{1, 2, 3, 4, 5}) || d.ColumnMeta.HighWater != 5 {
		t.Fatal("attribute positions", d.Attributes, d.ColumnMeta.HighWater)
	}
	for i, want := range []struct {
		name string
		typ  sqltype.ID
		attr int16
	}{{"oid", sqltype.Oid, 1}, {"spcname", sqltype.Name, 2}, {"spcowner", sqltype.Oid, 3}, {"spcacl", sqltype.ACLItemArray, 4}, {"spcoptions", 1009, 5}} {
		column := d.Columns[i]
		if column.Name != want.name || column.Usage.Type.ID() != want.typ || !column.Usage.Type.Concrete() || column.OriginTable != 1213 || column.OriginAttribute != want.attr || !column.NullabilityKnown || column.Nullable != (i >= 3) {
			t.Fatal("column metadata", i, column)
		}
		if index, ok := d.Index[want.name]; !ok || index != i {
			t.Fatal("column lookup", want.name, index, ok)
		}
	}
	if !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.Security.ACL.Entries[0].Grantor != d.Owner {
		t.Fatal("public readonly ACL", d.Security.ACL)
	}
	row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	if row.OwningRelation != d.Address() || row.Owner != d.Owner {
		t.Fatal("row identity link", row)
	}
	array := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if array.OwningRelation != d.Address() || array.Owner != d.Owner {
		t.Fatal("array identity link", array)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: e.CatalogRead(), Roles: e.Roles(), Ref: source.Ref, Source: d, Owner: &e.Catalog.Owner, Runtime: NewCatalogRuntime(CatalogRuntimeFields{Engine: e})}, capturePGTablespace); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", got, err)
	}
	if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision {
		t.Fatal("capture mutated state")
	}
}

func TestPGTablespaceCanonicalReplacement(t *testing.T) {
	e := New()
	_, retained := pgTablespaceUnitCapture(t, e)
	initial := e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	roles := cloneRoleFixture(e.RoleStore.Published)
	r := fixtureRole(roles, initial)
	r.Name = "renamed_initial"
	putFixtureRole(roles, r)
	e.RoleStore.PublishLocked(roles)
	d, renamed := pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("role rename changed canonical source owner", d.Owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Reset imports a fresh postgres-owned catalog. A renamed target without
	// that source owner must reject replacement, not bypass name provenance.
	state, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	if err := Reset(context.Background(), e); err == nil {
		t.Fatal("Reset bypassed missing replacement-owner provenance")
	}
	if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision {
		t.Fatal("rejected replacement changed target authority")
	}
	pgTablespaceUnitRows(t, retained)
	// A source with matching renamed provenance can genuinely replace it.
	source := New()
	sourceInitial := source.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	sourceRoles := cloneRoleFixture(source.RoleStore.Published)
	sourceRole := fixtureRole(sourceRoles, sourceInitial)
	sourceRole.Name = r.Name
	putFixtureRole(sourceRoles, sourceRole)
	source.RoleStore.PublishLocked(sourceRoles)
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	d, renamed = pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("aligned replacement lost canonical owner", d.Owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Restore the target name required by Reset's fresh source provenance.
	roles = cloneRoleFixture(e.RoleStore.Published)
	r = fixtureRole(roles, initial)
	r.Name = "postgres"
	putFixtureRole(roles, r)
	e.RoleStore.PublishLocked(roles)
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	d, fresh := pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("Reset rebound bootstrap by name", d.Owner)
	}
	pgTablespaceUnitRows(t, retained)
	pgTablespaceUnitRows(t, fresh)
	var dump strings.Builder
	if err := Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "CREATE TABLESPACE") {
		t.Fatal("builtin source serialized as physical creation")
	}
	if err := Restore(context.Background(), e, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	d, fresh = pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("Restore rebound bootstrap by name", d.Owner)
	}
	pgTablespaceUnitRows(t, retained)
	pgTablespaceUnitRows(t, fresh)
}

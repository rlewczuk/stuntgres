package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgTablespaceUnitCapture(t *testing.T, e *Engine) (*relationDescriptor, *querySource) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1213}
	d, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing pg_tablespace")
	}
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
	source, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.Roles(), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	return d, source
}

func pgTablespaceUnitRows(t *testing.T, source *querySource) {
	t.Helper()
	if len(source.anonymous) != 2 {
		t.Fatal("builtin-only row bag", source.anonymous)
	}
	for i, want := range []struct {
		oid  uint32
		name string
	}{{1663, "pg_default"}, {1664, "pg_global"}} {
		row := source.anonymous[i]
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
	if d.Owner != e.authorizationDomain.Principal(authorization.BootstrapPrincipalID) || d.RowType == 0 || d.RelationKind != catalogRelation || d.VirtualView || !representedSummaryTable(1213) {
		t.Fatal("catalog identity", d)
	}
	if !reflect.DeepEqual(d.Attributes, []int32{1, 2, 3, 4, 5}) || d.AttributeHighWater != 5 {
		t.Fatal("attribute positions", d.Attributes, d.AttributeHighWater)
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
	if !d.ACL.Explicit || len(d.ACL.Entries) != 1 || !d.ACL.Entries[0].Grantee.Public || d.ACL.Entries[0].Privileges != authorization.SelectPrivilege || d.ACL.Entries[0].Grantor != d.Owner {
		t.Fatal("public readonly ACL", d.ACL)
	}
	row := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	if row.OwningRelation != d.Address() || row.Owner != d.Owner {
		t.Fatal("row identity link", row)
	}
	array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if array.OwningRelation != d.Address() || array.Owner != d.Owner {
		t.Fatal("array identity link", array)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, oid, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	if got, err := e.capturePGTablespace(ctx, e.CatalogRead(), e.Roles(), source.ref, d); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", got, err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision {
		t.Fatal("capture mutated state")
	}
}

func TestPGTablespaceCanonicalReplacement(t *testing.T) {
	e := New()
	_, retained := pgTablespaceUnitCapture(t, e)
	initial := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	roles := cloneRoleFixture(e.roles)
	r := fixtureRole(roles, initial)
	r.Name = "renamed_initial"
	putFixtureRole(roles, r)
	e.roles = roles
	d, renamed := pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("role rename changed canonical source owner", d.Owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Reset imports a fresh postgres-owned catalog. A renamed target without
	// that source owner must reject replacement, not bypass name provenance.
	state, oid, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	if err := e.Reset(context.Background()); err == nil {
		t.Fatal("Reset bypassed missing replacement-owner provenance")
	}
	if e.catalog.state != state || e.roles != roles || e.catalog.nextOID != oid || e.catalog.nextRevision != revision {
		t.Fatal("rejected replacement changed target authority")
	}
	pgTablespaceUnitRows(t, retained)
	// A source with matching renamed provenance can genuinely replace it.
	source := New()
	sourceInitial := source.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	sourceRoles := cloneRoleFixture(source.roles)
	sourceRole := fixtureRole(sourceRoles, sourceInitial)
	sourceRole.Name = r.Name
	putFixtureRole(sourceRoles, sourceRole)
	source.roles = sourceRoles
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	d, renamed = pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("aligned replacement lost canonical owner", d.Owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Restore the target name required by Reset's fresh source provenance.
	roles = cloneRoleFixture(e.roles)
	r = fixtureRole(roles, initial)
	r.Name = "postgres"
	putFixtureRole(roles, r)
	e.roles = roles
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, fresh := pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("Reset rebound bootstrap by name", d.Owner)
	}
	pgTablespaceUnitRows(t, retained)
	pgTablespaceUnitRows(t, fresh)
	var dump strings.Builder
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "CREATE TABLESPACE") {
		t.Fatal("builtin source serialized as physical creation")
	}
	if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	d, fresh = pgTablespaceUnitCapture(t, e)
	if d.Owner != initial {
		t.Fatal("Restore rebound bootstrap by name", d.Owner)
	}
	pgTablespaceUnitRows(t, retained)
	pgTablespaceUnitRows(t, fresh)
}

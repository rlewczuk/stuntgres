package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgTablespaceUnitCapture(t *testing.T, e *Engine) (*relationDescriptor, *querySource) {
	t.Helper()
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 1213}
	d, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_tablespace")
	}
	ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: address}
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
		if len(row) != 4 {
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
		if !row[3].IsNull() || row[3].Type().ID() != 1009 {
			t.Fatal("typed options NULL", row[3])
		}
	}
}

func TestPGTablespaceDescriptorCapture(t *testing.T) {
	e := New()
	d, source := pgTablespaceUnitCapture(t, e)
	pgTablespaceUnitRows(t, source)
	if d.owner != (PrincipalRef{e, bootstrapPrincipalID}) || d.rowType == 0 || d.relationKind != catalogRelation || d.virtualView || !representedSummaryTable(1213) {
		t.Fatal("catalog identity", d)
	}
	if !reflect.DeepEqual(d.attributes, []int32{1, 2, 3, 5}) || d.attributeHighWater != 5 {
		t.Fatal("original attribute positions", d.attributes, d.attributeHighWater)
	}
	for i, want := range []struct {
		name string
		typ  sqltype.ID
		attr int16
	}{{"oid", sqltype.Oid, 1}, {"spcname", sqltype.Name, 2}, {"spcowner", sqltype.Oid, 3}, {"spcoptions", 1009, 5}} {
		column := d.columns[i]
		if column.Name != want.name || column.Usage.Type.ID() != want.typ || !column.Usage.Type.Concrete() || column.OriginTable != 1213 || column.OriginAttribute != want.attr || !column.NullabilityKnown || column.Nullable != (i == 3) {
			t.Fatal("column metadata", i, column)
		}
	}
	if _, exists := d.index["spcacl"]; exists {
		t.Fatal("unsupported ACL carrier fabricated")
	}
	if !d.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != SelectPrivilege || d.acl.Entries[0].Grantor != d.owner {
		t.Fatal("public readonly ACL", d.acl)
	}
	row := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.rowType)}].(*typeDescriptor)
	if row.owningRelation != d.address || row.owner != d.owner {
		t.Fatal("row identity link", row)
	}
	array := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}].(*typeDescriptor)
	if array.owningRelation != d.address || array.owner != d.owner {
		t.Fatal("array identity link", array)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, oid, revision := e.state, e.nextOID, e.nextRevision
	if got, err := capturePGTablespace(ctx, source.ref, d); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture", got, err)
	}
	if e.state != state || e.nextOID != oid || e.nextRevision != revision {
		t.Fatal("capture mutated state")
	}
}

func TestPGTablespaceCanonicalReplacement(t *testing.T) {
	e := New()
	_, retained := pgTablespaceUnitCapture(t, e)
	initial := PrincipalRef{e, bootstrapPrincipalID}
	roles := cloneRoles(e.roles)
	r := roles.roles[initial]
	delete(roles.names, r.Name)
	r.Name = "renamed_initial"
	roles.roles[initial] = r
	roles.names[r.Name] = initial
	e.roles = roles
	d, renamed := pgTablespaceUnitCapture(t, e)
	if d.owner != initial {
		t.Fatal("role rename changed canonical source owner", d.owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Reset imports a fresh postgres-owned catalog. A renamed target without
	// that source owner must reject replacement, not bypass name provenance.
	state, oid, revision := e.state, e.nextOID, e.nextRevision
	if err := e.Reset(context.Background()); err == nil {
		t.Fatal("Reset bypassed missing replacement-owner provenance")
	}
	if e.state != state || e.roles != roles || e.nextOID != oid || e.nextRevision != revision {
		t.Fatal("rejected replacement changed target authority")
	}
	pgTablespaceUnitRows(t, retained)
	// A source with matching renamed provenance can genuinely replace it.
	source := New()
	sourceInitial := PrincipalRef{source, bootstrapPrincipalID}
	sourceRoles := cloneRoles(source.roles)
	sourceRole := sourceRoles.roles[sourceInitial]
	delete(sourceRoles.names, sourceRole.Name)
	sourceRole.Name = r.Name
	sourceRoles.roles[sourceInitial] = sourceRole
	sourceRoles.names[sourceRole.Name] = sourceInitial
	source.roles = sourceRoles
	if _, err := e.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	d, renamed = pgTablespaceUnitCapture(t, e)
	if d.owner != initial {
		t.Fatal("aligned replacement lost canonical owner", d.owner)
	}
	pgTablespaceUnitRows(t, renamed)
	// Restore the target name required by Reset's fresh source provenance.
	roles = cloneRoles(e.roles)
	r = roles.roles[initial]
	delete(roles.names, r.Name)
	r.Name = "postgres"
	roles.roles[initial] = r
	roles.names[r.Name] = initial
	e.roles = roles
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, fresh := pgTablespaceUnitCapture(t, e)
	if d.owner != initial {
		t.Fatal("Reset rebound bootstrap by name", d.owner)
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
	if d.owner != initial {
		t.Fatal("Restore rebound bootstrap by name", d.owner)
	}
	pgTablespaceUnitRows(t, retained)
	pgTablespaceUnitRows(t, fresh)
}

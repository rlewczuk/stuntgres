package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type roleRoutineGrantsFixture struct {
	f       defaultUnitFixture
	state   *catalog.State
	routine *catalog.Procedure
}

func newRoleRoutineGrantsFixture(t *testing.T) roleRoutineGrantsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.catalog.State)
	routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 20522}, ObjectNamespace: f.schema, ObjectName: "rrg_unit", ObjectRevision: 1, Owner: f.owner, RoutineKind: catalog.FunctionRoutine, ACL: authorization.ACLValue{Explicit: true}}
	state.SetDescriptor(routine.Address(), routine)
	return roleRoutineGrantsFixture{f, state, routine}
}
func (f roleRoutineGrantsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoleRoutineGrantsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engineCatalogRuntime{auth: auth, database: "unit_database"}}
}
func roleRoutineGrantsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleRoutineGrantsOID)
	if !ok {
		t.Fatal("missing role_routine_grants producer")
	}
	captured, err := captureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous source identity", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 10 {
			t.Fatal("projection width", row)
		}
		for i, value := range row {
			if value.IsNull() || value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("source-owned domain carrier", i, value)
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			want := sqltype.Name
			if i >= 8 {
				want = sqltype.VarChar
			}
			authority, ok := in.Catalog.LookupType(want)
			if !ok || base.Type() != authority {
				t.Fatal("captured base authority", i, base)
			}
		}
	}
	return captured.Anonymous
}
func roleRoutineGrantsBag(t *testing.T, rows [][]sqltype.Value) map[[10]string]int {
	t.Helper()
	bag := make(map[[10]string]int)
	for _, row := range rows {
		var values [10]string
		for i, value := range row {
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			values[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		if values[7] == "rrg_unit" {
			bag[values]++
		}
	}
	return bag
}
func roleRoutineGrantsWant(grantor, grantee, privilege, option string) [10]string {
	return [10]string{grantor, grantee, "unit_database", "default_schema", "rrg_unit_20522", "unit_database", "default_schema", "rrg_unit", privilege, option}
}
func (f roleRoutineGrantsFixture) check(t *testing.T, state *catalog.State, actor authorization.PrincipalRef, want ...[10]string) {
	t.Helper()
	bag := make(map[[10]string]int)
	for _, row := range want {
		bag[row]++
	}
	got := roleRoutineGrantsBag(t, roleRoutineGrantsRows(t, f.input(t, state, actor)))
	if !reflect.DeepEqual(got, bag) {
		t.Fatalf("grants=%v want=%v", got, bag)
	}
}
func TestInfoRoleRoutineGrantsMetadata(t *testing.T) {
	f := newRoleRoutineGrantsFixture(t)
	in := f.input(t, f.state, f.f.reader)
	if rows := roleRoutineGrantsRows(t, in); len(rows) != 0 {
		t.Fatal("reader should have nonnil empty rows", rows)
	}
	source := in.Source
	if source.Address().ObjectID != 16220 || source.Namespace().ObjectID != 13229 || source.RowType != 13381 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("view identity", source)
	}
	names := [10]string{"grantor", "grantee", "specific_catalog", "specific_schema", "specific_name", "routine_catalog", "routine_schema", "routine_name", "privilege_type", "is_grantable"}
	for i, column := range source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 8 {
			domain, base = 13246, sqltype.VarChar
		}
		if i >= 9 {
			domain, base = 13256, sqltype.VarChar
		}
		captured, ok := in.Catalog.LookupType(domain)
		if !ok || column.Name != names[i] || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16220 || column.OriginAttribute != int16(i+1) || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.Usage.Type != captured || column.Usage.Type.BaseType().ID() != base {
			t.Fatal("column contract", i, column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13381})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13380 {
		t.Fatal("row backlink", row)
	}
	array, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13380})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("array backlink", array)
	}
}

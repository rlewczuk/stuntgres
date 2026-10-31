package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func pgConfigInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ConfigOID}
	return pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(address), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
}

func pgConfigCheckRows(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatal("configuration singleton shape", rows)
	}
	for i, text := range []string{"VERSION", "PostgreSQL 18.0 compatible; embedded stuntgres"} {
		if rows[0][i] != sqltype.String(in.Source.Columns[i].Usage.Type, text) || rows[0][i].IsNull() {
			t.Fatal("owned configuration TEXT", i, rows[0][i])
		}
	}
}

func TestPGConfigCapture(t *testing.T) {
	e := engine.New()
	input := pgConfigInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ConfigOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	pgConfigCheckRows(t, input, rows)
	rows[0][0] = sqltype.Null(input.Source.Columns[0].Usage.Type)
	rows[0] = nil
	later, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	pgConfigCheckRows(t, input, later)
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != input.Ref {
		t.Fatal("configuration capture identity", captured, err)
	}
	pgConfigCheckRows(t, input, captured.Anonymous)
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid producer input admitted", rows, err)
		}
		if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || err == nil {
			t.Fatal("invalid source published", source, err)
		}
	}
	foreign := engine.New()
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Catalog.Epoch(), in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copySource := *in.Source; in.Source = &copySource },
	} {
		in := input
		change(&in)
		reject(in)
	}
	for _, width := range []int{1, 3} {
		in := input
		source := *input.Source
		source.Columns = make([]catalog.Column, width)
		copy(source.Columns, input.Source.Columns)
		in.Source = &source
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.ObjectName = "forged_config" },
		func(r *catalog.Relation) { r.ObjectAddress.ObjectID++ },
		func(r *catalog.Relation) { r.VirtualView = false },
		func(r *catalog.Relation) { r.CatalogView = false },
		func(r *catalog.Relation) { r.Columns[0], r.Columns[1] = r.Columns[1], r.Columns[0] },
		func(r *catalog.Relation) { r.Columns[0].Name = "forged" },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = input.Catalog.LookupType(sqltype.Int8) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Text) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 950 },
		func(r *catalog.Relation) { r.Columns[0].Nullable = false },
		func(r *catalog.Relation) { r.Columns[1].NullabilityKnown = false },
		func(r *catalog.Relation) { r.Columns[0].DeclaredDimensions = 1 },
	} {
		state := *input.Catalog.State()
		source := *input.Source
		source.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&source)
		state.SetDescriptor(source.Address(), &source)
		in := input
		in.Catalog, in.Source = e.Catalog.Read(&state), &source
		reject(in)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context, input.Source = ctx, nil
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation precedes validation", rows, err)
	}
	if source, err := core.CaptureCatalogSourceRows(input, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source published", source, err)
	}
}

func TestPGConfigOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ConfigOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_config" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12132 || len(r.Columns) != 2 {
			t.Fatal("configuration relation identity", r)
		}
		checkACL := func(address ids.ObjectAddress, acl authorization.ACLValue, privilege authorization.Privileges) {
			t.Helper()
			if !acl.Explicit || len(acl.Entries) != 1 {
				t.Fatal("explicit owner-only ACL", address, acl)
			}
			entry := acl.Entries[0]
			if entry.Grantor != r.Owner || entry.Grantee.Role != r.Owner || entry.Grantee.Public || entry.Privileges != privilege || entry.GrantOptions != 0 {
				t.Fatal("owner grant", entry)
			}
			history := view.State().InitialPrivilege(address)
			if history == nil || history.PrivType != 'i' || !reflect.DeepEqual(history.ACL, acl) {
				t.Fatal("immutable bootstrap ACL", address, history, acl)
			}
		}
		if r.Owner.ID() != authorization.BootstrapPrincipalID {
			t.Fatal("bootstrap relation owner", r.Owner)
		}
		checkACL(address, r.Security.ACL, authorization.TableGrantPrivileges)
		procAddress := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3400}
		procedure, ok := view.Procedure(procAddress)
		if !ok || procedure.Builtin == nil || procedure.Builtin != routine.BuiltinRoutine(3400) || procedure.Builtin.Providers != 0 || procedure.Builtin.Parallel != 'r' || !procedure.Strict || !procedure.ReturnsSet() || procedure.Result != 2249 || procedure.CostEstimate() != 1 || procedure.RowEstimate() != 23 || procedure.Owner != r.Owner {
			t.Fatal("noncallable backing declaration", procedure)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
			t.Fatal(err)
		}
		if d, err := routine.LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_config", nil); d != nil || err == nil {
			t.Fatal("descriptor acquired callable provider", d, err)
		}
		checkACL(procAddress, procedure.ACL, authorization.ExecutePrivilege)
		outputs := procedure.OutputDeclaration()
		if outputs == nil || outputs.Len() != 2 || outputs.Inputs() != 0 {
			t.Fatal("configuration OUT declaration", outputs)
		}
		for i, name := range []string{"name", "setting"} {
			owned, ok := view.LookupType(sqltype.Text)
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 100 || c.DeclaredDimensions != 0 || c.OriginTable != 12130 || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) || outputs.Name(i) != name || outputs.Type(i) != 25 {
				t.Fatal("owned configuration attribute", i, c)
			}
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12132})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12131})
		ownedRow, rowOK := view.LookupType(12132)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12131 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("configuration reciprocal row/array ownership", row, array)
		}
		return r
	}
	oldInput := pgConfigInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12132)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := pgConfigInput(e)
	fresh := check(freshInput.Catalog)
	freshRow, _ := freshInput.Catalog.LookupType(12132)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if old == fresh || old.Columns[0].Usage.Type == fresh.Columns[0].Usage.Type || oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("replacement reused or mutated retained generation")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ConfigOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		rows, err := produce(in)
		if err != nil {
			t.Fatal(err)
		}
		pgConfigCheckRows(t, in, rows)
	}
	for _, source := range []*catalog.Relation{old, pgConfigInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale source generation published", got, err)
		}
	}
}

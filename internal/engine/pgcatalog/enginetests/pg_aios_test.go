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

func aiosInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.AIOSOID}
	return pgcatalog.Input{
		Context: context.Background(), Catalog: view, Source: view.MustRelation(address),
		Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{},
	}
}

func TestAIOSCapture(t *testing.T) {
	e := engine.New()
	input := aiosInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.AIOSOID)
	if !ok {
		t.Fatal("missing AIO producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("truthful empty progress", rows, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); err != nil || got == nil || got.Ref != input.Ref || got.Anonymous == nil || len(got.Anonymous) != 0 {
		t.Fatal("empty capture identity", got, err)
	}
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid input admitted", rows, err)
		}
		if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || err == nil {
			t.Fatal("invalid capture published", got, err)
		}
	}
	for _, width := range []int{14, 16} {
		in := input
		copySource := *input.Source
		copySource.Columns = make([]catalog.Column, width)
		copy(copySource.Columns, input.Source.Columns)
		in.Source = &copySource
		reject(in)
	}
	foreign := engine.New()
	for i, column := range input.Source.Columns {
		in := input
		copySource := *input.Source
		copySource.Columns = append([]catalog.Column(nil), input.Source.Columns...)
		copySource.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(column.Usage.Type.ID())
		in.Source = &copySource
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns[0].Name = "forged" },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = input.Catalog.LookupType(sqltype.Int8) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[3].Usage.Collation.ID = 0 },
		func(r *catalog.Relation) { r.Columns[0].Nullable = false },
		func(r *catalog.Relation) { r.Columns[0].DeclaredDimensions = 1 },
		func(r *catalog.Relation) { r.ObjectName = "unregistered_aios" },
		func(r *catalog.Relation) { r.VirtualView = false },
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
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) {
			copySource := *in.Source
			in.Source = &copySource
		},
		func(in *pgcatalog.Input) {
			copySource := *in.Source
			copySource.Columns = append([]catalog.Column(nil), in.Source.Columns...)
			copySource.Columns[0].Name = "forged"
			in.Source = &copySource
		},
		func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Catalog.Epoch(), in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Runtime = nil },
	} {
		in := input
		change(&in)
		reject(in)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context, input.Source = ctx, nil
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation precedence", rows, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published", got, err)
	}
}

func TestAIOSOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.AIOSOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_aios" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12357 || len(r.Columns) != 15 {
			t.Fatal("AIO relation contract", r)
		}
		if r.Owner.ID() != 10 || len(r.Security.ACL.Entries) != 2 {
			t.Fatal("owner/ACL", r.Owner, r.Security)
		}
		grant := r.Security.ACL.Entries[0]
		if grant.Grantee.Role != r.Owner || grant.Grantor != r.Owner || grant.Privileges != authorization.TableGrantPrivileges {
			t.Fatal("owner table privileges", grant)
		}
		stats := e.RoleStore.PublishedRead(view.Owner(), view).LookupRef("pg_read_all_stats")
		grant = r.Security.ACL.Entries[1]
		if grant.Grantor != r.Owner || grant.Grantee.Public || grant.Grantee.Role != stats || grant.Privileges != authorization.SelectPrivilege || grant.GrantOptions != 0 {
			t.Fatal("stats SELECT", grant)
		}
		procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6399})
		if !ok || procedure.Builtin == nil || procedure.Builtin != routine.BuiltinRoutine(6399) || procedure.Builtin.Providers != 0 || procedure.Builtin.Parallel != 'r' || !procedure.Strict || !procedure.ReturnsSet() || procedure.Result != 2249 || procedure.CostEstimate() != 1 || procedure.RowEstimate() != 100 {
			t.Fatal("noncallable backing declaration", procedure)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil { t.Fatal(err) }
		if d, err := routine.LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_get_aios", nil); d != nil || err == nil { t.Fatal("metadata gained callable provider", d, err) }
		outputs := procedure.OutputDeclaration()
		if outputs == nil || outputs.Len() != 15 || outputs.Inputs() != 0 { t.Fatal("OUT declaration", outputs) }
		for i, column := range r.Columns {
			if outputs.Name(i) != column.Name || outputs.Type(i) != ids.OID(column.Usage.Type.ID()) { t.Fatal("OUT field", i) }
		}
		if !procedure.ACL.Explicit || len(procedure.ACL.Entries) != 2 { t.Fatal("backing ACL", procedure.ACL) }
		for i, entry := range procedure.ACL.Entries {
			grantee := r.Owner
			if i == 1 { grantee = stats }
			if entry.Grantor != r.Owner || entry.Grantee.Role != grantee || entry.Grantee.Public || entry.Privileges != authorization.ExecutePrivilege || entry.GrantOptions != 0 { t.Fatal("backing EXECUTE", entry) }
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12357})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12356})
		ownedRow, rowOK := view.LookupType(12357)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12356 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		names := []string{"pid", "io_id", "io_generation", "state", "operation", "off", "length", "target", "handle_data_len", "raw_result", "result", "target_desc", "f_sync", "f_localmem", "f_buffered"}
		types := []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8, sqltype.Text, sqltype.Text, sqltype.Int8, sqltype.Int8, sqltype.Text, sqltype.Int2, sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Bool, sqltype.Bool, sqltype.Bool}
		for i, name := range names {
			id, collation := types[i], uint32(0)
			if id == sqltype.Text { collation = 100 }
			owned, ok := view.LookupType(id)
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || c.OriginTable != uint32(pgcatalog.AIOSOID) || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) {
				t.Fatal("attribute contract", i, c)
			}
		}
		return r
	}
	oldInput := aiosInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12357)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := aiosInput(e)
	fresh := check(freshInput.Catalog)
	if old == fresh {
		t.Fatal("reset reused descriptor")
	}
	for i := range old.Columns {
		if old.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("reset reused carrier", i)
		}
	}
	freshRow, _ := freshInput.Catalog.LookupType(12357)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("retained generation changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.AIOSOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		if rows, err := produce(in); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("valid generation capture", rows, err)
		}
	}
	for _, source := range []*catalog.Relation{old, aiosInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale generation published", got, err)
		}
	}
}

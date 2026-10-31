package enginetests_test

import (
	"bytes"
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

func pgFileSettingsInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.FileSettingsOID}
	return pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(address), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
}

func pgFileSettingsCheckRows(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 0 {
		t.Fatal("no GUC source must yield zero rows", rows)
	}
}

func TestPGFileSettingsCapture(t *testing.T) {
	e := engine.New()
	input := pgFileSettingsInput(e)
	registrations := pgcatalog.Registrations()
	found := 0
	for i, registration := range registrations {
		if i > 0 && registrations[i-1].OID >= registration.OID {
			t.Fatal("producer registry not strictly ordered", i)
		}
		if registration.OID == pgcatalog.FileSettingsOID {
			found++
		}
	}
	if found != 1 {
		t.Fatal("file settings registration count", found)
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.FileSettingsOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	rows, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	pgFileSettingsCheckRows(t, input, rows)
	later, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	pgFileSettingsCheckRows(t, input, later)
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != input.Ref || captured.Anonymous == nil {
		t.Fatal("configuration capture identity", captured, err)
	}
	pgFileSettingsCheckRows(t, input, captured.Anonymous)
	const checkpoints = 1 << 20
	counter := &dtpCancelContext{Context: context.Background(), remaining: checkpoints}
	counted := input
	counted.Context = counter
	if _, err := produce(counted); err != nil {
		t.Fatal(err)
	}
	final := checkpoints - counter.remaining
	if final < 2 {
		t.Fatal("missing final cancellation checkpoint", final)
	}
	for _, remaining := range []int{1, final} {
		counted.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if source, err := core.CaptureCatalogSourceRows(counted, produce); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled empty source published", source, err)
		}
	}
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
	for _, width := range []int{6, 8} {
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

func TestPGFileSettingsOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.FileSettingsOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_file_settings" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12112 || len(r.Columns) != 7 {
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
		procAddress := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3329}
		procedure, ok := view.Procedure(procAddress)
		if !ok || procedure.Builtin == nil || procedure.Builtin != routine.BuiltinRoutine(3329) || procedure.Builtin.Providers != 0 || procedure.Builtin.Parallel != 's' || !procedure.Strict || !procedure.ReturnsSet() || procedure.Result != 2249 || procedure.CostEstimate() != 1 || procedure.RowEstimate() != 1000 || procedure.Owner != r.Owner || procedure.Display.Source != "show_all_file_settings" {
			t.Fatal("noncallable backing declaration", procedure)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, procedure); err != nil {
			t.Fatal(err)
		}
		if d, err := routine.LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, "pg_show_all_file_settings", nil); d != nil || err == nil {
			t.Fatal("descriptor acquired callable provider", d, err)
		}
		checkACL(procAddress, procedure.ACL, authorization.ExecutePrivilege)
		outputs := procedure.OutputDeclaration()
		if outputs == nil || outputs.Len() != 7 || outputs.Inputs() != 0 {
			t.Fatal("configuration OUT declaration", outputs)
		}
		for i, name := range []string{"sourcefile", "sourceline", "seqno", "name", "setting", "applied", "error"} {
			owned, ok := view.LookupType([]sqltype.ID{25, 23, 23, 25, 25, 16, 25}[i])
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != []uint32{100, 0, 0, 100, 100, 0, 100}[i] || c.DeclaredDimensions != 0 || c.OriginTable != 12110 || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) || outputs.Name(i) != name || outputs.Type(i) != []ids.OID{25, 23, 23, 25, 25, 16, 25}[i] {
				t.Fatal("owned configuration attribute", i, c)
			}
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12112})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12111})
		ownedRow, rowOK := view.LookupType(12112)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12111 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("configuration reciprocal row/array ownership", row, array)
		}
		return r
	}
	oldInput := pgFileSettingsInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12112)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := pgFileSettingsInput(e)
	fresh := check(freshInput.Catalog)
	freshRow, _ := freshInput.Catalog.LookupType(12112)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if old == fresh || old.Columns[0].Usage.Type == fresh.Columns[0].Usage.Type || oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("replacement reused or mutated retained generation")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.FileSettingsOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		rows, err := produce(in)
		if err != nil {
			t.Fatal(err)
		}
		pgFileSettingsCheckRows(t, in, rows)
	}
	for _, source := range []*catalog.Relation{old, pgFileSettingsInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale source generation published", got, err)
		}
	}
	if err := engine.Restore(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	restoredInput := pgFileSettingsInput(e)
	restored := check(restoredInput.Catalog)
	restoredRow, _ := restoredInput.Catalog.LookupType(12112)
	restoredArray, _ := restoredInput.Catalog.LookupArray(restoredRow)
	if restored == old || restored == fresh || restoredRow == oldRow || restoredRow == freshRow || restoredArray == oldArray || restoredArray == freshArray || restored.Columns[0].Usage.Type == fresh.Columns[0].Usage.Type {
		t.Fatal("restore adopted a retained descriptor/type generation")
	}
	for _, in := range []pgcatalog.Input{oldInput, freshInput, restoredInput} {
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || got == nil || got.Anonymous == nil || len(got.Anonymous) != 0 {
			t.Fatal("retained generation lost typed empty capture", got, err)
		}
	}
}

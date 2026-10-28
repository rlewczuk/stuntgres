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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func basebackupInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressBasebackupOID}
	return pgcatalog.Input{
		Context: context.Background(), Catalog: view, Source: view.MustRelation(address),
		Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{},
	}
}

func TestBasebackupProgressCapture(t *testing.T) {
	e := engine.New()
	input := basebackupInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ProgressBasebackupOID)
	if !ok {
		t.Fatal("missing basebackup producer")
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
	for _, width := range []int{5, 7} {
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
	for _, change := range []func(*pgcatalog.Input){
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

func TestBasebackupProgressOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressBasebackupOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_stat_progress_basebackup" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12331 || len(r.Columns) != 6 {
			t.Fatal("basebackup relation contract", r)
		}
		if r.Owner.ID() != 10 || len(r.Security.ACL.Entries) != 1 {
			t.Fatal("owner/ACL", r.Owner, r.Security)
		}
		grant := r.Security.ACL.Entries[0]
		if !grant.Grantee.Public || grant.Grantor != r.Owner || grant.Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", grant)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12331})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12330})
		ownedRow, rowOK := view.LookupType(12331)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12330 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		for i, name := range []string{"pid", "phase", "backup_total", "backup_streamed", "tablespaces_total", "tablespaces_streamed"} {
			id, collation := sqltype.Int8, uint32(0)
			if i == 0 {
				id = sqltype.Int4
			}
			if i == 1 {
				id, collation = sqltype.Text, 100
			}
			owned, ok := view.LookupType(id)
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || c.OriginTable != uint32(pgcatalog.ProgressBasebackupOID) || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) {
				t.Fatal("attribute contract", i, c)
			}
		}
		return r
	}
	oldInput := basebackupInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12331)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := basebackupInput(e)
	fresh := check(freshInput.Catalog)
	if old == fresh {
		t.Fatal("reset reused descriptor")
	}
	for i := range old.Columns {
		if old.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("reset reused carrier", i)
		}
	}
	freshRow, _ := freshInput.Catalog.LookupType(12331)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("retained generation changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.ProgressBasebackupOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		if rows, err := produce(in); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("valid generation capture", rows, err)
		}
	}
	for _, source := range []*catalog.Relation{old, basebackupInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := core.CaptureCatalogSourceRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale generation published", got, err)
		}
	}
}

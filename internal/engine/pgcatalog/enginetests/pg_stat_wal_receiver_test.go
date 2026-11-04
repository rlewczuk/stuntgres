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
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

var walReceiverColumnNames = []string{"pid", "status", "receive_start_lsn", "receive_start_tli", "written_lsn", "flushed_lsn", "received_tli", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time", "slot_name", "sender_host", "sender_port", "conninfo"}
var walReceiverColumnTypes = []sqltype.ID{23, 25, 3220, 23, 3220, 3220, 23, 1184, 1184, 3220, 1184, 25, 25, 23, 25}

func walReceiverInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatWALReceiverOID}
	return pgcatalog.Input{
		Context: context.Background(), Catalog: view, Source: view.MustRelation(address),
		Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{}),
	}
}

func TestPGStatWALReceiverCapture(t *testing.T) {
	e := engine.New()
	input := walReceiverInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatWALReceiverOID)
	if !ok {
		t.Fatal("missing walReceiver producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("truthful empty receivers", rows, err)
	}
	if got, err := catalogrt.CaptureRows(input, produce); err != nil || got == nil || got.Ref != input.Ref || got.Anonymous == nil || len(got.Anonymous) != 0 {
		t.Fatal("empty capture identity", got, err)
	}
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid input admitted", rows, err)
		}
		if got, err := catalogrt.CaptureRows(in, produce); got != nil || err == nil {
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
		for _, forge := range []func(*catalog.Column){
			func(c *catalog.Column) { c.Name = "forged" },
			func(c *catalog.Column) { c.Usage.Type, _ = input.Catalog.LookupType(sqltype.Bool) },
			func(c *catalog.Column) { c.Usage.Type, _ = foreign.CatalogRead().LookupType(column.Usage.Type.ID()) },
		} {
			in := input
			copySource := *input.Source
			copySource.Columns = append([]catalog.Column(nil), input.Source.Columns...)
			forge(&copySource.Columns[i])
			in.Source = &copySource
			reject(in)
		}
	}
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { copySource := *in.Source; in.Source = &copySource },
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Catalog.Epoch(), in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref.Address.ClassID = ids.TypeClassID },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
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
	if got, err := catalogrt.CaptureRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published", got, err)
	}
}

func TestPGStatWALReceiverOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatWALReceiverOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_stat_wal_receiver" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12242 || len(r.Columns) != 15 {
			t.Fatal("walReceiver relation contract", r)
		}
		if r.Owner.ID() != 10 || len(r.Security.ACL.Entries) != 1 {
			t.Fatal("owner/ACL", r.Owner, r.Security)
		}
		grant := r.Security.ACL.Entries[0]
		if !grant.Grantee.Public || grant.Grantor != r.Owner || grant.Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", grant)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12242})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12241})
		ownedRow, rowOK := view.LookupType(12242)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12241 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		for i, name := range walReceiverColumnNames {
			id, collation := walReceiverColumnTypes[i], uint32(0)
			if id == sqltype.Text {
				collation = 100
			}
			owned, ok := view.LookupType(id)
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || c.OriginTable != uint32(pgcatalog.StatWALReceiverOID) || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) {
				t.Fatal("attribute contract", i, c)
			}
		}
		return r
	}
	oldInput := walReceiverInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12242)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := walReceiverInput(e)
	fresh := check(freshInput.Catalog)
	if old == fresh {
		t.Fatal("reset reused descriptor")
	}
	for i := range old.Columns {
		if old.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("reset reused carrier", i)
		}
	}
	freshRow, _ := freshInput.Catalog.LookupType(12242)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("retained generation changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.StatWALReceiverOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		if rows, err := produce(in); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("valid generation capture", rows, err)
		}
		if got, err := catalogrt.CaptureRows(in, produce); err != nil || got == nil || got.Ref != in.Ref || got.Anonymous == nil || len(got.Anonymous) != 0 {
			t.Fatal("valid captured generation", got, err)
		}
	}
	for _, source := range []*catalog.Relation{old, walReceiverInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := catalogrt.CaptureRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale generation published", got, err)
		}
	}
}

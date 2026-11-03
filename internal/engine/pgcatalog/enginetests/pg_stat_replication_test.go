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

var replicationColumnNames = []string{"pid", "usesysid", "usename", "application_name", "client_addr", "client_hostname", "client_port", "backend_start", "backend_xmin", "state", "sent_lsn", "write_lsn", "flush_lsn", "replay_lsn", "write_lag", "flush_lag", "replay_lag", "sync_priority", "sync_state", "reply_time"}
var replicationColumnTypes = []sqltype.ID{23, 26, 19, 25, 869, 25, 23, 1184, 28, 25, 3220, 3220, 3220, 3220, 1186, 1186, 1186, 23, 25, 1184}

func replicationInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatReplicationOID}
	return pgcatalog.Input{
		Context: context.Background(), Catalog: view, Source: view.MustRelation(address),
		Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{}),
	}
}

func TestPGStatReplicationCapture(t *testing.T) {
	e := engine.New()
	input := replicationInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatReplicationOID)
	if !ok {
		t.Fatal("missing replication producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("truthful empty senders", rows, err)
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
	for _, width := range []int{19, 21} {
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
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Owner = nil },
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
	if got, err := catalogrt.CaptureRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published", got, err)
	}
}

func TestPGStatReplicationOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatReplicationOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		r := view.MustRelation(address)
		if r.Name() != "pg_stat_replication" || r.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || r.RelationKind != catalog.CatalogRelation || !r.VirtualView || !r.CatalogView || pgcatalog.ClassKind(view, r) != "v" || r.History.HasIndexes || r.RowType != 12233 || len(r.Columns) != 20 {
			t.Fatal("replication relation contract", r)
		}
		if r.Owner.ID() != 10 || len(r.Security.ACL.Entries) != 1 {
			t.Fatal("owner/ACL", r.Owner, r.Security)
		}
		grant := r.Security.ACL.Entries[0]
		if !grant.Grantee.Public || grant.Grantor != r.Owner || grant.Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", grant)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12233})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12232})
		ownedRow, rowOK := view.LookupType(12233)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || row.SQLType != ownedRow || array.SQLType != ownedArray || row.Array != 12232 || array.SQLType.Element() != ownedRow || row.OwningRelation != address || array.OwningRelation != address || row.Owner != r.Owner || array.Owner != r.Owner {
			t.Fatal("row/array ownership", row, array)
		}
		for i, name := range replicationColumnNames {
			id, collation := replicationColumnTypes[i], uint32(0)
			if id == sqltype.Text {
				collation = 100
			} else if id == sqltype.Name {
				collation = 950
			}
			owned, ok := view.LookupType(id)
			c := r.Columns[i]
			if !ok || c.Usage.Type != owned || c.Name != name || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || c.OriginTable != uint32(pgcatalog.StatReplicationOID) || c.OriginAttribute != int16(i+1) || r.Attributes[i] != int32(i+1) {
				t.Fatal("attribute contract", i, c)
			}
		}
		return r
	}
	oldInput := replicationInput(e)
	old := check(oldInput.Catalog)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldInput.Catalog.LookupType(12233)
	oldArray, _ := oldInput.Catalog.LookupArray(oldRow)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshInput := replicationInput(e)
	fresh := check(freshInput.Catalog)
	if old == fresh {
		t.Fatal("reset reused descriptor")
	}
	for i := range old.Columns {
		if old.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("reset reused carrier", i)
		}
	}
	freshRow, _ := freshInput.Catalog.LookupType(12233)
	freshArray, _ := freshInput.Catalog.LookupArray(freshRow)
	if oldRow == freshRow || oldArray == freshArray || oldArray.Element() != oldRow || !reflect.DeepEqual(old.Columns, columns) || check(oldInput.Catalog) != old {
		t.Fatal("retained generation changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.StatReplicationOID)
	for _, in := range []pgcatalog.Input{oldInput, freshInput} {
		if rows, err := produce(in); err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("valid generation capture", rows, err)
		}
	}
	for _, source := range []*catalog.Relation{old, replicationInput(engine.New()).Source} {
		in := freshInput
		in.Source = source
		if got, err := catalogrt.CaptureRows(in, produce); got != nil || err == nil {
			t.Fatal("foreign/stale generation published", got, err)
		}
	}
}

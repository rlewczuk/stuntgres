package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatSLRUProjection(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSLRUOID}
	source, ok := view.Relation(address)
	if !ok { t.Fatal("missing SLRU source") }
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatSLRUOID)
	if !ok { t.Fatal("missing producer") }
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	names := []string{"commit_timestamp", "multixact_member", "multixact_offset", "notify", "serializable", "subtransaction", "transaction", "other"}
	columns := []string{"name", "blks_zeroed", "blks_hit", "blks_read", "blks_written", "blks_exists", "flushes", "truncates", "stats_reset"}
	for i, column := range source.Columns {
		id, collation := sqltype.Int8, uint32(0)
		if i == 0 { id, collation = sqltype.Text, 100 }
		if i == 8 { id = sqltype.Timestamptz }
		owned, _ := view.LookupType(id)
		if column.Name != columns[i] || column.Usage.Type != owned || column.Usage.Collation.ID != collation || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown { t.Fatal("exact attributes", i, column) }
	}
	at := time.Unix(100, 123456000).UTC()
	vector := [7]int64{11, 23, 37, 41, 53, 67, 79}
	check := func(input pgcatalog.Input, want [7]int64, stamp time.Time) {
		t.Helper()
		before := input.SLRU
		captured, err := catalogrt.CaptureRows(input, produce)
		if err != nil || captured == nil || captured.Ref != input.Ref { t.Fatal(captured, err) }
		rows := captured.Anonymous
		if len(rows) != 8 { t.Fatal("eight categories", rows) }
		for k, row := range rows {
			if len(row) != 9 { t.Fatal("nine fields", row) }
			name, err := row[0].Text()
			if err != nil || name != names[k] { t.Fatal("fixed unique category", k, name, err) }
			for j, n := range want {
				got, err := row[j+1].Integer()
				if err != nil || got != n || row[j+1].Type() != source.Columns[j+1].Usage.Type { t.Fatal("positive named slot", columns[j+1], got, n, err) }
			}
			if stamp.IsZero() {
				if !row[8].IsNull() { t.Fatal("invented reset history") }
			} else if got, err := row[8].TimestampTime(); err != nil || !got.Equal(stamp) { t.Fatal("genuine reset conversion", got, stamp, err) }
		}
		if input.SLRU != before { t.Fatal("projection mutated input") }
	}
	check(in, [7]int64{}, time.Time{})
	for j, n := range vector {
		var one [7]int64
		one[j] = n
		for k := range in.SLRU { in.SLRU[k] = statistics.SLRUFact{Counters: one, ResetAt: at} }
		check(in, one, at)
	}
	for k := range in.SLRU { in.SLRU[k] = statistics.SLRUFact{Counters: vector, ResetAt: at} }
	check(in, vector, at)
	foreign := engine.New()
	for _, change := range []func(*pgcatalog.Input){
		func(v *pgcatalog.Input) { v.Owner = nil }, func(v *pgcatalog.Input) { v.Source = nil },
		func(v *pgcatalog.Input) { v.Catalog = foreign.CatalogRead() }, func(v *pgcatalog.Input) { v.Ref.Epoch++ },
		func(v *pgcatalog.Input) { v.Roles = authorization.Read{} },
	} {
		bad := in; change(&bad)
		if rows, err := produce(bad); err == nil || rows != nil { t.Fatal("invalid authority accepted", rows, err) }
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:8] },
		func(r *catalog.Relation) { r.Columns[7].Name = "forged" },
		func(r *catalog.Relation) { r.Columns[0].Usage.Collation.ID = 0 },
		func(r *catalog.Relation) { r.Columns[8].Nullable = false },
	} {
		state, copied := *view.State(), *source
		copied.Columns = slices.Clone(source.Columns); change(&copied)
		state.SetDescriptor(address, &copied)
		bad := in; bad.Catalog, bad.Source = e.Catalog.Read(&state), &copied
		if rows, err := produce(bad); err == nil || rows != nil { t.Fatal("invalid attribute accepted", rows, err) }
	}
	ctx, cancel := context.WithCancel(t.Context()); cancel(); in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) { t.Fatal("canceled projection", rows, err) }
}

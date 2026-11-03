package enginetests_test

import (
	"context"
	"errors"
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

func TestPGStatCheckpointerProjection(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatCheckpointerOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing registered checkpointer")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatCheckpointerOID)
	if !ok {
		t.Fatal("missing checkpointer producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	var provider statistics.Provider
	check := func(fact statistics.SharedFact) [][]sqltype.Value {
		t.Helper()
		in.Checkpointer = fact
		rows, err := produce(in)
		if err != nil || len(rows) != 1 || len(rows[0]) != 11 {
			t.Fatal("eleven-cell singleton", rows, err)
		}
		for i, cell := range rows[0] {
			if cell.Type() != source.Columns[i].Usage.Type {
				t.Fatal("captured descriptor ownership", i, cell)
			}
		}
		for slot, column := range [...]int{0, 1, 2, 3, 4, 5, 8, 9} {
			if n, err := rows[0][column].Integer(); err != nil || n != fact.Counters[slot] {
				t.Fatal("counter slots", column, n, err)
			}
		}
		for i, duration := range [...]time.Duration{fact.WriteTime, fact.SyncTime} {
			if got, err := rows[0][6+i].Float(); err != nil || got != float64(duration)/float64(time.Millisecond) {
				t.Fatal("independent fractional milliseconds", i, got, err)
			}
		}
		cell := rows[0][10]
		if cell.IsNull() != fact.ResetAt.IsZero() {
			t.Fatal("reset absence", cell, fact.ResetAt)
		}
		if !fact.ResetAt.IsZero() {
			if got, err := cell.TimestampTime(); err != nil || !got.Equal(fact.ResetAt) {
				t.Fatal("exact reset instant", got, fact.ResetAt, err)
			}
		}
		return rows
	}
	check(provider.ReadShared(statistics.Checkpointer))
	if provider.ReadShared(statistics.Checkpointer) != (statistics.SharedFact{}) {
		t.Fatal("read initialized zero provider")
	}
	at := time.Date(2025, 7, 8, 9, 10, 11, 123456000, time.FixedZone("event", 7200))
	provider.ResetShared(statistics.Checkpointer, at)
	var writer statistics.Writer
	writer.Shared(&provider, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{11, 13, 17, 19, 23, 29, 31, 37}, WriteTime: 1250 * time.Microsecond, SyncTime: 2750 * time.Microsecond, TotalTime: 9 * time.Second, LastSuccess: "00000001000000000000002A", LastSuccessAt: at, LastFailure: "00000001000000000000002B", LastFailureAt: at.Add(time.Second)})
	writer.Flush(&provider, at, true)
	fact := provider.ReadShared(statistics.Checkpointer)
	old := check(fact)
	writer.Shared(&provider, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{1, 2, 3, 4, 5, 6, 7, 8}, WriteTime: 500 * time.Microsecond, SyncTime: 250 * time.Microsecond})
	writer.Flush(&provider, at.Add(time.Second), true)
	expected := fact
	expected.Counters = [8]int64{12, 15, 20, 23, 28, 35, 38, 45}
	expected.WriteTime, expected.SyncTime = 1750*time.Microsecond, 3*time.Millisecond
	if got := provider.ReadShared(statistics.Checkpointer); got != expected {
		t.Fatal("complete additive fact", got, expected)
	}
	check(expected)
	check(statistics.SharedFact{LastSuccess: fact.LastSuccess, LastFailureAt: fact.LastFailureAt})
	provider.ResetShared(statistics.Checkpointer, at.Add(2*time.Second))
	check(provider.ReadShared(statistics.Checkpointer))
	for i, column := range [...]int{0, 1, 2, 3, 4, 5, 8, 9} {
		want := ([8]int64{11, 13, 17, 19, 23, 29, 31, 37})[i]
		if n, _ := old[0][column].Integer(); n != want {
			t.Fatal("reset mutated detached counter", i, n, want)
		}
	}
	if got, err := old[0][10].TimestampTime(); err != nil || !got.Equal(at) {
		t.Fatal("reset mutated detached reset metadata", got, err)
	}
	in.Checkpointer = fact
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil || captured.Ref != in.Ref || &captured.Anonymous[0][0] == &old[0][0] {
		t.Fatal("common capture identity/backing", captured, err)
	}
	reject := func(wrong pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(wrong); rows != nil || err == nil {
			t.Fatal("invalid authority published", rows, err)
		}
	}
	foreign := engine.New()
	for _, change := range []func(*pgcatalog.Input){
		func(v *pgcatalog.Input) { v.Source = nil },
		func(v *pgcatalog.Input) { v.Owner = nil },
		func(v *pgcatalog.Input) { v.Owner = foreign.CatalogRead().Owner() },
		func(v *pgcatalog.Input) { v.Catalog = catalog.Read{} },
		func(v *pgcatalog.Input) {
			v.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
		func(v *pgcatalog.Input) { v.Ref.Epoch++ },
		func(v *pgcatalog.Input) { v.Ref.Address.ObjectID++ },
		func(v *pgcatalog.Input) { copied := *v.Source; v.Source = &copied },
	} {
		wrong := in
		change(&wrong)
		reject(wrong)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:10] },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 100 },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type = nil },
		func(r *catalog.Relation) { r.Columns[1].Nullable = false },
		func(r *catalog.Relation) { r.Columns[1].DeclaredDimensions = 1 },
	} {
		state, copied := *view.State(), *source
		copied.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&copied)
		state.SetDescriptor(address, &copied)
		wrong := in
		wrong.Catalog, wrong.Source = e.Catalog.Read(&state), &copied
		reject(wrong)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context, in.Source = ctx, nil
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary cancellation without partial row", rows, err)
	}
}

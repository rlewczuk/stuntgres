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

func TestPGStatBGWriterProjection(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatBGWriterOID}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing registered bgwriter")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatBGWriterOID)
	if !ok {
		t.Fatal("missing bgwriter producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	var provider statistics.Provider
	check := func(fact statistics.SharedFact) [][]sqltype.Value {
		t.Helper()
		in.BGWriter = fact
		rows, err := produce(in)
		if err != nil || len(rows) != 1 || len(rows[0]) != 4 {
			t.Fatal("four-cell singleton", rows, err)
		}
		for i, cell := range rows[0] {
			if cell.Type() != source.Columns[i].Usage.Type {
				t.Fatal("captured descriptor ownership", i, cell)
			}
		}
		for column := range 3 {
			if n, err := rows[0][column].Integer(); err != nil || n != fact.Counters[column] {
				t.Fatal("counter slots", column, n, err)
			}
		}
		cell := rows[0][3]
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
	check(provider.ReadShared(statistics.BGWriter))
	if provider.ReadShared(statistics.BGWriter) != (statistics.SharedFact{}) {
		t.Fatal("read initialized zero provider")
	}
	at := time.Date(2025, 7, 8, 9, 10, 11, 123456000, time.FixedZone("event", 7200))
	provider.ResetShared(statistics.BGWriter, at)
	var writer statistics.Writer
	writer.Shared(&provider, statistics.BGWriter, statistics.SharedFact{Counters: [6]int64{3, 5, 7, 91, 92, 93}, LastSuccess: "00000001000000000000002A", LastSuccessAt: at, LastFailure: "00000001000000000000002B", LastFailureAt: at.Add(time.Second)})
	writer.Flush(&provider, at, true)
	fact := provider.ReadShared(statistics.BGWriter)
	old := check(fact)
	check(statistics.SharedFact{LastSuccess: fact.LastSuccess, LastFailureAt: fact.LastFailureAt})
	provider.ResetShared(statistics.BGWriter, at.Add(2*time.Second))
	check(provider.ReadShared(statistics.BGWriter))
	for i, want := range [...]int64{3, 5, 7} {
		if n, _ := old[0][i].Integer(); n != want {
			t.Fatal("reset mutated detached counter", i, n, want)
		}
	}
	if got, err := old[0][3].TimestampTime(); err != nil || !got.Equal(at) {
		t.Fatal("reset mutated detached reset metadata", got, err)
	}
	in.BGWriter = fact
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
		func(r *catalog.Relation) { r.Columns = r.Columns[:3] },
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
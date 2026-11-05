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

func TestPGStatWALProjection(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatWALOID}
	source, ok := view.Relation(address)
	if !ok || source.View != nil || source.CatalogReturnRule != pgcatalog.StatWALRuleOID || source.ReturnRuleOID() != pgcatalog.StatWALRuleOID {
		t.Fatal("missing registered WAL source or owned return rule")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatWALOID)
	if !ok {
		t.Fatal("missing WAL producer")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	for _, pretty := range []bool{false, true} {
		seen := false
		pgcatalog.BuiltinViewDefinition(pgcatalog.StatWALOID, pretty, nil, func(name pgcatalog.BuiltinViewName) string {
			if name.Class == ids.ProcedureClassID {
				if name.Namespace != ids.PGCatalogNamespaceOID || name.Name != "pg_stat_get_wal" || name.Signature != "" || name.RoutineOID != 1136 {
					t.Fatal("lost exact backing procedure dependency", name)
				}
				seen = true
			}
			return name.Name
		})
		if !seen {
			t.Fatal("symbolic definition omitted backing procedure")
		}
	}
	var provider statistics.Provider
	check := func(fact statistics.SharedFact) [][]sqltype.Value {
		t.Helper()
		in.WAL = fact
		rows, err := produce(in)
		if err != nil || len(rows) != 1 || len(rows[0]) != 5 {
			t.Fatal("five-cell singleton", rows, err)
		}
		for i, cell := range rows[0] {
			if cell.Type() != source.Columns[i].Usage.Type {
				t.Fatal("captured descriptor ownership", i, cell)
			}
		}
		for _, column := range [...]int{0, 1, 3} {
			if n, err := rows[0][column].Integer(); err != nil || n != 0 {
				t.Fatal("physical absence formula ignores all unrelated slots", column, n, err)
			}
		}
		zero, err := sqltype.NumericUint64Value(source.Columns[2].Usage.Type, 0)
		if err != nil || rows[0][2] != zero {
			t.Fatal("exact NUMERIC zero and descriptor", rows[0][2], zero, err)
		}
		cell := rows[0][4]
		if cell.IsNull() != fact.ResetAt.IsZero() {
			t.Fatal("only genuine missing reset is NULL", cell, fact.ResetAt)
		}
		if !fact.ResetAt.IsZero() {
			if got, err := cell.TimestampTime(); err != nil || !got.Equal(fact.ResetAt) {
				t.Fatal("exact genuine reset instant", got, fact.ResetAt, err)
			}
		}
		return rows
	}
	check(provider.ReadShared(statistics.SharedWAL))
	if provider.ReadShared(statistics.SharedWAL) != (statistics.SharedFact{}) {
		t.Fatal("read initialized zero provider")
	}
	at := time.Date(2025, 7, 8, 9, 10, 11, 123456000, time.FixedZone("event", 7200))
	// The producer fixture deliberately fills unrelated fields; these are not
	// accepted WAL events and cannot change the documented zero formula.
	provider.ResetShared(statistics.SharedWAL, at)
	fact := provider.ReadShared(statistics.SharedWAL)
	fact.Counters = [8]int64{3, 5, 7, 11, 13, 17, 19, 23}
	fact.TotalTime, fact.WriteTime, fact.SyncTime = time.Second, time.Millisecond, time.Microsecond
	fact.LastSuccess, fact.LastFailure = "unrelated-success", "unrelated-failure"
	fact.LastSuccessAt, fact.LastFailureAt = at.Add(time.Second), at.Add(2*time.Second)
	old := check(fact)
	check(statistics.SharedFact{Counters: fact.Counters, LastSuccess: fact.LastSuccess})
	provider.ResetShared(statistics.SharedWAL, at.Add(3*time.Second))
	check(provider.ReadShared(statistics.SharedWAL))
	if got, err := old[0][4].TimestampTime(); err != nil || !got.Equal(at) {
		t.Fatal("reset mutated detached reset row", got, err)
	}
	in.WAL = fact
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
		func(r *catalog.Relation) { r.CatalogReturnRule++ },
		func(r *catalog.Relation) { r.Columns = r.Columns[:4] },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 100 },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
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

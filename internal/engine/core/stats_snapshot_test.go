package core_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func configureStats(s *session.Session, mode string) {
	s.Settings.Mu.Lock()
	before := s.Settings.Value("stats_fetch_consistency")
	s.Settings.Values["stats_fetch_consistency"] = mode
	s.Settings.Typed["stats_fetch_consistency"] = settings.Value{Text: mode}
	s.Settings.StatsChanged(before)
	s.Settings.Mu.Unlock()
}
func TestStatsSnapshotLifecycleAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE stats_owner(id integer)")
	ref := enginetest.RoleTableRef(t, e, "stats_owner")
	key := statistics.Key{Object: ref}
	var writer statistics.Writer
	writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 1})
	writer.Flush(&e.Statistics, time.Now(), true)
	configureStats(s, "snapshot")
	tx := enginetest.TransactionBegin(t, s)
	op := enginetest.TransactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	stampType, voidType := enginetest.Usage(e, sqltype.Timestamptz).Type, enginetest.Usage(e, sqltype.Void).Type
	value, err := ctx.StatsSnapshotValue(false, stampType)
	if err != nil || !value.IsNull() {
		t.Fatal("eager timestamp", value, err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
	if runtime.TableStatistics(ref.Address).Inserts != 1 {
		t.Fatal("initial published fact")
	}
	first, err := ctx.StatsSnapshotValue(false, stampType)
	if err != nil || first.IsNull() {
		t.Fatal("missing lazy timestamp", err)
	}
	writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 2})
	writer.Flush(&e.Statistics, time.Now(), true)
	if runtime.TableStatistics(ref.Address).Inserts != 1 {
		t.Fatal("snapshot consulted live provider")
	}
	configureStats(s, "none")
	configureStats(s, "snapshot")
	value, err = ctx.StatsSnapshotValue(false, stampType)
	if err != nil || !value.IsNull() {
		t.Fatal("roundtrip generation not invalidated")
	}
	if runtime.TableStatistics(ref.Address).Inserts != 3 {
		t.Fatal("generation retained stale facts")
	}
	value, err = ctx.StatsSnapshotValue(true, voidType)
	if err != nil || value.IsNull() || value.Type() != voidType {
		t.Fatal("clear VOID", err)
	}
	if e.Statistics.Read(key).Inserts != 3 {
		t.Fatal("clear reset provider")
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	terminal := enginetest.TransactionOperation(t, s)
	if _, err = e.Rollback(terminal, tx); err != nil {
		t.Fatal(err)
	}
	terminal.Done()
	// A new incarnation and a peer cannot inherit the former reader or authority.
	for _, actor := range []*session.Session{s, peer} {
		op := enginetest.TransactionOperation(t, actor)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
		v, err := ctx.StatsSnapshotValue(false, stampType)
		if err != nil || !v.IsNull() {
			t.Fatal("terminal/peer reader retained", err)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
	}
	statsBulkLifecycle(t)
}
func TestStatsSnapshotSourceSelection(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot", "none"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE stats_select_x(id integer)")
			enginetest.SessionSQL(t, s, "CREATE TABLE stats_select_y(id integer)")
			x, y := enginetest.RoleTableRef(t, e, "stats_select_x"), enginetest.RoleTableRef(t, e, "stats_select_y")
			configureStats(s, mode)
			var w statistics.Writer
			w.Add(&e.Statistics, statistics.Key{Object: x}, statistics.Fact{Inserts: 1})
			w.Flush(&e.Statistics, time.Now(), true)
			enginetest.TransactionBegin(t, s)
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			read := auth.Catalog
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatUserTablesOID}
			source := read.MustRelation(address)
			runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
			produce, ok := pgcatalog.ProducerFor(pgcatalog.StatUserTablesOID)
			if !ok {
				t.Fatal("missing producer")
			}
			input := pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: source, Runtime: runtime, Keep: pgcatalog.Keep{Column: 2, Test: func(v sqltype.Value) bool { name, err := v.Text(); return err != nil || name == "stats_select_x" }}}
			rows, err := produce(input)
			if err != nil || len(rows) != 1 {
				t.Fatal("filtered source", len(rows), err)
			}
			retained := rows[0][9]
			w.Add(&e.Statistics, statistics.Key{Object: y}, statistics.Fact{Inserts: 2})
			w.Add(&e.Statistics, statistics.Key{Object: x}, statistics.Fact{Inserts: 2})
			w.Flush(&e.Statistics, time.Now(), true)
			want := int64(2)
			if mode == "snapshot" {
				want = 0
			}
			if got := runtime.TableStatistics(y.Address).Inserts; got != want {
				t.Fatal("rejected Y was fetched before Keep", got, want)
			}
			if n, err := retained.Integer(); err != nil || n != 1 {
				t.Fatal("retained row mutated", n, err)
			}
			ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
			if _, err = ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
				t.Fatal(err)
			}
			rows, err = produce(input)
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := rows[0][9].Integer(); n != 3 {
				t.Fatal("source shared a stale dynamic capture", n)
			}
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestStatsSnapshotMaintenanceSeparation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE stats_physical(id integer)")
	ref := enginetest.RoleTableRef(t, e, "stats_physical")
	enginetest.SessionSQL(t, s, "SET track_counts=off")
	enginetest.SessionSQL(t, s, "INSERT INTO stats_physical VALUES (1),(2)")
	enginetest.SessionSQL(t, s, "DELETE FROM stats_physical WHERE id=1")
	enginetest.SessionSQL(t, s, "ANALYZE stats_physical")
	enginetest.SessionSQL(t, s, "VACUUM stats_physical")
	e.Mu.Lock()
	store := e.Storage.Relations[ref.Address]
	metadata := e.Storage.RelationMetadata[ref.Address]
	if store == nil || !store.Maintenance.Scanned || store.Maintenance.Tuples != 1 || store.Maintenance.Live != 1 || store.Maintenance.Dead != 0 || metadata == nil || metadata.AnalyzeCount != 1 {
		t.Fatal("suppression disabled physical maintenance", store, metadata)
	}
	e.Mu.Unlock()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f := e.Statistics.Read(statistics.Key{Object: ref}); f.Inserts != 0 || f.Deletes != 0 || f.VacuumCount != 0 || f.AnalyzeCount != 0 || !f.LastVacuum.IsZero() || !f.LastAnalyze.IsZero() {
		t.Fatal("disabled interval collected facts", f)
	}
}

package core_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"testing/synctest"
	"time"
)

// The generic test exporter intentionally omits TIMESTAMPTZ/VOID. Consume the
// actual typed database inquiry without changing its cumulative demand point.
func databaseResetCaptureDatabase(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, enginetest.Statement(t, "SELECT stats_reset FROM pg_stat_database WHERE datname=current_database()"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows == nil {
		t.Fatal("database inquiry returned no typed cursor")
	}
	defer result.Rows.Close()
	var values [1]sqltype.Value
	present, err := result.Rows.Next(op.Context(), values[:])
	if err != nil || !present || values[0].IsNull() || values[0].Type() == nil || values[0].Type().ID() != sqltype.Timestamptz {
		t.Fatal("database reset timestamp inquiry", present, values, err)
	}
	if present, err := result.Rows.Next(op.Context(), values[:]); err != nil || present {
		t.Fatal("database inquiry cardinality", present, err)
	}
}

func TestStatsResetDatabaseCacheLifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"relation", "database"} {
			t.Run(mode+"_"+first, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					x := databaseResetFixtureOf(t)
					observer := enginetest.StartSession(t, x.e, nil)
					databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{x.a: {Inserts: 5}, x.db: {Database: statistics.DatabaseFact{Inserts: 7, ResetAt: time.Unix(100, 0)}}, x.sharedDB: {Database: statistics.DatabaseFact{Inserts: 13, ResetAt: time.Unix(100, 0)}}})
					statsFunctionExec(t, observer, "SET stats_fetch_consistency="+mode, "BEGIN")
					if first == "database" {
						databaseResetCaptureDatabase(t, x.e, observer)
					}
					sharedStatsCapture(t, x.e, observer, x.a.Object)
					owned := reflect.ValueOf(core.PayloadOf(observer)).Elem().FieldByName("statsReader")
					cache := owned.FieldByName("cache")
					backing := cache.UnsafePointer()
					stamp := reflect.ValueOf(*core.PayloadOf(observer)).FieldByName("statsReader").FieldByName("stamp")
					if mode == "snapshot" && (!owned.FieldByName("full").Bool() || !cache.MapIndex(reflect.ValueOf(x.sharedDB)).IsValid() || !cache.MapIndex(reflect.ValueOf(x.peerDB)).IsValid()) {
						t.Fatal("incomplete actual capture domain")
					}
					if mode == "snapshot" {
						entry := cache.MapIndex(reflect.ValueOf(x.sharedDB))
						if !entry.FieldByName("exists").Bool() { t.Fatal("shared database fact lost captured presence") }
						shared := entry.FieldByName("fact").FieldByName("Database")
						if shared.FieldByName("Inserts").Int() != 13 || !shared.FieldByName("ResetAt").Equal(reflect.ValueOf(time.Unix(100, 0))) {
							t.Fatal("shared timestamp not in original first-access cut")
						}
					}
					databaseResetCall(t, x.s, "pg_stat_reset_single_table_counters", x.a)
					resetStatsExec(t, observer, "SELECT pg_stat_reset()")
					if cache.UnsafePointer() != backing || !owned.FieldByName("stamp").Equal(stamp) {
						t.Fatal("SQL reset changed Reader backing")
					}
					if mode != "none" && cache.MapIndex(reflect.ValueOf(x.a)).FieldByName("fact").FieldByName("Inserts").Int() != 5 {
						t.Fatal("old object capture lost")
					}
					resetStatsExec(t, observer, "SELECT pg_stat_clear_snapshot()")
					if owned.FieldByName("cache").Len() != 0 || owned.FieldByName("full").Bool() {
						t.Fatal("clear did not release capture")
					}
					sharedStatsCapture(t, x.e, observer, x.a.Object)
					if mode != "none" && owned.FieldByName("cache").MapIndex(reflect.ValueOf(x.a)).FieldByName("fact").FieldByName("Inserts").Int() != 0 {
						t.Fatal("new capture stale")
					}
					statsFunctionExec(t, observer, "ROLLBACK", "BEGIN")
					resetStatsExec(t, observer, "SELECT pg_stat_reset()")
					if owned.FieldByName("full").Bool() || !owned.FieldByName("stamp").IsZero() {
						t.Fatal("reset eagerly created snapshot")
					}
					statsFunctionExec(t, observer, "ROLLBACK")
				})
			})
		}
	}
}
func TestStatsResetDatabasePendingLifetime(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "savepoint", "prepared_commit", "prepared_rollback"} {
		t.Run(finish, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				x := databaseResetFixtureOf(t)
				resetter := enginetest.StartSession(t, x.e, nil)
				statsFunctionExec(t, x.s, "SET track_functions=pl", "INSERT INTO database_reset_a VALUES(1),(2),(3),(4),(5)")
				// Drain the baseline through the actual physical owner, not a provider seed.
				statsFunctionRetire(t, x.s)
				x.s = enginetest.StartSession(t, x.e, nil)
				statsFunctionExec(t, x.s, "SET track_functions=pl", "BEGIN", "INSERT INTO database_reset_a VALUES(6),(7)", "SELECT database_reset_f()")
				if x.e.Statistics.Read(x.a).Inserts != 5 || x.e.Statistics.Read(x.f).Calls != 0 {
					t.Fatal("unresolved ledger published")
				}
				writer := reflect.ValueOf(core.PayloadOf(x.s)).Elem().FieldByName("statsWriter")
				pending := writer.FieldByName("pending").UnsafePointer()
				if writer.FieldByName("pending").MapIndex(reflect.ValueOf(x.f)).FieldByName("Calls").Int() != 1 {
					t.Fatal("actual completed PL call not pending")
				}
				owner := x.s.Transactions
				ledger := owner.Stats.Clone()
				ledgerBacking := reflect.ValueOf(owner.Stats).UnsafePointer()
				if finish == "savepoint" {
					statsFunctionExec(t, x.s, "SAVEPOINT reset_lifetime")
				}
				resetStatsExec(t, resetter, "SELECT pg_stat_reset()")
				if x.e.Statistics.Read(x.a).Inserts != 0 || x.s.Transactions != owner || writer.FieldByName("pending").UnsafePointer() != pending || !reflect.DeepEqual(owner.Stats, ledger) || reflect.ValueOf(owner.Stats).UnsafePointer() != ledgerBacking {
					t.Fatal("reset changed ledger/writer ownership")
				}
				if finish == "savepoint" {
					statsFunctionExec(t, x.s, "ROLLBACK TO reset_lifetime")
				}
				switch finish {
				case "prepared_commit", "prepared_rollback":
					t.Cleanup(func() {
						if core.EnginePreparedRef(x.e).Lookup("database_reset_pending") != nil {
							statsFunctionExec(t, resetter, "ROLLBACK PREPARED 'database_reset_pending'")
						}
					})
					statsFunctionExec(t, x.s, "PREPARE TRANSACTION 'database_reset_pending'")
					if x.e.Statistics.Read(x.a).Inserts != 0 {
						t.Fatal("PREPARE settled ledger")
					}
					command := "COMMIT PREPARED 'database_reset_pending'"
					if finish == "prepared_rollback" {
						command = "ROLLBACK PREPARED 'database_reset_pending'"
					}
					statsFunctionExec(t, resetter, command)
				case "rollback":
					statsFunctionExec(t, x.s, "ROLLBACK")
				default:
					statsFunctionExec(t, x.s, "COMMIT")
				}
				statsFunctionRetire(t, x.s)
				statsFunctionRetire(t, resetter)
				if x.e.Statistics.Read(x.a).Inserts != 2 || x.e.Statistics.Read(x.db).Database.Inserts != 2 || x.e.Statistics.Read(x.f).Calls != 1 {
					t.Fatal("late settlement lost/duplicated", x.e.Statistics.Read(x.a), x.e.Statistics.Read(x.db), x.e.Statistics.Read(x.f))
				}
			})
		})
	}
	t.Run("active_function", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			x := databaseResetFixtureOf(t)
			statsFunctionExec(t, x.s, "CREATE FUNCTION database_reset_active() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.100); RETURN 7; END$$", "SET track_functions=pl")
			key := statsFunctionKey(t, x.e, "database_reset_active")
			databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{key: {Calls: 5}})
			statement := enginetest.Statement(t, "SELECT database_reset_active()")
			done := make(chan error, 1)
			scopes := make(chan *core.StatementScope, 1)
			callContext, cancel := context.WithCancel(context.Background())
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			go func() {
				op, err := x.s.Acquire(callContext)
				if err != nil {
					scopes <- nil
					done <- err
					return
				}
				defer op.Done()
				scope, err := x.e.StartStatement(op)
				if err != nil {
					scopes <- nil
					done <- err
					return
				}
				scopes <- scope
				r, err := scope.Execute(statement, 0, nil)
				err = scope.Finish(err)
				if err == nil && r.Rows != nil {
					enginetest.Tuples(r)
					err = r.Rows.Close()
				}
				done <- err
			}()
			scope := <-scopes
			if scope == nil {
				failure := <-done
				joined = true
				t.Fatal(failure)
			}
			synctest.Wait()
			// Wait establishes admission before observation. Materialize the
			// immutable frame object now, not a live handle to the scope's
			// mutable stack slot, which completion will pop concurrently.
			frame := reflect.ValueOf(scope).Elem().FieldByName("statsFunction").Elem()
			if !frame.IsValid() {
				t.Fatal("genuine active PL frame absent")
			}
			framePointer := frame.Addr().Pointer()
			usage := frame.FieldByName("usage")
			generation := usage.FieldByName("generation").Uint()
			total, self := usage.FieldByName("total").Int(), usage.FieldByName("chargedSelf").Int()
			resetter := enginetest.StartSession(t, x.e, nil)
			databaseResetCall(t, resetter, "pg_stat_reset_single_function_counters", key)
			if x.e.Statistics.Read(key).Calls != 0 || frame.Addr().Pointer() != framePointer || usage.FieldByName("generation").Uint() != generation || usage.FieldByName("total").Int() != total || usage.FieldByName("chargedSelf").Int() != self {
				t.Fatal("reset changed active frame/baselines")
			}
			failure := <-done
			joined = true
			if failure != nil {
				t.Fatal(failure)
			}
			// The completion channel now owns the happens-before edge for
			// inspecting the mutable scope slot again.
			if !reflect.ValueOf(scope).Elem().FieldByName("statsFunction").IsNil() {
				t.Fatal("completed function retained active stack ownership")
			}
			statsFunctionRetire(t, x.s)
			if got := x.e.Statistics.Read(key); got.Calls != 1 || got.TotalTime != 100*time.Millisecond || got.SelfTime != 100*time.Millisecond {
				t.Fatal("active completion lost", got)
			}
		})
	})
	t.Run("pl_exception", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			x := databaseResetFixtureOf(t)
			statsFunctionExec(t, x.s, "INSERT INTO database_reset_a VALUES(1),(2),(3),(4),(5)")
			statsFunctionRetire(t, x.s)
			x.s = enginetest.StartSession(t, x.e, nil)
			statsFunctionExec(t, x.s, "CREATE FUNCTION database_reset_exception() RETURNS integer LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM pg_stat_reset_single_table_counters('database_reset_a'::regclass::oid); RAISE EXCEPTION 'discard reset'; EXCEPTION WHEN others THEN PERFORM 1; END; RETURN 7; END$$", "SELECT database_reset_exception()")
			if x.e.Statistics.Read(x.a).Inserts != 0 || x.e.Statistics.Read(x.db).Database.ResetAt.IsZero() {
				t.Fatal("PL exception rolled back published reset")
			}
		})
	})
	for _, control := range []string{"reset", "restore"} {
		t.Run("native_"+control, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				x := databaseResetFixtureOf(t)
				statsFunctionExec(t, x.s, "INSERT INTO database_reset_a VALUES(1),(2),(3),(4),(5)")
				statsFunctionRetire(t, x.s)
				x.s = enginetest.StartSession(t, x.e, nil)
				if got := x.e.Statistics.Read(x.a); got.Inserts != 5 {
					t.Fatal("actual published baseline absent", got)
				}
				// Attachment alone does not promise a publication boundary. This
				// real command publishes the admitted database batch and establishes
				// this same writer's throttle before the measured tuple mutation.
				at := time.Now()
				statsFunctionExec(t, x.s, "SELECT 1")
				writer := reflect.ValueOf(core.PayloadOf(x.s)).Elem().FieldByName("statsWriter")
				if writer.FieldByName("last").IsZero() {
					t.Fatal("real command did not establish writer publication frontier")
				}
				statsFunctionExec(t, x.s, "INSERT INTO database_reset_a VALUES(6),(7)")
				if !time.Now().Equal(at) || x.e.Statistics.Read(x.a).Inserts != 5 {
					t.Fatal("measured tuple prefix crossed the same-writer throttle interval")
				}
				pending := reflect.ValueOf(core.PayloadOf(x.s)).Elem().FieldByName("statsWriter").FieldByName("pending").MapIndex(reflect.ValueOf(x.a))
				if !pending.IsValid() || pending.FieldByName("Inserts").Int() != 2 {
					t.Fatal("actual native pending prefix absent")
				}
				retained := enginetest.SessionSQL(t, x.s, "SELECT v FROM database_reset_a ORDER BY v")
				var snapshot statistics.Reader
				snapshot.Configure("snapshot", 1)
				old := snapshot.Read(&x.e.Statistics, x.a, time.Now())
				if old.Inserts != 5 || !statsLifetimeSnapshotContains(&snapshot, x.a) {
					t.Fatal("retained published snapshot did not exclude pending two", old)
				}
				independent := engine.New()
				independentSession := enginetest.StartSession(t, independent, nil)
				statsFunctionExec(t, independentSession, "CREATE TABLE independent_reset_rows(v integer)", "INSERT INTO independent_reset_rows VALUES(1)")
				independentKey := statistics.Key{Object: enginetest.RoleTableRef(t, independent, "independent_reset_rows"), Category: statistics.Relation}
				statsFunctionRetire(t, independentSession)
				foreign := independent.Statistics.Read(independentKey)
				generation := x.e.Statistics.Generation()
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), x.e, &dump); err != nil {
					t.Fatal(err)
				}
				var err error
				if control == "reset" {
					err = engine.Reset(context.Background(), x.e)
				} else {
					err = engine.Restore(context.Background(), x.e, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				if x.e.Statistics.Generation() <= generation || snapshot.Read(&x.e.Statistics, x.a, time.Now()) != old || independent.Statistics.Read(independentKey) != foreign {
					t.Fatal("native generation/capture/foreign authority")
				}
				rows := enginetest.Tuples(retained)
				if len(rows) != 7 {
					t.Fatal("replacement changed retained rows", rows)
				}
				retained.Rows.Close()
				statsFunctionRetire(t, x.s)
				if x.e.Statistics.Read(x.a).Inserts != 0 {
					t.Fatal("native replacement accepted old actual batch")
				}
				if control == "restore" {
					next := statistics.Key{Object: enginetest.RoleTableRef(t, x.e, "database_reset_a"), Category: statistics.Relation}
					if next.Object.Epoch == x.a.Object.Epoch || next.Object.Domain() != x.a.Object.Domain() || x.e.Statistics.Read(next).Inserts != 0 {
						t.Fatal("restored successor adopted pending prefix")
					}
				}
			})
		})
	}
}

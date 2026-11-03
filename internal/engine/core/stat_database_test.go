package core_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
	"time"
)

func TestStatDatabaseCatalogIdentities(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	relation := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12212})
	row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12272})
	array := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12271})
	if relation.Address().ObjectID != pgcatalog.StatDatabaseOID || relation.Name() != "pg_stat_database" || relation.RowType != 12272 || row.Array != 12271 || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("database view row/array ownership", relation, row, array)
	}
	if rule := relation.ReturnRuleOID(); rule == 0 || rule != pgcatalog.StatDatabaseRuleOID || rule >= e.Catalog.NextOID() {
		t.Fatal("database view return-rule reservation", rule, e.Catalog.NextOID())
	}
	// Check the complete installed catalog, not only neighboring view constants:
	// a rule must differ from every descriptor and every other owned definition.
	if err := catalog.ValidateDefinitionOIDs(read); err != nil {
		t.Fatal("database view installed with colliding definition identities", err)
	}
}

func statDatabaseRuntimeFact(e *core.Engine) statistics.DatabaseFact {
	key := statistics.Key{Object: e.Catalog.Domain.Ref(0, e.Catalog.DatabaseAddress()), Category: statistics.Database}
	return e.Statistics.Read(key).Database
}
func statDatabaseRuntimeSetup(t *testing.T) (*core.Engine, statistics.DatabaseFact) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE stat_db_runtime(id integer)")
	statsFunctionRetire(t, s)
	return e, statDatabaseRuntimeFact(e)
}

// Top-level SQL admission captures worker/cost settings and owns the SQL lease
// whose completion arms genuine idle timeouts. Acquire is for native controls.
func statDatabaseCommandSQL(t *testing.T, s *session.Session, query string) error {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	op.SQLAvailable = true
	result, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, query), 0, nil)
	if err != nil {
		return err
	}
	if result.Rows != nil {
		enginetest.Tuples(result)
		return result.Rows.Close()
	}
	return nil
}
func TestStatDatabaseTransactions(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		queries                     []string
		commits, rollbacks, inserts int64
	}{
		{"implicit_read", []string{"SELECT 1"}, 1, 0, 0},
		{"commit_abort", []string{"BEGIN", "INSERT INTO stat_db_runtime VALUES(1)", "COMMIT", "BEGIN", "INSERT INTO stat_db_runtime VALUES(2)", "ROLLBACK"}, 1, 1, 2},
		{"idle_close", nil, 0, 0, 0},
		{"idle_ends", []string{"COMMIT", "ROLLBACK"}, 1, 1, 0},
		{"idle_prepare_warning", []string{"PREPARE TRANSACTION 'stat_db_idle'"}, 1, 0, 0},
		{"savepoint", []string{"BEGIN", "SAVEPOINT child", "INSERT INTO stat_db_runtime VALUES(1)", "ROLLBACK TO child", "RELEASE child", "COMMIT"}, 1, 0, 1},
		{"chain", []string{"BEGIN", "SELECT 1", "COMMIT AND CHAIN", "SELECT 2", "ROLLBACK"}, 1, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, base := statDatabaseRuntimeSetup(t)
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, tc.queries...)
			statsFunctionRetire(t, s)
			got := statDatabaseRuntimeFact(e)
			if got.Commits-base.Commits != tc.commits || got.Rollbacks-base.Rollbacks != tc.rollbacks || got.Inserts-base.Inserts != tc.inserts {
				t.Fatal(base, got, tc)
			}
		})
	}
	t.Run("once_aborted", func(t *testing.T) {
		e, base := statDatabaseRuntimeSetup(t)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN", "INSERT INTO stat_db_runtime VALUES(1)")
		if err := statsFunctionSQL(t, s, "SELECT 1/0"); err == nil {
			t.Fatal("failure missing")
		}
		for range 3 {
			if err := statsFunctionSQL(t, s, "SELECT 1"); err == nil {
				t.Fatal("aborted transaction accepted")
			}
		}
		statsFunctionExec(t, s, "ROLLBACK")
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Commits != base.Commits || got.Rollbacks != base.Rollbacks+1 || got.Inserts != base.Inserts+1 {
			t.Fatal(base, got)
		}
	})
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared_abort", true: "prepared_commit"}[commit], func(t *testing.T) {
			e, base := statDatabaseRuntimeSetup(t)
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "BEGIN", "INSERT INTO stat_db_runtime VALUES(1)", "PREPARE TRANSACTION 'stat_db_prepared'")
			statsFunctionRetire(t, s)
			if got := statDatabaseRuntimeFact(e); got.Inserts != base.Inserts || got.Commits != base.Commits || got.Rollbacks != base.Rollbacks {
				t.Fatal("PREPARE resolved transaction", base, got)
			}
			finish := enginetest.StartSession(t, e, nil)
			end := "ROLLBACK PREPARED 'stat_db_prepared'"
			if commit {
				end = "COMMIT PREPARED 'stat_db_prepared'"
			}
			statsFunctionExec(t, finish, end)
			statsFunctionRetire(t, finish)
			got := statDatabaseRuntimeFact(e)
			commits, rollbacks := int64(1), int64(1)
			if commit {
				commits, rollbacks = 2, 0
			}
			if got.Commits-base.Commits != commits || got.Rollbacks-base.Rollbacks != rollbacks || got.Inserts-base.Inserts != 1 {
				t.Fatal("prepared original plus successful finisher command", base, got)
			}
		})
	}
	t.Run("failed_prepared_finisher_once", func(t *testing.T) {
		e, base := statDatabaseRuntimeSetup(t)
		s := enginetest.StartSession(t, e, nil)
		if err := statsFunctionSQL(t, s, "COMMIT PREPARED 'stat_db_missing'"); err == nil {
			t.Fatal("missing prepared transaction accepted")
		}
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Commits != base.Commits || got.Rollbacks != base.Rollbacks+1 {
			t.Fatal("actual failed control command", base, got)
		}
	})
	t.Run("admitted_parser_and_empty_once", func(t *testing.T) {
		e, base := statDatabaseRuntimeSetup(t)
		s := enginetest.StartSession(t, e, nil)
		for _, source := range []string{"SELEC 1", " ; -- empty\n"} {
			op, options, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			op.OriginalSQL, op.SQLAvailable = source, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			statements, parseErr := parser.ParseWithOptions(source, options)
			if parseErr == nil {
				if len(statements) != 0 {
					t.Fatal(statements)
				}
				scope.AcceptEmptySQL()
			}
			first := scope.Finish(parseErr)
			if again := scope.Finish(parseErr); again != first {
				t.Fatal("Finish changed terminal authority", first, again)
			}
			op.Done()
		}
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Commits != base.Commits+1 || got.Rollbacks != base.Rollbacks+1 {
			t.Fatal("actual parse/empty command once", base, got)
		}
	})
	t.Run("executed_ledger_vectors", statDatabaseRuntimeLedger)
}
func TestStatDatabaseTupleFrontiers(t *testing.T) {
	for _, tc := range []struct {
		name, query       string
		returned, fetched int64
	}{
		{"filtered", "SELECT id FROM stat_db_runtime WHERE id<3", 6, 0},
		{"aggregate", "SELECT count(*) FROM stat_db_runtime", 6, 0},
		{"limit", "SELECT id FROM stat_db_runtime LIMIT 2", 2, 0},
		{"index_predicate", "SELECT id FROM stat_db_runtime WHERE id=2", 1, 1},
		{"index_rejected_predicate", "SELECT id FROM stat_db_runtime WHERE id=2", 1, 1},
		{"update_target_scan", "UPDATE stat_db_runtime SET id=id WHERE id<0", 6, 0},
		{"delete_target_scan", "DELETE FROM stat_db_runtime WHERE id<0", 6, 0},
		{"update_from_target_scan", "UPDATE stat_db_runtime SET id=stat_db_runtime.id FROM (VALUES(100)) AS s(id) WHERE stat_db_runtime.id=s.id", 6, 0},
		{"delete_using_target_scan", "DELETE FROM stat_db_runtime USING (VALUES(100)) AS s(id) WHERE stat_db_runtime.id=s.id", 6, 0},
		{"merge_actual_rescans", "MERGE INTO stat_db_runtime AS t USING (VALUES(100),(200)) AS s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=s.id", 12, 0},
		{"merge_source_empty", "MERGE INTO stat_db_runtime AS t USING (SELECT id FROM stat_db_runtime WHERE false) AS s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET id=t.id", 6, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := statDatabaseRuntimeSetup(t)
			setup := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, setup, "INSERT INTO stat_db_runtime VALUES(1),(2),(3),(4),(5),(6)")
			if tc.fetched != 0 {
				statsFunctionExec(t, setup, "CREATE INDEX stat_db_hash ON stat_db_runtime USING hash(id)")
			}
			statsFunctionRetire(t, setup)
			base := statDatabaseRuntimeFact(e)
			s := enginetest.StartSession(t, e, nil)
			if tc.fetched != 0 {
				statsFunctionExec(t, s, "SET enable_seqscan=off")
			}
			if tc.name == "index_rejected_predicate" {
				op, _, err := s.AcquireCommand(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				query, source, release := enginetest.HashCaptureQuery(t, e, op, tc.query)
				defer release()
				filter := exec.HashQueryFilter(query.Root)
				if filter == nil {
					t.Fatal("missing real indexed predicate")
				}
				exec.AdmitScan(query.Root, source)
				if source.ScanDecision.Method != plan.HashCandidates {
					t.Fatal("indexed execution not admitted", source.ScanDecision)
				}
				scope := core.ScopeOf(source.LiveHost())
				state := &exec.State{Source: source, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame, Input: scope.Input, Output: scope.Output}
				rejected := 0
				// Compose a downstream rejection over the admitted indexed input.
				// GROUP/HAVING is not an indexed plan shape in this executor.
				predicate := enginetest.QueryProbe(filter.Predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					value, err := filter.Predicate.Eval(c)
					if err != nil {
						return value, err
					}
					rejected++
					return sqltype.Boolean(value.Type(), false), nil
				})
				stage, err := exec.OpenNode(op.Context(), state, &plan.FilterNode{QuerySchema: filter.Schema(), Child: filter, Predicate: predicate})
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close()
				if err := stage.Drain(op.Context()); err != nil || stage.Count() != 0 || rejected != 1 {
					t.Fatal("downstream rejection did not consume one indexed row", stage.Count(), rejected, err)
				}
				stage.Close()
				release()
				op.Done()
			} else if err := statDatabaseCommandSQL(t, s, tc.query); err != nil {
				t.Fatal(err)
			}
			statsFunctionRetire(t, s)
			got := statDatabaseRuntimeFact(e)
			if got.Returned-base.Returned != tc.returned || got.Fetched-base.Fetched != tc.fetched {
				t.Fatal("actual no-EXPLAIN source frontier", base, got, tc)
			}
		})
	}
	t.Run("retained_cursor", func(t *testing.T) {
		e, _ := statDatabaseRuntimeSetup(t)
		setup := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, setup, "INSERT INTO stat_db_runtime VALUES(1),(2),(3),(4),(5),(6)")
		statsFunctionRetire(t, setup)
		base := statDatabaseRuntimeFact(e)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN", "DECLARE c SCROLL CURSOR FOR SELECT id FROM stat_db_runtime", "FETCH 2 FROM c", "FETCH 2 FROM c", "FETCH BACKWARD 1 FROM c", "FETCH ALL FROM c", "CLOSE c", "COMMIT")
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Returned-base.Returned != 6 || got.Fetched != base.Fetched {
			t.Fatal("retained replay recounted heap", base, got)
		}
	})
	t.Run("counts_disabled", func(t *testing.T) {
		e, base := statDatabaseRuntimeSetup(t)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SET track_counts=off", "BEGIN", "INSERT INTO stat_db_runtime VALUES(1)", "SELECT id FROM stat_db_runtime", "ROLLBACK")
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Inserts != base.Inserts || got.Returned != base.Returned || got.Rollbacks != base.Rollbacks+1 || got.Sessions != base.Sessions+1 {
			t.Fatal("collection gates conflated", base, got)
		}
	})
	t.Run("inheritance_once", func(t *testing.T) {
		e, _ := statDatabaseRuntimeSetup(t)
		setup := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, setup, "CREATE TABLE stat_db_child() INHERITS(stat_db_runtime)", "INSERT INTO stat_db_runtime VALUES(1),(2),(3)", "INSERT INTO stat_db_child VALUES(4),(5),(6)")
		statsFunctionRetire(t, setup)
		base := statDatabaseRuntimeFact(e)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SELECT id FROM stat_db_runtime")
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.Returned-base.Returned != 6 || got.Fetched != base.Fetched {
			t.Fatal("inheritance counted physical leaves more than once", base, got)
		}
	})
	t.Run("access_timing_policy", statDatabaseRuntimeAccessTiming)
}
func TestStatDatabaseSessionCauses(t *testing.T) {
	for _, cause := range []session.DisconnectCause{session.DisconnectNormal, session.DisconnectAbandoned, session.DisconnectFatal, session.DisconnectKilled} {
		t.Run(map[session.DisconnectCause]string{session.DisconnectNormal: "normal", session.DisconnectAbandoned: "abandoned", session.DisconnectFatal: "fatal", session.DisconnectKilled: "killed"}[cause], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			s.ReserveDisconnectCause(cause)
			s.ReserveDisconnectCause(session.DisconnectAbandoned)
			s.RequestEndCause(session.InvalidConnection, session.DisconnectFatal)
			s.RequestEnd(session.Terminated)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := statDatabaseRuntimeFact(e)
			a, f, k := int64(0), int64(0), int64(0)
			switch cause {
			case session.DisconnectAbandoned:
				a = 1
			case session.DisconnectFatal:
				f = 1
			case session.DisconnectKilled:
				k = 1
			}
			if got.Sessions != 1 || got.Abandoned != a || got.Fatal != f || got.Killed != k || got.Commits != 0 || got.Rollbacks != 0 {
				t.Fatal("one-shot accepted cause", got)
			}
		})
	}
	t.Run("private", func(t *testing.T) {
		e := engine.New()
		public := enginetest.StartSession(t, e, nil)
		auth := public.CountedAuthenticated
		private, err := e.StartRestoreSession(context.Background(), auth)
		if err != nil {
			t.Fatal(err)
		}
		statsFunctionRetire(t, private)
		statsFunctionRetire(t, public)
		if got := statDatabaseRuntimeFact(e); got.Sessions != 1 {
			t.Fatal(got)
		}
	})
	t.Run("real_timeout", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		if err := statDatabaseCommandSQL(t, s, "SET idle_session_timeout='1ms'"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		got := statDatabaseRuntimeFact(e)
		if got.Fatal != 1 || got.Killed != 0 || got.Abandoned != 0 {
			t.Fatal(got)
		}
	})
	t.Run("first_cause_race", func(t *testing.T) {
		for range 8 {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			var joined sync.WaitGroup
			joined.Add(2)
			go func() { defer joined.Done(); s.Mu.Lock(); s.TerminateLocked(); s.Mu.Unlock() }()
			go func() { defer joined.Done(); s.RequestEndCause(session.InvalidConnection, session.DisconnectAbandoned) }()
			joined.Wait()
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := statDatabaseRuntimeFact(e)
			if got.Sessions != 1 || got.Abandoned+got.Killed != 1 || got.Fatal != 0 {
				t.Fatal("accepted race cause not once-only", got)
			}
			s.RequestEndCause(session.InvalidConnection, session.DisconnectFatal)
			if after := statDatabaseRuntimeFact(e); after != got {
				t.Fatal("late failed fatal write reclassified ending", got, after)
			}
		}
	})
}

func TestStatDatabaseWorkers(t *testing.T) {
	e, _ := statDatabaseRuntimeSetup(t)
	setup := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, setup, "INSERT INTO stat_db_runtime VALUES(1),(2),(3),(4),(5),(6)")
	statsFunctionRetire(t, setup)
	base := statDatabaseRuntimeFact(e)
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "SET max_parallel_workers_per_gather=2", "SET parallel_setup_cost=0", "SET parallel_tuple_cost=0", "SET min_parallel_table_scan_size=0")
	for _, query := range []string{"EXPLAIN SELECT id FROM stat_db_runtime", "SELECT id FROM stat_db_runtime", "SELECT string_agg(id::text,','::text) FROM stat_db_runtime"} {
		if err := statDatabaseCommandSQL(t, s, query); err != nil {
			t.Fatal(query, err)
		}
	}
	// Started workers remain real launches when projection subsequently fails.
	if err := statDatabaseCommandSQL(t, s, "SELECT id/(id-id) FROM stat_db_runtime"); err == nil {
		t.Fatal("parallel execution must fail")
	}
	for _, query := range []string{"SET track_counts=off", "SELECT id FROM stat_db_runtime", "SET max_parallel_workers_per_gather=0", "SELECT count(*) FROM stat_db_runtime"} {
		if err := statDatabaseCommandSQL(t, s, query); err != nil {
			t.Fatal(query, err)
		}
	}
	statsFunctionRetire(t, s)
	got := statDatabaseRuntimeFact(e)
	if got.WorkersPlanned-base.WorkersPlanned != 8 || got.WorkersLaunched-base.WorkersLaunched != 8 {
		t.Fatal("projection/aggregate/failure/counts-off workers", base, got)
	}
	t.Run("direct_aggregate_three", func(t *testing.T) {
		e := engine.New()
		const querySQL = "SELECT string_agg(id::text,','::text) AS v FROM t"
		query, source := enginetest.QueryFixture(t, e, querySQL)
		s := enginetest.StartSession(t, e, nil)
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		raw := enginetest.Statement(t, querySQL)
		if err := scope.Gate(raw); err != nil {
			t.Fatal(err)
		}
		if err := scope.AcquireRelation(raw, true); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err == nil {
			binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, source.Ref, scope.Input, nil, nil)
			err = failure
			query = binding.Query
			scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		source.Host = core.HostOf(scope)
		aggregate, eligible := exec.ParallelAggregatePlan(query.Root, source)
		if !eligible {
			t.Fatal("real aggregate plan not eligible")
		}
		base := statDatabaseRuntimeFact(e)
		rows, _, err := exec.FinalizeParallelAggregate(op.Context(), query.Root, aggregate, source, nil, 3)
		if err != nil {
			t.Fatal(err)
		}
		values := strings.Split(aggregateCombineString(t, rows), ",")
		sort.Strings(values)
		if strings.Join(values, ",") != "1,2,3,4" {
			t.Fatal(values)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.WorkersPlanned-base.WorkersPlanned != 3 || got.WorkersLaunched-base.WorkersLaunched != 3 {
			t.Fatal("actual three aggregate launch loops", base, got)
		}
	})
	t.Run("direct_projection_cancel", func(t *testing.T) {
		e := engine.New()
		const querySQL = "SELECT id+1 FROM t WHERE id>0"
		query, source := enginetest.QueryFixture(t, e, querySQL)
		s := enginetest.StartSession(t, e, nil)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(context.Canceled)
		if err := scope.Gate(enginetest.Statement(t, querySQL)); err != nil {
			t.Fatal(err)
		}
		source.Host = core.HostOf(scope)
		project, ordered, eligible := exec.ParallelQueryPlan(query.Root, source)
		if !eligible {
			t.Fatal("real projection plan not eligible")
		}
		filter, ok := project.Child.(*plan.FilterNode)
		if !ok {
			t.Fatal("real input predicate missing")
		}
		predicate := filter.Predicate
		entered := make(chan struct{}, 2)
		// Gate in the real input predicate, after one physical row per worker.
		// A projection-only gate would follow each worker's full input Drain.
		// No production hook or fabricated worker/read fact is used.
		filter.Predicate = enginetest.QueryProbe(predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
			value, err := predicate.Eval(c)
			if err != nil {
				return value, err
			}
			entered <- struct{}{}
			<-c.Context.Done()
			return value, c.Context.Err()
		})
		base := statDatabaseRuntimeFact(e)
		ctx, cancel, join := parallelTestStart(t, func(ctx context.Context) parallelTestOutcome {
			rows, count, err := exec.FinalizeParallelQuery(ctx, project, ordered, source, nil, 2)
			return parallelTestOutcome{rows, count, err}
		})
		defer func() { cancel(); join() }()
		for range 2 {
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("both actual worker rows did not enter", ctx.Err())
			}
		}
		cancel()
		out := join()
		if out.rows != nil || out.count != 0 || !errors.Is(out.err, context.Canceled) {
			t.Fatal("canceled worker result", out)
		}
		if err := scope.Finish(out.err); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		op.Done()
		statsFunctionRetire(t, s)
		got := statDatabaseRuntimeFact(e)
		if got.WorkersPlanned-base.WorkersPlanned != 2 || got.WorkersLaunched-base.WorkersLaunched != 2 || got.Returned-base.Returned != 2 {
			t.Fatal("canceled workers lost performed launches or input", base, got)
		}
	})
}

func statDatabaseRuntimeLedger(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, truncate := range []bool{false, true} {
			e, _ := statDatabaseRuntimeSetup(t)
			setup := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, setup, "INSERT INTO stat_db_runtime VALUES(1),(2),(3)")
			statsFunctionRetire(t, setup)
			base := statDatabaseRuntimeFact(e)
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "BEGIN")
			inserts, updates, deletes := int64(2), int64(0), int64(0)
			if truncate {
				statsFunctionExec(t, s, "INSERT INTO stat_db_runtime VALUES(7),(8)", "TRUNCATE stat_db_runtime", "INSERT INTO stat_db_runtime VALUES(9)")
				if commit {
					inserts = 1
				}
			} else {
				statsFunctionExec(t, s, "INSERT INTO stat_db_runtime VALUES(4)", "UPDATE stat_db_runtime SET id=11 WHERE id=1", "DELETE FROM stat_db_runtime WHERE id=2", "SAVEPOINT child", "INSERT INTO stat_db_runtime VALUES(44)", "ROLLBACK TO child", "RELEASE child")
				updates, deletes = 1, 1
			}
			end := "ROLLBACK"
			if commit {
				end = "COMMIT"
			}
			statsFunctionExec(t, s, end)
			statsFunctionRetire(t, s)
			got := statDatabaseRuntimeFact(e)
			if got.Inserts-base.Inserts != inserts || got.Updates-base.Updates != updates || got.Deletes-base.Deletes != deletes {
				t.Fatal("executed commit/abort/child/truncate vector", commit, truncate, base, got)
			}
			drop := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, drop, "DROP TABLE stat_db_runtime")
			statsFunctionRetire(t, drop)
			after := statDatabaseRuntimeFact(e)
			if after.Inserts != got.Inserts || after.Updates != got.Updates || after.Deletes != got.Deletes {
				t.Fatal("relation retirement erased database attempts", got, after)
			}
		}
	}
}

func statDatabaseRuntimeAccessTiming(t *testing.T) {
	e, base := statDatabaseRuntimeSetup(t)
	off := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, off, "SET track_io_timing=off", "SET max_parallel_workers_per_gather=0", "INSERT INTO stat_db_runtime VALUES(1)", "SELECT id FROM stat_db_runtime")
	statsFunctionRetire(t, off)
	disabled := statDatabaseRuntimeFact(e)
	if disabled.ReadTime != base.ReadTime || disabled.WriteTime != base.WriteTime {
		t.Fatal("disabled timing sampled elapsed", base, disabled)
	}
	on := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, on, "SET track_io_timing=on", "SET max_parallel_workers_per_gather=0")
	start := time.Now()
	statsFunctionExec(t, on, "INSERT INTO stat_db_runtime VALUES(2)", "SELECT id FROM stat_db_runtime")
	statsFunctionRetire(t, on)
	elapsed := time.Since(start)
	enabled := statDatabaseRuntimeFact(e)
	read, write := enabled.ReadTime-disabled.ReadTime, enabled.WriteTime-disabled.WriteTime
	if read < 0 || write < 0 || read > elapsed || write > elapsed {
		t.Fatal("measured serial native access intervals outside real boundary", read, write, elapsed)
	}
	// No deterministic positive duration is invented for a synchronous memory
	// access when the host clock cannot resolve it; provider units prove5ms math.
	if enabled.Returned-disabled.Returned != 2 || enabled.Inserts-disabled.Inserts != 1 {
		t.Fatal("real timed accesses not executed", disabled, enabled)
	}
}

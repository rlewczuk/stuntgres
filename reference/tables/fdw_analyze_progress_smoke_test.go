package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeProgressSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwAnalyzeDirectSetup(t, p, b, r, run)
					id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_direct")
					if !b.PG {
						gate := fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AnalyzeForeignTable", prepared, nil)
						fdwAnalyzeProgressAbsent(t, p, b, id)
						gate.cancel()
						fdwAnalyzeProgressCanceled(t, b, gate.join())
						gate.release()
						run(fixture.Select("SELECT count(*)"+fdwAnalyzeDirectData, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
					fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
					fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
					fdwAnalyzeProgressDirectProducts(run)
					if !b.PG {
						joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
						expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
						fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET b=b+10 WHERE tag='rare'", 300)
						gate := fdwAnalyzeProgressHold(t, p, b, c, r, "ANALYZE fdw_direct", "AcquireSampleRows", prepared, nil)
						fdwAnalyzeProgressObserve(t, p, b, id, "acquiring sample rows", 0, 0, 0, 0, 0, 0, 0, gate.finished)
						gate.cancel()
						fdwAnalyzeProgressCanceled(t, b, gate.join())
						gate.release()
						fdwAnalyzeProgressAbsent(t, p, b, id)
						if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
							t.Fatal("smoke cancellation completed unfinished typed work")
						}
						fdwAnalyzeProgressResources(t, r)
					}
				})
			})
			t.Run("inherited", func(t *testing.T) { fdwAnalyzeProgressWaitSmoke(t, prepared) })
		})
	}
}

func fdwAnalyzeProgressWaitSmoke(t *testing.T, prepared bool) {
	t.Helper()
	fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwAnalyzeMixedSetup(t, p, b, r, run, "ordinary", 10, 30)
		run(fixture.Exec("DROP FOREIGN TABLE fdw_analyze_foreign", 0))
		fdwSourceExec(t, p, b, r, run, "ALTER TABLE fdw_source.data ADD COLUMN extra bigint", 0)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign(extra bigint,flag boolean,label text,id integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_analyze_foreign INHERIT fdw_analyze_parent", 0))
		run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='fdw_analyze_foreign'::regclass AND attnum>0 ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"extra"}, {"flag"}, {"label"}, {"id"}}))
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		run(fixture.Exec("CREATE TABLE fdw_progress_blocked() INHERITS(fdw_analyze_parent)", 0))
		run(fixture.Exec("CREATE STATISTICS fdw_analyze_joint(ndistinct,dependencies,mcv) ON id,flag FROM fdw_analyze_parent", 0))
		run(fixture.Exec("CREATE STATISTICS fdw_analyze_expression ON (id+1) FROM fdw_analyze_parent", 0))
		run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
		fdwAnalyzeDomainJointGroups(run, 1, 2)
		id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_analyze_parent")
		for _, cancelWork := range []bool{false, true} {
			if cancelWork {
				run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES"+fdwAnalyzeValues(20, 3, "new", false), 20))
			}
			joint := fdwAnalyzeJointPayloads(t, p, c)
			expression := fdwAnalyzeDomainExpressions(t, p, c)
			before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
			fdwAnalyzeProgressHeldChild(t, p, b, c, id, prepared, cancelWork)
			fdwAnalyzeProgressAbsent(t, p, b, id)
			after := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+1)
			if !after.at.After(before.at) {
				t.Fatal("completed local maintenance missing after inherited wait", before, after)
			}
			population := float64(10)
			if cancelWork {
				population = 30
				if !reflect.DeepEqual(fdwAnalyzeJointPayloads(t, p, c), joint) || !reflect.DeepEqual(fdwAnalyzeDomainExpressions(t, p, c), expression) {
					t.Fatal("canceled inherited wait published unfinished typed products")
				}
			}
			fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
			fdwAnalyzeBaselineFacts(t, p, b, c, run)
			fdwAnalyzeLocalBaselineFacts(t, p, b, c, run, "ordinary", population)
			fdwAnalyzeDomainJointGroups(run, 1, 2)
		}
		step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
		step.Prepared = prepared
		run(step)
		fdwAnalyzeProgressAbsent(t, p, b, id)
		fdwAnalyzeDomainJointGroups(run, 2, 3)
		if !b.PG {
			fdwAnalyzeProgressResources(t, r)
		}
	})
}

func fdwAnalyzeProgressHeldChild(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, id fdwAnalyzeProgressIdentity, prepared, cancelWork bool) {
	t.Helper()
	holder, _ := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		holder.ExecContext(ctx, "ROLLBACK")
	})
	fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE ONLY fdw_progress_blocked IN ACCESS EXCLUSIVE MODE", 0)
	query := "ANALYZE (SKIP_LOCKED) ONLY fdw_analyze_parent"
	statement := fdwAnalyzeProgressPrepare(t, p, c, query, prepared)
	ctx, cancel := context.WithCancel(p.Ctx)
	t.Cleanup(cancel)
	finished := make(chan struct{})
	wait := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		return fdwAnalyzeProgressExecute(ctx, c, statement, query)
	})
	holderPID, childOID := 0, int64(0)
	if b.PG {
		holderPID = fixture.LockPID(t, p.Ctx, holder)
		childOID = fixture.LockOID(t, p.Ctx, "fdw_progress_blocked")
	}
	fixture.RelationLockPending(t, p, b, int(id.pid), holderPID, childOID, "AccessShareLock", finished)
	for range 2 {
		if b.PG {
			// Physical blocks/delay are deliberately not pinned. Identity,
			// actual phase, retained two-definition completion and unknown
			// pre-eligibility child counters remain exact released semantics.
			for _, preparedObserver := range []bool{false, true} {
				select {
				case <-finished:
					t.Fatal("held inherited worker completed")
				default:
				}
				q := fmt.Sprintf("SELECT pid,datid,datname,relid,phase,sample_blks_total>=sample_blks_scanned AND sample_blks_scanned>=0 AS sample_bounds,ext_stats_total,ext_stats_computed,child_tables_total,child_tables_done,current_child_table_relid,delay_time>=0 AS delay_bounds FROM pg_stat_progress_analyze WHERE pid=%d", id.pid)
				step := fixture.Select(q, []string{"pid", "datid", "datname", "relid", "phase", "sample_bounds", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done", "current_child_table_relid", "delay_bounds"}, []string{"INT4", "OID", "NAME", "OID", "TEXT", "BOOL", "INT8", "INT8", "INT8", "INT8", "OID", "BOOL"}, [][]any{{id.pid, fmt.Sprint(id.database), id.name, fmt.Sprint(id.relation), "acquiring inherited sample rows", true, int64(2), int64(2), int64(0), int64(0), "0", true}})
				step.Prepared = preparedObserver
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		} else {
			// Ten or thirty actual local records both occupy one logical block.
			fdwAnalyzeProgressObserve(t, p, b, id, "acquiring inherited sample rows", 1, 1, 2, 2, 0, 0, 0, finished)
		}
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT 42 AS peer", []string{"peer"}, []string{"INT4"}, [][]any{{int64(42)}}))
	fdwAnalyzeProgressRestricted(t, p, b, id, finished)
	if cancelWork {
		if b.PG {
			var signaled bool
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", id.pid).Scan(&signaled); err != nil || !signaled {
				t.Fatal("cancel held physical worker", signaled, err)
			}
		} else {
			cancel()
		}
		fdwAnalyzeProgressCanceled(t, b, wait())
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
	} else {
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		fdwAnalyzeProgressSuccess(t, wait())
	}
}

func fdwAnalyzeProgressRestricted(t *testing.T, p *fixture.Pair, b fixture.Backend, id fdwAnalyzeProgressIdentity, finished <-chan struct{}) {
	t.Helper()
	observer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		observer.ExecContext(ctx, "RESET ROLE")
	}()
	fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("SET ROLE fdw_reader", 0))
	// lib/pq returns raw OID cells as text; native returns integer identities.
	var database any = id.database
	if b.PG {
		database = fmt.Sprint(id.database)
	}
	for _, prepared := range []bool{false, true} {
		select {
		case <-finished:
			t.Fatal("worker finished before restricted observation")
		default:
		}
		query := fmt.Sprintf("SELECT %s FROM pg_stat_progress_analyze WHERE pid=%d", fdwAnalyzeProgressColumns, id.pid)
		step := fixture.Select(query,
			[]string{"pid", "datid", "datname", "relid", "phase", "sample_blks_total", "sample_blks_scanned", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done", "current_child_table_relid", "delay_time"},
			[]string{"INT4", "OID", "NAME", "OID", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "OID", "FLOAT8"},
			[][]any{{id.pid, database, id.name, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, step)
	}
}

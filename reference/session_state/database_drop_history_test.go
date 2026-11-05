package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropHistory(t *testing.T) {
	// REL_18_6 pg_stat_statements stores scalar dbid and has no object-drop
	// hook. Independent prediction (root must characterize on pinned 18.6):
	// historical query identity/calls survive database removal. Never hide those
	// entries behind a join to the now-absent pg_database row.
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b, actor = "dd_history_a", "dd_history_b", "dd_history_actor"
			fixture.RoleReferenceCleanup(t, []string{actor})
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					admin, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					run := func(x fixture.Executor, record *fixture.Recorder, s fixture.Step) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, x, record, s)
					}
					for _, q := range []string{
						"CREATE ROLE " + actor + " LOGIN SUPERUSER PASSWORD 'role-core-fixture'",
						"CREATE DATABASE " + a, "CREATE DATABASE " + b,
					} {
						run(admin, backend.Recorder, fixture.Exec(q, 0))
					}
					bDB, _, bRecord := fixture.DatabaseScenarioPool(t, p, backend.PG, b, "")
					observer, err := bDB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					run(observer, bRecord, fixture.Exec("CREATE EXTENSION pg_stat_statements", 0))
					workerDB, workerRaw, workerRecord := fixture.DatabaseScenarioPool(t, p, backend.PG, b, actor)
					worker, err := workerDB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					run(worker, workerRecord, fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
					run(worker, workerRecord, fixture.Exec("SET pg_stat_statements.track='top'", 0))
					var userID, peerID string
					if err := admin.QueryRowContext(p.Ctx, "SELECT (SELECT oid::text FROM pg_roles WHERE rolname=$1),(SELECT oid::text FROM pg_database WHERE datname=$2)", actor, b).Scan(&userID, &peerID); err != nil {
						t.Fatal(err)
					}
					const workload = "SELECT v FROM public.dd_history_value WHERE v=11"
					const normalized = "SELECT v FROM public.dd_history_value WHERE v=$1"
					populate := func() (string, string) {
						t.Helper()
						db, raw, record := fixture.DatabaseScenarioPool(t, p, backend.PG, a, actor)
						owner, err := db.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						// Register before assertions; Close the pools as well as the
						// borrower, otherwise an idle physical owner blocks DROP.
						t.Cleanup(func() { owner.Close() })
						run(owner, record, fixture.Exec("SET pg_stat_statements.track='top'", 0))
						run(owner, record, fixture.Exec("CREATE TABLE public.dd_history_value(v integer)", 0))
						run(owner, record, fixture.Exec("INSERT INTO public.dd_history_value VALUES(11)", 1))
						s := fixture.Select(workload, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
						run(owner, record, s)
						run(owner, record, fixture.Prepared(s))
						stmt, err := owner.PrepareContext(p.Ctx, workload)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
						held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: workload}
						for range 2 {
							run(held, record, s)
						}
						var dbid string
						if err := admin.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_database WHERE datname=$1", a).Scan(&dbid); err != nil {
							t.Fatal(err)
						}
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
						if err := owner.Close(); err != nil {
							t.Fatal(err)
						}
						for _, pool := range []*sql.DB{db, raw} {
							if err := pool.Close(); err != nil {
								t.Fatal(err)
							}
						}
						fixture.DatabaseReferenceOwners(t, p, observer, bRecord, a, 0)
						var id string
						if err := observer.QueryRowContext(p.Ctx, "SELECT queryid::text FROM pg_stat_statements WHERE dbid=$1::oid AND userid=$2::oid AND query=$3 AND toplevel", dbid, userID, normalized).Scan(&id); err != nil || id == "0" || id == "" {
							t.Fatal("independently selected workload identity", id, err)
						}
						return dbid, id
					}
					history := func(dbid, id string) {
						t.Helper()
						run(observer, bRecord, fixture.Select("SELECT dbid::text,userid::text,queryid::text,query,toplevel,calls,rows FROM pg_stat_statements WHERE dbid=$1::oid AND userid=$2::oid AND query=$3 AND toplevel", []string{"dbid", "userid", "queryid", "query", "toplevel", "calls", "rows"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "INT8", "INT8"}, [][]any{{dbid, userID, id, normalized, true, int64(4), int64(4)}}, dbid, userID, normalized))
					}
					const dropSQL = "DROP DATABASE /* dd history utility */ dd_history_a"
					var dropper fixture.Executor = worker
					if mode == "held" {
						stmt, err := worker.PrepareContext(p.Ctx, dropSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						dropper = fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: dropSQL}
					}
					utility := func(calls int64) {
						t.Helper()
						rows := [][]any(nil)
						if calls != 0 {
							rows = [][]any{{dropSQL, true, true, calls, int64(0)}}
						}
						run(observer, bRecord, fixture.Select("SELECT query,queryid<>0 AS identified,toplevel,calls,rows FROM pg_stat_statements WHERE dbid=$1::oid AND userid=$2::oid AND query=$3", []string{"query", "identified", "toplevel", "calls", "rows"}, []string{"TEXT", "BOOL", "BOOL", "INT8", "INT8"}, rows, peerID, userID, dropSQL))
					}
					utility(0) // Preparation alone contributes no utility entry.
					oldDBID, oldQueryID := populate()
					history(oldDBID, oldQueryID)
					drop := fixture.Exec(dropSQL, 0)
					if mode == "fresh" {
						drop = fixture.Prepared(drop)
					}
					run(dropper, workerRecord, drop)
					utility(1)
					run(observer, bRecord, fixture.Select("SELECT datname FROM pg_database WHERE oid=$1::oid", []string{"datname"}, []string{"NAME"}, nil, oldDBID))
					history(oldDBID, oldQueryID)
					run(admin, backend.Recorder, fixture.Exec("CREATE DATABASE "+a, 0))
					newDBID, newQueryID := populate()
					// Query IDs are database-local fingerprints, not incarnation
					// IDs. PostgreSQL18.6 may reuse the same fingerprint; the
					// distinct dbid keeps these two historical entries separate.
					if oldDBID == newDBID {
						t.Fatalf("new incarnation reused database identity: old %s/%s new %s/%s", oldDBID, oldQueryID, newDBID, newQueryID)
					}
					history(oldDBID, oldQueryID)
					history(newDBID, newQueryID)
					run(dropper, workerRecord, drop)
					// Documented REL_18_6 retained raw utility collector defect:
					// second SAME held command executes but contributes no call.
					collected := int64(2)
					if backend.PG && mode == "held" {
						collected = 1
					}
					utility(collected)
					run(observer, bRecord, fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, nil, a))
					history(oldDBID, oldQueryID)
					history(newDBID, newQueryID)
					// Failed execution is not another successful utility sample.
					want := fixture.ControlsReferenceDiagnostic("3D000", "database \""+a+"\" does not exist")
					failed := fixture.ErrorDiagnostic(dropSQL, want)
					failed.ErrorStage = "execute"
					if mode == "fresh" {
						failed = fixture.Prepared(failed)
					}
					run(dropper, workerRecord, failed)
					utility(collected)
					if err := worker.Close(); err != nil {
						t.Fatal(err)
					}
					for _, pool := range []*sql.DB{workerDB, workerRaw} {
						if err := pool.Close(); err != nil {
							t.Fatal(err)
						}
					}
					// Observer and its pools are closed by registered cleanup.
				})
			}
		})
	}
}

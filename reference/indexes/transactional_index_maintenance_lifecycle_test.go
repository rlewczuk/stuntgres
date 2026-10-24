package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceVisibility(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		for _, private := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/private=%t/commit=%t", action, private, commit), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							run := transactionalIndexRun(t, p, b, a)
							other := transactionalIndexRun(t, p, b, peer)
							run(fixture.Exec("CREATE TABLE unrelated(n INT)", 0))
							run(fixture.Exec("INSERT INTO unrelated VALUES(90)", 1))
							if !private {
								indexTxMaintenanceSeed(run, action)
								run(fixture.Exec("CREATE INDEX sibling ON t(lower(s),(n+1)) WHERE n>0", 0))
							}
							run(fixture.Exec("BEGIN", 0))
							defer a.ExecContext(p.Ctx, "ROLLBACK")
							if private {
								indexTxMaintenanceSeed(run, action)
								run(fixture.Exec("CREATE INDEX sibling ON t(lower(s),(n+1)) WHERE n>0", 0))
							}
							var oid int64
							if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='ix' AND relnamespace='public'::regnamespace").Scan(&oid); err != nil {
								t.Fatal(err)
							}
							run(fixture.Exec(indexTxMaintenanceSQL(action), 0))
							run(indexTxMaintenanceState(action, true))
							if action != "drop" {
								run(fixture.Select("SELECT oid=$1::oid AS same FROM pg_class WHERE relname IN ('ix','fresh') AND relnamespace='public'::regnamespace", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oid))
								run(fixture.Exec("INSERT INTO t VALUES(3,'c')", 1))
							} else {
								run(fixture.Exec("INSERT INTO t VALUES(1,'duplicate')", 1))
							}
							run(fixture.Exec("DELETE FROM t WHERE n=2", 1))
							if private {
								other(transactionalIndexName("ix", false))
								other(transactionalIndexName("fresh", false))
								other(transactionalIndexName("t", false))
							} else {
								other(indexTxMaintenanceState(action, false))
							}
							// This independent commit is not part of the owner's rollback domain.
							other(fixture.Exec("INSERT INTO unrelated VALUES(91)", 1))
							end := "ROLLBACK"
							if commit {
								end = "COMMIT"
							}
							run(fixture.Exec(end, 0))
							other(fixture.Select("SELECT n FROM unrelated ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(90)}, {int64(91)}}))
							if private && !commit {
								other(transactionalIndexName("t", false))
								return
							}
							other(indexTxMaintenanceState(action, commit))
							other(transactionalIndexName("sibling", true))
							rows := [][]any{{int64(1), "a"}, {int64(2), "b"}, {nil, nil}}
							if commit {
								rows = [][]any{{int64(1), "a"}, {int64(3), "c"}, {nil, nil}}
								if action == "drop" {
									rows = [][]any{{int64(1), "a"}, {int64(1), "duplicate"}, {nil, nil}}
								}
							}
							other(fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST,s", []string{"n", "s"}, []string{"INT4", "TEXT"}, rows))
							if !commit || action != "drop" {
								constraint := "ix"
								if commit && action == "rename" {
									constraint = "fresh"
								}
								other(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", fixture.UniqueReferenceDuplicate("t", constraint, "Key (n)=(1) already exists.")))
							} else {
								other(fixture.Exec("INSERT INTO t VALUES(1,'allowed')", 1))
							}
						})
					}
				})
			}
		}
	}
}

func TestReferenceTransactionalIndexMaintenanceSavepoints(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalIndexRun(t, p, b, a)
			indexTxMaintenanceSeed(run, "rename")
			run(fixture.Exec("CREATE SEQUENCE seq", 0))
			var oid int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"BEGIN", "ALTER INDEX ix RENAME TO fresh", "ALTER INDEX fresh SET(fillfactor=80)", "INSERT INTO t VALUES(3,'before')", "SAVEPOINT outer_mark", "SAVEPOINT mark"} {
				run(fixture.Exec(q, map[string]int64{"INSERT INTO t VALUES(3,'before')": 1}[q]))
			}
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			for i := range 2 {
				run(fixture.Exec("ALTER INDEX fresh RESET(fillfactor)", 0))
				run(fixture.Exec("ALTER INDEX fresh RENAME TO later", 0))
				run(fixture.Exec("DROP INDEX later", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'postmark')", 1))
				run(fixture.Select("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(i + 1)}}))
				run(fixture.Exec("ROLLBACK TO mark", 0))
				run(fixture.Select("SELECT oid=$1::oid AS same,reloptions::text FROM pg_class WHERE relname='fresh'", []string{"same", "reloptions"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{fillfactor=80}"}}, oid))
				run(fixture.Exec("SAVEPOINT enforce", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", fixture.UniqueReferenceDuplicate("t", "fresh", "Key (n)=(1) already exists.")))
				run(fixture.Exec("ROLLBACK TO enforce", 0))
				run(fixture.Exec("RELEASE enforce", 0))
			}
			run(fixture.Exec("RELEASE mark", 0))
			run(fixture.Exec("ROLLBACK TO outer_mark", 0))
			run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
			transactionalIndexRun(t, p, b, peer)(indexTxMaintenanceState("rename", false))
			run(fixture.Exec("ROLLBACK", 0))
			run(indexTxMaintenanceState("rename", false))
			run(fixture.Select("SELECT oid=$1::oid AS same FROM pg_class WHERE relname='ix'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oid))
			run(fixture.Select("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
		})
	}
}

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "failed-restore-commit"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, a)
					indexTxMaintenanceSeed(run, "set")
					for _, q := range []string{"CREATE TABLE q(n INT CONSTRAINT u UNIQUE)", "CREATE TABLE p(n INT) PARTITION BY RANGE(n)", "CREATE TABLE p1 PARTITION OF p FOR VALUES FROM(0) TO(10)", "CREATE UNIQUE INDEX pi ON p(n)", "ALTER INDEX ix SET(fillfactor=75)", "ALTER INDEX u SET(deduplicate_items=off)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO q VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					var child string
					if err := a.QueryRowContext(p.Ctx, "SELECT c.relname FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE i.indrelid='p1'::regclass").Scan(&child); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("ALTER INDEX "+child+" SET(fillfactor=70)", 0))
					run(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "ALTER INDEX fresh SET(fillfactor=80)", "ALTER INDEX u RESET(deduplicate_items)", "DROP INDEX pi", "SAVEPOINT mark"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO t VALUES(3,'private')", 1))
					transactionalIndexRun(t, p, b, peer)(fixture.Select("SELECT relname,reloptions::text FROM pg_class WHERE relname IN ('ix','fresh','u') ORDER BY relname", []string{"relname", "reloptions"}, []string{"NAME", "TEXT"}, [][]any{{"ix", "{fillfactor=75}"}, {"u", "{deduplicate_items=off}"}}))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						return
					}
					var committed bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(committed.String(), "fresh") {
						t.Fatal("Dump leaked private rename", committed.String())
					}
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
					if err != nil {
						t.Fatal(err)
					}
					clone := sql.OpenDB(connector)
					defer clone.Close()
					if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(committed.Bytes())); err != nil {
						t.Fatal("committed replay", err, committed.String())
					}
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					cloneRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
					cloneRun(fixture.Select("SELECT relname,reloptions::text FROM pg_class WHERE relname IN ('ix','fresh','u') ORDER BY relname", []string{"relname", "reloptions"}, []string{"NAME", "TEXT"}, [][]any{{"ix", "{fillfactor=75}"}, {"u", "{deduplicate_items=off}"}}))
					cloneRun(fixture.Select("SELECT reloptions::text FROM pg_class WHERE relname=$1", []string{"reloptions"}, []string{"TEXT"}, [][]any{{"{fillfactor=70}"}}, child))
					cloneRun(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
					cloneRun(fixture.ErrorDiagnostic("INSERT INTO q VALUES(1)", fixture.UniqueReferenceDuplicate("q", "u", "Key (n)=(1) already exists.")))
					cloneRun(fixture.Exec("INSERT INTO p VALUES(2)", 1))
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(committed.String()+"\nALTER INDEX ix SET(fillfactor=9);")); err == nil {
						t.Fatal("late-invalid replay succeeded")
					}
					run(fixture.Select("SELECT reloptions::text FROM pg_class WHERE relname='fresh'", []string{"reloptions"}, []string{"TEXT"}, [][]any{{"{fillfactor=80}"}}))
					run(transactionalIndexName("pi", false))
					if replacement == "failed-restore-commit" {
						run(fixture.Exec("COMMIT", 0))
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(after.Bytes())); err != nil {
							t.Fatal(err, after.String())
						}
						cloneRun(fixture.Select("SELECT relname,reloptions::text FROM pg_class WHERE relname IN ('ix','fresh','u') ORDER BY relname", []string{"relname", "reloptions"}, []string{"NAME", "TEXT"}, [][]any{{"fresh", "{fillfactor=80}"}, {"u", nil}}))
						cloneRun(transactionalIndexName("pi", false))
						cloneRun(transactionalIndexName(child, false))
						cloneRun(fixture.Exec("INSERT INTO t VALUES(4,'healthy')", 1))
						return
					}
					rows, err := a.QueryContext(p.Ctx, "SELECT n FROM t ORDER BY n NULLS LAST")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(committed.Bytes()))
					}
					if err != nil {
						t.Fatal(err)
					}
					fixture.DeleteReferenceConsume(t, false, rows, nil, fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
					_, err = a.ExecContext(p.Ctx, "ALTER INDEX fresh RESET(fillfactor)")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					_, err = a.ExecContext(p.Ctx, "COMMIT")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Exec("CREATE TABLE healthy(n INT)", 0))
					run(fixture.Exec("INSERT INTO healthy VALUES(1)", 1))
					run(fixture.Select("SELECT n FROM healthy", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}

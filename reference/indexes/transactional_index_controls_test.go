package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type transactionalIndexFailedReader struct{ err error }

func (r transactionalIndexFailedReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceTransactionalIndexControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "failed-restore-commit"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("CREATE INDEX committed_idx ON t(n)", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
					run(fixture.Exec("CREATE INDEX private_idx ON t(n DESC)", 0))
					run(fixture.Exec("SAVEPOINT mark", 0))
					transactionalIndexRun(t, p, b, peer)(transactionalIndexName("private_idx", false))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(dump.String(), "private_idx") {
						t.Fatal("Dump leaked private index", dump.String())
					}
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
					if err != nil {
						t.Fatal(err)
					}
					clone := sql.OpenDB(connector)
					defer clone.Close()
					if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					recordedClone, cloneRecorder := fixture.RecordedNativePool(t, connector)
					cloneRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recordedClone, cloneRecorder, s) }
					cloneRun(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					cloneRun(transactionalIndexName("committed_idx", true))
					cloneRun(transactionalIndexName("private_idx", false))
					cloneRun(fixture.Select("SELECT pg_get_indexdef('committed_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX committed_idx ON public.t USING btree (n)"}}))
					cloneRun(fixture.Exec("INSERT INTO t VALUES(4)", 1))
					cloneRun(fixture.Exec("UPDATE t SET n=5 WHERE n=1", 1))
					cloneRun(fixture.Exec("DELETE FROM t WHERE n=2", 1))
					cloneRun(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}}))
					readErr := errors.New("transactional index reader failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, transactionalIndexFailedReader{readErr}); !errors.Is(err, readErr) {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE late(n INT); INSERT INTO late VALUES('bad');")); err == nil {
						t.Fatal("late-invalid Restore succeeded")
					}
					run(transactionalIndexName("private_idx", true))
					if replacement == "failed-restore-commit" {
						run(fixture.Exec("COMMIT", 0))
						transactionalIndexRun(t, p, b, peer)(transactionalIndexName("private_idx", true))
						return
					}
					rows, err := c.QueryContext(p.Ctx, "SELECT n FROM t ORDER BY n")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
					}
					if err != nil {
						t.Fatal(err)
					}
					fixture.DeleteReferenceConsume(t, false, rows, nil, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					_, err = c.ExecContext(p.Ctx, "ROLLBACK TO mark")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					_, err = c.ExecContext(p.Ctx, "COMMIT")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					run(fixture.Exec("ROLLBACK", 0))
					run(transactionalIndexName("private_idx", false))
				})
			}
		})
	}
}

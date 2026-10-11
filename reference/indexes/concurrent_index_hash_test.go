package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexHash(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmtConcurrentCase(held, true, false), func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				first, later, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				fp, lp, bp := fixture.ConcurrentIndexPID(t, p, b, first), fixture.ConcurrentIndexPID(t, p, b, later), fixture.ConcurrentIndexPID(t, p, b, builder)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
				fixture.ConcurrentIndexExec(t, p, b, first, "BEGIN", 0)
				fixture.ConcurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(2)", 1)
				build := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)", held)
				fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
				fixture.ConcurrentIndexExec(t, p, b, later, "BEGIN", 0)
				fixture.ConcurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(4)", 1)
				fixture.ConcurrentIndexExec(t, p, b, first, "COMMIT", 0)
				fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(5)", 1)
				fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET n=6 WHERE n=1", 1)
				fixture.ConcurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=3", 1)
				fixture.ConcurrentIndexExec(t, p, b, later, "COMMIT", 0)
				if err := build.Join(t, p); err != nil {
					t.Fatal(err)
				}
				fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 4, 5, 6)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(4)", 1)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4, 4)
				fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET n=7 WHERE n=4", 2)
				fixture.ConcurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=5", 1)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 6, 7, 7)
				want := fixture.ControlsReferenceDiagnostic("0A000", `access method "hash" does not support unique indexes`)
				step := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX CONCURRENTLY bad ON t USING hash(n)", want)
				step.ErrorStage = "execute"
				if held {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if _, found := fixture.ConcurrentIndexState(t, p, b, "bad"); found {
					t.Fatal("unsupported hash UNIQUE published a descriptor")
				}
				if b.PG {
					fixture.ConcurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(2),(6),(7),(7)", 4)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX hi ON public.t USING hash(n)", 0)
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(dump.String(), " USING hash (") || strings.Contains(dump.String(), "CONCURRENTLY") {
						t.Fatalf("missing ordinary hash replay definition: %s", dump.String())
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE IF EXISTS t CASCADE"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal(err)
						}
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT n FROM t WHERE n=7", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t CASCADE"); err != nil {
							t.Fatal(err)
						}
					}
				}
				fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_indexdef('hi'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX hi ON public.t USING hash (n)"}}))
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=7", 7, 7)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 6, 7, 7)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(7)", 1)
				fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=7", 7, 7, 7)
			})
		})
	}
}

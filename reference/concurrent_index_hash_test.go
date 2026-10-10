package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceConcurrentIndexHash(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmtConcurrentCase(held, true, false), func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				first, later, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
				fp, lp, bp := concurrentIndexPID(t, p, b, first), concurrentIndexPID(t, p, b, later), concurrentIndexPID(t, p, b, builder)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
				concurrentIndexExec(t, p, b, first, "BEGIN", 0)
				concurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(2)", 1)
				build := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)", held)
				concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
				concurrentIndexExec(t, p, b, later, "BEGIN", 0)
				concurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(4)", 1)
				concurrentIndexExec(t, p, b, first, "COMMIT", 0)
				concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(5)", 1)
				concurrentIndexExec(t, p, b, c, "UPDATE t SET n=6 WHERE n=1", 1)
				concurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=3", 1)
				concurrentIndexExec(t, p, b, later, "COMMIT", 0)
				if err := build.join(t, p); err != nil {
					t.Fatal(err)
				}
				concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 4, 5, 6)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(4)", 1)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4, 4)
				concurrentIndexExec(t, p, b, c, "UPDATE t SET n=7 WHERE n=4", 2)
				concurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=5", 1)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 6, 7, 7)
				want := controlsReferenceDiagnostic("0A000", `access method "hash" does not support unique indexes`)
				step := referenceErrorDiagnostic("CREATE UNIQUE INDEX CONCURRENTLY bad ON t USING hash(n)", want)
				step.errorStage = "execute"
				if held {
					step = referencePrepared(step)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, step)
				if _, found := concurrentIndexState(t, p, b, "bad"); found {
					t.Fatal("unsupported hash UNIQUE published a descriptor")
				}
				if b.pg {
					concurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(2),(6),(7),(7)", 4)
					concurrentIndexExec(t, p, b, c, "CREATE INDEX hi ON public.t USING hash(n)", 0)
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(dump.String(), " USING hash (") || strings.Contains(dump.String(), "CONCURRENTLY") {
						t.Fatalf("missing ordinary hash replay definition: %s", dump.String())
					}
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					if p.postgres != nil {
						if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE IF EXISTS t CASCADE"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT n FROM t WHERE n=7", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
						if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t CASCADE"); err != nil {
							t.Fatal(err)
						}
					}
				}
				concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT pg_get_indexdef('hi'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX hi ON public.t USING hash (n)"}}))
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=7", 7, 7)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 6, 7, 7)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(7)", 1)
				concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=7", 7, 7, 7)
			})
		})
	}
}

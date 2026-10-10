package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexNullsNotDistinct(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmtConcurrentCase(held, false, false), func(t *testing.T) {
			t.Run("first-build", func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					c := concurrentIndexConn(t, p, b)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(NULL,'x','second')", 2)
					w := concurrentIndexStart(t, p, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a,b) INCLUDE(payload) NULLS NOT DISTINCT", held)
					want := concurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) is duplicated.", true)
					referenceAssertDiagnostic(t, w.join(t, p), &want)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','notready')", 1)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
					if !b.pg {
						concurrentIndexDumpInvalid(t, p, "ui")
					}
				})
			})
			t.Run("grandfathered-second-writer", func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					first, later, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					fp, lp, bp := concurrentIndexPID(t, p, b, first), concurrentIndexPID(t, p, b, later), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','seed')", 1)
					concurrentIndexExec(t, p, b, first, "BEGIN", 0)
					concurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(1,'x','first')", 1)
					w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a,b) INCLUDE(payload) NULLS NOT DISTINCT", held)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
					concurrentIndexExec(t, p, b, later, "BEGIN", 0)
					concurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(NULL,'x','grandfathered')", 1)
					concurrentIndexExec(t, p, b, first, "COMMIT", 0)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
					want := concurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) already exists.", false)
					concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','postready')", want)
					concurrentIndexExec(t, p, b, later, "COMMIT", 0)
					referenceAssertDiagnostic(t, w.join(t, p), &want)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'y','distinct')", 1)
					concurrentIndexExec(t, p, b, c, "UPDATE t SET payload='retained' WHERE b='y'", 1)
					concurrentIndexError(t, p, b, c, "UPDATE t SET b='x' WHERE b='y'", want)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
				})
			})
			t.Run("readiness-and-replay", func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					reader, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					rp, bp := concurrentIndexPID(t, p, b, reader), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(1,'x','second')", 2)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(8)", 1)
					concurrentIndexExec(t, p, b, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					concurrentIndexRows(t, p, b, reader, "SELECT n FROM u ORDER BY n", 8)
					w := concurrentIndexStart(t, p, builder, `CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a DESC NULLS LAST,b COLLATE "C" text_ops) INCLUDE(payload) NULLS NOT DISTINCT`, held)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, rp, "waiting for old snapshots")
					want := concurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) already exists.", false)
					concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','different')", want)
					concurrentIndexError(t, p, b, c, "UPDATE t SET a=NULL WHERE a=1", want)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'y','different-key')", 1)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT a FROM t WHERE a IS NOT NULL ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					concurrentIndexExec(t, p, b, reader, "COMMIT", 0)
					if err := w.join(t, p); err != nil {
						t.Fatal(err)
					}
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT pg_get_indexdef('ui'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{`CREATE UNIQUE INDEX ui ON public.t USING btree (a DESC NULLS LAST, b COLLATE "C") INCLUDE (payload) NULLS NOT DISTINCT`}}))
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(dump.String(), "NULLS NOT DISTINCT") {
							t.Fatal("actual Dump lost NND", dump.String())
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						if p.postgres != nil {
							if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE IF EXISTS t CASCADE; DROP TABLE IF EXISTS u CASCADE"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
								t.Fatal(err)
							}
							_, err := p.postgres.ExecContext(p.ctx, "INSERT INTO t VALUES(NULL,'x','replayed')")
							referenceAssertDiagnostic(t, err, &want)
							if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t CASCADE; DROP TABLE u CASCADE"); err != nil {
								t.Fatal(err)
							}
						}
					} else {
						// Independently specified unchanged PostgreSQL-compatible replay.
						concurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
						concurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
						concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(1,'x','second'),(NULL,'y','different-key')", 3)
						concurrentIndexExec(t, p, b, c, `CREATE UNIQUE INDEX ui ON public.t USING btree (a DESC NULLS LAST,b COLLATE "C" text_ops) INCLUDE(payload) NULLS NOT DISTINCT`, 0)
					}
					concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','restored')", want)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
					for _, suffix := range []string{"", " NULLS DISTINCT"} {
						concurrentIndexExec(t, p, b, c, "CREATE TABLE nullable(a INTEGER,b TEXT)", 0)
						concurrentIndexExec(t, p, b, c, "INSERT INTO nullable VALUES(NULL,'x'),(NULL,'x')", 2)
						concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY distinct_i ON nullable(a,b)"+suffix, 0)
						concurrentIndexExec(t, p, b, c, "INSERT INTO nullable VALUES(NULL,'x')", 1)
						concurrentIndexExec(t, p, b, c, "DROP TABLE nullable", 0)
					}
				})
			})
		})
	}
}

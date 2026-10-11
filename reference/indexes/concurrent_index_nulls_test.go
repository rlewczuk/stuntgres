package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexNullsNotDistinct(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmtConcurrentCase(held, false, false), func(t *testing.T) {
			t.Run("first-build", func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := fixture.ConcurrentIndexConn(t, p, b)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(NULL,'x','second')", 2)
					w := fixture.ConcurrentIndexStart(t, p, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a,b) INCLUDE(payload) NULLS NOT DISTINCT", held)
					want := fixture.ConcurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) is duplicated.", true)
					fixture.AssertDiagnostic(t, w.Join(t, p), &want)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','notready')", 1)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
					if !b.PG {
						fixture.ConcurrentIndexDumpInvalid(t, p, "ui")
					}
				})
			})
			t.Run("grandfathered-second-writer", func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					first, later, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					fp, lp, bp := fixture.ConcurrentIndexPID(t, p, b, first), fixture.ConcurrentIndexPID(t, p, b, later), fixture.ConcurrentIndexPID(t, p, b, builder)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','seed')", 1)
					fixture.ConcurrentIndexExec(t, p, b, first, "BEGIN", 0)
					fixture.ConcurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(1,'x','first')", 1)
					w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a,b) INCLUDE(payload) NULLS NOT DISTINCT", held)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
					fixture.ConcurrentIndexExec(t, p, b, later, "BEGIN", 0)
					fixture.ConcurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(NULL,'x','grandfathered')", 1)
					fixture.ConcurrentIndexExec(t, p, b, first, "COMMIT", 0)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
					want := fixture.ConcurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) already exists.", false)
					fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','postready')", want)
					fixture.ConcurrentIndexExec(t, p, b, later, "COMMIT", 0)
					fixture.AssertDiagnostic(t, w.Join(t, p), &want)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'y','distinct')", 1)
					fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET payload='retained' WHERE b='y'", 1)
					fixture.ConcurrentIndexError(t, p, b, c, "UPDATE t SET b='x' WHERE b='y'", want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
				})
			})
			t.Run("readiness-and-replay", func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					reader, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					rp, bp := fixture.ConcurrentIndexPID(t, p, b, reader), fixture.ConcurrentIndexPID(t, p, b, builder)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(1,'x','second')", 2)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(8)", 1)
					fixture.ConcurrentIndexExec(t, p, b, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					fixture.ConcurrentIndexRows(t, p, b, reader, "SELECT n FROM u ORDER BY n", 8)
					w := fixture.ConcurrentIndexStart(t, p, builder, `CREATE UNIQUE INDEX CONCURRENTLY ui ON t(a DESC NULLS LAST,b COLLATE "C" text_ops) INCLUDE(payload) NULLS NOT DISTINCT`, held)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, rp, "waiting for old snapshots")
					want := fixture.ConcurrentIndexUnique("t", "ui", "Key (a, b)=(null, x) already exists.", false)
					fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','different')", want)
					fixture.ConcurrentIndexError(t, p, b, c, "UPDATE t SET a=NULL WHERE a=1", want)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'y','different-key')", 1)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM t WHERE a IS NOT NULL ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.ConcurrentIndexExec(t, p, b, reader, "COMMIT", 0)
					if err := w.Join(t, p); err != nil {
						t.Fatal(err)
					}
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_indexdef('ui'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{`CREATE UNIQUE INDEX ui ON public.t USING btree (a DESC NULLS LAST, b COLLATE "C") INCLUDE (payload) NULLS NOT DISTINCT`}}))
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(dump.String(), "NULLS NOT DISTINCT") {
							t.Fatal("actual Dump lost NND", dump.String())
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						if p.Postgres != nil {
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE IF EXISTS t CASCADE; DROP TABLE IF EXISTS u CASCADE"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatal(err)
							}
							_, err := p.Postgres.ExecContext(p.Ctx, "INSERT INTO t VALUES(NULL,'x','replayed')")
							fixture.AssertDiagnostic(t, err, &want)
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t CASCADE; DROP TABLE u CASCADE"); err != nil {
								t.Fatal(err)
							}
						}
					} else {
						// Independently specified unchanged PostgreSQL-compatible replay.
						fixture.ConcurrentIndexExec(t, p, b, c, "DROP TABLE t", 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(a INTEGER,b TEXT,payload TEXT)", 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','first'),(1,'x','second'),(NULL,'y','different-key')", 3)
						fixture.ConcurrentIndexExec(t, p, b, c, `CREATE UNIQUE INDEX ui ON public.t USING btree (a DESC NULLS LAST,b COLLATE "C" text_ops) INCLUDE(payload) NULLS NOT DISTINCT`, 0)
					}
					fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL,'x','restored')", want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT COUNT(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
					for _, suffix := range []string{"", " NULLS DISTINCT"} {
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE nullable(a INTEGER,b TEXT)", 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO nullable VALUES(NULL,'x'),(NULL,'x')", 2)
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY distinct_i ON nullable(a,b)"+suffix, 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO nullable VALUES(NULL,'x')", 1)
						fixture.ConcurrentIndexExec(t, p, b, c, "DROP TABLE nullable", 0)
					}
				})
			})
		})
	}
}

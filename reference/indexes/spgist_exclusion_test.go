package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTExclusion(t *testing.T) {
	for _, tc := range []struct{ typ, op, value string }{{"text", "=", "a"}, {"point", "~=", "(1,2)"}} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_ex(v "+tc.typ+",CONSTRAINT spgist_exclusion EXCLUDE USING spgist(v WITH "+tc.op+"))", 0))
				run(fixture.Exec("INSERT INTO spgist_ex VALUES('"+tc.value+"'),(NULL),(NULL)", 3))
				conflict := fixture.ExclusionReferenceConflict("spgist_ex", "spgist_exclusion", "v", tc.value, tc.value)
				classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("INSERT INTO spgist_ex VALUES('"+tc.value+"')", conflict))
				classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("INSERT INTO spgist_ex VALUES(NULL),('"+tc.value+"')", conflict))
				run(fixture.Select("SELECT count(*) AS n FROM spgist_ex", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				run(fixture.Exec("CREATE TABLE spgist_deferred(v "+tc.typ+",CONSTRAINT spgist_deferred_exclusion EXCLUDE USING spgist(v WITH "+tc.op+") DEFERRABLE INITIALLY DEFERRED)", 0))
				run(fixture.Exec("BEGIN", 0))
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("INSERT INTO spgist_deferred VALUES('"+tc.value+"')", 1))
				run(fixture.Exec("SAVEPOINT prefix", 0))
				run(fixture.Exec("INSERT INTO spgist_deferred VALUES('"+tc.value+"')", 1))
				run(fixture.ErrorDiagnostic("SET CONSTRAINTS spgist_deferred_exclusion IMMEDIATE", fixture.ExclusionReferenceConflict("spgist_deferred", "spgist_deferred_exclusion", "v", tc.value, tc.value)))
				run(fixture.Exec("ROLLBACK TO prefix", 0))
				run(fixture.Exec("SET CONSTRAINTS spgist_deferred_exclusion IMMEDIATE", 0))
				run(fixture.Exec("COMMIT", 0))
				classRoutes(t, p, b, conn, fixture.Select("SELECT count(*) AS n FROM spgist_deferred", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
	for _, op := range []string{"<", "^@", "<>"} {
		t.Run("invalid_"+op, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				d := fixture.ControlsReferenceDiagnostic("42809", "operator "+op+"(text,text) is not commutative")
				d.Detail = "Only commutative operators can be used in exclusion constraints."
				if op == "<>" {
					d.Message = `operator <>(text,text) is not a member of operator family "text_ops"`
					d.Detail = "The exclusion operator must be related to the index operator class for the constraint."
				}
				classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE TABLE spgist_invalid_ex(v text,EXCLUDE USING spgist(v WITH "+op+"))", d))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='spgist_invalid_ex'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
	t.Run("concurrent_commit", func(t *testing.T) {
		fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
			run(a, fixture.Exec("CREATE TABLE t(v text,CONSTRAINT ex EXCLUDE USING spgist(v WITH =))", 0))
			run(a, fixture.Exec("BEGIN", 0))
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			run(a, fixture.Exec("INSERT INTO t VALUES('a')", 1))
			var ap, bp int
			if b.PG {
				if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
					t.Fatal(err)
				}
				if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
					t.Fatal(err)
				}
			}
			if !b.PG {
				run(peer, fixture.Exec("SET lock_timeout='10ms'", 0))
				run(peer, fixture.ErrorDiagnostic("INSERT INTO t VALUES('a')", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
				run(peer, fixture.Exec("SET lock_timeout=0", 0))
			}
			done := fixture.RowAsync(t, p.Ctx, peer, "INSERT INTO t VALUES('a')", true, false)
			if b.PG {
				fixture.RowWait(t, p.Ctx, bp, ap)
			}
			run(a, fixture.Exec("COMMIT", 0))
			out := fixture.LockResult(t, done)
			d := fixture.ExclusionReferenceConflict("t", "ex", "v", "a", "a")
			fixture.AssertDiagnostic(t, out.Err, &d)
			run(peer, fixture.Exec("INSERT INTO t VALUES('b')", 1))
			run(peer, fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
		})
	})
}

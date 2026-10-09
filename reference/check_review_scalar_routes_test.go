package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// The shorter parameter stream retains the independent direct/newly prepared
// positive routes; the companion scenario adds pre-change held-frame reuse.
func TestReferenceCheckReviewScalarCommentDMLRoutes(t *testing.T) {
	for _, provider := range []struct{ name, expr, target string }{
		{"object", "obj_description('anchor'::regclass,'pg_class')", "TABLE anchor"},
		{"legacy", "obj_description('anchor'::regclass)", "TABLE anchor"},
		{"column", "col_description('anchor'::regclass,1)", "COLUMN anchor.a"},
		{"shared", "shobj_description(10,'pg_authid')", "ROLE postgres"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", provider.name, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						conn, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
						expr := provider.expr
						if provider.name == "shared" && b.pg {
							var oid int64
							var prior sql.NullString
							if err := conn.QueryRowContext(p.ctx, "SELECT oid,shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='postgres'").Scan(&oid, &prior); err != nil || oid <= 0 || oid > 1<<32-1 {
								t.Fatal(oid, err)
							}
							expr = fmt.Sprintf("shobj_description(%d,'pg_authid')", oid)
							defer func() {
								value := "NULL"
								if prior.Valid {
									value = "'" + strings.ReplaceAll(prior.String, "'", "''") + "'"
								}
								if _, err := conn.ExecContext(context.Background(), "COMMENT ON ROLE postgres IS "+value); err != nil {
									t.Error(err)
								}
							}()
						}
						run(referenceExec("CREATE TABLE anchor(a INT)", 0))
						run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(referenceExec("CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
						run(referenceExec("INSERT INTO t VALUES($1)", 1, "old"))
						run(referenceExec("COMMENT ON "+provider.target+" IS 'new'", 0))
						run(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "old"), "old"))
						run(referenceExec("INSERT INTO t VALUES($1)", 1, "new"))
						run(referenceExec("UPDATE t SET a=$1", 2, "new"))
						run(referenceImplicitError("UPDATE t SET a=$1", referenceCheckViolation("t", "c", "old"), "old"))
						run(referenceExec("INSERT INTO t SELECT 'new'", 1))
						run(referenceExec("INSERT INTO t VALUES('new') ORDER BY 1", 1))
						run(referenceImplicitError("INSERT INTO t SELECT 'old'", referenceCheckViolation("t", "c", "old")))
						run(referenceImplicitError("INSERT INTO t VALUES('old') ORDER BY 1", referenceCheckViolation("t", "c", "old")))
						s := referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"new"}, {"new"}, {"new"}, {"new"}})
						s.bag = true
						run(s)
						run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(referenceExec("INSERT INTO t VALUES($1)", 1, "old"))
						run(referenceExec("UPDATE t SET a=$1", 5, "old"))
						run(referenceExec("INSERT INTO t DEFAULT VALUES", 1))
						s = referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}})
						s.bag = true
						run(s)
					})
				}
			})
		}
	}
}

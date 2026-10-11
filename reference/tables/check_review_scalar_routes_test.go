package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
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
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
						expr := provider.expr
						if provider.name == "shared" && b.PG {
							var oid int64
							var prior sql.NullString
							if err := conn.QueryRowContext(p.Ctx, "SELECT oid,shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='postgres'").Scan(&oid, &prior); err != nil || oid <= 0 || oid > 1<<32-1 {
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
						run(fixture.Exec("CREATE TABLE anchor(a INT)", 0))
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(fixture.Exec("CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
						run(fixture.Exec("INSERT INTO t VALUES($1)", 1, "old"))
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'new'", 0))
						run(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "c", "old"), "old"))
						run(fixture.Exec("INSERT INTO t VALUES($1)", 1, "new"))
						run(fixture.Exec("UPDATE t SET a=$1", 2, "new"))
						run(fixture.ImplicitError("UPDATE t SET a=$1", fixture.CheckViolation("t", "c", "old"), "old"))
						run(fixture.Exec("INSERT INTO t SELECT 'new'", 1))
						run(fixture.Exec("INSERT INTO t VALUES('new') ORDER BY 1", 1))
						run(fixture.ImplicitError("INSERT INTO t SELECT 'old'", fixture.CheckViolation("t", "c", "old")))
						run(fixture.ImplicitError("INSERT INTO t VALUES('old') ORDER BY 1", fixture.CheckViolation("t", "c", "old")))
						s := fixture.Select("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"new"}, {"new"}, {"new"}, {"new"}})
						s.Bag = true
						run(s)
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(fixture.Exec("INSERT INTO t VALUES($1)", 1, "old"))
						run(fixture.Exec("UPDATE t SET a=$1", 5, "old"))
						run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
						s = fixture.Select("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}})
						s.Bag = true
						run(s)
					})
				}
			})
		}
	}
}

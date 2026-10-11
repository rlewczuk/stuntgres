package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashIndexStandaloneUniqueBoundaries(t *testing.T) {
	for _, method := range []string{"btree", "gin", "missing"} {
		for _, prefix := range []struct{ name, sql string }{{"plain", ""}, {"unicode", "/*é*/ "}} {
			t.Run(method+"-"+prefix.name, func(t *testing.T) {
				hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					q := prefix.sql + "CREATE UNIQUE INDEX boundary ON t USING " + method + "(n)"
					if method == "gin" {
						want := fixture.Diagnostic{Code: "0A000", Message: `access method "gin" does not support unique indexes`}
						if !b.PG {
							want.Message = "non-btree indexes are unsupported"
						}
						hashIndexSemanticFailure(t, p, b, run, q, want)
					} else if method == "missing" {
						hashIndexSemanticFailure(t, p, b, run, q, fixture.Diagnostic{Code: "42704", Message: `access method "missing" does not exist`})
					} else {
						for _, prepared := range []bool{false, true} {
							s := fixture.Exec(q, 0)
							s.Prepared = prepared
							run(s)
							run(fixture.Exec("DROP INDEX boundary", 0))
						}
						for _, nonContext := range []bool{false, true} {
							var stmt *sql.Stmt
							var err error
							if nonContext {
								stmt, err = b.DB.Prepare(q)
							} else {
								stmt, err = b.DB.PrepareContext(p.Ctx, q)
							}
							if err != nil {
								t.Fatal("UNIQUE semantic admission was not deferred", err)
							}
							run(fixture.Select("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
							for range 2 {
								s := fixture.Exec(q, 0)
								s.NonContext = nonContext
								fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
								run(fixture.Exec("DROP INDEX boundary", 0))
							}
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						}
					}
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			})
		}
	}
}

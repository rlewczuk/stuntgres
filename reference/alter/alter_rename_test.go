package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameValues(t *testing.T) {
	for _, utilityPrepared := range []bool{false, true} {
		t.Run(fmt.Sprint(utilityPrepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s")
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,'O''Brien',9),(8,NULL,10)", 2))
			columnRename := fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0)
			columnRename.Prepared = utilityPrepared
			p.Step(t, columnRename)
			tableRename := fixture.Exec("ALTER TABLE t RENAME TO newtable", 0)
			tableRename.Prepared = utilityPrepared
			p.Step(t, tableRename)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}})
				s.Prepared = prepared
				p.Step(t, s)
				for _, backend := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM newtable", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, 2)
				}
			}
			for _, tc := range []struct{ q, code, message, position string }{{"SELECT a FROM newtable", "42703", `column "a" does not exist`, "8"}, {"SELECT * FROM t", "42P01", `relation "t" does not exist`, "15"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					p.Step(t, s)
				}
			}
			p.Step(t, fixture.ImplicitError("INSERT INTO newtable VALUES(NULL,'bad',11)", fixture.Diagnostic{Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (null, bad, 11).", Schema: "public", Table: "newtable", Column: "renamed"}))
			p.Step(t, fixture.Exec("CREATE SCHEMA s CREATE TABLE same(a INT)", 0))
			p.Step(t, fixture.Exec("ALTER TABLE newtable RENAME TO same", 0))
			p.Step(t, fixture.Exec("ALTER TABLE s.same RENAME TO same_schema_local", 0))
			p.Step(t, fixture.Select("SELECT * FROM same ORDER BY renamed", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}}))
			long := strings.Repeat("界", 22)
			short := strings.Repeat("界", 21)
			p.Step(t, fixture.Exec(`CREATE TABLE quoted("select" INT)`, 0))
			p.Step(t, fixture.Exec(`INSERT INTO quoted VALUES(7)`, 1))
			p.Step(t, fixture.Exec(`ALTER TABLE quoted RENAME "select" TO "`+long+`"`, 0))
			p.Step(t, fixture.Prepared(fixture.Select(`SELECT "`+short+`" FROM quoted`, []string{short}, []string{"INT4"}, [][]any{{int64(7)}})))
		})
	}
}

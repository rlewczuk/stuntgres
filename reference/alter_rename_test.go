package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceAlterRenameValues(t *testing.T) {
	for _, utilityPrepared := range []bool{false, true} {
		t.Run(fmt.Sprint(utilityPrepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			schemaReferenceCleanup(t, "s")
			p.step(t, referenceExec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(7,'O''Brien',9),(8,NULL,10)", 2))
			columnRename := referenceExec("ALTER TABLE t RENAME COLUMN a TO renamed", 0)
			columnRename.prepared = utilityPrepared
			p.step(t, columnRename)
			tableRename := referenceExec("ALTER TABLE t RENAME TO newtable", 0)
			tableRename.prepared = utilityPrepared
			p.step(t, tableRename)
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}})
				s.prepared = prepared
				p.step(t, s)
				for _, backend := range p.backends() {
					referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, "SELECT * FROM newtable", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, 2)
				}
			}
			for _, tc := range []struct{ q, code, message, position string }{{"SELECT a FROM newtable", "42703", `column "a" does not exist`, "8"}, {"SELECT * FROM t", "42P01", `relation "t" does not exist`, "15"}} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					p.step(t, s)
				}
			}
			p.step(t, referenceImplicitError("INSERT INTO newtable VALUES(NULL,'bad',11)", referenceDiagnostic{Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (null, bad, 11).", Schema: "public", Table: "newtable", Column: "renamed"}))
			p.step(t, referenceExec("CREATE SCHEMA s CREATE TABLE same(a INT)", 0))
			p.step(t, referenceExec("ALTER TABLE newtable RENAME TO same", 0))
			p.step(t, referenceExec("ALTER TABLE s.same RENAME TO same_schema_local", 0))
			p.step(t, referenceSelect("SELECT * FROM same ORDER BY renamed", []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}}))
			long := strings.Repeat("界", 22)
			short := strings.Repeat("界", 21)
			p.step(t, referenceExec(`CREATE TABLE quoted("select" INT)`, 0))
			p.step(t, referenceExec(`INSERT INTO quoted VALUES(7)`, 1))
			p.step(t, referenceExec(`ALTER TABLE quoted RENAME "select" TO "`+long+`"`, 0))
			p.step(t, referencePrepared(referenceSelect(`SELECT "`+short+`" FROM quoted`, []string{short}, []string{"INT4"}, [][]any{{int64(7)}})))
		})
	}
}

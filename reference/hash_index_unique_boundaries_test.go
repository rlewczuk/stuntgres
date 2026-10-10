package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceHashIndexStandaloneUniqueBoundaries(t *testing.T) {
	for _,method:=range []string{"btree","gin","missing"} {
		for _,prefix:=range []struct{name,sql string}{{"plain",""},{"unicode","/*é*/ "}} {
			t.Run(method+"-"+prefix.name,func(t *testing.T){
				hashIndexScenario(t,func(t *testing.T,p *referencePair,b referenceBackend,run func(referenceStep)){
					run(referenceExec("CREATE TABLE t(n INT)",0));run(referenceExec("INSERT INTO t VALUES(1),(2)",2))
					q:=prefix.sql+"CREATE UNIQUE INDEX boundary ON t USING "+method+"(n)"
					if method=="gin" {
						want := referenceDiagnostic{Code:"0A000",Message:`access method "gin" does not support unique indexes`}
						if !b.pg { want.Message = "non-btree indexes are unsupported" }
						hashIndexSemanticFailure(t,p,b,run,q,want)
					} else if method=="missing" {
						hashIndexSemanticFailure(t,p,b,run,q,referenceDiagnostic{Code:"42704",Message:`access method "missing" does not exist`})
					} else {
						for _, prepared := range []bool{false, true} {
							s := referenceExec(q, 0)
							s.prepared = prepared
							run(s)
							run(referenceExec("DROP INDEX boundary", 0))
						}
						for _, nonContext := range []bool{false, true} {
							var stmt *sql.Stmt
							var err error
							if nonContext {
								stmt, err = b.db.Prepare(q)
							} else {
								stmt, err = b.db.PrepareContext(p.ctx, q)
							}
							if err != nil {
								t.Fatal("UNIQUE semantic admission was not deferred", err)
							}
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
							for range 2 {
								s := referenceExec(q, 0)
								s.nonContext = nonContext
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
								run(referenceExec("DROP INDEX boundary", 0))
							}
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						}
					}
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='boundary'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			})
		}
	}
}

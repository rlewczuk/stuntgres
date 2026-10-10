package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferencePartialIndexHashBoundary(t *testing.T) {
	for _, predicate := range []string{"active", "TRUE", "FALSE", "NULL"} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(predicate+"/"+prefix, func(t *testing.T) {
				partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
					run(referenceExec("CREATE TABLE t(id INT,n INT,active BOOL)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,7,TRUE),(2,7,FALSE),(3,7,NULL),(4,NULL,TRUE)", 4))
					const name = "partial_hash_boundary"
					q := prefix + "CREATE INDEX " + name + " ON t USING hash(n) WITH(fillfactor=75) WHERE " + predicate
					absent := referenceSelect("SELECT relname FROM pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil)
					unchanged := referenceSelect("SELECT id,n,active FROM t ORDER BY id", []string{"id", "n", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), true}, {int64(2), int64(7), false}, {int64(3), int64(7), nil}, {int64(4), nil, true}})
					if !b.pg {
						before := partialIndexDump(t, p)
						want := referenceDiagnostic{Code: "0A000", Message: "partial indexes are unsupported"}
						hashIndexSemanticFailure(t, p, b, run, q, want)
						plain := referenceImplicitError(q, want)
						plain.nonContext = true
						plain.errorStage = "execute"
						run(plain)
						run(absent)
						run(unchanged)
						if after := partialIndexDump(t, p); after != before {
							t.Fatal("unsupported partial HASH changed Dump", before, after)
						}
					} else {
						for _, plain := range []bool{false, true} {
							s := referenceExec(q, 0)
							s.nonContext = plain
							run(s)
							run(unchanged)
							run(partialIndexIDs("SELECT id FROM t WHERE n=7 ORDER BY id", 1, 2, 3))
							run(referenceExec("DROP INDEX "+name, 0))
							var stmt *sql.Stmt
							var err error
							if plain {
								stmt, err = b.db.Prepare(q)
							} else {
								stmt, err = b.db.PrepareContext(p.ctx, q)
							}
							if err != nil {
								t.Fatal(err)
							}
							run(absent)
							for range 2 {
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}, b.recorder, s)
								run(unchanged)
								run(referenceExec("DROP INDEX "+name, 0))
							}
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						}
					}
					// The same name recovers through the unchanged ordinary HASH owner.
					run(referencePrepared(referenceExec("CREATE INDEX "+name+" ON t USING hash(n)", 0)))
					run(unchanged)
					run(partialIndexIDs("SELECT id FROM t WHERE n=7 ORDER BY id", 1, 2, 3))
					run(partialIndexIDs("SELECT id FROM t WHERE n IS NULL ORDER BY id", 4))
					for _, definition := range []struct{ sql, message string }{
						{"CREATE UNIQUE INDEX hash_precedence ON t USING hash(n) WHERE active", `access method "hash" does not support unique indexes`},
						{"CREATE INDEX hash_precedence ON t USING hash(n) INCLUDE(id) WHERE active", `access method "hash" does not support included columns`},
						{"CREATE INDEX hash_precedence ON t USING hash(n,id) WHERE active", `access method "hash" does not support multicolumn indexes`},
					} {
						hashIndexSemanticFailure(t, p, b, run, prefix+definition.sql, referenceDiagnostic{Code: "0A000", Message: definition.message})
					}
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='hash_precedence'", []string{"relname"}, []string{"NAME"}, nil))
					run(unchanged)
				})
			})
		}
	}
}

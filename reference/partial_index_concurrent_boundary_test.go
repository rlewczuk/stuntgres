package reference_test

import (
	"fmt"
	"testing"
)

func TestReferencePartialIndexConcurrentBoundary(t *testing.T) {
	for _, predicate := range []string{"active", "TRUE"} {
		for _, unique := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				for _, plain := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/unique=%t/held=%t/plain=%t", predicate, unique, held, plain), func(t *testing.T) {
						partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
							run := func(s referenceStep) { s.nonContext = plain; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
							run(referenceExec("CREATE TABLE c(id INT,k INT,active BOOL)", 0))
							run(referenceExec("INSERT INTO c VALUES(1,7,FALSE),(2,8,NULL),(3,9,TRUE)", 3))
							prefix := "CREATE INDEX "
							if unique {
								prefix = "CREATE UNIQUE INDEX "
							}
							q := prefix + "CONCURRENTLY pc ON c(k) WHERE " + predicate
							var before string
							if !b.pg {
								before = partialIndexDump(t, p)
							}
							if b.pg {
								s := referenceExec(q, 0)
								s.prepared = held
								run(s)
								definition := prefix + "pc ON public.c USING btree (k)"
								if predicate != "TRUE" {
									definition += " WHERE active"
								}
								run(referenceSelect("SELECT pg_get_indexdef('pc'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
								if unique {
									s := referenceErrorDiagnostic("INSERT INTO c VALUES(4,9,TRUE)", uniqueReferenceDuplicate("c", "pc", "Key (k)=(9) already exists."))
									s.prepared = held
									run(s)
								}
								run(referenceExec("DROP INDEX pc", 0))
							} else {
								s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("0A000", "partial concurrent indexes are unsupported"))
								s.prepared, s.errorStage = held, "execute"
								run(s)
								run(referenceSelect("SELECT relname FROM pg_class WHERE relname='pc'", []string{"relname"}, []string{"NAME"}, nil))
								if after := partialIndexDump(t, p); after != before {
									t.Fatal("rejected concurrent predicate changed Dump")
								}
							}
							run(referenceSelect("SELECT id,k,active FROM c ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(2), int64(8), nil}, {int64(3), int64(9), true}}))
							// Both existing owners remain available under the rejected name.
							s := referenceExec(prefix+"pc ON c(k) WHERE "+predicate, 0)
							s.prepared = held
							run(s)
							run(referenceExec("DROP INDEX pc", 0))
							s = referenceExec(prefix+"CONCURRENTLY pc ON c(k)", 0)
							s.prepared = held
							run(s)
							run(referenceExec("DROP INDEX pc", 0))
						})
					})
				}
			}
		}
	}
}

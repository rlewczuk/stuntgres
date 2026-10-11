package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartialIndexConcurrentBoundary(t *testing.T) {
	for _, predicate := range []string{"active", "TRUE"} {
		for _, unique := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				for _, plain := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/unique=%t/held=%t/plain=%t", predicate, unique, held, plain), func(t *testing.T) {
						partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
							run := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
							run(fixture.Exec("CREATE TABLE c(id INT,k INT,active BOOL)", 0))
							run(fixture.Exec("INSERT INTO c VALUES(1,7,FALSE),(2,8,NULL),(3,9,TRUE)", 3))
							prefix := "CREATE INDEX "
							if unique {
								prefix = "CREATE UNIQUE INDEX "
							}
							q := prefix + "CONCURRENTLY pc ON c(k) WHERE " + predicate
							var before string
							if !b.PG {
								before = partialIndexDump(t, p)
							}
							if b.PG {
								s := fixture.Exec(q, 0)
								s.Prepared = held
								run(s)
								definition := prefix + "pc ON public.c USING btree (k)"
								if predicate != "TRUE" {
									definition += " WHERE active"
								}
								run(fixture.Select("SELECT pg_get_indexdef('pc'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
								if unique {
									s := fixture.ErrorDiagnostic("INSERT INTO c VALUES(4,9,TRUE)", fixture.UniqueReferenceDuplicate("c", "pc", "Key (k)=(9) already exists."))
									s.Prepared = held
									run(s)
								}
								run(fixture.Exec("DROP INDEX pc", 0))
							} else {
								s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "partial concurrent indexes are unsupported"))
								s.Prepared, s.ErrorStage = held, "execute"
								run(s)
								run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc'", []string{"relname"}, []string{"NAME"}, nil))
								if after := partialIndexDump(t, p); after != before {
									t.Fatal("rejected concurrent predicate changed Dump")
								}
							}
							run(fixture.Select("SELECT id,k,active FROM c ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(2), int64(8), nil}, {int64(3), int64(9), true}}))
							// Both existing owners remain available under the rejected name.
							s := fixture.Exec(prefix+"pc ON c(k) WHERE "+predicate, 0)
							s.Prepared = held
							run(s)
							run(fixture.Exec("DROP INDEX pc", 0))
							s = fixture.Exec(prefix+"CONCURRENTLY pc ON c(k)", 0)
							s.Prepared = held
							run(s)
							run(fixture.Exec("DROP INDEX pc", 0))
						})
					})
				}
			}
		}
	}
}

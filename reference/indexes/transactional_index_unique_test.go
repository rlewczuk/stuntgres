package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexUnique(t *testing.T) {
	for _, own := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, policy := range []string{"", " NULLS NOT DISTINCT"} {
				t.Run(fmt.Sprintf("own=%t/held=%t/policy=%s", own, held, policy), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							run := transactionalIndexRun(t, p, b, c)
							run(fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0))
							if !own {
								run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(1,'x')", 2))
							}
							run(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							if own {
								run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(1,'x')", 2))
							}
							run(fixture.Exec("SAVEPOINT before_build", 0))
							q := "CREATE UNIQUE INDEX idx ON t(n,s)" + policy
							var e fixture.Executor = c
							if held {
								stmt, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							}
							s := fixture.ErrorDiagnostic(q, uniqueIndexReferenceBuildDiagnostic("t", "idx", "Key (n, s)=(1, x) is duplicated."))
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
							run(fixture.ErrorDiagnostic("SELECT 1", transactionalIndexAborted()))
							run(fixture.Exec("ROLLBACK TO before_build", 0))
							run(transactionalIndexName("idx", false))
							run(fixture.Select("SELECT n,s FROM t ORDER BY n,s", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "x"}}))
							run(fixture.Exec("DELETE FROM t", 2))
							run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(1,'y'),(2,'x'),(NULL,'x'),(1,NULL),(NULL,NULL)", 6))
							fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
							run(fixture.Exec("SAVEPOINT writes", 0))
							run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'x')", fixture.UniqueReferenceDuplicate("t", "idx", "Key (n, s)=(1, x) already exists.")))
							run(fixture.Exec("ROLLBACK TO writes", 0))
							if policy == "" {
								run(fixture.Exec("INSERT INTO t VALUES(NULL,'x'),(1,NULL),(NULL,NULL)", 3))
							} else {
								for _, tc := range []struct{ values, detail string }{{"(NULL,'x')", "null, x"}, {"(1,NULL)", "1, null"}, {"(NULL,NULL)", "null, null"}} {
									run(fixture.ErrorDiagnostic("INSERT INTO t VALUES"+tc.values, fixture.UniqueReferenceDuplicate("t", "idx", "Key (n, s)=("+tc.detail+") already exists.")))
									run(fixture.Exec("ROLLBACK TO writes", 0))
								}
							}
							transactionalIndexRun(t, p, b, peer)(transactionalIndexName("idx", false))
							run(fixture.Exec("ROLLBACK", 0))
							run(transactionalIndexName("idx", false))
							count := int64(2)
							if own {
								count = 0
							}
							run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
						})
					}
				})
			}
		}
	}
}

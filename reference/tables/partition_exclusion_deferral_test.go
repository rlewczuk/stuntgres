package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionDeferral(t *testing.T) {
	for _, target := range []string{"ALL", "pe_ex", "pe_low_tenant_during_excl"} {
		partitionExclusionCase(t, "set_immediate/"+target, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			run := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.ExclusionReferenceRun(t, p, b, c, s)
			}
			partitionExclusionTree(run, "DEFERRABLE INITIALLY DEFERRED")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
			run(fixture.Exec("SAVEPOINT before_conflict", 0))
			// The empty middle member must not hide the nonadjacent overlap.
			run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.1','empty'),(3,'10.0.0.1','[3,7)')", 2))
			run(partitionExclusionConflict("SET CONSTRAINTS "+target+" IMMEDIATE", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
			run(fixture.Exec("ROLLBACK TO before_conflict", 0))
			run(fixture.Exec("SET CONSTRAINTS "+target+" IMMEDIATE", 0))
			run(fixture.Exec("INSERT INTO pe_root VALUES(4,'10.0.0.2','[3,7)')", 1))
			run(fixture.Exec("COMMIT", 0))
			run(partitionExclusionIDs(1, 4))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "sql_commit", true: "go_commit"}[prepared], func(t *testing.T) {
			partitionExclusionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
				partitionExclusionTree(func(s fixture.Step) { run(c, s) }, "DEFERRABLE INITIALLY DEFERRED")
				want := fixture.ExclusionReferenceConflict("pe_low", "pe_low_tenant_during_excl", "tenant, during", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")
				if prepared {
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.1','empty')", 2))
					run(tx, fixture.Prepared(fixture.Exec("INSERT INTO pe_root VALUES(3,'10.0.0.1','[3,7)')", 1)))
					fixture.AssertDiagnostic(t, tx.Commit(), &want)
				} else {
					run(c, fixture.Exec("BEGIN", 0))
					run(c, fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.1','empty'),(3,'10.0.0.1','[3,7)')", 3))
					run(c, fixture.ErrorDiagnostic("COMMIT", want))
				}
				run(c, partitionExclusionIDs())
				run(c, fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
			})
		})
	}
	partitionExclusionCase(t, "ordinary_repair_uses_same_provider", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.ExclusionReferenceRun(t, p, b, c, s)
		}
		// Ordinary-table repair shares the exclusion provider; hierarchy DELETE
		// remains outside this witness.
		run(fixture.Exec("CREATE TABLE pe_ordinary(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_ordinary_ex EXCLUDE USING gist("+partitionExclusionKeys+") DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO pe_ordinary VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.1','[3,7)'),(3,'10.0.0.1','[2,4)')", 3))
		run(fixture.Exec("UPDATE pe_ordinary SET during='[5,8)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM pe_ordinary WHERE id=3", 1))
		run(fixture.Exec("SET CONSTRAINTS pe_ordinary_ex IMMEDIATE", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("SELECT id,during FROM pe_ordinary ORDER BY id", []string{"id", "during"}, []string{"INT4", "INT4RANGE"}, [][]any{{int64(1), "[1,5)"}, {int64(2), "[5,8)"}}))
	})
}

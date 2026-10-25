package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionRows(t *testing.T) {
	partitionExclusionCase(t, "overlap_and_typed_parameters", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
		const q = "INSERT INTO pe_root VALUES($1,$2,$3)"
		run(partitionExclusionConflict(q, "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)", int64(2), "10.0.0.1", "[3,7)"))
		run(fixture.Exec(q, 1, int64(3), "10.0.0.2", "[3,7)"))
		run(fixture.Exec(q, 1, int64(4), "10.0.0.129", "[3,7)"))
		run(fixture.Exec(q, 1, int64(5), "10.0.0.1", "[5,8)"))
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.ExclusionReferenceRun(t, p, b, held, partitionExclusionConflict(q, "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,4)", "10.0.0.1, [1,5)", int64(6), "10.0.0.1", "[3,4)"))
		run(partitionExclusionIDs(1, 3, 4, 5))
		s := fixture.Select("SELECT id,tenant,during FROM pe_root ORDER BY id", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, [][]any{{int64(1), "10.0.0.1", "[1,5)"}, {int64(3), "10.0.0.2", "[3,7)"}, {int64(4), "10.0.0.129", "[3,7)"}, {int64(5), "10.0.0.1", "[5,8)"}})
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true, true})
		run(s)
		run(partitionExclusionGraph())
	})
	partitionExclusionCase(t, "null_empty_canonical_unbounded", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','empty'),(2,'10.0.0.1','empty'),(3,'10.0.0.1',NULL),(4,'10.0.0.1',NULL),(5,NULL,'[1,5)'),(6,NULL,'[1,5)'),(7,'10.0.0.1','(0,4]'),(8,'10.0.0.1','[5,)')", 8))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(9,'10.0.0.1','(,2)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, (,2)", "10.0.0.1, [1,5)"))
		s := fixture.Select("SELECT id,during FROM pe_root ORDER BY id", []string{"id", "during"}, []string{"INT4", "INT4RANGE"}, [][]any{{int64(1), "empty"}, {int64(2), "empty"}, {int64(3), nil}, {int64(4), nil}, {int64(5), "[1,5)"}, {int64(6), "[1,5)"}, {int64(7), "[1,5)"}, {int64(8), "[5,)"}})
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
		run(s)
	})
	for _, producer := range []string{"VALUES(2,'10.0.0.129','[1,5)'),(3,'10.0.0.1','[3,7)')", "SELECT id,tenant,during FROM pe_source ORDER BY id"} {
		partitionExclusionCase(t, "late_statement_conflict/"+producer, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			partitionExclusionTree(run, "")
			run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
			run(fixture.Exec("CREATE TABLE pe_source(id INT,tenant INET,during INT4RANGE)", 0))
			run(fixture.Exec("INSERT INTO pe_source VALUES(2,'10.0.0.129','[1,5)'),(3,'10.0.0.1','[3,7)')", 2))
			run(partitionExclusionConflict("INSERT INTO pe_root "+producer, "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
			run(partitionExclusionIDs(1))
			run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.129','[1,5)')", 1))
			run(partitionExclusionIDs(1, 2))
		})
	}
	partitionExclusionCase(t, "predicate_and_expression", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE,active BOOL,CONSTRAINT pe_ex EXCLUDE USING gist(tenant inet_ops WITH =,(during * during) WITH &&) WHERE(active)) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)',TRUE),(2,'10.0.0.1','[3,7)',FALSE),(3,'10.0.0.1','[3,7)',NULL),(4,'10.0.0.1','empty',TRUE),(5,'10.0.0.1','empty',TRUE),(7,'10.0.0.1',NULL,TRUE),(8,'10.0.0.1',NULL,TRUE)", 7))
		run(fixture.ErrorDiagnostic("INSERT INTO pe_root VALUES(6,'10.0.0.1','[3,7)',TRUE)", fixture.ExclusionReferenceConflict("pe_low", "pe_low_tenant_expr_excl", "tenant, (during * during)", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")))
		run(partitionExclusionIDs(1, 2, 3, 4, 5, 7, 8))
	})
}

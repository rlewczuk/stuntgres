package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeTransactions(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := analyzeConn(t, p, b)
		observer := analyzeConn(t, p, b)
		run := func(s fixture.Step) { analyzeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE analyze_tx(a INT,b INT)", 0))
		analyzeInsert(t, p, b, c, "analyze_tx", 10, 0, 0)
		run(fixture.Exec("CREATE TABLE analyze_late(y INT)", 0))
		run(fixture.Exec("ANALYZE analyze_tx", 0))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(fixture.Prepared(fixture.Exec("ANALYZE analyze_tx", 0)))
		run(fixture.Exec("COMMIT", 0))
		unseen := int64(0)
		if b.PG {
			unseen = 1
		}
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		analyzeInsert(t, p, b, c, "analyze_tx", 0, 10, 0)
		run(fixture.Exec("ANALYZE analyze_tx", 0))
		analyzeTuples(t, p, b, c, "analyze_tx", 20)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=1", 10)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=2", 10)
		// Maintenance is global, column facts are private. The observer's old
		// all-1 distribution is scaled by the new global relation estimate.
		analyzeTuples(t, p, b, observer, "analyze_tx", 20)
		analyzeWantEstimate(t, p, observer, "analyze_tx", "a=1", 20)
		analyzeWantEstimate(t, p, observer, "analyze_tx", "a=2", unseen)
		run(fixture.Exec("SAVEPOINT analyze_mark", 0))
		run(fixture.Exec("UPDATE analyze_tx SET a=3", 20))
		run(fixture.Exec("ANALYZE analyze_tx(a)", 0))
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=3", 20)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT analyze_mark", 0))
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=1", 10)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=3", unseen)
		run(fixture.Exec("ROLLBACK", 0))
		analyzeTuples(t, p, b, c, "analyze_tx", 20)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=1", 20)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=2", unseen)
		// Missing relations are resolved before any work; missing columns are
		// checked per target, so prior standalone publications survive.
		analyzeInsert(t, p, b, c, "analyze_tx", 0, 10, 0)
		run(fixture.ErrorDiagnostic("ANALYZE analyze_tx,analyze_absent", analyzeError("42P01", `relation "analyze_absent" does not exist`)))
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=2", unseen)
		run(fixture.ErrorDiagnostic("ANALYZE analyze_tx,analyze_late(nope)", analyzeError("42703", `column "nope" of relation "analyze_late" does not exist`)))
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=2", 10)
		// Failed explicit work never leaks the earlier target's column delta.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE analyze_tx SET a=3", 20))
		run(fixture.ErrorDiagnostic("ANALYZE analyze_tx,analyze_late(nope)", analyzeError("42703", `column "nope" of relation "analyze_late" does not exist`)))
		run(fixture.ErrorDiagnostic("SELECT 1", analyzeError("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		run(fixture.Exec("ROLLBACK", 0))
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=2", 10)
		analyzeWantEstimate(t, p, c, "analyze_tx", "a=3", unseen)
		// Private DDL/DML and statistics compose and publish at normal COMMIT.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE TABLE analyze_private(a INT)", 0))
		run(fixture.Exec("INSERT INTO analyze_private VALUES(8),(8),(8)", 3))
		run(fixture.Exec("ANALYZE analyze_private", 0))
		analyzeWantEstimate(t, p, c, "analyze_private", "a=8", 3)
		run(fixture.Exec("COMMIT", 0))
		analyzeWantEstimate(t, p, observer, "analyze_private", "a=8", 3)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE analyze_tx SET a=3", 20))
		run(fixture.Prepared(fixture.Exec("ANALYZE analyze_tx(a)", 0)))
		analyzeWantEstimate(t, p, observer, "analyze_tx", "a=2", 10)
		run(fixture.Exec("COMMIT", 0))
		analyzeWantEstimate(t, p, observer, "analyze_tx", "a=3", 20)
	})
}

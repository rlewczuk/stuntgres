package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBrinIndexLifecycle(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		run(fixture.Exec("CREATE INDEX bn ON t USING brin(n) WITH(pages_per_range=2,autosummarize=on)", 0))
		run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT brin_nonnegative CHECK(n>=0)", 0))
		hashIndexSemanticFailure(t, p, b, run, "INSERT INTO t VALUES(8,8,'good'),(9,-1,'bad')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "brin_nonnegative"`, Detail: "Failing row contains (9, -1, bad).", Schema: "public", Table: "t", Constraint: "brin_nonnegative"})
		run(brinIndexIDs("id>=8"))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		trun(fixture.Exec("INSERT INTO t VALUES(7,0,'')", 1))
		trun(brinIndexIDs("n=0", 7))
		trun(fixture.Exec("SAVEPOINT brin_save", 0))
		trun(fixture.Exec("UPDATE t SET n=2 WHERE id=2", 1))
		trun(brinIndexIDs("n=2", 2))
		trun(fixture.Exec("ROLLBACK TO brin_save", 0))
		trun(brinIndexIDs("n=9", 2))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(brinIndexIDs("n=0"))
		run(brinIndexIDs("n=9", 2))
		tx, err = b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO t VALUES(7,0,'')", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(brinIndexIDs("n=0", 7))
		run(fixture.Exec("ALTER INDEX bn RENAME TO renamed_brin", 0))
		run(fixture.Exec("ALTER TABLE t ALTER n TYPE BIGINT", 0))
		run(fixture.Exec("REINDEX INDEX renamed_brin", 0))
		run(brinIndexIDs("n>=4", 2, 3, 5, 6))
		run(fixture.Exec("DROP INDEX renamed_brin", 0))
		run(brinIndexIDs("n>=4", 2, 3, 5, 6))
	})
}

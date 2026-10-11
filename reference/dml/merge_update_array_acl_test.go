package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateArrayAuthority(t *testing.T) {
	mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,a INT[])", "CREATE TABLE source(id INT)", "CREATE TABLE indices(i INT)", "GRANT UPDATE(a) ON t TO merge_update_actor", "GRANT SELECT ON source TO merge_update_actor"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,ARRAY[10,20])", 1))
		run(fixture.Exec("INSERT INTO indices VALUES(1)", 1))
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_actor", 0)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// No source candidates and FALSE action do not waive bound subscript authority.
		q := "MERGE INTO t USING source s ON TRUE WHEN MATCHED AND FALSE THEN UPDATE SET a[(SELECT i FROM indices)]=7"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table indices"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		run(fixture.Exec("GRANT SELECT(i) ON indices TO merge_update_actor", 0))
		s = fixture.Exec(q, 0)
		s.Prepared = prepared
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		run(fixture.ArrayReferenceSelect("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{10,20}"}}, b.PG))
		// Implicit preservation of the old array is not a SELECT reference in
		// PostgreSQL's assignment transform. Exercise UPDATE and MERGE with only
		// UPDATE(a), then contrast an explicit old-value RHS read.
		s = fixture.Exec("UPDATE t SET a[1]=7", 1)
		s.Prepared = prepared
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
		s = fixture.Exec("MERGE INTO t USING source s ON TRUE WHEN MATCHED THEN UPDATE SET a[(SELECT i FROM indices)]=8", 1)
		s.Prepared = prepared
		if b.PG {
			s.Affected = 0 // lib/pq's MERGE result export; rows prove the update.
		}
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		explicit := "MERGE INTO t USING source s ON TRUE WHEN MATCHED THEN UPDATE SET a[1]=t.a[2]"
		s = fixture.ImplicitError(explicit, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
		run(fixture.ArrayReferenceSelect("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{8,20}"}}, b.PG))
	})
}

package reference_test

import "testing"

func TestReferenceMergeInsertHeldACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"merge_held_writer"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := mergeInsertRun(t, p, b, false)
			if b.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0)) }
			for _, q := range []string{"CREATE USER merge_held_writer", "CREATE TABLE t(id INT,v INT DEFAULT 9)", "CREATE TABLE source(id INT)", "GRANT INSERT(id,v),SELECT(id) ON t TO merge_held_writer", "GRANT SELECT(id) ON source TO merge_held_writer"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO source VALUES(1)", 1))
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("SET SESSION AUTHORIZATION merge_held_writer", 0))
			defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED AND s.id>0 THEN INSERT(id) VALUES(s.id) WHEN NOT MATCHED THEN INSERT(v) VALUES(1)"
			held, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			execute := func(denied string) {
				result, err := held.ExecContext(p.ctx)
				if denied == "" {
					mergeInsertAffected(t, b, result, err, 1)
					return
				}
				if result != nil {
					t.Fatal("denied held command exposed result")
				}
				d := controlsReferenceDiagnostic("42501", "permission denied for table "+denied)
				referenceAssertDiagnostic(t, err, &d)
			}
			read := func(n int) referenceStep {
				rows := make([][]any, n)
				for i := range rows {
					rows[i] = []any{int64(i + 1), int64(9)}
				}
				return referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
			}
			execute("")
			run(read(1))
			run(referenceExec("INSERT INTO source VALUES(2)", 1))
			run(referenceExec("REVOKE INSERT(v) ON t FROM merge_held_writer", 0))
			execute("t")
			run(read(1))
			run(referenceExec("GRANT INSERT(v) ON t TO merge_held_writer", 0))
			execute("")
			run(read(2))
			run(referenceExec("INSERT INTO source VALUES(3)", 1))
			run(referenceExec("REVOKE SELECT(id) ON source FROM merge_held_writer", 0))
			execute("source")
			run(read(2))
			run(referenceExec("GRANT SELECT(id) ON source TO merge_held_writer", 0))
			execute("")
			run(read(3))
		})
	}
}

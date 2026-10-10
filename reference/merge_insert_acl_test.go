package reference_test

import "testing"

func TestReferenceMergeInsertAuthorization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			if *referenceMode {
				roleReferenceCleanup(t, []string{"merge_denied_actor"})
			}
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := mergeInsertRun(t, p, b, prepared)
					run(referenceExec("CREATE USER merge_denied_actor", 0))
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					run(referenceExec("CREATE TABLE s(id INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(99)", 1))
					if b.pg {
						run(referenceExec("GRANT USAGE ON SCHEMA public TO merge_denied_actor", 0))
					}
					c, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("SET SESSION AUTHORIZATION merge_denied_actor", 0))
					defer func() {
						if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
					}()
					for _, q := range []string{
						"MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(1)",
						"MERGE INTO t USING (VALUES(99)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
					} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
						s.prepared, s.errorStage = prepared, "execute"
						insertSelectRun(t, p.ctx, c, b.recorder, s)
					}
					insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				})
			}
		})
	}
}

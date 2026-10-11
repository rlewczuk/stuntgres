package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeletePrivileges(t *testing.T) {
	for _, tc := range []struct {
		name, grants, on, arms string
		empty, denied          bool
	}{
		{"deleteOnly", "GRANT DELETE ON t TO merge_update_actor", "TRUE", "WHEN MATCHED THEN DELETE", false, false},
		{"targetReadDenied", "GRANT DELETE ON t TO merge_update_actor", "t.id=s.id", "WHEN MATCHED THEN DELETE", false, true},
		{"targetReadGranted", "GRANT DELETE,SELECT(id) ON t TO merge_update_actor", "t.id=s.id", "WHEN MATCHED THEN DELETE", false, false},
		{"falseDeleteDenied", "GRANT SELECT(id) ON t TO merge_update_actor", "t.id=s.id", "WHEN MATCHED AND FALSE THEN DELETE", false, true},
		{"emptyDeleteDenied", "GRANT SELECT(id) ON t TO merge_update_actor", "t.id=s.id", "WHEN MATCHED THEN DELETE", true, true},
		{"emptyBySourceDenied", "GRANT SELECT(id) ON t TO merge_update_actor", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN DELETE", true, true},
		{"mixedUpdateDenied", "GRANT DELETE,SELECT(id) ON t TO merge_update_actor", "t.id=s.id", "WHEN MATCHED AND FALSE THEN UPDATE SET v=99 WHEN MATCHED THEN DELETE", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
				}
				for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "GRANT SELECT(id) ON source TO merge_update_actor", tc.grants} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				if !tc.empty {
					run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
				}
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_actor", 0)
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "MERGE INTO t USING source s ON " + tc.on + " " + tc.arms
				s := fixture.Exec(q, 1)
				if tc.denied {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
					s.ErrorStage = "execute"
				}
				s.Prepared = prepared
				if b.PG {
					s.Affected = 0
				}
				fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
				var want [][]any
				if tc.denied {
					want = [][]any{{int64(1), int64(10)}}
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceMergeDeleteHeldACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"merge_delete_held"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			}
			for _, q := range []string{"CREATE USER merge_delete_held", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "GRANT DELETE,SELECT(id) ON t TO merge_delete_held", "GRANT SELECT(id) ON source TO merge_delete_held"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_delete_held", 0)
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			held, err := c.PrepareContext(p.Ctx, "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND $1::boolean THEN DELETE")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			r, err := held.ExecContext(p.Ctx, false)
			mergeInsertAffected(t, b, r, err, 0)
			for _, tc := range []struct{ table, priv string }{{"t", "DELETE"}, {"t", "SELECT(id)"}, {"source", "SELECT(id)"}} {
				run(fixture.Exec("REVOKE "+tc.priv+" ON "+tc.table+" FROM merge_delete_held", 0))
				r, err = held.ExecContext(p.Ctx, false)
				if r != nil {
					t.Fatal("denied held result")
				}
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table "+tc.table)
				fixture.AssertDiagnostic(t, err, &d)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				run(fixture.Exec("GRANT "+tc.priv+" ON "+tc.table+" TO merge_delete_held", 0))
				r, err = held.ExecContext(p.Ctx, false)
				mergeInsertAffected(t, b, r, err, 0)
			}
			r, err = held.ExecContext(p.Ctx, true)
			mergeInsertAffected(t, b, r, err, 1)
			run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
		})
	}
}

func TestReferenceMergeDeleteReadOnly(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, owner, "BEGIN READ ONLY", 0)
		s := fixture.ImplicitError("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DELETE", fixture.Diagnostic{Code: "25006", Message: "cannot execute MERGE in a read-only transaction"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		fixture.LockExec(t, p.Ctx, owner, "ROLLBACK", 0)
	})
}

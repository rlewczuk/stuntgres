package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdatePrivileges(t *testing.T) {
	for _, tc := range []struct {
		name, grants, arms string
		denied             bool
	}{
		{"updateOnly", "GRANT UPDATE(a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=7", false},
		{"targetRead", "GRANT UPDATE(a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=t.a+1", true},
		{"conditionRead", "GRANT UPDATE(a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE AND t.a=10 THEN UPDATE SET a=7", true},
		{"falseDestination", "GRANT UPDATE(a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET secret=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=7", true},
		{"emptyInsertAuthority", "GRANT UPDATE(a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=7 WHEN NOT MATCHED THEN INSERT(id,a) VALUES(s.id,8)", true},
		{"mixedGranted", "GRANT UPDATE(a),INSERT(id,a) ON t TO merge_update_actor", "WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=7 WHEN NOT MATCHED THEN INSERT(id,a) VALUES(s.id,8)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
				}
				for _, q := range []string{"CREATE USER merge_update_actor", "CREATE TABLE t(id INT,a INT,secret INT)", "CREATE TABLE source(id INT)", "GRANT SELECT(id) ON source TO merge_update_actor", tc.grants} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(1,10,99)", 1))
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_actor", 0)
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "MERGE INTO t USING source s ON FALSE " + tc.arms
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
				a := int64(10)
				if !tc.denied {
					a = 7
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "a", "secret"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), a, int64(99)}}))
			})
		})
	}
}

func TestReferenceMergeSourceUpdateHeldACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"merge_update_held"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			}
			for _, q := range []string{"CREATE USER merge_update_held", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE source(id INT)", "GRANT SELECT(id),UPDATE(v) ON t TO merge_update_held", "GRANT SELECT(id) ON source TO merge_update_held"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO t VALUES(2,10)", 1))
			run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_held", 0)
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			held, err := c.PrepareContext(p.Ctx, "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=20")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			execute := func(denied string) {
				r, err := held.ExecContext(p.Ctx)
				if denied == "" {
					mergeInsertAffected(t, b, r, err, 1)
				} else {
					if r != nil {
						t.Fatal("denied held result")
					}
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table "+denied)
					fixture.AssertDiagnostic(t, err, &d)
				}
			}
			execute("")
			for _, tc := range []struct{ table, priv string }{{"t", "UPDATE(v)"}, {"source", "SELECT(id)"}} {
				run(fixture.Exec("REVOKE "+tc.priv+" ON "+tc.table+" FROM merge_update_held", 0))
				execute(tc.table)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
				run(fixture.Exec("GRANT "+tc.priv+" ON "+tc.table+" TO merge_update_held", 0))
				execute("")
			}
		})
	}
}

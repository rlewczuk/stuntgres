package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// An INSERT action requires authority even when its destination manifest is
// empty, its condition is false, or another action handles every candidate.
func TestReferenceMergeUpdateDefaultInsertAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, grants, source, on, arms string
		denied                         bool
	}{
		{"selectedDenied", "", "(VALUES(1)) s(id)", "FALSE", "WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", true},
		{"falseDenied", "", "(VALUES(1)) s(id)", "FALSE", "WHEN NOT MATCHED AND FALSE THEN INSERT DEFAULT VALUES", true},
		{"emptyDenied", "", "(SELECT 1 AS id WHERE FALSE) s", "FALSE", "WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", true},
		{"mixedDenied", "GRANT UPDATE(a) ON t TO merge_update_actor", "(VALUES(1)) s(id)", "TRUE", "WHEN MATCHED THEN UPDATE SET a=8 WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", true},
		{"columnAllowed", "GRANT INSERT(b) ON t TO merge_update_actor", "(VALUES(1)) s(id)", "FALSE", "WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", false},
		{"relationAllowed", "GRANT INSERT ON t TO merge_update_actor", "(VALUES(1)) s(id)", "FALSE", "WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeUpdateACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
				}
				for _, q := range []string{
					"CREATE USER merge_update_actor",
					"CREATE TABLE t(a INT DEFAULT 7,b INT DEFAULT 9)",
				} {
					run(fixture.Exec(q, 0))
				}
				if tc.grants != "" {
					run(fixture.Exec(tc.grants, 0))
				}
				run(fixture.Exec("INSERT INTO t VALUES(99,100)", 1))
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_update_actor", 0)
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				q := "MERGE INTO t USING " + tc.source + " ON " + tc.on + " " + tc.arms
				s := fixture.Exec(q, 1)
				if b.PG {
					s.Affected = 0
				}
				if tc.denied {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
					s.ErrorStage = "execute"
				}
				s.Prepared = prepared
				fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
				want := [][]any{{int64(99), int64(100)}}
				if !tc.denied {
					want = [][]any{{int64(7), int64(9)}, {int64(99), int64(100)}}
				}
				run(fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceMergeUpdateDefaultInsertHeldACL(t *testing.T) {
	for _, privilege := range []string{"INSERT(b)", "INSERT"} {
		t.Run(map[string]string{"INSERT(b)": "column", "INSERT": "relation"}[privilege], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"merge_default_held"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := fixture.MergeRun(t, p, b, false)
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
					}
					for _, q := range []string{
						"CREATE USER merge_default_held",
						"CREATE TABLE t(a INT DEFAULT 7,b INT DEFAULT 9)",
						"GRANT " + privilege + " ON t TO merge_default_held",
					} {
						run(fixture.Exec(q, 0))
					}
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION merge_default_held", 0)
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					held, err := c.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					execute := func(denied bool) {
						result, err := held.ExecContext(p.Ctx)
						if !denied {
							mergeInsertAffected(t, b, result, err, 1)
							return
						}
						if result != nil {
							t.Fatal("denied held DEFAULT VALUES command exposed result")
						}
						d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
						fixture.AssertDiagnostic(t, err, &d)
					}
					read := func(n int) {
						rows := make([][]any, n)
						for i := range rows {
							rows[i] = []any{int64(7), int64(9)}
						}
						run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, rows))
					}
					execute(false)
					read(1)
					run(fixture.Exec("REVOKE "+privilege+" ON t FROM merge_default_held", 0))
					execute(true)
					read(1)
					run(fixture.Exec("GRANT "+privilege+" ON t TO merge_default_held", 0))
					execute(false)
					read(2)
				})
			}
		})
	}
}

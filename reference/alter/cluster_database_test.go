package reference_test

import (
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterCommandDatabaseScenario(t *testing.T) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"cluster_db_maintainer"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			run := func(s fixture.Step) { persistenceRun(t, p, b, c, s) }
			// Empty selection must be successful and create no worker.
			run(fixture.Exec("CLUSTER", 0))
			for _, q := range []string{
				"CREATE ROLE cluster_db_maintainer", "GRANT USAGE,CREATE ON SCHEMA public TO cluster_db_maintainer",
				"CREATE TABLE db_a(id int)", "CREATE INDEX db_a_idx ON db_a(id)", "ALTER TABLE db_a CLUSTER ON db_a_idx",
				"CREATE TABLE db_b(id int)", "CREATE INDEX db_b_idx ON db_b(id DESC)", "ALTER TABLE db_b CLUSTER ON db_b_idx",
				"CREATE TABLE db_unmarked(id int)", "CREATE INDEX db_unmarked_idx ON db_unmarked(id)",
				"CREATE TABLE db_denied(id int)", "CREATE INDEX db_denied_idx ON db_denied(id)", "ALTER TABLE db_denied CLUSTER ON db_denied_idx",
				"GRANT MAINTAIN ON db_a,db_b TO cluster_db_maintainer",
			} {
				persistenceExec(t, p, b, c, q)
			}
			for _, table := range []string{"db_a", "db_b", "db_unmarked", "db_denied"} {
				run(fixture.Exec("INSERT INTO "+table+" VALUES(2),(1),(2)", 3))
			}
			foreign, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer foreign.Close()
			for _, q := range []string{"CREATE TEMP TABLE db_foreign_temp(id int)", "CREATE INDEX db_foreign_idx ON db_foreign_temp(id)", "ALTER TABLE db_foreign_temp CLUSTER ON db_foreign_idx"} {
				persistenceExec(t, p, b, foreign, q)
			}
			persistenceExec(t, p, b, c, "SET ROLE cluster_db_maintainer")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			for _, q := range []string{"CREATE TEMP TABLE db_own_temp(id int)", "CREATE INDEX db_own_idx ON db_own_temp(id)", "ALTER TABLE db_own_temp CLUSTER ON db_own_idx"} {
				persistenceExec(t, p, b, c, q)
			}
			run(fixture.Exec("INSERT INTO db_own_temp VALUES(3),(1)", 2))
			notices = nil
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("CLUSTER", 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				want := []fixture.Diagnostic{
					{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to cluster "db_denied", skipping it`},
					{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to cluster "db_foreign_temp", skipping it`},
				}
				// Database target traversal order is not a public contract.
				sort.Slice(notices, func(i, j int) bool { return notices[i].Message < notices[j].Message })
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("CLUSTER warnings=%+v want=%+v", notices, want)
				}
				notices = nil
			}
			// Authorization admits the remembered foreign TEMP target; its heap
			// is locked before the execution-time foreign-session exclusion.
			persistenceExec(t, p, b, foreign, "GRANT MAINTAIN ON db_foreign_temp TO cluster_db_maintainer")
			foreignTx, err := foreign.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer foreignTx.Rollback()
			persistenceExec(t, p, b, foreignTx, "LOCK TABLE db_foreign_temp IN ACCESS EXCLUSIVE MODE")
			persistenceExec(t, p, b, c, "SET lock_timeout='10ms'")
			run(fixture.ErrorDiagnostic("CLUSTER", persistenceDiagnostic("55P03", "canceling statement due to lock timeout")))
			if err = foreignTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			persistenceExec(t, p, b, c, "RESET lock_timeout")
			notices = nil
			run(fixture.Exec("CLUSTER", 0))
			want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to cluster "db_denied", skipping it`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("authorized foreign TEMP warnings=%+v want=%+v", notices, want)
			}
			notices = nil
			run(fixture.Select("SELECT id FROM db_own_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			persistenceExec(t, p, b, c, "BEGIN")
			run(fixture.ErrorDiagnostic("CLUSTER", persistenceDiagnostic("25001", "CLUSTER cannot run inside a transaction block")))
			persistenceExec(t, p, b, c, "ROLLBACK")
			persistenceExec(t, p, b, c, "RESET ROLE")
			for _, table := range []string{"db_a", "db_b", "db_unmarked", "db_denied"} {
				run(fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}))
				run(clusterState(table, [][]any{{table + "_idx", table != "db_unmarked"}}))
			}
			run(clusterState("db_own_temp", [][]any{{"db_own_idx", true}}))
			persistenceRun(t, p, b, foreign, clusterState("db_foreign_temp", [][]any{{"db_foreign_idx", true}}))
			run(clusterCommandProgressIdle())
		})
	}
}

func TestReferenceClusterDatabaseSelection(t *testing.T) { clusterCommandDatabaseScenario(t) }

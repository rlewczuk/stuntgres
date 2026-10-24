package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexSavepoints(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "release-outer-rollback", true: "commit-prefix"}[commit], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, c)
					for _, q := range []string{"CREATE TABLE early(n INT)", "CREATE TABLE later(n INT)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("INSERT INTO early VALUES(1)", 1))
					run(fixture.Exec("CREATE INDEX early_idx ON early(n)", 0))
					run(fixture.Exec("SAVEPOINT kept", 0))
					for range 2 {
						run(fixture.Exec("INSERT INTO later VALUES(2)", 1))
						run(fixture.Exec("CREATE INDEX later_idx ON later(n)", 0))
						run(fixture.Exec("ROLLBACK TO kept", 0))
						run(transactionalIndexName("early_idx", true))
						run(transactionalIndexName("later_idx", false))
						run(fixture.Select("SELECT n FROM early", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT n FROM later", []string{"n"}, []string{"INT4"}, nil))
					}
					run(fixture.Exec("INSERT INTO later VALUES(1),(1)", 2))
					s := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX failed_idx ON later(n)", uniqueIndexReferenceBuildDiagnostic("later", "failed_idx", "Key (n)=(1) is duplicated."))
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ErrorDiagnostic("SELECT 1", transactionalIndexAborted()))
					run(fixture.Exec("ROLLBACK TO kept", 0))
					run(transactionalIndexName("failed_idx", false))
					run(transactionalIndexName("early_idx", true))
					run(fixture.Exec("CREATE UNIQUE INDEX failed_idx ON later(n)", 0))
					run(fixture.Exec("ROLLBACK TO kept", 0))
					run(fixture.Exec("RELEASE kept", 0))
					if commit {
						run(fixture.Exec("COMMIT", 0))
					} else {
						run(fixture.Exec("ROLLBACK", 0))
					}
					check := transactionalIndexRun(t, p, b, peer)
					check(transactionalIndexName("early_idx", commit))
					check(transactionalIndexName("later_idx", false))
					check(transactionalIndexName("failed_idx", false))
					var rows [][]any
					if commit {
						rows = [][]any{{int64(1)}}
					}
					check(fixture.Select("SELECT n FROM early", []string{"n"}, []string{"INT4"}, rows))
					check(fixture.Select("SELECT n FROM later", []string{"n"}, []string{"INT4"}, nil))
				})
			}
		})
	}
}

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterAdditionalKinds(t *testing.T) {
	t.Helper()
	t.Run("foreign-and-composite", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			persistenceExec(t, p, b, c, "CREATE FOREIGN DATA WRAPPER cluster_fdw")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER cluster_fdw CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{"CREATE SERVER cluster_server FOREIGN DATA WRAPPER cluster_fdw", "CREATE FOREIGN TABLE foreign_t(id int) SERVER cluster_server", "CREATE TYPE pair AS(id int)"} {
				persistenceExec(t, p, b, c, q)
			}
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"ALTER TABLE foreign_t SET WITHOUT OIDS", "ALTER TABLE foreign_t SET WITHOUT OIDS, SET WITHOUT OIDS"} {
					step := fixture.Exec(q, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					persistenceRun(t, p, b, c, step)
				}
				for _, action := range []string{"CLUSTER ON missing_idx", "SET WITHOUT CLUSTER", "SET WITHOUT OIDS"} {
					label := action
					if action == "CLUSTER ON missing_idx" {
						label = "CLUSTER ON"
					}
					want := persistenceDiagnostic("42809", `"pair" is a composite type`)
					want.Hint = "Use ALTER TYPE instead."
					persistenceFailure(t, p, b, c, "ALTER TABLE pair "+action, prepared, want)
					if action != "SET WITHOUT OIDS" {
						want = persistenceDiagnostic("42809", "ALTER action "+label+` cannot be performed on relation "foreign_t"`)
						want.Detail = "This operation is not supported for foreign tables."
						persistenceFailure(t, p, b, c, "ALTER TABLE foreign_t "+action, prepared, want)
					}
				}
			}
		})
	})
	t.Run("partition-validation-order", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE p(id int,note text) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF p DEFAULT", "CREATE INDEX method_idx ON p USING spgist(note)", "CREATE UNIQUE INDEX partial_idx ON p(id) WHERE id>0"} {
				persistenceExec(t, p, b, c, q)
			}
			for _, prepared := range []bool{false, true} {
				persistenceFailure(t, p, b, c, "ALTER TABLE p CLUSTER ON method_idx", prepared, persistenceDiagnostic("0A000", `cannot cluster on index "method_idx" because access method does not support clustering`))
				persistenceFailure(t, p, b, c, "ALTER TABLE p CLUSTER ON partial_idx", prepared, persistenceDiagnostic("0A000", `cannot cluster on partial index "partial_idx"`))
			}
			persistenceRun(t, p, b, c, clusterState("p", [][]any{{"method_idx", false}, {"partial_idx", false}}))
		})
	})
}

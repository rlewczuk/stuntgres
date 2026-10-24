package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForCancellation(t *testing.T) {
	for _, body := range []string{"", "CONTINUE;"} {
		for _, prepared := range []bool{false, true} {
			name := map[bool]string{false: "direct", true: "held"}[prepared]
			if body == "" {
				name += "_empty"
			} else {
				name += "_continue"
			}
			t.Run(name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					// Finite but long enough to observe an admitted range walker.
					userOperatorExec(t, p, b, c, "CREATE FUNCTION if_cancel() RETURNS void LANGUAGE plpgsql AS $$BEGIN FOR i IN -2147483648..2147483647 LOOP "+body+" END LOOP; END$$")
					q := "SELECT if_cancel() AS v"
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					action := "FOR with integer loop variable"
					if body != "" {
						action = "CONTINUE"
					}
					fixture.PLpgSQLIntegerForActiveCancel(t, p, b, c, q, "PL/pgSQL function if_cancel() line 1 at "+action, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if stmt != nil {
							rows, err = stmt.QueryContext(ctx)
						} else {
							rows, err = c.QueryContext(ctx, q)
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					plpgsqlValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
				})
			})
		}
	}
}

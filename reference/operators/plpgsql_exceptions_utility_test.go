package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionUtilityPublication(t *testing.T) {
	for _, route := range []string{"select_direct", "select_prepared", "do"} {
		t.Run(route, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ex_set_outer() RETURNS integer LANGUAGE SQL AS $$SET application_name TO 'outer'; SELECT 1$$")
				body := "DECLARE x integer; BEGIN x := ex_put(1); BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_set_outer(); x := ex_put(3); END; x := ex_put(4);"
				if route == "do" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DO $$"+body+" END$$", 0))
				} else {
					plpgsqlExceptionDefine(t, p, b, c, "ex_utility", "integer", body+" RETURN x; END")
					step := fixture.Select("SELECT ex_utility() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}})
					if route == "select_prepared" {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
				// Rollback removes only protected put2. SET completes a nested
				// utility, not the root's still-pending row publication.
				plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outer"}}))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				plpgsqlExceptionRows(t, p, b, peer, 1, 3, 4)
			})
		})
	}
}

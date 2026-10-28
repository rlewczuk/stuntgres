package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwValidatorAudit(rows [][]any) fixture.Step {
	return fixture.Select("SELECT kind::bigint FROM fdw_validator_audit ORDER BY kind", []string{"kind"}, []string{"INT8"}, rows)
}

func TestReferenceFDWSQLValidatorTableEffects(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			for _, column := range []bool{false, true} {
				name, class := "table", int64(3118)
				if column {
					name, class = "column", 1249
				}
				t.Run(name, func(t *testing.T) {
					fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE fdw_validator_audit(kind oid)", 0))
						run(fixture.Exec("CREATE TABLE fdw_validator_gate(v integer)", 0))
						run(fixture.Exec("INSERT INTO fdw_validator_gate VALUES(0)", 1))
						run(fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; SELECT 1'", 0))
						run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0))
						run(fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v", 0))
						run(fixture.Exec("CREATE FOREIGN TABLE fdw_effects(id integer OPTIONS(marker 'incumbent')) SERVER fdw_vs OPTIONS(marker 'incumbent')", 0))
						run(fixture.Exec("TRUNCATE fdw_validator_audit", 0))
						run(fixture.Exec("INSERT INTO fdw_validator_audit VALUES(99)", 1))
						var oid int64
						if err := c.QueryRowContext(p.Ctx, "SELECT 'fdw_effects'::regclass::oid::bigint").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						query := "ALTER FOREIGN TABLE fdw_effects OPTIONS(SET marker 'accepted')"
						metadata := "SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid=$1"
						if column {
							query = "ALTER FOREIGN TABLE fdw_effects ALTER COLUMN id OPTIONS(SET marker 'accepted')"
							metadata = "SELECT attfdwoptions::text AS options FROM pg_attribute WHERE attrelid=$1 AND attname='id'"
						}
						check := func(value string, rows [][]any) {
							run(fixture.Select(metadata, []string{"options"}, []string{"TEXT"}, [][]any{{value}}, oid))
							run(fdwValidatorAudit(rows))
						}
						execute := func(step fixture.Step) {
							step.Prepared = prepared
							run(step)
						}
						accepted := [][]any{{int64(99)}, {class}}
						execute(fixture.Exec(query, 0))
						check("{marker=accepted}", accepted)
						query = strings.ReplaceAll(query, "'accepted'", "'rejected'")
						// Both rejection routes reach a transactional INSERT before failing.
						for _, nullResult := range []bool{false, true} {
							body := "SELECT 1/v FROM public.fdw_validator_gate"
							d := fdwDiagnostic("22012", "division by zero")
							d.Context = `SQL function "fdw_accept" statement 2`
							if nullResult {
								body = "SELECT NULL::integer"
								var procOID int64
								if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='fdw_accept' AND pronamespace='public'::regnamespace").Scan(&procOID); err != nil {
									t.Fatal(err)
								}
								d = fdwDiagnostic("XX000", fmt.Sprintf("function %d returned NULL", procOID))
							}
							run(fixture.Exec("CREATE OR REPLACE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; "+body+"'", 0))
							execute(fixture.ErrorDiagnostic(query, d))
							check("{marker=accepted}", accepted)
						}
						run(fixture.Exec("CREATE OR REPLACE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'INSERT INTO public.fdw_validator_audit SELECT $2; SELECT 1'", 0))
						run(fixture.Exec("BEGIN", 0))
						rollbackQuery := "ALTER FOREIGN TABLE fdw_effects OPTIONS(SET marker 'rolledback')"
						if column {
							rollbackQuery = "ALTER FOREIGN TABLE fdw_effects ALTER COLUMN id OPTIONS(SET marker 'rolledback')"
						}
						execute(fixture.Exec(rollbackQuery, 0))
						check("{marker=rolledback}", [][]any{{int64(99)}, {class}, {class}})
						run(fixture.Exec("ROLLBACK", 0))
						check("{marker=accepted}", accepted)
					})
				})
			}
		})
	}
}

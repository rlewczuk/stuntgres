package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each leaf owns an unregistered driver: ordinary SQL validators must not gain
// executable authority from an application callback registration.
func fdwValidatorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"fdw_validator_owner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Errorf("validator connection close: %v", err)
				}
			})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{
					"ROLLBACK", "RESET ROLE",
					"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_v CASCADE",
					"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_null_target CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_accept(text[],oid) CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_guard(text[],oid) CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_text(text[],oid) CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_void(text[],oid) CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_null_integer(text[],oid) CASCADE",
					"DROP FUNCTION IF EXISTS public.fdw_null_text(text[],oid) CASCADE",
				} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Errorf("cleanup %s: %v", q, err)
					}
				}
			})
			body(t, p, b, c, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) })
		})
	}
}

func fdwValidatorDivision() fixture.Diagnostic {
	d := fdwDiagnostic("22012", "division by zero")
	// Pinned PostgreSQL 18.6 reports this SQL routine execution context.
	d.Context = `SQL function "fdw_guard" statement 1`
	return d
}

func fdwValidatorServerOptions(value any) fixture.Step {
	return fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname='fdw_vs'", []string{"options"}, []string{"TEXT"}, [][]any{{value}})
}

func TestReferenceFDWSQLValidator(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("ignored-results", func(t *testing.T) {
				fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					for _, step := range []fixture.Step{
						fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0),
						fixture.Exec("CREATE FUNCTION public.fdw_text(text[],oid) RETURNS text LANGUAGE SQL AS 'SELECT ''ignored''::text'", 0),
						fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0),
						fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(arbitrary 'value')", 0),
						fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_vs OPTIONS(secret '')", 0),
						fdwValidatorServerOptions("{arbitrary=value}"),
						fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_text OPTIONS(ADD arbitrary 'text')", 0),
					} {
						step.Prepared = prepared
						run(step)
					}
					for _, definition := range []string{
						"CREATE FUNCTION public.fdw_void(text[],oid) RETURNS void LANGUAGE SQL AS 'SELECT 1'",
						"CREATE FUNCTION public.fdw_null_integer(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT NULL::integer'",
						"CREATE FUNCTION public.fdw_null_text(text[],oid) RETURNS text LANGUAGE SQL AS 'SELECT NULL::text'",
					} {
						step := fixture.Exec(definition, 0)
						step.Prepared = prepared
						run(step)
					}
					for _, name := range []string{"fdw_void", "fdw_null_integer", "fdw_null_text"} {
						var oid int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname=$1 AND pronamespace='public'::regnamespace", name).Scan(&oid); err != nil {
							t.Fatal(err)
						}
						nullResult := fdwDiagnostic("XX000", fmt.Sprintf("function %d returned NULL", oid))
						for _, step := range []fixture.Step{
							fixture.ErrorDiagnostic("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public."+name+" OPTIONS(SET arbitrary 'rejected')", nullResult),
							fixture.Select("SELECT fdwoptions::text AS options,fdwvalidator=(SELECT oid FROM pg_proc WHERE proname='fdw_text' AND pronamespace='public'::regnamespace) AS same_validator FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", []string{"options", "same_validator"}, []string{"TEXT", "BOOL"}, [][]any{{"{arbitrary=text}", true}}),
							fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_null_target VALIDATOR public."+name, nullResult),
							fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_null_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
						} {
							step.Prepared = prepared
							run(step)
						}
					}
				})
			})
			for _, class := range []int{2328, 1417, 1418} {
				t.Run(fmt.Sprintf("empty-class-%d", class), func(t *testing.T) {
					fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
						execute := func(step fixture.Step) { step.Prepared = prepared; run(step) }
						// A runtime denominator avoids planner-time folding of 1/0.
						body := fmt.Sprintf("SELECT 1 / CASE WHEN $1 IS NOT NULL AND cardinality($1)=0 AND $2=%d::oid THEN cardinality($1) ELSE 1 END", class)
						execute(fixture.Exec("CREATE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL STRICT AS '"+body+"'", 0))
						if class == 2328 {
							execute(fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard", fdwValidatorDivision()))
							run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
						if class == 2328 {
							execute(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard OPTIONS(seed 'wrapper')", 0))
						} else {
							execute(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard", 0))
							execute(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v OPTIONS(ADD seed 'wrapper')", 0))
						}
						if class == 1417 {
							execute(fixture.ErrorDiagnostic("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v", fdwValidatorDivision()))
							run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_vs'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
						if class == 1417 {
							execute(fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'server')", 0))
						} else {
							execute(fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v", 0))
							execute(fixture.Exec("ALTER SERVER fdw_vs OPTIONS(ADD seed 'server')", 0))
						}
						if class == 1418 {
							execute(fixture.ErrorDiagnostic("CREATE USER MAPPING FOR PUBLIC SERVER fdw_vs", fdwValidatorDivision()))
							run(fixture.Select("SELECT count(*) FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_vs')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
						if class == 1418 {
							execute(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_vs OPTIONS(seed 'mapping')", 0))
						} else {
							execute(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_vs", 0))
							execute(fixture.Exec("ALTER USER MAPPING FOR PUBLIC SERVER fdw_vs OPTIONS(ADD seed 'mapping')", 0))
						}
						target, catalog, predicate, column := "FOREIGN DATA WRAPPER fdw_v", "pg_foreign_data_wrapper", "fdwname='fdw_v'", "fdwoptions"
						value := "{seed=wrapper}"
						if class == 1417 {
							target, catalog, predicate, column, value = "SERVER fdw_vs", "pg_foreign_server", "srvname='fdw_vs'", "srvoptions", "{seed=server}"
						} else if class == 1418 {
							target, catalog, predicate, column, value = "USER MAPPING FOR PUBLIC SERVER fdw_vs", "pg_user_mapping", "umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_vs')", "umoptions", "{seed=mapping}"
						}
						execute(fixture.ErrorDiagnostic("ALTER "+target+" OPTIONS(DROP seed)", fdwValidatorDivision()))
						run(fixture.Select("SELECT "+column+"::text AS options FROM "+catalog+" WHERE "+predicate, []string{"options"}, []string{"TEXT"}, [][]any{{value}}))
						// Nonempty targets and ancestors remain usable after both failures.
						run(fdwValidatorServerOptions("{seed=server}"))
						run(fixture.Select("SELECT fdwoptions::text AS options FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", []string{"options"}, []string{"TEXT"}, [][]any{{"{seed=wrapper}"}}))
					})
				})
			}
			t.Run("function-dependency", func(t *testing.T) {
				fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					execute := func(step fixture.Step) { step.Prepared = prepared; run(step) }
					execute(fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
					execute(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0))
					execute(fixture.ErrorDiagnostic("DROP FUNCTION public.fdw_accept(text[],oid)", fixture.PgDependRestrict("function fdw_accept(text[],oid)", "foreign-data wrapper fdw_v depends on function fdw_accept(text[],oid)")))
					run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					execute(fixture.Exec("DROP FUNCTION public.fdw_accept(text[],oid) CASCADE", 0))
					run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			})
			t.Run("replacement", func(t *testing.T) {
				fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					execute := func(step fixture.Step) { step.Prepared = prepared; run(step) }
					execute(fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
					execute(fixture.Exec("CREATE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL STRICT AS 'SELECT 1 / (cardinality($1)-cardinality($1))'", 0))
					execute(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept OPTIONS(seed 'wrapper')", 0))
					execute(fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'server')", 0))
					execute(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_vs OPTIONS(seed 'mapping')", 0))
					execute(fixture.Exec("CREATE TABLE fdw_validator_before AS SELECT oid,fdwvalidator FROM pg_foreign_data_wrapper WHERE fdwname='fdw_v'", 1))
					execute(fixture.ErrorDiagnostic("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard OPTIONS(SET seed 'rejected')", fdwValidatorDivision()))
					run(fixture.Select("SELECT w.oid=b.oid AS same_object,w.fdwvalidator=b.fdwvalidator AS same_validator,w.fdwoptions::text AS options FROM pg_foreign_data_wrapper w CROSS JOIN fdw_validator_before b WHERE w.fdwname='fdw_v'", []string{"same_object", "same_validator", "options"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "{seed=wrapper}"}}))
					execute(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard", 0))
					run(fdwValidatorServerOptions("{seed=server}"))
					execute(fixture.ErrorDiagnostic("ALTER SERVER fdw_vs OPTIONS(SET seed 'rejected')", fdwValidatorDivision()))
					run(fdwValidatorServerOptions("{seed=server}"))
					execute(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept OPTIONS(SET seed 'accepted')", 0))
					execute(fixture.Exec("ALTER SERVER fdw_vs OPTIONS(SET seed 'accepted')", 0))
					run(fdwValidatorServerOptions("{seed=accepted}"))
				})
			})
		})
	}
	t.Run("native-roundtrip", fdwValidatorRoundtrip)
}

func fdwValidatorRoundtrip(t *testing.T) {
	fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE ROLE fdw_validator_owner SUPERUSER", 0))
		run(fixture.Exec("CREATE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL STRICT AS 'SELECT 1 / CASE WHEN $1 IS NOT NULL AND cardinality($1)=0 AND $2=1417::oid THEN cardinality($1) ELSE 1 END'", 0))
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard", 0))
		run(fixture.Exec("CREATE SERVER fdw_vs TYPE '' VERSION NULL FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'quote''=雪')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_validator_owner SERVER fdw_vs OPTIONS(secret '',note 'a=b')", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v OWNER TO fdw_validator_owner", 0))
		run(fixture.Exec("ALTER SERVER fdw_vs OWNER TO fdw_validator_owner", 0))
		metadata := []fixture.Step{
			fixture.Select("SELECT p.prorettype=23::oid AS integer_result,p.proisstrict,w.fdwvalidator=p.oid AS bound FROM pg_proc p JOIN pg_foreign_data_wrapper w ON w.fdwvalidator=p.oid WHERE w.fdwname='fdw_v'", []string{"integer_result", "proisstrict", "bound"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}),
			fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_foreign_data_wrapper w ON d.objid=w.oid WHERE d.classid=2328::oid AND d.refclassid=1255::oid AND d.refobjid=w.fdwvalidator AND w.fdwname='fdw_v'", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}),
			fixture.Select("SELECT s.srvtype,s.srvversion,r.rolname FROM pg_foreign_server s JOIN pg_roles r ON r.oid=s.srvowner WHERE s.srvname='fdw_vs'", []string{"srvtype", "srvversion", "rolname"}, []string{"TEXT", "TEXT", "NAME"}, [][]any{{"", nil, "fdw_validator_owner"}}),
			fixture.Select("SELECT r.rolname FROM pg_foreign_data_wrapper w JOIN pg_roles r ON r.oid=w.fdwowner WHERE w.fdwname='fdw_v'", []string{"rolname"}, []string{"NAME"}, [][]any{{"fdw_validator_owner"}}),
			fixture.Select("SELECT o.option_name,o.option_value FROM pg_user_mapping m CROSS JOIN LATERAL pg_options_to_table(m.umoptions) o WHERE m.umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_vs') ORDER BY o.option_name", []string{"option_name", "option_value"}, []string{"TEXT", "TEXT"}, [][]any{{"note", "a=b"}, {"secret", ""}}),
		}
		for _, step := range metadata {
			run(step)
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		d := stuntgres.NewDriver()
		connector, err := d.OpenConnector("fdw-sql-validator-restore")
		if err != nil {
			t.Fatal(err)
		}
		target := fdwRecordedPool(t, connector)
		control := sql.OpenDB(connector)
		t.Cleanup(func() { target.Close(); control.Close() })
		recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
		apply := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, target, recorder, step) }
		apply(fixture.Exec("CREATE ROLE fdw_validator_owner SUPERUSER", 0))
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		for _, step := range metadata {
			apply(step)
		}
		apply(fixture.Select("SELECT o.option_name,o.option_value FROM pg_foreign_server s CROSS JOIN LATERAL pg_options_to_table(s.srvoptions) o WHERE s.srvname='fdw_vs'", []string{"option_name", "option_value"}, []string{"TEXT", "TEXT"}, [][]any{{"seed", "quote'=雪"}}))
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("ALTER SERVER fdw_vs OPTIONS(SET seed 'accepted')", 0)
			step.Prepared = prepared
			apply(step)
			step = fixture.ErrorDiagnostic("ALTER SERVER fdw_vs OPTIONS(DROP seed)", fdwValidatorDivision())
			step.Prepared = prepared
			apply(step)
			apply(fdwValidatorServerOptions("{seed=accepted}"))
		}
		// Failed replacement still preserves a working destination graph.
		err = stuntgres.Restore(p.Ctx, control, strings.NewReader("CREATE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL STRICT AS 'SELECT 1 / CASE WHEN $1 IS NOT NULL AND cardinality($1)=0 AND $2=1417::oid THEN cardinality($1) ELSE 1 END'; CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard; CREATE TABLE must_not_publish(v integer); CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v"))
		fixture.AssertControlDiagnostic(t, err, fdwValidatorDivision())
		apply(fdwValidatorServerOptions("{seed=accepted}"))
		apply(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='must_not_publish'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

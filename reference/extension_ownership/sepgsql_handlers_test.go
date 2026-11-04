package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationHandlers(t *testing.T) {
	t.Run("sampling_factory_not_count_or_row_callback", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"sepgsql_handler_reader"})
		setup := func(run sepgsqlRun) {
			run(fixture.Exec("CREATE EXTENSION tsm_system_rows", 0))
			sepgsqlTable(run)
			run(fixture.Exec("CREATE ROLE sepgsql_handler_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO sepgsql_handler_reader", 0))
			run(fixture.Exec("GRANT SELECT ON sepgsql_items TO sepgsql_handler_reader", 0))
			run(fixture.Exec("REVOKE ALL ON FUNCTION system_rows(internal) FROM PUBLIC", 0))
		}
		ordinary := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("SET ROLE sepgsql_handler_reader", 0))
			run(sepgsqlBool("SELECT has_function_privilege(current_user,'system_rows(internal)','EXECUTE') AS allowed", "allowed", false))
			for _, prepared := range []bool{false, true} {
				for _, count := range []struct {
					argument string
					rows     int64
				}{{"0", 0}, {"100", 2}} {
					step := sepgsqlCount("SELECT count(*) FROM sepgsql_items TABLESAMPLE system_rows("+count.argument+")", count.rows)
					step.Prepared = prepared
					run(step)
				}
			}
			run(fixture.ImplicitError("SELECT count(*) FROM sepgsql_items TABLESAMPLE system_rows(-1)", fixture.Diagnostic{Code: "2202H", Message: "sample size must not be negative"}))
			run(fixture.Exec("RESET ROLE", 0))
		}
		sepgsqlCase(t, ordinary, func(config *stuntgres.SepgsqlSimulationConfig) {
			config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("CREATE FUNCTION sepgsql_sample_count() RETURNS bigint LANGUAGE sql VOLATILE AS $$SELECT CASE WHEN sepgsql_getcon()='"+sepgsqlR+"' THEN 100::bigint ELSE -1::bigint END$$", 0))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION system_rows(internal) IS '"+sepgsqlS+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			run(fixture.Exec("SET ROLE sepgsql_handler_reader", 0))
			run(sepgsqlBool("SELECT has_function_privilege(current_user,'system_rows(internal)','EXECUTE') AS allowed", "allowed", false))
			query := "SELECT count(*) FROM sepgsql_items TABLESAMPLE system_rows(sepgsql_sample_count())"
			run(sepgsqlCount(query, 2))
			run(fixture.Prepared(sepgsqlCount(query, 2)))
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(fixture.Exec("RESET ROLE", 0))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("ALTER FUNCTION system_rows(internal) RENAME TO sepgsql_sample_method", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items TABLESAMPLE sepgsql_sample_method(100)", 2))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_sample_method(internal) IS '"+sepgsqlU+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			// The actual factory runs before evaluating the invalid count. It
			// has no invented SQL execute check despite its revoked SQL ACL.
			run(sepgsqlDenied("SELECT count(*) FROM sepgsql_items TABLESAMPLE sepgsql_sample_method(-1)"))
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		})
	})
	t.Run("time_factory_restores_before_argument_and_scan", func(t *testing.T) {
		setup := func(run sepgsqlRun) {
			run(fixture.Exec("CREATE EXTENSION tsm_system_time", 0))
			sepgsqlTable(run)
		}
		diagnostics := func(run sepgsqlRun, method string) {
			for _, prepared := range []bool{false, true} {
				for _, invalid := range []struct{ argument, message string }{
					{"NULL", "TABLESAMPLE parameter cannot be null"},
					{"-1", "sample collection time must not be negative"},
					{"'NaN'::float8", "sample collection time must not be negative"},
				} {
					step := fixture.ImplicitError("SELECT count(*) FROM sepgsql_items TABLESAMPLE "+method+"("+invalid.argument+")", fixture.Diagnostic{Code: "2202H", Message: invalid.message})
					step.Prepared = prepared
					run(step)
				}
				for _, budget := range []struct {
					argument string
					rows     int64
				}{{"0", 0}, {"'Infinity'::float8", 2}} {
					step := sepgsqlCount("SELECT count(*) FROM sepgsql_items TABLESAMPLE "+method+"("+budget.argument+")", budget.rows)
					step.Prepared = prepared
					run(step)
				}
			}
		}
		sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			diagnostics(run, "system_time")
			run(fixture.Exec("ALTER FUNCTION system_time(internal) RENAME TO sepgsql_time_method", 0))
			diagnostics(run, "sepgsql_time_method")
		}, func(config *stuntgres.SepgsqlSimulationConfig) {
			// Direct handler invocation is not executor expression execute.
			config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}})
			config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
		}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
			setup(run)
			run(fixture.Exec("CREATE FUNCTION sepgsql_sample_millis() RETURNS double precision LANGUAGE sql VOLATILE AS $$SELECT CASE WHEN sepgsql_getcon()='"+sepgsqlR+"' THEN 'Infinity'::float8 ELSE -1::float8 END$$", 0))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION system_time(internal) IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("REVOKE ALL ON FUNCTION system_time(internal) FROM PUBLIC", 0))
			run(sepgsqlSet(sepgsqlR))
			query := "SELECT count(*) FROM sepgsql_items TABLESAMPLE system_time(sepgsql_sample_millis())"
			run(sepgsqlCount(query, 2))
			run(fixture.Prepared(sepgsqlCount(query, 2)))
			diagnostics(run, "system_time")
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("ALTER FUNCTION system_time(internal) RENAME TO sepgsql_time_method", 0))
			run(sepgsqlSet(sepgsqlR))
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items TABLESAMPLE sepgsql_time_method(sepgsql_sample_millis())", 2))
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_time_method(internal) IS '"+sepgsqlU+"'", 0))
			run(sepgsqlSet(sepgsqlR))
			// Even NULL and invalid SQL parameters follow the genuine factory.
			for _, prepared := range []bool{false, true} {
				for _, argument := range []string{"NULL", "-1", "'Infinity'::float8"} {
					step := sepgsqlDenied("SELECT count(*) FROM sepgsql_items TABLESAMPLE sepgsql_time_method(" + argument + ")")
					step.Prepared = prepared
					run(step)
					run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
				}
			}
			run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
		})
	})
}

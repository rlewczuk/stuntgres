package reference_test

import (
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSepgsqlSimulationSubject(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL application_name='inside_savepoint'", 0))
		run(fixture.Exec("SAVEPOINT state_mark", 0))
		run(fixture.Exec("SET LOCAL application_name='after_mark'", 0))
		run(fixture.Exec("ROLLBACK TO state_mark", 0))
		run(sepgsqlText("SELECT current_setting('application_name') AS setting", "setting", "inside_savepoint"))
		run(fixture.Exec("ROLLBACK", 0))
		run(sepgsqlBool("SELECT session_user=current_user AS unchanged", "unchanged", true))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_context", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(fixture.Exec("ROLLBACK TO before_context", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("COMMIT", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(fixture.Exec("BEGIN", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("ROLLBACK", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		run(fixture.ImplicitError("SELECT sepgsql_setcon('unknown')", fixture.Diagnostic{Code: "42602", Message: "SELinux: invalid security label: \"unknown\""}))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlA))
		peer, err := p.Memory.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run(sepgsqlSet(sepgsqlR))
		var got string
		if err = peer.QueryRowContext(p.Ctx, "SELECT sepgsql_getcon()").Scan(&got); err != nil || got != sepgsqlA {
			t.Fatal("physical subject leaked to peer", got, err)
		}
	})
}
func TestReferenceSepgsqlSimulationRoutines(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE FUNCTION sepgsql_identity(value text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS 'SELECT value'", 0))
		run(sepgsqlText("SELECT sepgsql_identity('caller') AS value", "value", "caller"))
		run(fixture.Select("SELECT sepgsql_identity(NULL::text) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Exec("ALTER FUNCTION sepgsql_identity(text) RENAME TO renamed_identity", 0))
		run(sepgsqlText("SELECT renamed_identity('retained') AS value", "value", "retained"))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE FUNCTION sepgsql_context() RETURNS text LANGUAGE sql VOLATILE AS 'SELECT sepgsql_getcon()'", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_context() IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Select("SELECT sepgsql_getcon() AS before,sepgsql_context() AS body,sepgsql_getcon() AS after", []string{"before", "body", "after"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{sepgsqlR, sepgsqlT, sepgsqlR}}))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("CREATE FUNCTION sepgsql_argument(value text) RETURNS text LANGUAGE sql VOLATILE AS $$SELECT value || '/' || sepgsql_getcon()$$", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION sepgsql_argument(text) IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlText("SELECT sepgsql_argument(sepgsql_getcon()) AS value", "value", sepgsqlR+"/"+sepgsqlT))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("ALTER FUNCTION sepgsql_argument(text) STRICT", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Select("SELECT sepgsql_argument(NULL::text) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{nil}}))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		// The retained SQL wrapper initializes its genuine INTERNAL helper under
		// the entered subject, not under argument/caller authority.
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION pg_catalog.round(numeric,integer) IS '"+sepgsqlU+"'", 0))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION pg_catalog.round(numeric) IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		held, err := c.PrepareContext(p.Ctx, "SELECT (round('3.14'::numeric))::text")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var rounded string
		if err = held.QueryRowContext(p.Ctx).Scan(&rounded); err != nil || rounded != "3" {
			t.Fatal("actual trusted body helper", rounded, err)
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION pg_catalog.round(numeric) IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		err = held.QueryRowContext(p.Ctx).Scan(&rounded)
		var denied interface{ SQLState() string }
		if !errors.As(err, &denied) || denied.SQLState() != "42501" {
			t.Fatal("held wrapper retained old domain/allow", err)
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("CREATE TABLE sepgsql_overlap_source(a timestamp,b timestamp,c timestamp,d interval)", 0))
		run(fixture.Exec("INSERT INTO sepgsql_overlap_source VALUES(TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04',NULL)", 1))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION pg_catalog.timestamp_pl_interval(timestamp without time zone,interval) IS '"+sepgsqlU+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{
				"SELECT (a,NULL::interval) OVERLAPS (b,c) AS value FROM sepgsql_overlap_source",
				"SELECT (b,c) OVERLAPS (a,NULL::interval) AS value FROM sepgsql_overlap_source",
			} {
				step := sepgsqlBool(query, "value", true)
				step.Prepared = prepared
				run(step)
			}
			for _, query := range []string{
				"SELECT (a,d) OVERLAPS (b,c) FROM sepgsql_overlap_source",
				"SELECT (b,c) OVERLAPS (a,d) FROM sepgsql_overlap_source",
			} {
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation", Context: `SQL function "overlaps" statement 1`})
				step.Prepared = prepared
				run(step)
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON FUNCTION pg_catalog.timestamp_pl_interval(timestamp without time zone,interval) IS '"+sepgsqlP+"'", 0))
		run(fixture.Exec("DROP TABLE sepgsql_overlap_source", 0))
		run(sepgsqlSet(sepgsqlR))
	})
}
func TestReferenceSepgsqlSimulationFunctions(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Select("SELECT lower('ABC') AS lowered,lower(NULL::text) AS missing", []string{"lowered", "missing"}, []string{"TEXT", "TEXT"}, [][]any{{"abc", nil}}))
		run(fixture.Exec("CREATE FUNCTION sepgsql_ordinary() RETURNS boolean LANGUAGE sql AS 'SELECT true'", 0))
		run(sepgsqlBool("SELECT sepgsql_ordinary() AS value", "value", true))
	}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(sepgsqlText("SELECT sepgsql_mcstrans_out('"+sepgsqlP+"') AS value", "value", "Public display label"))
		run(sepgsqlText("SELECT sepgsql_mcstrans_in('Public display label') AS value", "value", sepgsqlP))
		run(sepgsqlText("SELECT sepgsql_mcstrans_in('unmapped') AS value", "value", "unmapped"))
		run(fixture.Select("SELECT sepgsql_mcstrans_in(NULL::text) AS inward,sepgsql_mcstrans_out(NULL::text) AS outward", []string{"inward", "outward"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
		run(fixture.Exec("CREATE FUNCTION renamed_getcon() RETURNS text AS '$libdir/sepgsql','sepgsql_getcon' LANGUAGE C", 0))
		run(sepgsqlText("SELECT renamed_getcon() AS context", "context", sepgsqlA))
		run(fixture.Exec("ALTER FUNCTION renamed_getcon() RENAME TO actual_getcon", 0))
		run(sepgsqlText("SELECT actual_getcon() AS context", "context", sepgsqlA))
		run(fixture.Exec("DROP FUNCTION actual_getcon()", 0))
		sepgsqlTable(run)
		run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlS+"'", 0))
		run(sepgsqlBool("SELECT sepgsql_restorecon(NULL) AS restored", "restored", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_items'::regclass AND objsubid=0 AND label='"+sepgsqlP+"'", 1))
		run(fixture.ImplicitError("SELECT sepgsql_restorecon('not-registered-profile')", fixture.Diagnostic{Code: "XX000", Message: "SELinux: failed to initialize labeling handle: No such file or directory"}))
	})
}

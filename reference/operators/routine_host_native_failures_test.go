package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Native callback errors are not Python diagnostics. Each is reached only after
// the same PL statement has admitted its SECURITY DEFINER/SAVE frame and INSERT.
func hostNativeFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, q string) {
	t.Helper()
	cases := []struct{ name, program, input, message string }{
		{"from", "increment-v1", "a=>1,from_error=>yes", "host: FROM SQL failed"},
		{"handler", "increment-v1", "a=>1,handler_error=>yes", "host: handler failed"},
		{"program", "fail-v1", "a=>1", "host: program failed"},
		{"to", "increment-v1", "a=>1,to_error=>yes", "host: TO SQL failed"},
		{"from_object", "increment-v1", "a=>1,from_wrong_object=>yes", "host: expected owned map object"},
		{"from_null", "increment-v1", "a=>1,from_null_payload=>yes", "contradictory host null object"},
		{"nil_map", "nil-map-v1", "a=>1,bad=>yes", "host: result is not a nonnil map"},
		{"null_object", "null-payload-v1", "a=>1,bad=>yes", "contradictory host null result"},
		{"logical_type", "increment-v1", "a=>1,wrong_result=>yes", "host result has wrong SQL type"},
		{"null_carrier", "increment-v1", "a=>1,sql_null_payload=>yes", "contradictory host SQL NULL"},
		{"extra_carrier", "increment-v1", "a=>1,sql_extra_payload=>yes", "contradictory host SQL result payload"},
		{"nil_carrier", "increment-v1", "a=>1,sql_nil_entries=>yes", "contradictory host SQL result payload"},
	}
	for _, tc := range cases {
		t.Run("native_"+tc.name, func(t *testing.T) {
			fn := "host_native_failure_" + tc.name
			hostFunction(t, p, b, c, fn, tc.program, "", "SET application_name='host-native-inner'", true)
			hostExec(t, p, b, c, "CREATE OR REPLACE FUNCTION host_native_prefix(v hstore) RETURNS hstore LANGUAGE plpgsql SECURITY DEFINER SET application_name='host-native-wrapper' AS $$ BEGIN IF current_user <> 'host_failure_owner' OR current_setting('application_name') <> 'host-native-wrapper' THEN RAISE EXCEPTION 'wrong host wrapper environment'; END IF; INSERT INTO host_effects VALUES (2); RETURN "+fn+"(v); END $$")
			hostExec(t, p, b, c, "ALTER FUNCTION host_native_prefix(hstore) OWNER TO host_failure_owner")
			hostNativeError(t, p.Ctx, c, "SELECT host_native_prefix($1::hstore)", tc.message, tc.input)
			hostRows(t, p, b, c, "SELECT v FROM host_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"postgres", "postgres", ""}})
			const failedQuery = "SELECT key,value FROM each(host_native_prefix($1::hstore)) ORDER BY key"
			failedStatement, err := c.PrepareContext(p.Ctx, failedQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer failedStatement.Close()
			var key, value any
			err = failedStatement.QueryRowContext(p.Ctx, tc.input).Scan(&key, &value)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("SAME held failure=%v want %q", err, tc.message)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Errorf("native held failure fabricated SQLSTATE: %v", err)
			}
			// Rebind only the callee's immutable program after the error; the SAME
			// wrapper statement must unwind its previous frame and publish fresh data.
			hostFunction(t, p, b, c, fn, "increment-v1", "", "SET application_name='host-native-inner'", true)
			failedHeld := fixture.HeldStatement{Executor: c, Statement: failedStatement, SQL: failedQuery}
			hostRows(t, p, b, failedHeld, failedQuery, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
			hostRows(t, p, b, c, "SELECT v FROM host_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM host_effects WHERE v=2", 1))
			for _, input := range []string{"a=>7", "a=>1"} {
				value := "8"
				if input == "a=>1" {
					value = "2"
				}
				hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", value}, {"c", "3"}}, input)
			}
		})
	}
}

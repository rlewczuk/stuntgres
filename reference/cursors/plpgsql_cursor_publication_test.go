package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func plCursorPublicationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			body(t, p, backend, c)
		})
	}
}

func plCursorPublicationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_pub_seq START 10", 0))
	plCursorFunction(t, p, b, c, "pc_pub_recover", "", "integer", "DECLARE c refcursor:='pc_pub_recovery'; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN x; END")
}

// Reuse one held statement across two failures as well as direct and fresh
// Prepare. Full diagnostics are compared non-fatally so discovery exposes every
// route's unknown fields while still checking rollback, portal cleanup and reuse.
func plCursorPublicationFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want fixture.Diagnostic, sequence int64, pgRepeatPrefix string) {
	t.Helper()
	query := "SELECT pc_pub_failure() AS v"
	held, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("held Prepare must succeed before reached OPEN", err)
	}
	defer held.Close()
	for _, route := range []string{"direct", "fresh", "held_first", "held_reuse"} {
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT setval('pc_pub_seq',10,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pc_publication_error", 0))
		var failure error
		var rows *sql.Rows
		stage := "execute"
		if route == "direct" {
			rows, failure = c.QueryContext(p.Ctx, query)
		} else if route == "fresh" {
			var fresh *sql.Stmt
			fresh, failure = c.PrepareContext(p.Ctx, query)
			if failure != nil {
				stage = "prepare"
			} else {
				rows, failure = fresh.QueryContext(p.Ctx)
				fresh.Close()
			}
		} else {
			rows, failure = held.QueryContext(p.Ctx)
		}
		if rows != nil {
			rows.Close()
			t.Errorf("%s: failed OPEN exposed rows", route)
		}
		if stage != "execute" {
			t.Errorf("%s: error stage=%s, want execute", route, stage)
		}
		routeWant := want
		// PostgreSQL's cached simple-expression error callback also reports
		// expression context after the first invocation has failed.
		if b.PG && route != "direct" && pgRepeatPrefix != "" {
			routeWant.Context = pgRepeatPrefix + "\n" + routeWant.Context
		}
		if failure == nil {
			t.Errorf("%s: expected SQLSTATE %s, got success", route, want.Code)
		} else if got := fixture.ExtractDiagnostic(t, failure); got != routeWant {
			t.Errorf("%s: diagnostic got=%#v want=%#v", route, got, routeWant)
		}
		sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pc_publication_error", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE pc_publication_error", 0))
		// Sequence effects survive rollback; portal installations must not.
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT currval('pc_pub_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{sequence}}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT count(*) AS v FROM pg_cursors WHERE name<>''", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_pub_recover() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT count(*) AS v FROM pg_cursors WHERE name<>''", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT 7 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}

func TestReferencePLpgSQLCursorFoundPublication(t *testing.T) {
	for _, prior := range []bool{false, true} {
		for _, tc := range []struct {
			name, actual, code string
			want               bool
		}{
			{"formal_conversion", "'bad'", "22P02", true},
			{"argument_binding", "missing_input", "42703", prior},
			{"argument_evaluation", "1/n", "22012", prior},
		} {
			t.Run(fmt.Sprintf("%s/prior_%t", tc.name, prior), func(t *testing.T) {
				plCursorPublicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					seed := "SELECT 1 WHERE false"
					if prior {
						seed = "SELECT 1"
					}
					body := "DECLARE seed refcursor; x integer; c CURSOR(a integer) FOR SELECT a; n integer:=0; BEGIN OPEN seed FOR " + seed + "; FETCH seed INTO x; CLOSE seed; BEGIN OPEN c(" + tc.actual + "); EXCEPTION WHEN SQLSTATE '" + tc.code + "' THEN RETURN FOUND; END; RETURN NULL; END"
					plCursorFunction(t, p, b, c, "pc_found_publication", "", "boolean", body)
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					plCursorValue(t, p, b, c, "SELECT pc_found_publication() AS v", "BOOL", tc.want)
					plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name<>''", "INT8", int64(0))
					plCursorValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
	t.Run("empty_query_replaces_prior_true", func(t *testing.T) {
		plCursorPublicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorFunction(t, p, b, c, "pc_found_empty", "", "boolean", "DECLARE seed refcursor; x integer; c CURSOR(a integer) FOR SELECT a; seen boolean; BEGIN OPEN seed FOR SELECT 1; FETCH seed INTO x; CLOSE seed; OPEN c(generate_series(1,0)); seen:=FOUND; FETCH c INTO x; CLOSE c; RETURN NOT seen AND FOUND AND x IS NULL; END")
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			plCursorValue(t, p, b, c, "SELECT pc_found_empty() AS v", "BOOL", true)
			plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name<>''", "INT8", int64(0))
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		})
	})
	for _, tc := range []struct{ name, actual, code, message, query, position, prefix string }{
		{"formal_conversion_diagnostic", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "", "", ""},
		{"argument_binding_diagnostic", "missing_input", "42703", `column "missing_input" does not exist`, "missing_input", "1", ""},
		{"argument_evaluation_diagnostic", "1/n", "22012", "division by zero", "", "", `PL/pgSQL expression "1/n"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plCursorPublicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorPublicationSetup(t, p, b, c)
				plCursorFunction(t, p, b, c, "pc_pub_failure", "", "void", "DECLARE c CURSOR(a integer) FOR SELECT a; n integer:=0; BEGIN OPEN c("+tc.actual+"); END")
				want := plCursorRuntime(tc.code, tc.message, "pc_pub_failure()", "OPEN")
				want.InternalQuery, want.InternalPosition = tc.query, tc.position
				if tc.prefix != "" {
					want.Context = tc.prefix + "\n" + want.Context
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				plCursorPublicationFailure(t, p, b, c, want, 10, "")
				sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}

func TestReferencePLpgSQLCursorConstantOpen(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		t.Run(fmt.Sprintf("nonnull_constant/dynamic_%t", dynamic), func(t *testing.T) {
			plCursorPublicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				open := "OPEN c FOR SELECT 7 AS i;"
				if dynamic {
					open = "OPEN c FOR EXECUTE 'SELECT $1::integer AS i' USING 7;"
				}
				plCursorFunction(t, p, b, c, "pc_constant_named", "", "integer", "DECLARE c CONSTANT refcursor:='pc_constant_named_portal'; x integer; BEGIN "+open+" FETCH c INTO x; CLOSE c; RETURN x; END")
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				plCursorValue(t, p, b, c, "SELECT pc_constant_named() AS v", "INT4", int64(7))
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name<>''", "INT8", int64(0))
				sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
	for _, tc := range []struct {
		name, open, code, message, query, position, prefix, pgRepeatPrefix string
		sequence                                                           int64
	}{
		{"null_static", "OPEN c FOR SELECT 7;", "22005", `variable "c" is declared CONSTANT`, "", "", "", "", 10},
		{"null_dynamic", "OPEN c FOR EXECUTE 'SELECT 7';", "22005", `variable "c" is declared CONSTANT`, "", "", "", "", 10},
		{"static_target_not_demanded", "OPEN c FOR SELECT nextval('pc_pub_seq');", "22005", `variable "c" is declared CONSTANT`, "", "", "", "", 10},
		{"dynamic_using_before_constant", "OPEN c FOR EXECUTE 'SELECT $1::bigint AS i' USING nextval('pc_pub_seq');", "22005", `variable "c" is declared CONSTANT`, "", "", "", "", 11},
		{"dynamic_command_and_using_before_constant", "OPEN c FOR EXECUTE pc_pub_command() USING nextval('pc_pub_seq');", "22005", `variable "c" is declared CONSTANT`, "", "", "", "", 12},
		{"static_query_binding_before_constant", "OPEN c FOR SELECT missing_input;", "42703", `column "missing_input" does not exist`, "SELECT missing_input", "8", "", "", 10},
		{"static_query_planning_before_constant", "OPEN c FOR SELECT 1/0;", "22012", "division by zero", "", "", `SQL statement "SELECT 1/0"`, "", 10},
		{"dynamic_command_error_before_constant", "OPEN c FOR EXECUTE (1/n)::text;", "22012", "division by zero", "", "", "", `PL/pgSQL expression "(1/n)::text"`, 10},
		{"dynamic_null_command_before_constant", "OPEN c FOR EXECUTE cmd;", "22004", "query string argument of EXECUTE is null", "", "", "", "", 10},
		{"dynamic_using_error_before_constant", "OPEN c FOR EXECUTE 'SELECT $1::integer AS i' USING 1/n;", "22012", "division by zero", "", "", "", `PL/pgSQL expression "1/n"`, 10},
		{"dynamic_parse_before_constant", "OPEN c FOR EXECUTE 'SELECT FROM';", "42601", "syntax error at end of input", "SELECT FROM", "12", "", "", 10},
		{"dynamic_query_binding_after_using", "OPEN c FOR EXECUTE 'SELECT missing_input' USING nextval('pc_pub_seq');", "42703", `column "missing_input" does not exist`, "SELECT missing_input", "8", "", "", 11},
		{"dynamic_query_planning_after_using", "OPEN c FOR EXECUTE 'SELECT 1/0' USING nextval('pc_pub_seq');", "22012", "division by zero", "", "", `SQL statement "SELECT 1/0"`, "", 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plCursorPublicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorPublicationSetup(t, p, b, c)
				plCursorFunction(t, p, b, c, "pc_pub_command", "", "text", "DECLARE n bigint; BEGIN n:=nextval('pc_pub_seq'); RETURN 'SELECT $1::bigint AS i'; END")
				plCursorFunction(t, p, b, c, "pc_pub_failure", "", "void", "DECLARE c CONSTANT refcursor:=NULL; n integer:=0; cmd text; BEGIN "+tc.open+" END")
				want := plCursorRuntime(tc.code, tc.message, "pc_pub_failure()", "OPEN")
				want.InternalQuery, want.InternalPosition = tc.query, tc.position
				if tc.prefix != "" {
					want.Context = tc.prefix + "\n" + want.Context
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				plCursorPublicationFailure(t, p, b, c, want, tc.sequence, tc.pgRepeatPrefix)
				sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}

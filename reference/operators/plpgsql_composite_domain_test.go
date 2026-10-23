package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plpgsqlCompositeDomainSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TYPE pl_domain_pair AS (a integer)",
		"CREATE DOMAIN pl_positive_row AS pl_domain_pair CONSTRAINT pl_row_positive CHECK((VALUE).a>0)",
		"CREATE DOMAIN pl_required_row AS pl_positive_row NOT NULL")
}

func TestReferencePLpgSQLCoreCompositeDomain(t *testing.T) {
	for _, tc := range []struct {
		name, body, typ string
		value           any
	}{
		{"valid_whole", "DECLARE r pl_positive_row; BEGIN r := ROW(2); RETURN r.a; END", "INT4", int64(2)},
		{"valid_field", "DECLARE r pl_positive_row := ROW(2); BEGIN r.a := 3; RETURN r.a; END", "INT4", int64(3)},
		{"nullable_default", "DECLARE r pl_positive_row; BEGIN RETURN r IS DISTINCT FROM NULL; END", "BOOL", false},
		{"nullable_clear", "DECLARE r pl_positive_row := ROW(2); BEGIN r := NULL; RETURN r IS DISTINCT FROM NULL; END", "BOOL", false},
		{"nullable_field", "DECLARE r pl_positive_row := ROW(2); BEGIN r.a := NULL; RETURN r.a IS NULL AND r IS DISTINCT FROM NULL; END", "BOOL", true},
		{"required_row_null", "DECLARE r pl_required_row := ROW(NULL::integer); BEGIN RETURN r.a IS NULL AND r IS DISTINCT FROM NULL; END", "BOOL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlCompositeDomainSetup(t, p, b, c)
				result := "integer"
				if tc.typ == "BOOL" {
					result = "boolean"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_domain_value() RETURNS "+result+" LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_domain_value() AS v", tc.typ, tc.value)
			})
		})
	}
	for _, tc := range []struct{ name, body, domain, code, message, constraint, action string }{
		{"invalid_whole", "DECLARE r pl_positive_row; BEGIN r := ROW(-1); RETURN r.a; END", "pl_positive_row", "23514", `value for domain pl_positive_row violates check constraint "pl_row_positive"`, "pl_row_positive", "assignment"},
		{"invalid_field", "DECLARE r pl_positive_row := ROW(2); BEGIN r.a := -1; RETURN r.a; END", "pl_positive_row", "23514", `value for domain pl_positive_row violates check constraint "pl_row_positive"`, "pl_row_positive", "assignment"},
		{"invalid_alias_field", "DECLARE r pl_positive_row := ROW(2); a ALIAS FOR r; BEGIN a.a := -1; RETURN r.a; END", "pl_positive_row", "23514", `value for domain pl_positive_row violates check constraint "pl_row_positive"`, "pl_row_positive", "assignment"},
		{"invalid_initializer", "DECLARE r pl_positive_row := ROW(-1); BEGIN RETURN r.a; END", "pl_positive_row", "23514", `value for domain pl_positive_row violates check constraint "pl_row_positive"`, "pl_row_positive", "statement block local variable initialization"},
		{"required_default", "DECLARE r pl_required_row; BEGIN RETURN 1; END", "pl_required_row", "23502", "domain pl_required_row does not allow null values", "", "statement block local variable initialization"},
		{"required_initializer", "DECLARE r pl_required_row := NULL; BEGIN RETURN 1; END", "pl_required_row", "23502", "domain pl_required_row does not allow null values", "", "statement block local variable initialization"},
		{"required_assignment", "DECLARE r pl_required_row := ROW(2); BEGIN r := NULL; RETURN 1; END", "pl_required_row", "23502", "domain pl_required_row does not allow null values", "", "assignment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlCompositeDomainSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_domain_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$",
					"CREATE FUNCTION pl_domain_recovery() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r pl_positive_row := ROW(2); BEGIN r.a := 7; RETURN r.a; END$$")
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_domain_error()", tc.action)
				want.Schema, want.DataType, want.Constraint = "public", tc.domain, tc.constraint
				plpgsqlError(t, p, c, "SELECT pl_domain_error()", true, want)
				plpgsqlValue(t, p, b, c, "SELECT pl_domain_recovery() AS v", "INT4", int64(7))
			})
		})
	}
	t.Run("check_expression_error", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE pl_domain_pair AS (a integer)",
				"CREATE FUNCTION pl_domain_boom(x integer) RETURNS boolean LANGUAGE SQL AS 'SELECT $1; SELECT 1/$1>0'",
				"CREATE DOMAIN pl_error_row AS pl_domain_pair CHECK(pl_domain_boom((VALUE).a))",
				"CREATE FUNCTION pl_domain_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r pl_error_row; BEGIN r := ROW(0); RETURN r.a; END$$",
				"CREATE FUNCTION pl_domain_recovery() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r pl_error_row := ROW(1); BEGIN RETURN r.a; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "pl_domain_error()", "assignment")
			want.Context = "SQL function \"pl_domain_boom\" statement 2\n" + want.Context
			plpgsqlError(t, p, c, "SELECT pl_domain_error()", true, want)
			plpgsqlValue(t, p, b, c, "SELECT pl_domain_recovery() AS v", "INT4", int64(1))
		})
	})
	t.Run("check_execute_authority", plpgsqlCompositeDomainAuthority)
}

func plpgsqlCompositeDomainAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pl_domain_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		userOperatorExec(t, p, b, c,
			"CREATE TYPE pl_domain_pair AS (a integer)",
			"CREATE FUNCTION pl_domain_gate(x integer) RETURNS boolean LANGUAGE SQL AS 'SELECT $1; SELECT $1>0'",
			"CREATE DOMAIN pl_gated_row AS pl_domain_pair CHECK(pl_domain_gate((VALUE).a))",
			"CREATE FUNCTION pl_domain_authority(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r pl_gated_row := ROW(x); BEGIN r.a := x+1; RETURN r.a; END$$",
			"CREATE ROLE pl_domain_reader", "GRANT USAGE ON SCHEMA public TO pl_domain_reader",
			"GRANT USAGE ON TYPE pl_domain_pair,pl_gated_row TO pl_domain_reader",
			"REVOKE EXECUTE ON FUNCTION pl_domain_gate(integer) FROM PUBLIC,pl_domain_reader",
			"SET SESSION AUTHORIZATION pl_domain_reader")
		q := "SELECT pl_domain_authority($1) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(phase string) {
			t.Helper()
			if !t.Run(phase, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, 2))
			}) {
				t.FailNow()
			}
		}
		want := plpgsqlRuntimeDiagnostic("42501", "permission denied for function pl_domain_gate", "pl_domain_authority(integer)", "statement block local variable initialization")
		denied := func(phase string) {
			t.Helper()
			if !t.Run(phase, func(t *testing.T) {
				plpgsqlError(t, p, c, "SELECT pl_domain_authority(2)", true, want)
				rows, err := held.QueryContext(p.Ctx, 2)
				if rows != nil {
					rows.Close()
					t.Fatal("denied domain CHECK published held rows")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}) {
				t.FailNow()
			}
		}
		// Denial precedes every successful evaluation of this domain's CHECK.
		denied("initial_denied")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION pl_domain_gate(integer) TO pl_domain_reader", "SET SESSION AUTHORIZATION pl_domain_reader")
		check("granted")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION pl_domain_gate(integer) FROM PUBLIC,pl_domain_reader", "SET SESSION AUTHORIZATION pl_domain_reader")
		denied("revoked_after_success")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION pl_domain_gate(integer) TO pl_domain_reader", "SET SESSION AUTHORIZATION pl_domain_reader")
		check("regranted")
	})
}

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentOrder(t *testing.T) {
	t.Run("sequential_not_update", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION pa_sequential() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := '{1,2}'; BEGIN a[1] := a[2]; a[2] = a[1]; RETURN a; END$$",
				"CREATE TABLE pa_swap(a integer[])")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pa_swap VALUES ('{1,2}')", 1))
			plpgsqlValue(t, p, b, c, "SELECT pa_sequential()::text AS v", "TEXT", "{2,2}")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE pa_swap SET a[1]=a[2], a[2]=a[1]", 1))
			plpgsqlValue(t, p, b, c, "SELECT a::text AS v FROM pa_swap", "TEXT", "{2,1}")
		})
	})
	t.Run("bounds_before_rhs", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE pa_order_seq",
				"CREATE FUNCTION pa_null_order() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := '{1,2}'; BEGIN a[NULL::integer] := nextval('pa_order_seq')::integer; RETURN a; END$$")
			plpgsqlValue(t, p, b, c, "SELECT setval('pa_order_seq',10,true) AS v", "INT8", int64(10))
			want := plpgsqlRuntimeDiagnostic("22004", "array subscript in assignment must not be null", "pa_null_order()", "assignment")
			plpgsqlError(t, p, c, "SELECT pa_null_order()", true, want)
			plpgsqlValue(t, p, b, c, "SELECT currval('pa_order_seq') AS v", "INT8", int64(10))
		})
	})
	t.Run("dimension_order_and_once", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE pa_order_seq",
				"CREATE FUNCTION pa_order() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := '{{1,2},{3,4}}'; BEGIN a[(nextval('pa_order_seq')-2)::integer:(nextval('pa_order_seq')+1)::integer][(nextval('pa_order_seq')-3)::integer:nextval('pa_order_seq')::integer] := ARRAY[ARRAY[nextval('pa_order_seq')::integer,6],ARRAY[7,8]]; RETURN a; END$$")
			for _, prepared := range []bool{false, true} {
				userOperatorExec(t, p, b, c, "ALTER SEQUENCE pa_order_seq RESTART WITH 1")
				s := fixture.Select("SELECT pa_order()::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{{5,6},{7,8}}"}})
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				plpgsqlValue(t, p, b, c, "SELECT currval('pa_order_seq') AS v", "INT8", int64(5))
			}
		})
	})
}

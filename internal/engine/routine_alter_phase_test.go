package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

// Exercise planning and actual producer demand under the invoker, rather than
// merely checking that a recipe flag was wired. A revoked hidden default must
// fail when demanded but remain dormant for an explicit actual argument.
func TestRoutineAlterFrozenExecutionAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE ROLE routine_phase_reader",
		"GRANT USAGE ON SCHEMA public TO routine_phase_reader",
		"CREATE FUNCTION routine_phase_callee(integer) RETURNS integer RETURN $1+1000",
		"CREATE FUNCTION routine_phase_hard() RETURNS integer RETURN routine_phase_callee(1)",
		"CREATE FUNCTION routine_phase_default(x integer DEFAULT routine_phase_callee(1)) RETURNS integer RETURN x",
		"CREATE FUNCTION routine_phase_soft() RETURNS integer LANGUAGE SQL AS $$SELECT routine_phase_callee(1)$$",
		"REVOKE EXECUTE ON FUNCTION routine_phase_callee(integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION routine_phase_callee(integer) TO routine_phase_reader",
		"ALTER FUNCTION routine_phase_callee(integer) RENAME TO routine_phase_renamed",
		"CREATE FUNCTION routine_phase_callee(integer) RETURNS integer RETURN $1+2000",
		"SET ROLE routine_phase_reader",
	} {
		roleExecute(t, e, op, q)
	}
	check := func(q string, want ...driver.Value) {
		t.Helper()
		r := roleExecute(t, e, op, q)
		if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{want}) {
			t.Fatalf("%s: %v want %v", q, got, want)
		}
		if err := r.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	check("SELECT routine_phase_hard()", int64(1001))
	check("SELECT routine_phase_default()", int64(1001))
	check("SELECT routine_phase_soft()", int64(2001))
	check("SELECT routine_phase_hard(),routine_phase_default(),routine_phase_soft()", int64(1001), int64(1001), int64(2001))
	for _, q := range []string{
		"RESET ROLE", "REVOKE EXECUTE ON FUNCTION routine_phase_renamed(integer) FROM routine_phase_reader", "SET ROLE routine_phase_reader",
	} {
		roleExecute(t, e, op, q)
	}
	for _, q := range []string{"SELECT routine_phase_hard()", "SELECT routine_phase_default()", "SELECT routine_phase_default(routine_phase_renamed(1))"} {
		_, err := e.Execute(op, statement(t, q), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function routine_phase_renamed" {
			t.Fatalf("%s: current captured callee authority not enforced: %v", q, err)
		}
	}
	check("SELECT routine_phase_default(9),routine_phase_soft()", int64(9), int64(2001))
	check("SELECT routine_phase_default(routine_phase_callee(1))", int64(2001))
	for _, q := range []string{
		"RESET ROLE", "GRANT EXECUTE ON FUNCTION routine_phase_renamed(integer) TO routine_phase_reader", "SET ROLE routine_phase_reader",
	} {
		roleExecute(t, e, op, q)
	}
	check("SELECT routine_phase_hard(),routine_phase_default()", int64(1001), int64(1001))
}

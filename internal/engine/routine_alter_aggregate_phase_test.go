package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRoutineAlterAggregateHardPhaseAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE ROLE routine_aggregate_owner", "CREATE ROLE routine_aggregate_reader",
		"GRANT USAGE,CREATE ON SCHEMA public TO routine_aggregate_owner",
		"GRANT USAGE ON SCHEMA public TO routine_aggregate_reader",
		"SET ROLE routine_aggregate_owner",
		"CREATE FUNCTION routine_aggregate_step(bigint,integer) RETURNS bigint RETURN $1+$2",
		"REVOKE EXECUTE ON FUNCTION routine_aggregate_step(bigint,integer) FROM PUBLIC",
		"CREATE AGGREGATE routine_aggregate_sum(integer) (SFUNC=routine_aggregate_step,STYPE=bigint,INITCOND='0')",
		"REVOKE EXECUTE ON FUNCTION routine_aggregate_sum(integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION routine_aggregate_sum(integer) TO routine_aggregate_reader",
		"CREATE VIEW routine_aggregate_view AS SELECT routine_aggregate_sum(x) AS v FROM(VALUES(2),(3)) s(x)",
		"GRANT SELECT ON routine_aggregate_view TO routine_aggregate_reader",
		"ALTER FUNCTION routine_aggregate_sum(integer) RENAME TO routine_aggregate_renamed",
		"ALTER FUNCTION routine_aggregate_step(bigint,integer) RENAME TO routine_aggregate_step_new",
		"RESET ROLE", "SET ROLE routine_aggregate_reader",
	} {
		roleExecute(t, e, op, q)
	}
	check := func() {
		t.Helper()
		r := roleExecute(t, e, op, "SELECT v FROM routine_aggregate_view")
		if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{int64(5)}}) {
			t.Fatal("hard aggregate/support selected wrong identity", got)
		}
		if err := r.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	denied := func(q string) {
		t.Helper()
		_, err := e.Execute(op, statement(t, q), 0, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function routine_aggregate_step_new" {
			t.Fatalf("%s: current support EXECUTE authority not enforced: %v", q, err)
		}
	}
	check() // Support runs as aggregate owner, not this EXECUTE-denied invoker.
	denied("SELECT routine_aggregate_step_new(0::bigint,1)")
	for _, q := range []string{
		"RESET ROLE", "SET ROLE routine_aggregate_owner",
		"REVOKE EXECUTE ON FUNCTION routine_aggregate_step_new(bigint,integer) FROM routine_aggregate_owner",
		"RESET ROLE", "SET ROLE routine_aggregate_reader",
	} {
		roleExecute(t, e, op, q)
	}
	denied("SELECT v FROM routine_aggregate_view")
	for _, q := range []string{
		"RESET ROLE", "SET ROLE routine_aggregate_owner",
		"GRANT EXECUTE ON FUNCTION routine_aggregate_step_new(bigint,integer) TO routine_aggregate_owner",
		"RESET ROLE", "SET ROLE routine_aggregate_reader",
	} {
		roleExecute(t, e, op, q)
	}
	check()
}

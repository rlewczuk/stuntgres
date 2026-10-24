package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineDropTransitions(t *testing.T) {
	transactionalRoutineRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, e fixture.Executor, exec func(string), value func(string, string, int64), finish func(), explicit, commit bool) {
		value("SELECT txr_drop(4) AS v", "INT4", 5)
		integerOID := transactionalRoutineOID(t, p, e, "SELECT 'txr_drop(integer)'::regprocedure::oid")
		bigintOID := transactionalRoutineOID(t, p, e, "SELECT 'txr_drop(bigint)'::regprocedure::oid")
		if integerOID == bigintOID {
			t.Fatal("overloads share identity", integerOID)
		}
		transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_drop'", 2)
		if explicit {
			exec("SAVEPOINT removal")
		}
		exec("DROP ROUTINE txr_drop(integer)")
		q := "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_drop'"
		transactionalRoutineCount(t, p, b, e, q, 1)
		integerProbe := fmt.Sprintf("SELECT count(*) AS v FROM pg_proc WHERE oid=%d::oid", integerOID)
		transactionalRoutineCount(t, p, b, e, integerProbe, 0)
		transactionalRoutineCount(t, p, b, e, fmt.Sprintf("SELECT count(*) AS v FROM pg_proc WHERE oid=%d::oid", bigintOID), 1)
		if explicit {
			routineValue(t, p, b, peer, "SELECT txr_drop(4) AS v", "v", "INT4", int64(5))
			exec("ROLLBACK TO removal")
			value("SELECT txr_drop(4) AS v", "INT4", 5)
			exec("RELEASE removal")
			exec("DROP ROUTINE txr_drop(integer)")
		}
		finish()
		want := int64(1)
		if !commit {
			want = 2
		}
		transactionalRoutineCount(t, p, b, peer, q, want)
		wantInteger := int64(0)
		if !commit {
			wantInteger = 1
		}
		transactionalRoutineCount(t, p, b, peer, integerProbe, wantInteger)
		if after := transactionalRoutineOID(t, p, peer, "SELECT 'txr_drop(bigint)'::regprocedure::oid"); after != bigintOID {
			t.Fatal("DROP rewrote overload sibling", bigintOID, after)
		}
		routineValue(t, p, b, peer, "SELECT txr_drop(1::bigint) AS v", "v", "INT8", int64(108))
		if !commit {
			routineValue(t, p, b, c, "SELECT txr_drop(4) AS v", "v", "INT4", int64(5))
		}
	}, transactionalRoutineRouteOptions{Setup: []string{"CREATE FUNCTION txr_drop(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION txr_drop(bigint) RETURNS bigint RETURN 108::bigint"}})
}

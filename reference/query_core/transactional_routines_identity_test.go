package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesIdentity(t *testing.T) {
	t.Run("private-owner-acl", transactionalRoutinePrivateOwner)
	t.Run("drop-transitions", transactionalRoutineDropTransitions)
	transactionalRoutineRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, e fixture.Executor, exec func(string), value func(string, string, int64), finish func(), explicit, commit bool) {
		exec("CREATE SCHEMA txr_dest")
		exec("CREATE FUNCTION public.txr_id(integer) RETURNS integer RETURN $1+1")
		exec("CREATE FUNCTION public.txr_id(text) RETURNS bigint RETURN 104::bigint")
		exec("CREATE FUNCTION public.txr_id(bigint) RETURNS bigint RETURN 108::bigint")
		exec("CREATE FUNCTION public.txr_hard() RETURNS integer RETURN public.txr_id(4)")
		exec("CREATE FUNCTION public.txr_default(x integer DEFAULT public.txr_id(4)) RETURNS integer RETURN x")
		exec("CREATE VIEW txr_view AS SELECT public.txr_id(4) AS v")
		oid := transactionalRoutineOID(t, p, e, "SELECT 'public.txr_id(integer)'::regprocedure::oid")
		if explicit {
			exec("SAVEPOINT identity_mark")
		}
		exec("ALTER FUNCTION public.txr_id(integer) RENAME TO txr_new")
		exec("ALTER FUNCTION public.txr_new(integer) SET SCHEMA txr_dest")
		exec("ALTER ROUTINE txr_dest.txr_new(integer) OWNER TO CURRENT_USER")
		after := transactionalRoutineOID(t, p, e, "SELECT 'txr_dest.txr_new(integer)'::regprocedure::oid")
		if after != oid {
			t.Fatal("identity changed", oid, after)
		}
		value("SELECT txr_dest.txr_new(4) AS v", "INT4", 5)
		value("SELECT public.txr_id('x'::text) AS v", "INT8", 104)
		value("SELECT public.txr_id(1::bigint) AS v", "INT8", 108)
		value("SELECT public.txr_hard() AS v", "INT4", 5)
		value("SELECT public.txr_default() AS v", "INT4", 5)
		// A view reports source-column nullability rather than scalar-call metadata.
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM txr_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
		if explicit {
			exec("ROLLBACK TO identity_mark")
			value("SELECT public.txr_id(4) AS v", "INT4", 5)
			exec("ROLLBACK TO identity_mark")
			exec("RELEASE identity_mark")
		} else {
			exec("ALTER FUNCTION txr_dest.txr_new(integer) SET SCHEMA public")
			exec("ALTER FUNCTION public.txr_new(integer) RENAME TO txr_id")
		}
		// Grouped roots remove the actual dependent closure but not overloads.
		if explicit {
			exec("SAVEPOINT drop_mark")
		}
		exec("DROP FUNCTION public.txr_id(integer),public.txr_hard(),public.txr_default(integer) CASCADE")
		transactionalRoutineCount(t, p, b, e, "SELECT count(*) AS v FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='txr_view'", 0)
		value("SELECT public.txr_id(1::bigint) AS v", "INT8", 108)
		if explicit {
			exec("ROLLBACK TO drop_mark")
			value("SELECT public.txr_id(4) AS v", "INT4", 5)
			exec("RELEASE drop_mark")
		} else {
			exec("CREATE FUNCTION public.txr_id(integer) RETURNS integer RETURN $1+1")
			after = transactionalRoutineOID(t, p, e, "SELECT 'public.txr_id(integer)'::regprocedure::oid")
			if after == oid {
				t.Fatal("DROP CREATE reused identity", oid, after)
			}
		}
		finish()
		want := int64(0)
		if commit {
			want = 3
		}
		transactionalRoutineCount(t, p, b, peer, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_id'", want)
	})
}

func transactionalRoutineOID(t *testing.T, p *fixture.Pair, e fixture.Executor, q string) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing identity", rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid identity", oid, rows.Err())
	}
	return oid
}

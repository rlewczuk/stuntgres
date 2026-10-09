package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceLockModesRolesJoin(t *testing.T) {
	roleReferenceCleanup(t, []string{"lockowner", "lockstranger"})
	lockModesScenarioBackends(t, func(t *testing.T, ctx context.Context, backend referenceBackend, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE USER lockowner")
		lockModesExec(t, ctx, a, "CREATE USER lockstranger")
		if backend.pg {
			lockModesExec(t, ctx, a, "GRANT USAGE, CREATE ON SCHEMA public TO lockowner")
			lockModesExec(t, ctx, a, "GRANT USAGE ON SCHEMA public TO lockstranger")
		}
		for _, q := range []string{"SET SESSION AUTHORIZATION lockowner", "CREATE TABLE left_t(v INT)", "CREATE TABLE right_t(v INT)", "CREATE VIEW joined AS SELECT left_t.v FROM left_t JOIN right_t USING(v)", "CREATE VIEW nested AS SELECT v FROM joined"} {
			lockModesExec(t, ctx, a, q)
		}
		for _, mode := range []string{"ACCESS SHARE", "SHARE"} {
			lockModesExec(t, ctx, a, "BEGIN")
			lockModesExec(t, ctx, a, "LOCK ONLY(nested) IN "+mode+" MODE")
			for _, target := range []string{"left_t", "right_t"} {
				code, message := "", ""
				if mode == "SHARE" {
					code = "55P03"
					message = `could not obtain lock on relation "` + target + `"`
				}
				lockModesProbe(t, ctx, b, "LOCK "+target+" IN ROW EXCLUSIVE MODE NOWAIT", code, message)
			}
			lockModesExec(t, ctx, a, "ROLLBACK")
		}
		// Handles prepared as the owner must check the executing principal anew.
		for _, target := range []struct{ query, message string }{
			{"LOCK left_t IN ACCESS SHARE MODE NOWAIT", "permission denied for table left_t"},
			{"LOCK nested IN SHARE MODE NOWAIT", "permission denied for view nested"},
		} {
			stmt := lockModesPrepare(t, ctx, a, target.query)
			lockModesExec(t, ctx, a, "RESET SESSION AUTHORIZATION")
			lockModesExec(t, ctx, a, "SET SESSION AUTHORIZATION lockstranger")
			_, err := a.ExecContext(ctx, target.query)
			lockModesDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")
			_, err = stmt.ExecContext(ctx)
			lockModesDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")
			lockModesExec(t, ctx, a, "BEGIN")
			_, err = stmt.ExecContext(ctx)
			lockModesDiagnostic(t, err, "42501", target.message, "")
			_, err = a.ExecContext(ctx, "LOCK missing")
			lockModesDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			lockModesExec(t, ctx, a, "ROLLBACK")
			lockModesExec(t, ctx, a, "RESET SESSION AUTHORIZATION")
			lockModesExec(t, ctx, a, "SET SESSION AUTHORIZATION lockowner")
		}
		lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION lockstranger")
		lockModesProbe(t, ctx, b, "LOCK left_t IN ACCESS SHARE MODE NOWAIT", "42501", "permission denied for table left_t")
		lockModesProbe(t, ctx, b, "LOCK nested IN SHARE MODE NOWAIT", "42501", "permission denied for view nested")
	})
}

func TestReferenceLockModesStackPublication(t *testing.T) {
	lockModesScenarioBackends(t, func(t *testing.T, ctx context.Context, backend referenceBackend, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE TABLE child(v INT)")
		lockModesExec(t, ctx, a, "BEGIN")
		lockModesExec(t, ctx, a, "LOCK t IN ACCESS SHARE MODE")
		lockModesExec(t, ctx, a, "SAVEPOINT parent")
		lockModesExec(t, ctx, a, "SAVEPOINT inner_mark")
		lockModesExec(t, ctx, a, "LOCK child")
		lockModesExec(t, ctx, a, "RELEASE inner_mark")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "55P03", `could not obtain lock on relation "child"`)
		lockModesExec(t, ctx, a, "ROLLBACK TO parent")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
			lockModesExec(t, ctx, a, end)
			referenceRunStep(t, ctx, b, backend.recorder, referenceSelect("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "", "")
			if end == "COMMIT" {
				lockModesExec(t, ctx, a, "BEGIN")
				lockModesExec(t, ctx, a, "LOCK t IN ACCESS SHARE MODE")
			}
		}
	})
}

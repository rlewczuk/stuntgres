package smoke_test

import (
	"context"
	"database/sql"
	"testing"
)

func smokeLockModesRolesJoin(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		for _, q := range []string{"CREATE USER lockowner", "CREATE USER lockstranger", "SET SESSION AUTHORIZATION lockowner", "CREATE TABLE left_t(v INT)", "CREATE TABLE right_t(v INT)", "CREATE VIEW joined AS SELECT left_t.v FROM left_t JOIN right_t USING(v)", "CREATE VIEW nested AS SELECT v FROM joined"} {
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
		lockModesExec(t, ctx, b, "SET SESSION AUTHORIZATION lockstranger")
		lockModesProbe(t, ctx, b, "LOCK left_t IN ACCESS SHARE MODE NOWAIT", "42501", "permission denied for table left_t")
		lockModesProbe(t, ctx, b, "LOCK nested IN SHARE MODE NOWAIT", "42501", "permission denied for view nested")
	})
}

func smokeLockModesStackPublication(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE TABLE child(v INT)")
		lockModesExec(t, ctx, a, "BEGIN")
		defer a.ExecContext(ctx, "ROLLBACK")
		lockModesExec(t, ctx, a, "LOCK t IN ACCESS SHARE MODE")
		lockModesExec(t, ctx, a, "SAVEPOINT parent")
		lockModesExec(t, ctx, a, "SAVEPOINT inner_mark")
		lockModesExec(t, ctx, a, "LOCK child")
		lockModesExec(t, ctx, a, "RELEASE inner_mark")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "55P03", `could not obtain lock on relation "child"`)
		lockModesExec(t, ctx, a, "ROLLBACK TO parent")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			if _, err := a.ExecContext(ctx, "INSERT INTO t VALUES(7)"); err != nil {
				t.Fatal(err)
			}
			lockModesExec(t, ctx, a, end)
			var count int
			if err := b.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("publication/discard", end, count)
			}
			lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "", "")
			if end == "COMMIT" {
				lockModesExec(t, ctx, a, "BEGIN")
				lockModesExec(t, ctx, a, "LOCK t IN ACCESS SHARE MODE")
			}
		}
	})
}

package smoke_test

import (
	"context"
	"database/sql"
	"testing"
)

func smokeLockModesSavepointStack(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE TABLE child(v INT)")
		lockModesExec(t, ctx, a, "BEGIN")
		defer a.ExecContext(ctx, "ROLLBACK")
		lockModesExec(t, ctx, a, "LOCK t IN SHARE UPDATE EXCLUSIVE MODE")
		lockModesExec(t, ctx, a, "LOCK t IN SHARE MODE")
		lockModesExec(t, ctx, a, "SAVEPOINT outermark")
		lockModesExec(t, ctx, a, "SAVEPOINT s")
		lockModesExec(t, ctx, a, "LOCK child IN ACCESS SHARE MODE")
		lockModesExec(t, ctx, a, "SAVEPOINT s")
		lockModesExec(t, ctx, a, "LOCK child")
		lockModesExec(t, ctx, a, "ROLLBACK TO s")
		lockModesProbe(t, ctx, b, "LOCK child IN ROW EXCLUSIVE MODE NOWAIT", "", "")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "55P03", `could not obtain lock on relation "child"`)
		lockModesExec(t, ctx, a, "RELEASE s")
		lockModesExec(t, ctx, a, "ROLLBACK TO s")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
		lockModesExec(t, ctx, a, "LOCK child")
		lockModesExec(t, ctx, a, "RELEASE s")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "55P03", `could not obtain lock on relation "child"`)
		lockModesExec(t, ctx, a, "ROLLBACK TO outermark")
		lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
		// Both incomparable premark modes must survive: S denies RX, SUE denies S.
		lockModesProbe(t, ctx, b, "LOCK t IN ROW EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesProbe(t, ctx, b, "LOCK t IN SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "", "")
	})
}
func smokeLockModesAutomaticMatrix(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		if _, err := a.ExecContext(ctx, "INSERT INTO t VALUES(1)"); err != nil {
			t.Fatal(err)
		}
		// Ordinary SELECT, row-mark SELECT, and DML contribute table AS/RS/RX.
		for _, automatic := range []struct {
			q    string
			held int
		}{
			{"SELECT v FROM t", 0}, {"SELECT v FROM t FOR SHARE", 1}, {"UPDATE t SET v=2", 2},
		} {
			for i, mode := range lockModesNames {
				t.Run(automatic.q+"/"+mode, func(t *testing.T) {
					lockModesExec(t, ctx, a, "BEGIN")
					defer a.ExecContext(ctx, "ROLLBACK")
					if automatic.held < 2 {
						rows, err := a.QueryContext(ctx, automatic.q)
						if err != nil {
							t.Fatal(err)
						}
						for rows.Next() {
						}
						if err = rows.Err(); err != nil {
							t.Fatal(err)
						}
						rows.Close()
					} else {
						if _, err := a.ExecContext(ctx, automatic.q); err != nil {
							t.Fatal(err)
						}
					}
					code, message := "", ""
					if lockModesConflicts[i][automatic.held] {
						code = "55P03"
						message = `could not obtain lock on relation "t"`
					}
					lockModesProbe(t, ctx, b, "LOCK t IN "+mode+" MODE NOWAIT", code, message)
				})
			}
		}
	})
}

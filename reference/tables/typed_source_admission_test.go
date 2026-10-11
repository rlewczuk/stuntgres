package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTypedTableAtomicityCreateSourceAdmission(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		holder, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, typedTablePairRows("t", [][]any{{int64(11), "hey"}}))
		alterConn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer alterConn.Close()
		alterSQL := "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE"
		var alterStmt *sql.Stmt
		if prepared {
			alterStmt, err = alterConn.PrepareContext(p.Ctx, alterSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer alterStmt.Close()
		}
		alterCtx, cancelAlter := context.WithTimeout(p.Ctx, 10*time.Second)
		defer cancelAlter()
		finished := make(chan error, 1)
		go func() {
			var result sql.Result
			var err error
			if prepared {
				result, err = alterStmt.ExecContext(alterCtx)
			} else {
				result, err = alterConn.ExecContext(alterCtx, alterSQL)
			}
			if err == nil {
				var count int64
				count, err = result.RowsAffected()
				if err == nil && count != 0 {
					err = errors.New("ALTER returned nonzero utility count")
				}
			}
			finished <- err
		}()
		joined := false
		defer func() {
			cancelAlter()
			_ = holder.Rollback()
			if !joined {
				select {
				case <-finished:
				case <-time.After(12 * time.Second):
					t.Error("bounded ALTER worker did not drain during cleanup")
				}
			}
		}()
		run(fixture.Exec("SET lock_timeout='50ms'", 0))
		// A fresh AccessShare reader times out only after ALTER's target AX
		// request is queued behind holder. This public fairness probe avoids
		// guessing scheduler timing or relying on an unsupported lock catalog.
		probeCtx, cancelProbe := context.WithTimeout(p.Ctx, 3*time.Second)
		defer cancelProbe()
		wantTimeout := typedTableError("55P03", "canceling statement due to lock timeout")
		wantProbe := wantTimeout
		wantProbe.Position = "16"
		for {
			rows, probeErr := c.QueryContext(probeCtx, "SELECT id FROM t LIMIT 0")
			if rows != nil {
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if probeErr != nil {
				if got := fixture.ExtractDiagnostic(t, probeErr); got != wantProbe {
					t.Fatalf("queue probe diagnostic=%#v want=%#v", got, wantProbe)
				}
				break
			}
			if probeCtx.Err() != nil {
				t.Fatal("ALTER never queued behind retained target reader", probeCtx.Err())
			}
			select {
			case err := <-finished:
				joined = true
				t.Fatal("ALTER completed before holder release", err)
			default:
			}
		}
		fixture.CompositeReferenceError(t, p, c, "CREATE TABLE late OF pair", "execute", wantTimeout)
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='late'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		cancelConn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cancelConn.Close()
		cancelCtx, cancelCreate := context.WithTimeout(p.Ctx, 100*time.Millisecond)
		var result sql.Result
		if prepared {
			stmt, prepareErr := cancelConn.PrepareContext(p.Ctx, "CREATE TABLE canceled OF pair")
			if prepareErr != nil {
				cancelCreate()
				t.Fatal(prepareErr)
			}
			result, err = stmt.ExecContext(cancelCtx)
			_ = stmt.Close()
		} else {
			result, err = cancelConn.ExecContext(cancelCtx, "CREATE TABLE canceled OF pair")
		}
		cancelCreate()
		if result != nil || err == nil {
			t.Fatal("blocked canceled CREATE exposed successful result", result, err)
		}
		if b.PG {
			want := typedTableError("57014", "canceling statement due to user request")
			if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Errorf("canceled CREATE diagnostic=%#v want=%#v", got, want)
			}
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("native source wait did not honor deadline", err)
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-finished:
			joined = true
			if err != nil {
				t.Fatal("ALTER did not recover after reader release", err)
			}
		case <-alterCtx.Done():
			t.Fatal("ALTER did not finish", alterCtx.Err())
		}
		run(fixture.Exec("SET lock_timeout=0", 0))
		run(fixture.Exec("CREATE TABLE late OF pair", 0))
		run(typedTableAttachment("late", "pair", true))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM late", []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, []bool{true, true, true}, 0)
		run(fixture.Exec("INSERT INTO late VALUES($1,$2,$3)", 1, int64(7), "ok", true))
		run(fixture.Select("SELECT * FROM late", []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(7), "ok", true}}))
		run(fixture.Select("SELECT * FROM t", []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(11), "hey", nil}}))
	})
}

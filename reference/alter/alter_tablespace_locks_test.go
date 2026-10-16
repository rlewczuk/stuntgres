package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterTablespaceLocks(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsExec(t, p, b, c, "CREATE TABLE i(id INT,note TEXT)")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO i VALUES(7,'keep')", 1))
		atsLockWitness(t, p, b, c, "i", "ALTER TABLE i SET TABLESPACE pg_default", false)
	})
}

func TestReferenceAlterTablespacePhysicalLocks(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !atsPhysical(t, p, b, c) {
			return
		}
		atsExec(t, p, b, c, "CREATE TABLE ats_blocked(id INT,note TEXT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_blocked VALUES(7,'keep')", 1))
		// Exactly one selected relation makes NOWAIT's diagnostic deterministic.
		atsLockWitness(t, p, b, c, "ats_blocked", "ALTER TABLE ALL IN TABLESPACE ats_src SET TABLESPACE ats_dst", true)
	})
}

func TestReferenceAlterTablespacePhysicalBulkCancellation(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !atsPhysical(t, p, b, c) {
			return
		}
		for _, name := range []string{"ats_blocked", "ats_second"} {
			atsExec(t, p, b, c, "CREATE TABLE "+name+"(id INT,note TEXT) TABLESPACE ats_src")
		}
		atsExec(t, p, b, c, "CREATE TABLE ats_excluded(id INT,note TEXT) TABLESPACE ats_dst")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_blocked VALUES(7,'blocked')", 1))
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_second VALUES(8,'unblocked')", 1))
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_excluded VALUES(9,'excluded')", 1))
		ctx, stop := context.WithTimeout(p.Ctx, 10*time.Second)
		defer stop()
		holder, err := b.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		observer, err := b.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		consumer, err := b.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer consumer.Close()
		placements := func(x fixture.Executor, space string) {
			atsRun(t, p, b, x, fixture.Select("SELECT c.relname,s.spcname FROM pg_class c JOIN pg_tablespace s ON s.oid=c.reltablespace WHERE c.relnamespace='public'::regnamespace AND c.relname IN ('ats_blocked','ats_second','ats_excluded') ORDER BY c.relname", []string{"relname", "spcname"}, []string{"NAME", "NAME"}, [][]any{{"ats_blocked", space}, {"ats_excluded", "ats_dst"}, {"ats_second", space}}))
		}
		data := fixture.Select("SELECT 'ats_blocked'::text AS name,id,note FROM ats_blocked UNION ALL SELECT 'ats_excluded'::text,id,note FROM ats_excluded UNION ALL SELECT 'ats_second'::text,id,note FROM ats_second ORDER BY name", []string{"name", "id", "note"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"ats_blocked", int64(7), "blocked"}, {"ats_excluded", int64(9), "excluded"}, {"ats_second", int64(8), "unblocked"}})
		placements(observer, "ats_src")
		atsRun(t, p, b, observer, data)
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		atsExec(t, p, b, tx, "LOCK TABLE ats_blocked IN ACCESS SHARE MODE")
		// A pre-opened owned Rows consumer avoids taking fresh AS on a candidate
		// the worker may already hold AX on. No catalog scan order is assumed.
		retained, err := consumer.QueryContext(ctx, data.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		q := "ALTER TABLE ALL IN TABLESPACE ats_src SET TABLESPACE ats_dst"
		held, err := c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		workerPID := fixture.LockPID(t, ctx, c)
		holderPID := fixture.LockPID(t, ctx, holder)
		oid := fixture.LockOID(t, ctx, "ats_blocked")
		waitCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			result, e := held.ExecContext(waitCtx)
			if result != nil {
				e = errors.New("waiting utility unexpectedly published result")
			}
			done <- e
		}()
		joined := false
		defer func() {
			cancel()
			tx.Rollback()
			if !joined {
				select {
				case <-done:
				case <-ctx.Done():
					t.Error("bulk worker did not join", ctx.Err())
				}
			}
		}()
		fixture.LockWaitBehind(t, ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
		// Placement is a live nonblocking catalog observation. The retained Rows
		// prove consumer data stability, not a fresh in-flight heap read; fresh
		// complete post-cancel reads below prove the current data stayed intact.
		placements(observer, "ats_src")
		names, err := retained.Columns()
		if err != nil || len(names) != 3 || names[0] != "name" || names[1] != "id" || names[2] != "note" {
			t.Fatal("retained columns", names, err)
		}
		types, err := retained.ColumnTypes()
		if err != nil || len(types) != 3 || types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "INT4" || types[2].DatabaseTypeName() != "TEXT" {
			t.Fatal("retained types", types, err)
		}
		for _, want := range data.Rows {
			if !retained.Next() {
				t.Fatal("retained row missing", retained.Err())
			}
			var name, note string
			var id int64
			if err = retained.Scan(&name, &id, &note); err != nil || name != want[0] || id != want[1] || note != want[2] {
				t.Fatal("retained data", name, id, note, err)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained extra row/error", retained.Err())
		}
		var ok bool
		if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
			t.Fatal(ok, err)
		}
		select {
		case err = <-done:
			joined = true
			want := atsDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
		case <-ctx.Done():
			t.Fatal("bulk cancel did not join", ctx.Err())
		}
		placements(observer, "ats_src")
		atsRun(t, p, b, observer, data)
		var pending int
		if err = observer.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", workerPID).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("pending bulk locks after cancel", pending, err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		placements(observer, "ats_src")
		atsRun(t, p, b, observer, data)
		probe, err := observer.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer probe.Rollback()
		atsExec(t, p, b, probe, "LOCK TABLE ats_blocked,ats_second IN ACCESS EXCLUSIVE MODE NOWAIT")
		if err = probe.Rollback(); err != nil {
			t.Fatal(err)
		}
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		atsExec(t, p, b, c, "BEGIN")
		atsRun(t, p, b, executor, fixture.Exec(q, 0))
		placements(c, "ats_dst")
		atsRun(t, p, b, c, data)
		atsExec(t, p, b, c, "ROLLBACK")
		placements(observer, "ats_src")
		atsRun(t, p, b, observer, data)
		// SAME physical session and prepared utility recover after cancellation.
		atsRun(t, p, b, executor, fixture.Exec(q, 0))
		placements(observer, "ats_dst")
		atsRun(t, p, b, observer, data)
	})
}

func atsLockWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, q string, bulk bool) {
	t.Helper()
	ctx, stop := context.WithTimeout(p.Ctx, 10*time.Second)
	defer stop()
	holder, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	observer, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	var original int64
	if err = c.QueryRowContext(ctx, "SELECT reltablespace::bigint FROM pg_class WHERE oid='"+name+"'::regclass::oid").Scan(&original); err != nil {
		t.Fatal(err)
	}
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	atsExec(t, p, b, tx, "LOCK TABLE "+name+" IN ACCESS SHARE MODE")
	// Quiet Prepare is behind a conflicting holder and resolves no lock/capability.
	held, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal("utility Prepare behind lock", err)
	}
	defer held.Close()
	atsExec(t, p, b, c, "SET lock_timeout='100ms'")
	if bulk {
		atsError(t, p, b, c, q+" NOWAIT", atsDiagnostic("55006", "aborting because lock on relation \"public."+name+"\" is not available"))
	} else {
		atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, fixture.ErrorDiagnostic(q, atsDiagnostic("55P03", "canceling statement due to lock timeout")))
	}
	atsExec(t, p, b, c, "RESET lock_timeout")
	atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT")
	if !bulk {
		// Equal bulk returns before locks even with AX held, unlike named SET.
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		tx, err = holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		atsExec(t, p, b, tx, "LOCK TABLE i IN ACCESS EXCLUSIVE MODE")
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT")
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		tx, err = holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		atsExec(t, p, b, tx, "LOCK TABLE i IN ACCESS SHARE MODE")
	}
	workerPID, holderPID := 0, 0
	var oid int64
	if b.PG {
		workerPID = fixture.LockPID(t, ctx, c)
		holderPID = fixture.LockPID(t, ctx, holder)
		oid = fixture.LockOID(t, ctx, name)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		result, e := held.ExecContext(waitCtx)
		if result != nil {
			if e != nil {
				e = errors.New("canceled utility published result")
			} else {
				n, x := result.RowsAffected()
				if x != nil {
					e = x
				} else if n != 0 {
					e = errors.New("nonzero utility count")
				}
			}
		}
		done <- e
	}()
	joined := false
	defer func() {
		cancel()
		tx.Rollback()
		if !joined {
			select {
			case <-done:
			case <-ctx.Done():
				t.Error("worker did not join", ctx.Err())
			}
		}
	}()
	if b.PG {
		fixture.LockWaitBehind(t, ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
	} else {
		fixture.AlterIdentityObserveAX(t, ctx, observer, done)
	}
	if b.PG {
		var ok bool
		if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
			t.Fatal(ok, err)
		}
	} else {
		cancel()
	}
	select {
	case err = <-done:
		joined = true
		if b.PG {
			want := atsDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancel did not join", ctx.Err())
	}
	atsPlacement(t, p, b, observer, name, original)
	if b.PG {
		var pending int
		if err = observer.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", workerPID).Scan(&pending); err != nil || pending != 0 {
			t.Fatal("pending canceled locks", pending, err)
		}
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	atsRun(t, p, b, c, fixture.Select("SELECT id,note FROM "+name, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}}))
	probe, err := observer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	atsExec(t, p, b, probe, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE NOWAIT")
	if err = probe.Rollback(); err != nil {
		t.Fatal(err)
	}
	atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, fixture.Exec(q, 0))
	if bulk {
		var dst int64
		if err = c.QueryRowContext(ctx, "SELECT oid::bigint FROM pg_tablespace WHERE spcname='ats_dst'").Scan(&dst); err != nil {
			t.Fatal(err)
		}
		atsPlacement(t, p, b, c, name, dst)
	}
}

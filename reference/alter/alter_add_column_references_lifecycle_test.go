package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

func columnReferencesMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, table, name string) {
	t.Helper()
	run := columnReferencesRun(t, p, b, c, prepared)
	q := "SELECT contype,conkey::text,confkey::text,confupdtype,confdeltype,confmatchtype,conenforced,convalidated,condeferrable,condeferred,connoinherit,confrelid='p'::regclass AS target,conindid=(SELECT conindid FROM pg_constraint WHERE conrelid='p'::regclass AND contype='p') AS provider FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2"
	run(fixture.Select(q, []string{"contype", "conkey", "confkey", "confupdtype", "confdeltype", "confmatchtype", "conenforced", "convalidated", "condeferrable", "condeferred", "connoinherit", "target", "provider"}, []string{"CHAR", "TEXT", "TEXT", "CHAR", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"f", "{2}", "{1}", "a", "a", "s", true, true, false, false, true, true, true}}, table, name))
	oid := enforcementIdentity(t, p, b, c, table, name, "FOREIGN KEY (pid) REFERENCES p(id)", true, true)
	ids := enforcementTriggers(t, p, b, c, oid, true)
	seen := make(map[int64]bool)
	for _, id := range ids {
		if seen[id] {
			t.Fatal("duplicate logical trigger OID", id)
		}
		seen[id] = true
	}
	triggerRows := [][]any{{"p", false, false, int64(2)}, {table, false, false, int64(2)}}
	if table < "p" {
		triggerRows[0], triggerRows[1] = triggerRows[1], triggerRows[0]
	}
	run(fixture.Select("SELECT tgrelid::regclass::text AS relation,tgdeferrable,tginitdeferred,count(*) FROM pg_trigger WHERE tgconstraint=$1::oid GROUP BY tgrelid,tgdeferrable,tginitdeferred ORDER BY relation", []string{"relation", "tgdeferrable", "tginitdeferred", "count"}, []string{"TEXT", "BOOL", "BOOL", "INT8"}, triggerRows, oid))
	run(fixture.Select("SELECT deptype,refobjid::regclass::text AS refname,refobjsubid FROM pg_depend WHERE classid='pg_constraint'::regclass AND objid=$1::oid AND refclassid='pg_class'::regclass ORDER BY deptype,refname,refobjsubid", []string{"deptype", "refname", "refobjsubid"}, []string{"CHAR", "TEXT", "INT4"}, [][]any{{"a", table, int64(2)}, {"n", "p", int64(1)}, {"n", "p_pkey", int64(0)}}, oid))
	run(fixture.Select("SELECT constraint_schema,constraint_name,table_schema,table_name,constraint_type,is_deferrable,initially_deferred,enforced FROM information_schema.table_constraints WHERE table_schema='public' AND table_name=$1 AND constraint_name=$2", []string{"constraint_schema", "constraint_name", "table_schema", "table_name", "constraint_type", "is_deferrable", "initially_deferred", "enforced"}, []string{"NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"public", name, "public", table, "FOREIGN KEY", "NO", "NO", "YES"}}, table, name))
	run(fixture.Select("SELECT constraint_name,table_name,column_name,ordinal_position,position_in_unique_constraint FROM information_schema.key_column_usage WHERE table_schema='public' AND table_name=$1 AND constraint_name=$2", []string{"constraint_name", "table_name", "column_name", "ordinal_position", "position_in_unique_constraint"}, []string{"NAME", "NAME", "NAME", "INT4", "INT4"}, [][]any{{name, table, "pid", int64(1), int64(1)}}, table, name))
	run(fixture.Select("SELECT constraint_name,unique_constraint_schema,unique_constraint_name,match_option,update_rule,delete_rule FROM information_schema.referential_constraints WHERE constraint_schema='public' AND constraint_name=$1", []string{"constraint_name", "unique_constraint_schema", "unique_constraint_name", "match_option", "update_rule", "delete_rule"}, []string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{name, "public", "p_pkey", "NONE", "NO ACTION", "NO ACTION"}}, name))
	run(fixture.Select("SELECT table_schema,table_name,column_name,constraint_schema,constraint_name FROM information_schema.constraint_column_usage WHERE constraint_schema='public' AND constraint_name=$1", []string{"table_schema", "table_name", "column_name", "constraint_schema", "constraint_name"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME"}, [][]any{{"public", "p", "id", "public", name}}, name))
	run(fixture.Select("SELECT table_schema,table_name,constraint_schema,constraint_name FROM information_schema.constraint_table_usage WHERE constraint_schema='public' AND constraint_name=$1", []string{"table_schema", "table_name", "constraint_schema", "constraint_name"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"public", "p", "public", name}}, name))
}

func TestReferenceAlterAddColumnReferencesMetadata(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "t", "control", "self", "deferred", "p")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("ALTER TABLE t ADD pid INT CONSTRAINT fk REFERENCES p", 0))
		columnReferencesMetadata(t, p, b, c, prepared, "t", "fk")
		run(fixture.Exec("CREATE TABLE control(id INT)", 0))
		run(fixture.Exec("ALTER TABLE control ADD pid INT, ADD CONSTRAINT control_fk FOREIGN KEY(pid) REFERENCES p", 0))
		columnReferencesMetadata(t, p, b, c, prepared, "control", "control_fk")
		run(fixture.Exec("CREATE TABLE self(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("ALTER TABLE self ADD pid INT REFERENCES self(id)", 0))
		oid := enforcementIdentity(t, p, b, c, "self", "self_pid_fkey", "FOREIGN KEY (pid) REFERENCES self(id)", true, true)
		ids := enforcementTriggers(t, p, b, c, oid, true)
		seen := make(map[int64]bool)
		for _, id := range ids {
			if seen[id] {
				t.Fatal("duplicate self trigger identity", id)
			}
			seen[id] = true
		}
		run(fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgconstraint=$1::oid AND tgrelid='self'::regclass AND tgconstrrelid='self'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, oid))
		run(fixture.Exec("CREATE TABLE deferred(id INT)", 0))
		run(fixture.Exec("ALTER TABLE deferred ADD pid INT CONSTRAINT deferred_fk REFERENCES p ON UPDATE CASCADE ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED", 0))
		run(fixture.Select("SELECT tgrelid::regclass::text AS relation,tgdeferrable,tginitdeferred,count(*) FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conrelid='deferred'::regclass AND conname='deferred_fk') GROUP BY tgrelid,tgdeferrable,tginitdeferred ORDER BY relation,tgdeferrable", []string{"relation", "tgdeferrable", "tginitdeferred", "count"}, []string{"TEXT", "BOOL", "BOOL", "INT8"}, [][]any{{"deferred", true, true, int64(2)}, {"p", false, false, int64(1)}, {"p", true, true, int64(1)}}))
	})
}

func TestReferenceAlterAddColumnReferencesLifecycle(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "t", "p")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE t(id INT,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
		before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
		holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
		q := "ALTER TABLE t ADD pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p"
		var executor fixture.Executor = c
		if prepared {
			executor = addConstraintHeld(t, p, b, c, q)
		}
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN ROW EXCLUSIVE MODE", 0))
		workerPID, holderPID := 0, 0
		if b.PG {
			workerPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			result, err := executor.ExecContext(ctx, q)
			if result != nil {
				done <- fmt.Errorf("canceled ALTER returned successful result: %v", result)
				return
			}
			done <- err
		}()
		joined := false
		defer func() {
			cancel()
			if !joined {
				select {
				case <-done:
				case <-p.Ctx.Done():
					t.Error("ADD REFERENCES worker did not join", p.Ctx.Err())
				}
			}
		}()
		if b.PG {
			fixture.LockWaitBehind(t, p.Ctx, workerPID, holderPID, fixture.LockOID(t, p.Ctx, "p"), "ShareRowExclusiveLock")
		} else {
			// A compatible holder and a NOWAIT peer distinguish a queued strict
			// provider grant from a worker that has not reached lock admission.
			for {
				select {
				case err := <-done:
					joined = true
					t.Fatal("ADD did not wait for its provider", err)
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				default:
				}
				probe, err := observer.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = probe.ExecContext(p.Ctx, "LOCK TABLE p IN ROW EXCLUSIVE MODE NOWAIT")
				if rollback := probe.Rollback(); rollback != nil {
					t.Fatal(rollback)
				}
				if err != nil {
					want := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "p"`)
					fixture.AssertDiagnostic(t, err, &want)
					break
				}
				runtime.Gosched()
			}
		}
		if b.PG {
			var ok bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
				t.Fatal(ok, err)
			}
		} else {
			cancel()
		}
		select {
		case err := <-done:
			joined = true
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, err, &want)
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-p.Ctx.Done():
			t.Fatal("ADD REFERENCES cancellation did not join", p.Ctx.Err())
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		columnReferencesUnchanged(t, p, b, c, before, attrs)
		run(fixture.Select("SELECT id,note FROM t", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
		run(fixture.Exec(q, 0))
		run(fixture.Select("SELECT id,note,pid FROM t", []string{"id", "note", "pid"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "keep", int64(1)}}))
		run(fixture.ErrorDiagnostic("UPDATE t SET pid=99", addConstraintFK("t", "fk", "pid", "99", "p")))
	})
}

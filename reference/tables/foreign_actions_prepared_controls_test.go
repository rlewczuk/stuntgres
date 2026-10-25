package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsPreparedReplacement(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		q := "UPDATE p SET id=2 WHERE id=1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.PG {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1))
		} else {
			old, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Rollback()
			fixture.RunStep(t, p.Ctx, old, b.Recorder, foreignKeyIntRows("SELECT id FROM p", "id", 1))
			captured, err := old.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			var before int64
			if err = b.DB.QueryRowContext(p.Ctx, "SELECT 'p'::regclass::oid").Scan(&before); err != nil || before <= 0 {
				t.Fatal(before, err)
			}
			dump := fixture.CheckDump(t, p)
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			s := fixture.ForeignKeyFail("SELECT id FROM p", fixture.ForeignKeyError("42P01", `relation "p" does not exist`, "", "", ""), false)
			s.SelectRows = true
			s.Diagnostic.Position = "16"
			run(s)
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			var after int64
			if err = b.DB.QueryRowContext(p.Ctx, "SELECT 'p'::regclass::oid").Scan(&after); err != nil || after <= 0 {
				t.Fatal("restored persistent relation lacks current identity", before, after, err)
			}
			// An idle held statement may legitimately reanalyse after Reset.
			// This real prepared transaction statement instead owns the old
			// transaction epoch, even if numeric OIDs repeat after replacement.
			r, err := captured.ExecContext(p.Ctx)
			if r != nil {
				t.Fatal("failed held action returned a result", r)
			}
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			if err := old.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
			run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1))
		}
		run(foreignKeyIntRows("SELECT id FROM p", "id", 2))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
		run(fixture.Prepared(fixture.Exec("UPDATE p SET id=3 WHERE id=2", 1)))
		run(foreignKeyIntRows("SELECT id FROM p", "id", 3))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 3))
		run(fixture.Exec("DELETE FROM p", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
	})
}

func TestReferenceForeignActionsTemporaryLists(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		step(fixture.Exec("CREATE TABLE persistent(id INT PRIMARY KEY)", 0))
		step(fixture.Exec("CREATE TEMP TABLE tp(a INT,b INT,PRIMARY KEY(a,b))", 0))
		step(fixture.Exec("CREATE TEMP TABLE tc(a INT,b INT,CONSTRAINT temp_subset_fk FOREIGN KEY(a,b) REFERENCES tp ON UPDATE CASCADE ON DELETE SET NULL(b,b))", 0))
		step(fixture.Exec("INSERT INTO tp VALUES(7,2)", 1))
		step(fixture.Exec("INSERT INTO tc VALUES(7,2)", 1))
		ids := map[string]int64{}
		for _, q := range []string{"SELECT 'tp'::regclass::oid", "SELECT 'tc'::regclass::oid", "SELECT oid::bigint FROM pg_constraint WHERE conname='temp_subset_fk'"} {
			var id int64
			if err = conn.QueryRowContext(p.Ctx, q).Scan(&id); err != nil || id <= 0 {
				t.Fatal(q, id, err)
			}
			ids[q] = id
		}
		q := "DELETE FROM tp WHERE b=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if !b.PG {
			dump := fixture.CheckDump(t, p)
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			for q, want := range ids {
				var got int64
				if err = conn.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != want {
					t.Fatal("TEMP subset authority changed", q, got, want, err)
				}
			}
			if after := fixture.CheckDump(t, p); !bytes.Equal(dump, after) {
				t.Fatal("TEMP subset appeared in dump")
			}
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 2))
		step(fixture.Select("SELECT a,b FROM tc", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}}))
		step(fixture.Exec("DROP TABLE tc,tp", 0))
	})
}

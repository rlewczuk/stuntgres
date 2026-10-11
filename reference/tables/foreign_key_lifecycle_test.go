package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignKeyLifecycle(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE TABLE p(id INT)", "CREATE UNIQUE INDEX supporting ON p(id)", "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p(id))"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		d := foreignKeyError("2BP01", "cannot drop index supporting because other objects depend on it", "constraint fk on table c depends on index supporting", "", "")
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			run(foreignKeyFail("DROP INDEX supporting RESTRICT", d, prepared))
		}
		run(fixture.Exec("DROP INDEX supporting CASCADE", 0))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
		run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
	})
}

func TestReferenceForeignKeyRetainedStatements(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO c VALUES($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		runHeld := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: "INSERT INTO c VALUES($1)"}, b.Recorder, s)
		}
		runHeld(fixture.Exec("INSERT INTO c VALUES($1)", 1, 1))
		run(fixture.Exec("ALTER TABLE p RENAME id TO renamed", 0))
		run(fixture.Exec("ALTER TABLE p RENAME TO renamed_parent", 0))
		runHeld(fixture.Exec("INSERT INTO c VALUES($1)", 1, 1))
		s := foreignKeyFail("INSERT INTO c VALUES($1)", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "renamed_parent".`), false)
		s.Args = []any{9}
		runHeld(s)
		run(fixture.Exec("ALTER TABLE renamed_parent ALTER renamed TYPE BIGINT", 0))
		runHeld(fixture.Exec("INSERT INTO c VALUES($1)", 1, 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1, 1, 1))
	})
}

func foreignKeyRoundtrip(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var dump, again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("FK redump changed", err, again.String(), dump.String())
	}
}

func TestReferenceForeignKeyNativeControls(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		// PostgreSQL establishes the same cyclic graph through its real ALTER.
		// Native uses only the documented isolated pending-CREATE replay path.
		if b.PG {
			for _, q := range []string{"CREATE TABLE a(id INT PRIMARY KEY,bid INT)", "CREATE TABLE b(id INT PRIMARY KEY,aid INT)", "INSERT INTO a VALUES(1,2)", "INSERT INTO b VALUES(2,1)", "ALTER TABLE a ADD CONSTRAINT a_fk FOREIGN KEY(bid) REFERENCES b(id)", "ALTER TABLE b ADD CONSTRAINT b_fk FOREIGN KEY(aid) REFERENCES a(id)"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
		} else {
			script := "CREATE TABLE a(id INT PRIMARY KEY,bid INT CONSTRAINT a_fk REFERENCES b(id)); CREATE TABLE b(id INT PRIMARY KEY,aid INT CONSTRAINT b_fk REFERENCES a(id)); INSERT INTO a VALUES(1,2); INSERT INTO b VALUES(2,1);"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Select("SELECT id,bid FROM a", []string{"id", "bid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		run(fixture.Select("SELECT id,aid FROM b", []string{"id", "aid"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
		run(foreignKeyFail("INSERT INTO a VALUES(3,9)", foreignKeyChild("a", "a_fk", `Key (bid)=(9) is not present in table "b".`), false))
		run(foreignKeyFail("DELETE FROM b", foreignKeyParent("b", "a", "a_fk", `Key (id)=(2) is still referenced from table "a".`), false))
		if b.PG {
			return
		} // Native APIs are never invoked against pq.
		run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		ids := make(map[string]int64)
		for _, q := range []string{"SELECT 'sentinel'::regclass::oid", "SELECT 'a'::regclass::oid", "SELECT 'b'::regclass::oid", "SELECT oid::bigint FROM pg_constraint WHERE conname='a_fk'", "SELECT oid::bigint FROM pg_constraint WHERE conname='b_fk'"} {
			var id int64
			if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&id); err != nil || id <= 0 {
				t.Fatal(q, id, err)
			}
			ids[q] = id
		}
		unchanged := func() {
			for q, id := range ids {
				var got int64
				if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != id {
					t.Fatal("failed Restore changed catalog identity", q, id, got, err)
				}
			}
		}
		for i, script := range []string{
			"CREATE TABLE p(id INT PRIMARY KEY); CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p); INSERT INTO c VALUES(9);",
			"CREATE TABLE c(pid INT REFERENCES absent);",
			"CREATE TABLE p(id INT); CREATE TABLE c(pid INT REFERENCES p(id));",
			"CREATE TABLE p(id INT PRIMARY KEY); CREATE TABLE c(pid TEXT REFERENCES p);",
			"CREATE TABLE p(id INT PRIMARY KEY); CREATE TABLE c(a INT CONSTRAINT same REFERENCES p,b INT CONSTRAINT same REFERENCES p);",
		} {
			held, err := b.DB.QueryContext(p.Ctx, "SELECT v FROM sentinel")
			if err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				held.Close()
				t.Fatal("invalid final FK import succeeded", script)
			}
			fixture.State(t, err, []string{"23503", "42P01", "42830", "42804", "42710"}[i])
			fixture.TransactionValues(t, held, 91)
			held.Close()
			var after bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed import changed target", err)
			}
			run(foreignKeyIntRows("SELECT v FROM sentinel", "v", 91))
			unchanged()
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(foreignKeyIntRows("SELECT v FROM sentinel", "v", 91))
		ioFailure := errors.New("foreign-key input failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, foreignKeyFailedReader{ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal(err)
		}
		unchanged()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("cancelled or I/O failed import changed target", err)
		}
	})
}

func TestReferenceForeignKeyFeatureSmoke(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1,'x'),(2,'y')", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1,$2)", 1, 1, "child")))
		run(fixture.Exec("CREATE TABLE composite_parent(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("CREATE TABLE full_child(a INT,b TEXT,CONSTRAINT full_fk FOREIGN KEY(a,b) REFERENCES composite_parent MATCH FULL)", 0))
		run(fixture.Exec("CREATE TABLE simple_child(a INT,b TEXT,FOREIGN KEY(a,b) REFERENCES composite_parent MATCH SIMPLE)", 0))
		run(fixture.Exec("INSERT INTO composite_parent VALUES(1,'x'),(2,'y')", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO full_child VALUES($1,$2)", 1, 1, "x")))
		run(fixture.Exec("INSERT INTO simple_child VALUES(NULL,'z')", 1))
		run(fixture.Exec("CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self)", 0))
		run(fixture.Exec("INSERT INTO self VALUES(2,1),(1,NULL)", 2))
		run(fixture.Exec("DELETE FROM self", 2))
		check := func() {
			run(fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "child"}}))
			run(foreignKeyFail("INSERT INTO c VALUES(9,'bad')", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), true))
			run(foreignKeyFail("DELETE FROM p", foreignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`), false))
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
			run(foreignKeyFail("UPDATE p SET id=id+10", foreignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`), true))
			run(foreignKeyFail("INSERT INTO full_child VALUES(NULL,'x')", foreignKeyChild("full_child", "full_fk", "MATCH FULL does not allow mixing of null and nonnull key values."), false))
			run(fixture.Select("SELECT a,b FROM full_child", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
			run(fixture.Select("SELECT a,b FROM simple_child", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "z"}}))
			run(fixture.Select("SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='full_child' AND c.conname='full_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (a, b) REFERENCES composite_parent(a, b) MATCH FULL"}}))
		}
		check()
		if !b.PG {
			foreignKeyRoundtrip(t, p)
			check()
		}
	})
}

type foreignKeyFailedReader struct{ err error }

func (r foreignKeyFailedReader) Read([]byte) (int, error) { return 0, r.err }

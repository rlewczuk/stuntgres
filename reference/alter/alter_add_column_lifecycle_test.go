package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnMixedLists(t *testing.T) {
	for _, actions := range []string{"ADD b TEXT, DROP b", "DROP b, ADD b TEXT"} {
		t.Run(actions, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,b INT DEFAULT 4)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,99)", 1))
				run(fixture.Exec("ALTER TABLE t "+actions, 0))
				run(fixture.Select("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
			})
		})
	}
	for _, tc := range []struct{ actions, code, message string }{
		{"ADD b INT, DROP b", "42703", `column "b" of relation "t" does not exist`},
		{"ADD b INT, ALTER b TYPE BIGINT", "42703", `column "b" of relation "t" does not exist`},
		{"ADD b INT, ALTER b SET NOT NULL", "23502", `column "b" of relation "t" contains null values`},
	} {
		t.Run(tc.actions, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
				if tc.code == "23502" {
					want.Schema, want.Table, want.Column = "public", "t", "b"
				}
				run(fixture.ImplicitError("ALTER TABLE t "+tc.actions, want))
				run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnAttributeIdentity(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT DEFAULT 3)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,2,3),(4,5,6)", 2))
		run(fixture.Exec("COMMENT ON COLUMN t.c IS 'old c'", 0))
		run(fixture.Exec("ALTER TABLE t DROP c, ADD d INT DEFAULT 4", 0))
		run(fixture.Select("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t'", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(4)}}))
		run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='t'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(4)}}))
		run(fixture.Select("SELECT col_description('t'::regclass,4)", []string{"col_description"}, []string{"TEXT"}, [][]any{{nil}}))
		run(fixture.Exec("ALTER TABLE t DROP a, DROP b, DROP d", 0))
		run(fixture.Exec("ALTER TABLE t ADD e INT DEFAULT 5", 0))
		run(fixture.Select("SELECT * FROM t", []string{"e"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(5)}}))
		run(fixture.Select("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t'", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(5)}}))
	})
}

func TestReferenceAlterAddColumnPreparedMetadata(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		utility, err := conn.PrepareContext(p.Ctx, "ALTER TABLE t ADD n INT DEFAULT 7")
		if err != nil {
			t.Fatal("utility preparation resolved missing table", err)
		}
		defer utility.Close()
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("CREATE VIEW frozen AS SELECT * FROM t", 0))
		run(fixture.Exec("CREATE INDEX key_idx ON t(id)", 0))
		selectQ, insertQ := "SELECT id FROM t ORDER BY id", "INSERT INTO t(id) VALUES($1)"
		survivor, err := conn.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer survivor.Close()
		insert, err := conn.PrepareContext(p.Ctx, insertQ)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, utility, "ALTER TABLE t ADD n INT DEFAULT 7"}, backend.Recorder, fixture.Exec("ALTER TABLE t ADD n INT DEFAULT 7", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, insert, insertQ}, backend.Recorder, fixture.Exec(insertQ, 1, 2))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, survivor, selectQ}, backend.Recorder, fixture.Select(selectQ, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT * FROM frozen ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}}))
		run(fixture.Exec("ALTER TABLE t RENAME n TO renamed", 0))
		run(fixture.Exec("ALTER TABLE t ALTER renamed TYPE BIGINT", 0))
		run(fixture.Select("SELECT renamed FROM t ORDER BY id", []string{"renamed"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(7)}}))
	})
}

func TestReferenceAlterAddColumnTransactions(t *testing.T) {
	for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
		t.Run(fmt.Sprint(isolation), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				for _, commit := range []bool{false, true} {
					tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: isolation})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					inside := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s) }
					inside(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					inside(fixture.Exec("UPDATE t SET id=3 WHERE id=1", 1))
					inside(fixture.Exec("DELETE FROM t WHERE id=2", 1))
					inside(fixture.Exec("INSERT INTO t VALUES(4)", 1))
					inside(fixture.Exec("SAVEPOINT before_add", 0))
					inside(fixture.Exec("ALTER TABLE t ADD transient INT DEFAULT 6", 0))
					inside(fixture.Exec("INSERT INTO t(id) VALUES(5)", 1))
					inside(fixture.Exec("ROLLBACK TO before_add", 0))
					inside(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
					inside(fixture.Exec("ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL CHECK(b>0)", 0))
					inside(fixture.Exec("ALTER TABLE t ADD c TEXT", 0))
					inside(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(7), nil}, {int64(4), int64(7), nil}}))
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if !commit {
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
				}
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(7), nil}, {int64(4), int64(7), nil}}))
			})
		})
	}
}

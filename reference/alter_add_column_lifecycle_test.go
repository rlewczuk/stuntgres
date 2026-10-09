package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterAddColumnMixedLists(t *testing.T) {
	for _, actions := range []string{"ADD b TEXT, DROP b", "DROP b, ADD b TEXT"} {
		t.Run(actions, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT,b INT DEFAULT 4)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,99)", 1))
				run(referenceExec("ALTER TABLE t "+actions, 0))
				run(referenceSelect("SELECT * FROM t", []string{"id", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
			})
		})
	}
	for _, tc := range []struct{ actions, code, message string }{
		{"ADD b INT, DROP b", "42703", `column "b" of relation "t" does not exist`},
		{"ADD b INT, ALTER b TYPE BIGINT", "42703", `column "b" of relation "t" does not exist`},
		{"ADD b INT, ALTER b SET NOT NULL", "23502", `column "b" of relation "t" contains null values`},
	} {
		t.Run(tc.actions, func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				want := referenceDiagnostic{Code: tc.code, Message: tc.message}
				if tc.code == "23502" {
					want.Schema, want.Table, want.Column = "public", "t", "b"
				}
				run(referenceImplicitError("ALTER TABLE t "+tc.actions, want))
				run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnAttributeIdentity(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT,b INT,c INT DEFAULT 3)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,2,3),(4,5,6)", 2))
		run(referenceExec("COMMENT ON COLUMN t.c IS 'old c'", 0))
		run(referenceExec("ALTER TABLE t DROP c, ADD d INT DEFAULT 4", 0))
		run(referenceSelect("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t'", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(4)}}))
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='t'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(4)}}))
		run(referenceSelect("SELECT col_description('t'::regclass,4)", []string{"col_description"}, []string{"TEXT"}, [][]any{{nil}}))
		run(referenceExec("ALTER TABLE t DROP a, DROP b, DROP d", 0))
		run(referenceExec("ALTER TABLE t ADD e INT DEFAULT 5", 0))
		run(referenceSelect("SELECT * FROM t", []string{"e"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(5)}}))
		run(referenceSelect("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t'", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(5)}}))
	})
}

func TestReferenceAlterAddColumnPreparedMetadata(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		utility, err := conn.PrepareContext(p.ctx, "ALTER TABLE t ADD n INT DEFAULT 7")
		if err != nil {
			t.Fatal("utility preparation resolved missing table", err)
		}
		defer utility.Close()
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		run(referenceExec("CREATE VIEW frozen AS SELECT * FROM t", 0))
		run(referenceExec("CREATE INDEX key_idx ON t(id)", 0))
		selectQ, insertQ := "SELECT id FROM t ORDER BY id", "INSERT INTO t(id) VALUES($1)"
		survivor, err := conn.PrepareContext(p.ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer survivor.Close()
		insert, err := conn.PrepareContext(p.ctx, insertQ)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		referenceRunStep(t, p.ctx, referenceHeldStatement{conn, utility, "ALTER TABLE t ADD n INT DEFAULT 7"}, backend.recorder, referenceExec("ALTER TABLE t ADD n INT DEFAULT 7", 0))
		referenceRunStep(t, p.ctx, referenceHeldStatement{conn, insert, insertQ}, backend.recorder, referenceExec(insertQ, 1, 2))
		referenceRunStep(t, p.ctx, referenceHeldStatement{conn, survivor, selectQ}, backend.recorder, referenceSelect(selectQ, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(referenceSelect("SELECT * FROM frozen ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}}))
		run(referenceExec("ALTER TABLE t RENAME n TO renamed", 0))
		run(referenceExec("ALTER TABLE t ALTER renamed TYPE BIGINT", 0))
		run(referenceSelect("SELECT renamed FROM t ORDER BY id", []string{"renamed"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(7)}}))
	})
}

func TestReferenceAlterAddColumnTransactions(t *testing.T) {
	for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
		t.Run(fmt.Sprint(isolation), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
				for _, commit := range []bool{false, true} {
					tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: isolation})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					inside := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, backend.recorder, s) }
					inside(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					inside(referenceExec("UPDATE t SET id=3 WHERE id=1", 1))
					inside(referenceExec("DELETE FROM t WHERE id=2", 1))
					inside(referenceExec("INSERT INTO t VALUES(4)", 1))
					inside(referenceExec("SAVEPOINT before_add", 0))
					inside(referenceExec("ALTER TABLE t ADD transient INT DEFAULT 6", 0))
					inside(referenceExec("INSERT INTO t(id) VALUES(5)", 1))
					inside(referenceExec("ROLLBACK TO before_add", 0))
					inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
					inside(referenceExec("ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL CHECK(b>0)", 0))
					inside(referenceExec("ALTER TABLE t ADD c TEXT", 0))
					inside(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(7), nil}, {int64(4), int64(7), nil}}))
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if !commit {
						run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
				}
				run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b", "c"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(7), nil}, {int64(4), int64(7), nil}}))
			})
		})
	}
}

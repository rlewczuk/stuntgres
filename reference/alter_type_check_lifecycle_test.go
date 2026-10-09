package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterTypeCheckPrepared(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(c INT CONSTRAINT ck CHECK(c>0))", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			insert, err := conn.PrepareContext(p.ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			selectOld, err := conn.PrepareContext(p.ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer selectOld.Close()
			eager, err := b.db.QueryContext(p.ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer eager.Close()
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
			if result, err := insert.ExecContext(p.ctx, int64(2)); err != nil {
				t.Fatal(err)
			} else if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			_, err = insert.ExecContext(p.ctx, int64(-1))
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "t", Constraint: "ck"}
			if got := referenceExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			rows, err := selectOld.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
			}
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
			if got := referenceExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			failure := referenceImplicitError("ALTER TABLE t ALTER c TYPE BIGINT USING -c", referenceDiagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
			failure.prepared, failure.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, conn, b.recorder, failure)
			metadata, err := eager.ColumnTypes()
			if err != nil || metadata[0].DatabaseTypeName() != "INT4" {
				t.Fatal(metadata, err)
			}
			if !eager.Next() {
				t.Fatal(eager.Err())
			}
			var old int64
			if err = eager.Scan(&old); err != nil || old != 1 {
				t.Fatal(old, err)
			}
			if eager.Next() || eager.Err() != nil {
				t.Fatal(eager.Err())
			}
			eager.Close()
			run(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferenceAlterTypeCheckTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,c INT CONSTRAINT ck CHECK(c>0))", 0))
					run(referenceExec("INSERT INTO t VALUES(1,1),(2,2),(3,3)", 3))
					tx, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
					tr(referenceExec("UPDATE t SET c=7 WHERE id=1", 1))
					tr(referenceExec("DELETE FROM t WHERE id=2", 1))
					tr(referenceExec("INSERT INTO t VALUES(4,4)", 1))
					tr(referenceExec("SAVEPOINT before_type", 0))
					action := "ALTER TABLE t ALTER c TYPE BIGINT USING CASE WHEN c=1 OR c=2 THEN 1/(c-c) ELSE c+1 END"
					tr(referenceExec(action, 0))
					want := [][]any{{int64(1), int64(8)}, {int64(3), int64(4)}, {int64(4), int64(5)}}
					tr(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					if !b.pg {
						var private bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &private); err != nil || strings.Contains(private.String(), "BIGINT") {
							t.Fatal("private CHECK/TYPE leaked to Dump", err, private.String())
						}
					}
					tr(referenceExec("SAVEPOINT after_type", 0))
					failure := referenceImplicitError("ALTER TABLE t ALTER c TYPE BIGINT USING -c", referenceDiagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
					failure.errorStage = "execute"
					tr(failure)
					tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					tr(referenceExec("ROLLBACK TO after_type", 0))
					tr(referenceExec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
					tr(referenceExec("ROLLBACK TO after_type", 0))
					tr(referenceExec("ROLLBACK TO after_type", 0))
					tr(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					tr(referenceExec("ROLLBACK TO before_type", 0))
					tr(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
					tr(referenceExec(action, 0))
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					} else {
						run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}))
					}
					failure = referenceImplicitError("INSERT INTO t VALUES(5,-1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (5, -1).", Schema: "public", Table: "t", Constraint: "ck"})
					failure.errorStage = "execute"
					run(failure)
				})
			}
		})
	}
}

func TestReferenceAlterTypeCheckControlsAndDefaultRemoval(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(p INT,c INT DEFAULT 7 CONSTRAINT ck CHECK(c>0) NO INHERIT,n INT DEFAULT 1 CONSTRAINT nn NOT NULL)", 0),
		referenceExec("INSERT INTO t VALUES(9,7,1)", 1),
		referenceExec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1,DROP p", 0),
		referenceSelect("SELECT * FROM t", []string{"c", "n"}, []string{"INT8", "INT4"}, [][]any{{int64(8), int64(1)}}),
	} {
		p.step(t, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"BIGINT", "ck", "NO INHERIT", "nn", "DEFAULT"} {
		if !strings.Contains(dump.String(), text) {
			t.Fatal(text, dump.String())
		}
	}
	if strings.Contains(dump.String(), "ALTER TABLE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	for _, step := range []referenceStep{
		referenceExec("INSERT INTO t DEFAULT VALUES", 1), referenceExec("INSERT INTO t(c) VALUES(NULL)", 1),
		referenceSelect("SELECT * FROM t ORDER BY c", []string{"c", "n"}, []string{"INT8", "INT4"}, [][]any{{int64(7), int64(1)}, {int64(8), int64(1)}, {nil, int64(1)}}),
		referenceImplicitError("INSERT INTO t(c) VALUES(-1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1, 1).", Schema: "public", Table: "t", Constraint: "ck"}),
		referenceExec("CREATE TABLE d(c TEXT DEFAULT '7' CONSTRAINT ck CHECK(c>'0'))", 0), referenceExec("INSERT INTO d VALUES('7')", 1), referenceExec("ALTER TABLE d ALTER c DROP DEFAULT", 0),
		referenceImplicitError("ALTER TABLE d ALTER c TYPE INT USING c::int", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer > text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}),
		referenceExec("ALTER TABLE d DROP CONSTRAINT ck,ALTER c TYPE INT USING c::int", 0), referenceSelect("SELECT c FROM d", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}),
	} {
		p.step(t, step)
	}
}

func TestReferenceAlterTypeCheckFloatingControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL))", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(2),(NULL)", 2))
			action := referenceExec("ALTER TABLE t ALTER c TYPE DOUBLE PRECISION USING c", 0)
			action.prepared = prepared
			p.step(t, action)
			failure := referenceImplicitError("INSERT INTO t VALUES(1.00000011)", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"})
			failure.errorStage = "execute"
			p.step(t, failure)
			p.step(t, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {nil}}))
			positive := referenceExec("INSERT INTO t VALUES(1.00000013)", 1)
			positive.prepared = prepared
			p.step(t, positive)
			p.step(t, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}}))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "$1") || strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("private TYPE history leaked", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			p.step(t, failure)
			p.step(t, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}}))
			p.step(t, positive)
			p.step(t, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {nil}}))
			p.step(t, referenceExec("INSERT INTO t VALUES(2.5),(NULL)", 2))
			p.step(t, referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {float64(2.5)}, {nil}, {nil}}))
		})
	}
}

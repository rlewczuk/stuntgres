package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeCheckPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c INT CONSTRAINT ck CHECK(c>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			insert, err := conn.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			selectOld, err := conn.PrepareContext(p.Ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer selectOld.Close()
			eager, err := b.DB.QueryContext(p.Ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer eager.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
			if result, err := insert.ExecContext(p.Ctx, int64(2)); err != nil {
				t.Fatal(err)
			} else if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			_, err = insert.ExecContext(p.Ctx, int64(-1))
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "t", Constraint: "ck"}
			if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			rows, err := selectOld.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
			}
			want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
			if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			failure := fixture.ImplicitError("ALTER TABLE t ALTER c TYPE BIGINT USING -c", fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
			failure.Prepared, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, failure)
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
			run(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferenceAlterTypeCheckTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT,c INT CONSTRAINT ck CHECK(c>0))", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,1),(2,2),(3,3)", 3))
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
					tr(fixture.Exec("UPDATE t SET c=7 WHERE id=1", 1))
					tr(fixture.Exec("DELETE FROM t WHERE id=2", 1))
					tr(fixture.Exec("INSERT INTO t VALUES(4,4)", 1))
					tr(fixture.Exec("SAVEPOINT before_type", 0))
					action := "ALTER TABLE t ALTER c TYPE BIGINT USING CASE WHEN c=1 OR c=2 THEN 1/(c-c) ELSE c+1 END"
					tr(fixture.Exec(action, 0))
					want := [][]any{{int64(1), int64(8)}, {int64(3), int64(4)}, {int64(4), int64(5)}}
					tr(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					if !b.PG {
						var private bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &private); err != nil || strings.Contains(private.String(), "BIGINT") {
							t.Fatal("private CHECK/TYPE leaked to Dump", err, private.String())
						}
					}
					tr(fixture.Exec("SAVEPOINT after_type", 0))
					failure := fixture.ImplicitError("ALTER TABLE t ALTER c TYPE BIGINT USING -c", fixture.Diagnostic{Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"})
					failure.ErrorStage = "execute"
					tr(failure)
					tr(fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					tr(fixture.Exec("ROLLBACK TO after_type", 0))
					tr(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
					tr(fixture.Exec("ROLLBACK TO after_type", 0))
					tr(fixture.Exec("ROLLBACK TO after_type", 0))
					tr(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					tr(fixture.Exec("ROLLBACK TO before_type", 0))
					tr(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
					tr(fixture.Exec(action, 0))
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT8"}, want))
					} else {
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}))
					}
					failure = fixture.ImplicitError("INSERT INTO t VALUES(5,-1)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (5, -1).", Schema: "public", Table: "t", Constraint: "ck"})
					failure.ErrorStage = "execute"
					run(failure)
				})
			}
		})
	}
}

func TestReferenceAlterTypeCheckControlsAndDefaultRemoval(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(p INT,c INT DEFAULT 7 CONSTRAINT ck CHECK(c>0) NO INHERIT,n INT DEFAULT 1 CONSTRAINT nn NOT NULL)", 0),
		fixture.Exec("INSERT INTO t VALUES(9,7,1)", 1),
		fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1,DROP p", 0),
		fixture.Select("SELECT * FROM t", []string{"c", "n"}, []string{"INT8", "INT4"}, [][]any{{int64(8), int64(1)}}),
	} {
		p.Step(t, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	for _, step := range []fixture.Step{
		fixture.Exec("INSERT INTO t DEFAULT VALUES", 1), fixture.Exec("INSERT INTO t(c) VALUES(NULL)", 1),
		fixture.Select("SELECT * FROM t ORDER BY c", []string{"c", "n"}, []string{"INT8", "INT4"}, [][]any{{int64(7), int64(1)}, {int64(8), int64(1)}, {nil, int64(1)}}),
		fixture.ImplicitError("INSERT INTO t(c) VALUES(-1)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (-1, 1).", Schema: "public", Table: "t", Constraint: "ck"}),
		fixture.Exec("CREATE TABLE d(c TEXT DEFAULT '7' CONSTRAINT ck CHECK(c>'0'))", 0), fixture.Exec("INSERT INTO d VALUES('7')", 1), fixture.Exec("ALTER TABLE d ALTER c DROP DEFAULT", 0),
		fixture.ImplicitError("ALTER TABLE d ALTER c TYPE INT USING c::int", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer > text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}),
		fixture.Exec("ALTER TABLE d DROP CONSTRAINT ck,ALTER c TYPE INT USING c::int", 0), fixture.Select("SELECT c FROM d", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}),
	} {
		p.Step(t, step)
	}
}

func TestReferenceAlterTypeCheckFloatingControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(c REAL CONSTRAINT ck CHECK(c>'1.0000001' AND c<>NULL))", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(2),(NULL)", 2))
			action := fixture.Exec("ALTER TABLE t ALTER c TYPE DOUBLE PRECISION USING c", 0)
			action.Prepared = prepared
			p.Step(t, action)
			failure := fixture.ImplicitError("INSERT INTO t VALUES(1.00000011)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "ck"`, Detail: "Failing row contains (1.00000011).", Schema: "public", Table: "t", Constraint: "ck"})
			failure.ErrorStage = "execute"
			p.Step(t, failure)
			p.Step(t, fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(2)}, {nil}}))
			positive := fixture.Exec("INSERT INTO t VALUES(1.00000013)", 1)
			positive.Prepared = prepared
			p.Step(t, positive)
			p.Step(t, fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}}))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "$1") || strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("private TYPE history leaked", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			p.Step(t, failure)
			p.Step(t, fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(2)}, {nil}}))
			p.Step(t, positive)
			p.Step(t, fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {nil}}))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(2.5),(NULL)", 2))
			p.Step(t, fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(1.00000013)}, {float64(1.00000013)}, {float64(2)}, {float64(2.5)}, {nil}, {nil}}))
		})
	}
}

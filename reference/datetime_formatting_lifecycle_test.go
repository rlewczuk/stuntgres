package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceDatetimeFormattingAtomicityCancellation(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_atomic(id integer,d date,input text)", 0))
		run(referenceExec("INSERT INTO dt_atomic VALUES(10,'2000-01-01','2000-02-29'),(20,'2000-01-01','2006-02-29')", 2))
		bad := *datetimeFormattingDiagnostic("22008", `date/time field value out of range: "2006-02-29"`, "", "")
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				"INSERT INTO dt_atomic VALUES(1,to_date('2000-02-29','YYYY-MM-DD'),'valid'),(2,to_date('2006-02-29','YYYY-MM-DD'),'bad')",
				"UPDATE dt_atomic SET d=to_date(input,'YYYY-MM-DD')",
				"DELETE FROM dt_atomic WHERE to_date(input,'YYYY-MM-DD')=DATE '2000-02-29'",
			} {
				s := referenceErrorDiagnostic(q, bad)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			run(referenceSelect("SELECT id,d::text AS d FROM dt_atomic ORDER BY id", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(10), "2000-01-01"}, {int64(20), "2000-01-01"}}))
			// Pinned PostgreSQL fails this ordered late-error SELECT at Query
			// with nil rows, just as the native eager cursor must.
			q := "SELECT id,to_date(input,'YYYY-MM-DD') AS d FROM dt_atomic ORDER BY id"
			var rows *sql.Rows
			var err error
			var stmt *sql.Stmt
			if prepared {
				stmt, err = conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.ctx)
			} else {
				rows, err = conn.QueryContext(p.ctx, q)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("late error published rows")
			}
			referenceAssertDiagnostic(t, err, &bad)
		}
		tx, err := conn.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT dt_recover", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT to_date('2006-02-29','YYYY-MM-DD')", bad))
		aborted := controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT to_char(TIMESTAMP '2000-01-01','YYYY')", aborted))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO dt_recover", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceSelect("SELECT to_char(TIMESTAMP '2000-01-01','YYYY') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2000"}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if b.pg {
			return
		} // Native context contracts, no race-prone pq precancel.
		q := "SELECT to_char(TIMESTAMP '2006-10-19','YYYY') AS result"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(p.ctx)
			want := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(p.ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			for _, executor := range []referenceExecutor{b.db, conn} {
				result, err := executor.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, want) {
					t.Fatalf("canceled Exec: %v %v", result, err)
				}
				rows, err := executor.QueryContext(ctx, q)
				if rows != nil || !errors.Is(err, want) {
					t.Fatalf("canceled Query: %v %v", rows, err)
				}
				prepared, err := executor.PrepareContext(ctx, q)
				if prepared != nil || !errors.Is(err, want) {
					t.Fatalf("canceled Prepare: %v %v", prepared, err)
				}
			}
			result, err := stmt.ExecContext(ctx)
			if result != nil || !errors.Is(err, want) {
				t.Fatalf("canceled held Exec: %v %v", result, err)
			}
			rows, err := stmt.QueryContext(ctx)
			if rows != nil || !errors.Is(err, want) {
				t.Fatalf("canceled held Query: %v %v", rows, err)
			}
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"result"}, []string{"TEXT"}, [][]any{{"2006"}}))
		}
	})
}

func TestReferenceDatetimeFormattingControls(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_control(id integer,ts timestamp,d date DEFAULT to_date('05 Dec 2000','DD Mon YYYY'))", 0))
		run(referenceExec("INSERT INTO dt_control(id,ts) VALUES(1,'2006-10-19'),(2,NULL)", 2))
		run(referenceExec("CREATE VIEW dt_control_view AS SELECT id,to_char(ts,'YYYY') AS year,to_char(d::timestamp,'YYYY-MM-DD') AS d FROM dt_control", 0))
		q := "SELECT id,year,d FROM dt_control_view ORDER BY id"
		want := [][]any{{int64(1), "2006", "2000-12-05"}, {int64(2), nil, "2000-12-05"}}
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, want)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		if b.pg {
			return
		} // Controls have no PostgreSQL API equivalent.
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := conn.QueryContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var retained [][]any
		for old.Next() {
			var id, year, d any
			if err := old.Scan(&id, &year, &d); err != nil {
				t.Fatal(err)
			}
			retained = append(retained, []any{id, year, d})
		}
		if err := old.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retained, want) {
			t.Fatal("retained rows", retained)
		}
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, want))
		run(referenceExec("INSERT INTO dt_control(id,ts) VALUES(3,'2007-01-01')", 1))
		run(referenceSelect("SELECT id,year,d FROM dt_control_view WHERE id=3", []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "2007", "2000-12-05"}}))
		var stable bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &stable); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(stable.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, append(want, []any{int64(3), "2007", "2000-12-05"})))
		// A later formatting parse failure must not publish even the valid
		// defaults, view and rows already built in Restore's staging catalog.
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		run(referenceExec("SET DateStyle='SQL,DMY'", 0))
		run(referenceExec("SET IntervalStyle='iso_8601'", 0))
		run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
		run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
		run(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		var beforeFailure bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &beforeFailure); err != nil {
			t.Fatal(err)
		}
		failureRows, err := conn.QueryContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer failureRows.Close()
		columns, err := failureRows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"id", "year", "d"}) {
			t.Fatal("preheld failure cursor columns", columns, err)
		}
		types, err := failureRows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, expected := range []string{"INT4", "TEXT", "TEXT"} {
			if types[i].DatabaseTypeName() != expected {
				t.Fatalf("preheld failure cursor type %d: %q", i, types[i].DatabaseTypeName())
			}
		}
		script := beforeFailure.String() + "\n" + strings.Join(datetimeFormattingReviewRestoreDDL, ";\n") + ";\n" + datetimeFormattingReviewRestoreBadInsert + ";\n"
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
		bad := datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
		referenceAssertDiagnostic(t, err, bad)
		var afterFailure bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &afterFailure); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeFailure.Bytes(), afterFailure.Bytes()) {
			t.Fatalf("failed Restore changed catalog/data\nbefore:\n%s\nafter:\n%s", beforeFailure.String(), afterFailure.String())
		}
		run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
		run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
		run(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		complete := append(want, []any{int64(3), "2007", "2000-12-05"})
		run(referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, complete))
		for _, relation := range []string{"dt_restore_stage", "dt_restore_stage_view"} {
			missing := controlsReferenceDiagnostic("42P01", `relation "`+relation+`" does not exist`)
			query := "SELECT * FROM " + relation
			missing.Position = referencePosition(len("SELECT * FROM ") + 1)
			s := referenceErrorDiagnostic(query, missing)
			s.selectRows = true
			run(s)
		}
		afterColumns, err := failureRows.Columns()
		if err != nil || !reflect.DeepEqual(afterColumns, columns) {
			t.Fatal("failed Restore changed preheld cursor columns", afterColumns, err)
		}
		afterTypes, err := failureRows.ColumnTypes()
		if err != nil || len(afterTypes) != len(types) {
			t.Fatal("failed Restore changed preheld cursor type count", err)
		}
		for i, before := range types {
			after := afterTypes[i]
			bn, bnk := before.Nullable()
			an, ank := after.Nullable()
			bl, blk := before.Length()
			al, alk := after.Length()
			bp, bs, bdk := before.DecimalSize()
			ap, as, adk := after.DecimalSize()
			if before.Name() != after.Name() || before.DatabaseTypeName() != after.DatabaseTypeName() || before.ScanType() != after.ScanType() || bn != an || bnk != ank || bl != al || blk != alk || bp != ap || bs != as || bdk != adk {
				t.Fatalf("failed Restore changed preheld cursor descriptor %d", i)
			}
		}
		var failureRetained [][]any
		for failureRows.Next() {
			var id, year, d any
			if err := failureRows.Scan(&id, &year, &d); err != nil {
				t.Fatal(err)
			}
			failureRetained = append(failureRetained, []any{id, year, d})
		}
		if err := failureRows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(failureRetained, complete) {
			t.Fatal("failed Restore changed retained rows", failureRetained)
		}
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, complete))
		run(referenceExec("INSERT INTO dt_control(id,ts) VALUES(4,'2008-01-01')", 1))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, append(complete, []any{int64(4), "2008", "2000-12-05"})))
	})
}

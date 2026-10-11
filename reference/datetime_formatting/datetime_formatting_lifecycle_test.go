package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeFormattingAtomicityCancellation(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_atomic(id integer,d date,input text)", 0))
		run(fixture.Exec("INSERT INTO dt_atomic VALUES(10,'2000-01-01','2000-02-29'),(20,'2000-01-01','2006-02-29')", 2))
		bad := *datetimeFormattingDiagnostic("22008", `date/time field value out of range: "2006-02-29"`, "", "")
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				"INSERT INTO dt_atomic VALUES(1,to_date('2000-02-29','YYYY-MM-DD'),'valid'),(2,to_date('2006-02-29','YYYY-MM-DD'),'bad')",
				"UPDATE dt_atomic SET d=to_date(input,'YYYY-MM-DD')",
				"DELETE FROM dt_atomic WHERE to_date(input,'YYYY-MM-DD')=DATE '2000-02-29'",
			} {
				s := fixture.ErrorDiagnostic(q, bad)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
			run(fixture.Select("SELECT id,d::text AS d FROM dt_atomic ORDER BY id", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(10), "2000-01-01"}, {int64(20), "2000-01-01"}}))
			// Pinned PostgreSQL fails this ordered late-error SELECT at Query
			// with nil rows, just as the native eager cursor must.
			q := "SELECT id,to_date(input,'YYYY-MM-DD') AS d FROM dt_atomic ORDER BY id"
			var rows *sql.Rows
			var err error
			var stmt *sql.Stmt
			if prepared {
				stmt, err = conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.Ctx)
			} else {
				rows, err = conn.QueryContext(p.Ctx, q)
			}
			if rows != nil {
				rows.Close()
				t.Fatal("late error published rows")
			}
			fixture.AssertDiagnostic(t, err, &bad)
		}
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT dt_recover", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT to_date('2006-02-29','YYYY-MM-DD')", bad))
		aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT to_char(TIMESTAMP '2000-01-01','YYYY')", aborted))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO dt_recover", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT to_char(TIMESTAMP '2000-01-01','YYYY') AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"2000"}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			return
		} // Native context contracts, no race-prone pq precancel.
		q := "SELECT to_char(TIMESTAMP '2006-10-19','YYYY') AS result"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(p.Ctx)
			want := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			for _, executor := range []fixture.Executor{b.DB, conn} {
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
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TEXT"}, [][]any{{"2006"}}))
		}
	})
}

func TestReferenceDatetimeFormattingControls(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_control(id integer,ts timestamp,d date DEFAULT to_date('05 Dec 2000','DD Mon YYYY'))", 0))
		run(fixture.Exec("INSERT INTO dt_control(id,ts) VALUES(1,'2006-10-19'),(2,NULL)", 2))
		run(fixture.Exec("CREATE VIEW dt_control_view AS SELECT id,to_char(ts,'YYYY') AS year,to_char(d::timestamp,'YYYY-MM-DD') AS d FROM dt_control", 0))
		q := "SELECT id,year,d FROM dt_control_view ORDER BY id"
		want := [][]any{{int64(1), "2006", "2000-12-05"}, {int64(2), nil, "2000-12-05"}}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, want)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		if b.PG {
			return
		} // Controls have no PostgreSQL API equivalent.
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := conn.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
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
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, want))
		run(fixture.Exec("INSERT INTO dt_control(id,ts) VALUES(3,'2007-01-01')", 1))
		run(fixture.Select("SELECT id,year,d FROM dt_control_view WHERE id=3", []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "2007", "2000-12-05"}}))
		var stable bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stable); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stable.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, append(want, []any{int64(3), "2007", "2000-12-05"})))
		// A later formatting parse failure must not publish even the valid
		// defaults, view and rows already built in Restore's staging catalog.
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		run(fixture.Exec("SET DateStyle='SQL,DMY'", 0))
		run(fixture.Exec("SET IntervalStyle='iso_8601'", 0))
		run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
		run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
		run(fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		var beforeFailure bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &beforeFailure); err != nil {
			t.Fatal(err)
		}
		failureRows, err := conn.QueryContext(p.Ctx, q)
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
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		bad := datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
		fixture.AssertDiagnostic(t, err, bad)
		var afterFailure bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &afterFailure); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeFailure.Bytes(), afterFailure.Bytes()) {
			t.Fatalf("failed Restore changed catalog/data\nbefore:\n%s\nafter:\n%s", beforeFailure.String(), afterFailure.String())
		}
		run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
		run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
		run(fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		complete := append(want, []any{int64(3), "2007", "2000-12-05"})
		run(fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, complete))
		for _, relation := range []string{"dt_restore_stage", "dt_restore_stage_view"} {
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+relation+`" does not exist`)
			query := "SELECT * FROM " + relation
			missing.Position = fixture.Position(len("SELECT * FROM ") + 1)
			s := fixture.ErrorDiagnostic(query, missing)
			s.SelectRows = true
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
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, complete))
		run(fixture.Exec("INSERT INTO dt_control(id,ts) VALUES(4,'2008-01-01')", 1))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"id", "year", "d"}, []string{"INT4", "TEXT", "TEXT"}, append(complete, []any{int64(4), "2008", "2000-12-05"})))
	})
}

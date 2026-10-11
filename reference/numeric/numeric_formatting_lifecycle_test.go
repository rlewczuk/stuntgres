package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func numericFormattingRetained(t *testing.T, rows *sql.Rows, columns []*sql.ColumnType, want [][]any) {
	t.Helper()
	after, err := rows.ColumnTypes()
	if err != nil || len(after) != len(columns) {
		t.Fatal("retained ColumnTypes", err)
	}
	for i, before := range columns {
		a := after[i]
		bl, blk := before.Length()
		al, alk := a.Length()
		bn, bnk := before.Nullable()
		an, ank := a.Nullable()
		bp, bs, bdk := before.DecimalSize()
		ap, as, adk := a.DecimalSize()
		if before.Name() != a.Name() || before.DatabaseTypeName() != a.DatabaseTypeName() || before.ScanType() != a.ScanType() || bl != al || blk != alk || bn != an || bnk != ank || bp != ap || bs != as || bdk != adk {
			t.Fatalf("retained descriptor %d changed", i)
		}
	}
	var got [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			row[i] = fixture.Normalize(v)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rows got %#v want %#v", got, want)
	}
}

func numericFormattingControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE nf_saved(id integer,i integer,j bigint,n numeric,r real,f double precision,parsed numeric DEFAULT to_number('12.50','99.99'))", 0))
	run(fixture.Exec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(1,12,9007199254740993,12.50,12.5,12.5),(2,NULL,NULL,NULL,NULL,NULL)", 2))
	run(fixture.Exec("CREATE VIEW nf_saved_view AS SELECT id,to_char(i,'FM99') AS i,to_char(j,'FM9999999999999999') AS j,to_char(n,'FM99.00') AS n,to_char(r,'FM99.0') AS r,to_char(f,'FM99.0') AS f,parsed FROM nf_saved", 0))
	q := "SELECT id,i,j,n,r,f,parsed FROM nf_saved_view WHERE id>=$1::integer ORDER BY id"
	names := []string{"id", "i", "j", "n", "r", "f", "parsed"}
	types := []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "NUMERIC"}
	want := [][]any{{int64(1), "12", "9007199254740993", "12.50", "12.5", "12.5", "12.50"}, {int64(2), nil, nil, nil, nil, nil, "12.50"}}
	numericFormattingBoth(t, p, b, c, fixture.Select(q, names, types, want, int64(1)))
	if b.PG {
		return
	} // No fabricated PostgreSQL control API.
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	old, err := c.QueryContext(p.Ctx, q, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldTypes, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, col := range oldTypes {
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
			t.Fatalf("old descriptor %d: %s/%s", i, col.Name(), col.DatabaseTypeName())
		}
	}
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
	numericFormattingRetained(t, old, oldTypes, want)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, names, types, want, int64(1)))
	// Reevaluate all restored source overloads, stored view and default, not
	// merely old materialized values. Change descriptor generations again.
	run(fixture.Exec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(3,13,9007199254740993,13.50,13.5,13.5)", 1))
	third := []any{int64(3), "13", "9007199254740993", "13.50", "13.5", "13.5", "12.50"}
	complete := append(want, third)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, names, types, [][]any{third}, int64(3)))
	var stable bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &stable); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stable.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, names, types, complete, int64(1)))
	retained, err := c.QueryContext(p.Ctx, q, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	retainedTypes, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	// Restore's scalar importer deliberately does not admit function-valued
	// INSERT cells. An omitted column instead reaches the stored provider
	// default during staged execution, before any catalog/data publication.
	bad := stable.String() + "\nCREATE TABLE nf_restore_stage(id integer,n numeric DEFAULT to_number('IL','RN')); INSERT INTO nf_restore_stage(id) VALUES(1);"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	d := numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", "")
	fixture.AssertDiagnostic(t, err, &d)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stable.Bytes(), after.Bytes()) {
		t.Fatalf("failed Restore changed full catalog/data\nbefore:\n%s\nafter:\n%s", stable.String(), after.String())
	}
	numericFormattingRetained(t, retained, retainedTypes, complete)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, names, types, complete, int64(1)))
	missing := numericFormattingDiagnostic("42P01", `relation "nf_restore_stage" does not exist`, "", "", "15")
	s := fixture.ErrorDiagnostic("SELECT * FROM nf_restore_stage", missing)
	s.SelectRows = true
	run(s)
	run(fixture.Exec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(4,14,9007199254740993,14.50,14.5,14.5)", 1))
	fourth := []any{int64(4), "14", "9007199254740993", "14.50", "14.5", "14.5", "12.50"}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, names, types, [][]any{fourth}, int64(4)))
	if p.Postgres != nil {
		// Replay the actual emitted native SQL on the pinned backend. A
		// handwritten parallel DDL script would not prove Dump correctness.
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("emitted SQL replay: %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(q, names, types, want, int64(1)))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(3,13,9007199254740993,13.50,13.5,13.5)", 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(q, names, types, [][]any{third}, int64(3)))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceNumericFormattingControls(t *testing.T) {
	numericFormattingScenario(t, numericFormattingControls)
}

func TestReferenceNumericFormattingCancellation(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT to_char($1::numeric,'FM99') AS rendered,to_number($2::text,'99') AS parsed"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		valid := fixture.Select(q, []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{"12", "13"}}, "12", "13")
		numericFormattingBoth(t, p, b, c, valid)
		if b.PG {
			return
		} // No race-prone pq pre-cancellation guarantee.
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("expired=%t", expired), func(t *testing.T) {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				want := error(context.Canceled)
				if expired {
					ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				}
				defer cancel()
				for _, executor := range []fixture.Executor{b.DB, c} {
					result, err := executor.ExecContext(ctx, q, "12", "13")
					if result != nil || !errors.Is(err, want) {
						t.Fatalf("canceled Exec result=%v err=%v", result, err)
					}
					rows, err := executor.QueryContext(ctx, q, "12", "13")
					if rows != nil || !errors.Is(err, want) {
						if rows != nil {
							rows.Close()
						}
						t.Fatalf("canceled Query rows=%v err=%v", rows, err)
					}
					prepared, err := executor.PrepareContext(ctx, q)
					if prepared != nil || !errors.Is(err, want) {
						if prepared != nil {
							prepared.Close()
						}
						t.Fatalf("canceled Prepare stmt=%v err=%v", prepared, err)
					}
				}
				result, err := stmt.ExecContext(ctx, "12", "13")
				if result != nil || !errors.Is(err, want) {
					t.Fatalf("held canceled Exec %v %v", result, err)
				}
				rows, err := stmt.QueryContext(ctx, "12", "13")
				if rows != nil || !errors.Is(err, want) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("held canceled Query %v %v", rows, err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, valid)
			})
		}
	})
}

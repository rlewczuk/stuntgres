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
			row[i] = referenceNormalize(v)
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

func numericFormattingControls(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE nf_saved(id integer,i integer,j bigint,n numeric,r real,f double precision,parsed numeric DEFAULT to_number('12.50','99.99'))", 0))
	run(referenceExec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(1,12,9007199254740993,12.50,12.5,12.5),(2,NULL,NULL,NULL,NULL,NULL)", 2))
	run(referenceExec("CREATE VIEW nf_saved_view AS SELECT id,to_char(i,'FM99') AS i,to_char(j,'FM9999999999999999') AS j,to_char(n,'FM99.00') AS n,to_char(r,'FM99.0') AS r,to_char(f,'FM99.0') AS f,parsed FROM nf_saved", 0))
	q := "SELECT id,i,j,n,r,f,parsed FROM nf_saved_view WHERE id>=$1::integer ORDER BY id"
	names := []string{"id", "i", "j", "n", "r", "f", "parsed"}
	types := []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "NUMERIC"}
	want := [][]any{{int64(1), "12", "9007199254740993", "12.50", "12.5", "12.5", "12.50"}, {int64(2), nil, nil, nil, nil, nil, "12.50"}}
	numericFormattingBoth(t, p, b, c, referenceSelect(q, names, types, want, int64(1)))
	if b.pg {
		return
	} // No fabricated PostgreSQL control API.
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	old, err := c.QueryContext(p.ctx, q, int64(1))
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
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	numericFormattingRetained(t, old, oldTypes, want)
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, names, types, want, int64(1)))
	// Reevaluate all restored source overloads, stored view and default, not
	// merely old materialized values. Change descriptor generations again.
	run(referenceExec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(3,13,9007199254740993,13.50,13.5,13.5)", 1))
	third := []any{int64(3), "13", "9007199254740993", "13.50", "13.5", "13.5", "12.50"}
	complete := append(want, third)
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, names, types, [][]any{third}, int64(3)))
	var stable bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &stable); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(stable.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, names, types, complete, int64(1)))
	retained, err := c.QueryContext(p.ctx, q, int64(1))
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
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
	d := numericFormattingDiagnostic("22P02", "invalid Roman numeral", "", "", "")
	referenceAssertDiagnostic(t, err, &d)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stable.Bytes(), after.Bytes()) {
		t.Fatalf("failed Restore changed full catalog/data\nbefore:\n%s\nafter:\n%s", stable.String(), after.String())
	}
	numericFormattingRetained(t, retained, retainedTypes, complete)
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, names, types, complete, int64(1)))
	missing := numericFormattingDiagnostic("42P01", `relation "nf_restore_stage" does not exist`, "", "", "15")
	s := referenceErrorDiagnostic("SELECT * FROM nf_restore_stage", missing)
	s.selectRows = true
	run(s)
	run(referenceExec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(4,14,9007199254740993,14.50,14.5,14.5)", 1))
	fourth := []any{int64(4), "14", "9007199254740993", "14.50", "14.5", "14.5", "12.50"}
	referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, names, types, [][]any{fourth}, int64(4)))
	if p.postgres != nil {
		// Replay the actual emitted native SQL on the pinned backend. A
		// handwritten parallel DDL script would not prove Dump correctness.
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("emitted SQL replay: %v\n%s", err, dump.String())
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, names, types, want, int64(1)))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO nf_saved(id,i,j,n,r,f) VALUES(3,13,9007199254740993,13.50,13.5,13.5)", 1))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, names, types, [][]any{third}, int64(3)))
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceNumericFormattingControls(t *testing.T) {
	numericFormattingScenario(t, numericFormattingControls)
}

func TestReferenceNumericFormattingCancellation(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT to_char($1::numeric,'FM99') AS rendered,to_number($2::text,'99') AS parsed"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		valid := referenceSelect(q, []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{"12", "13"}}, "12", "13")
		numericFormattingBoth(t, p, b, c, valid)
		if b.pg {
			return
		} // No race-prone pq pre-cancellation guarantee.
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("expired=%t", expired), func(t *testing.T) {
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				want := error(context.Canceled)
				if expired {
					ctx, cancel = context.WithDeadline(p.ctx, time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				}
				defer cancel()
				for _, executor := range []referenceExecutor{b.db, c} {
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
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, valid)
			})
		}
	})
}

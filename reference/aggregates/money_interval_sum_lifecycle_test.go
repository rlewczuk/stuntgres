package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumLifecycle(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mis_lifecycle(id INTEGER,m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_lifecycle VALUES (1,'1','1 mon'),(2,'2','30 days')", 2))
		q := "SELECT sum(m ORDER BY id) AS m,sum(iv ORDER BY id) AS iv FROM mis_lifecycle"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		check := func(rows [][]any) { fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumPair(q, rows)) }
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// Mutate on a separate physical owner; never send a second pq command
		// while its original cursor is still open.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO mis_lifecycle VALUES (3,'4','1 day')", 1))
		if !b.PG {
			run(fixture.Exec("SET IntervalStyle='iso_8601'", 0))
		}
		moneyIntervalSumReadPair(t, old, []any{"$3.00", "1 mon 30 days"})
		if !b.PG {
			run(fixture.Exec("SET IntervalStyle='postgres'", 0))
		}
		check([][]any{{"$7.00", "1 mon 31 days"}})
		run(fixture.Exec("SET IntervalStyle='iso_8601'", 0))
		check([][]any{{"$7.00", "P1M31D"}})
		run(fixture.Exec("SET IntervalStyle='postgres'", 0))
		run(fixture.Exec("DELETE FROM mis_lifecycle", 3))
		check([][]any{{nil, nil}})
		run(fixture.Exec("INSERT INTO mis_lifecycle VALUES (1,NULL,NULL)", 1))
		check([][]any{{nil, nil}})
		run(fixture.Exec("DELETE FROM mis_lifecycle", 1))
		run(fixture.Exec("INSERT INTO mis_lifecycle VALUES (1,'92233720368547758.07','1 day'),(2,'0.01','1 day')", 2))
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "money out of range"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("DELETE FROM mis_lifecycle", 2))
		run(fixture.Exec("INSERT INTO mis_lifecycle VALUES (1,'1','1 day')", 1))
		check([][]any{{"$1.00", "1 day"}})
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = conn.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
			check([][]any{{"$1.00", "1 day"}})
		}
	})
}

func moneyIntervalSumReadPair(t *testing.T, rows *sql.Rows, want []any) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"m", "iv"}) {
		t.Fatalf("columns %v", columns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].DatabaseTypeName() != "MONEY" || types[1].DatabaseTypeName() != "INTERVAL" {
		t.Fatalf("types %v", types)
	}
	if !rows.Next() {
		t.Fatal("missing sum row", rows.Err())
	}
	var m, iv any
	if err := rows.Scan(&m, &iv); err != nil {
		t.Fatal(err)
	}
	got := []any{fixture.Normalize(m), fixture.Normalize(iv)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("row %#v want %#v", got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra row or error", rows.Err())
	}
}

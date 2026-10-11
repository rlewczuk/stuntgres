package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericAvgControls(t *testing.T) {
	numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE stored(g INTEGER,n NUMERIC,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO stored VALUES (1,1.0000000000000000000000000,1),(1,3,3),(1,NULL,NULL),(2,'Infinity',1),(2,NULL,NULL),(3,'NaN',NULL)", 6))
		run(fixture.Exec("CREATE VIEW avg_view AS SELECT g,avg(n) AS n,avg(d) AS d FROM stored GROUP BY g", 0))
		q := "SELECT g,n,d FROM avg_view ORDER BY g"
		want := fixture.Select(q, []string{"g", "n", "d"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "2.0000000000000000000000000", float64(2)}, {int64(2), "Infinity", float64(1)}, {int64(3), "NaN", nil}})
		numericAvgBoth(t, p, conn, recorder, want)
		stored := fixture.Select("SELECT g,n,d FROM stored ORDER BY g,n NULLS LAST", []string{"g", "n", "d"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "1.0000000000000000000000000", float64(1)}, {int64(1), "3", float64(3)}, {int64(1), nil, nil}, {int64(2), "Infinity", float64(1)}, {int64(2), nil, nil}, {int64(3), "NaN", nil}})
		numericAvgBoth(t, p, conn, recorder, stored)
		run(fixture.Exec("INSERT INTO stored VALUES (1,8,8)", 1))
		changed := fixture.Select(q, want.Columns, want.Types, [][]any{{int64(1), "4.0000000000000000000000000", float64(4)}, {int64(2), "Infinity", float64(1)}, {int64(3), "NaN", nil}})
		numericAvgBoth(t, p, conn, recorder, changed)
		run(fixture.Exec("DELETE FROM stored WHERE d=8", 1))
		numericAvgBoth(t, p, conn, recorder, want)
		if pg {
			return
		}

		// Native-only controls operate on the same engine via the unwrapped pool.
		// No streaming/cancellation claim is made for lib/pq's cursor.
		held, err := conn.QueryContext(p.Ctx, "SELECT avg(n) AS n,avg(d) AS d FROM stored WHERE g=1")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		columns, err := held.Columns()
		if err != nil {
			t.Fatal(err)
		}
		metadata := numericAvgMetadata(false, []string{"NUMERIC", "FLOAT8"})
		metadata(t, held)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("DELETE FROM stored", 6))
		numericAvgBoth(t, p, conn, recorder, fixture.Select(q, want.Columns, want.Types, nil))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		numericAvgBoth(t, p, conn, recorder, stored)
		numericAvgBoth(t, p, conn, recorder, want)
		metadata(t, held)
		if got, err := held.Columns(); err != nil || !reflect.DeepEqual(got, columns) {
			t.Fatalf("retained columns: %v/%v", got, err)
		}
		if !held.Next() {
			t.Fatalf("retained row: %v", held.Err())
		}
		var n, d any
		if err := held.Scan(&n, &d); err != nil {
			t.Fatal(err)
		}
		if n != "2.0000000000000000000000000" || d != float64(2) {
			t.Errorf("retained mean: %v/%v", n, d)
		}
		if held.Next() || held.Err() != nil {
			t.Fatalf("retained completion: %v", held.Err())
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}

		run(fixture.Exec("CREATE TABLE sentinel(n NUMERIC,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES (99,9)", 1))
		for _, prepared := range []bool{false, true} {
			selectQuery := "SELECT avg(n),avg(d) FROM stored"
			insertQuery := "INSERT INTO sentinel SELECT avg(n),avg(d) FROM stored"
			var selectStmt, insertStmt *sql.Stmt
			if prepared {
				selectStmt, err = conn.PrepareContext(p.Ctx, selectQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer selectStmt.Close()
				insertStmt, err = conn.PrepareContext(p.Ctx, insertQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer insertStmt.Close()
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			var rows *sql.Rows
			if prepared {
				rows, err = selectStmt.QueryContext(canceled)
			} else {
				rows, err = conn.QueryContext(canceled, selectQuery)
			}
			if rows != nil {
				rows.Close()
				t.Error("canceled AVG published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled Query: %v", err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Errorf("ordinary cancellation gained SQLSTATE: %v", err)
			}
			var result sql.Result
			if prepared {
				result, err = insertStmt.ExecContext(canceled)
			} else {
				result, err = conn.ExecContext(canceled, insertQuery)
			}
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled INSERT: %v/%v", result, err)
			}
			run(fixture.Select("SELECT n,d FROM sentinel", []string{"n", "d"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"99", float64(9)}}))
		}
		numericAvgBoth(t, p, conn, recorder, want)
	})
}

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

func TestReferenceQuerySubqueryLifecycle(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryScopeFixture(run)
		q := "SELECT (SELECT v FROM inner_t WHERE k=$1 AND v=10) AS scalar"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ arg, value any }{{int64(1), int64(10)}, {int64(2), nil}, {nil, nil}, {int64(1), int64(10)}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"scalar"}, []string{"INT4"}, [][]any{{tc.value}}, tc.arg))
		}
		run(fixture.Exec("INSERT INTO inner_t VALUES(1,10)", 1))
		subqueryQueryFailure(t, p, b, held, q, subqueryCardinality(), int64(1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"scalar"}, []string{"INT4"}, [][]any{{nil}}, int64(2)))
		// Existing aggregate provider sees the same captured child frontier.
		countQ := "SELECT o.id,(SELECT count(*) FROM inner_t i WHERE i.k=o.id) AS n FROM outer_t o ORDER BY o.id"
		tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		before := fixture.Select(countQ, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(1)}, {int64(3), int64(0)}, {int64(3), int64(0)}})
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, before)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inner_t VALUES(3,30)", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(before))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		subqueryBoth(run, fixture.Select(countQ, []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(1)}, {int64(3), int64(1)}, {int64(3), int64(1)}}))
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared, true)
				run(fixture.Select("SELECT (SELECT 7) AS scalar", []string{"scalar"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			return
		}
		for _, prepared := range []bool{false, true} {
			cancelled, cancel := context.WithCancel(p.Ctx)
			cancel()
			var x fixture.Executor = c
			if prepared {
				s, err := c.PrepareContext(p.Ctx, "SELECT (SELECT 7)")
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				x = fixture.HeldStatement{Executor: c, Statement: s, SQL: "SELECT (SELECT 7)"}
			}
			rows, err := x.QueryContext(cancelled, "SELECT (SELECT 7)")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("precancel publication", rows, err)
			}
			result, err := x.ExecContext(cancelled, "SELECT (SELECT 7)")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("precancel result", result, err)
			}
		}
		// Published native output is detached from append, Reset and Restore.
		rows, err := c.QueryContext(p.Ctx, "SELECT d.id,(SELECT 7) AS scalar FROM (SELECT id FROM outer_t) d ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(columns, []string{"id", "scalar"}) {
			t.Fatal(columns)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO outer_t VALUES(9)", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, want := range []int64{1, 2, 3, 3} {
			if !rows.Next() {
				t.Fatal("retained row missing", rows.Err())
			}
			var id, value int64
			if err := rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			if id != want || value != 7 {
				t.Fatal(id, value, want)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained suffix", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		subqueryBoth(run, fixture.Select("SELECT id FROM (SELECT id FROM outer_t) d ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(3)}}))
	})
}

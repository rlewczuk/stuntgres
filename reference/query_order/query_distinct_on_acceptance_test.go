package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func queryDistinctOnAcceptance(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	queryDistinctOnEvents(run)
	full := fixture.Select("SELECT DISTINCT ON(k) id,k,stamp,payload FROM events ORDER BY k ASC NULLS LAST,stamp DESC,id DESC", []string{"id", "k", "stamp", "payload"}, []string{"INT4", "INT4", "TIMESTAMP", "TEXT"}, [][]any{{int64(3), int64(1), time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), "new1b"}, {int64(5), int64(2), time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), "new2"}, {int64(7), nil, time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC), "newnull"}})
	fixture.QueryDistinctBoth(run, full)
	queryDistinctOnHeld(t, p, b, c, full)
	fixture.QueryDistinctBoth(run, queryDistinctOnHidden())
	for _, prepared := range []bool{false, true} {
		s := queryDistinctOnHidden()
		s.NonContext, s.Prepared = true, prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	s := queryDistinctOnHidden()
	s.Query += " LIMIT 1 OFFSET 1"
	s.Rows = [][]any{{int64(5), "new2"}}
	fixture.QueryDistinctBoth(run, s)
	s = queryDistinctOnHidden()
	s.Query = "SELECT DISTINCT ON(k) id,payload FROM events WHERE k IS NULL ORDER BY k,stamp DESC,id DESC"
	s.Rows = [][]any{{int64(7), "newnull"}}
	fixture.QueryDistinctBoth(run, s)
	q := "SELECT DISTINCT ON(k) id,payload FROM events WHERE id>=$1 ORDER BY k ASC NULLS LAST,stamp DESC,id DESC"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		arg  any
		rows [][]any
	}{
		{1, queryDistinctOnHidden().Rows}, {4, [][]any{{int64(5), "new2"}, {int64(7), "newnull"}}}, {8, [][]any{}}, {nil, [][]any{}}, {1, queryDistinctOnHidden().Rows},
	} {
		s := fixture.Select(q, []string{"id", "payload"}, []string{"INT4", "TEXT"}, tc.rows, tc.arg)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
	}
	for _, args := range [][]any{{}, {struct{}{}}} {
		rows, err := stmt.QueryContext(p.Ctx, args...)
		if rows != nil {
			rows.Close()
			t.Fatal("argument failure exposed rows")
		}
		if err == nil {
			t.Fatal("invalid arguments succeeded")
		}
	}
	// Changing a held ORDER parameter changes the winner, not the grouping type.
	q = "SELECT DISTINCT ON(k) id FROM events WHERE k=1 ORDER BY k,id*$1"
	order, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer order.Close()
	for _, tc := range []struct {
		arg int
		id  int64
	}{{1, 1}, {-1, 3}, {1, 1}} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, order, q}, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{tc.id}}, tc.arg))
	}
	run(fixture.Exec("INSERT INTO events VALUES(8,1,'2025-01-05','fresh')", 1))
	s = fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}, -1)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, order, q}, b.Recorder, s)
}

func TestReferenceQueryDistinctOnAcceptance(t *testing.T) {
	queryDistinctOnScenario(t, queryDistinctOnAcceptance)
}

func TestReferenceQueryDistinctOnMetadata(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE typed(id INT NOT NULL,k INT,s VARCHAR(5),n NUMERIC(7,2))", 0))
		run(fixture.Exec("INSERT INTO typed VALUES(1,1,'hello',12.50),(2,1,'world',13.50)", 2))
		for _, empty := range []bool{false, true} {
			q := "SELECT DISTINCT ON(k) id AS shown,k,id+1 AS computed,s AS label,n AS price FROM typed"
			want := [][]any{{int64(2), int64(1), int64(3), "world", "13.50"}}
			if empty {
				q += " WHERE FALSE"
				want = [][]any{}
			}
			q += " ORDER BY k,id DESC"
			s := fixture.Select(q, []string{"shown", "k", "computed", "label", "price"}, []string{"INT4", "INT4", "INT4", "VARCHAR", "NUMERIC"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				m, err := rows.ColumnTypes()
				if err != nil || len(m) != 5 {
					t.Fatal(m, err)
				}
				for i, col := range m {
					n, ok := col.Nullable()
					wn, wk := i != 0 && i != 2, i != 2
					if b.PG {
						wn, wk = false, false
					}
					if n != wn || ok != wk {
						t.Fatal("nullable", i, n, ok, wn, wk)
					}
				}
				if n, ok := m[3].Length(); !ok || n != 5 {
					t.Fatal("length", n, ok)
				}
				if n, scale, ok := m[4].DecimalSize(); !ok || n != 7 || scale != 2 {
					t.Fatal("numeric", n, scale, ok)
				}
				if !b.PG {
					cols, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					cols[0] = "mutated"
					again, err := rows.Columns()
					if err != nil || again[0] != "shown" {
						t.Fatal("detached columns", again, err)
					}
				}
			}
			fixture.QueryDistinctBoth(run, s)
			queryDistinctOnHeld(t, p, b, c, s)
		}
	})
}

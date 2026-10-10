package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func queryDistinctOnAcceptance(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	queryDistinctOnEvents(run)
	full := referenceSelect("SELECT DISTINCT ON(k) id,k,stamp,payload FROM events ORDER BY k ASC NULLS LAST,stamp DESC,id DESC", []string{"id", "k", "stamp", "payload"}, []string{"INT4", "INT4", "TIMESTAMP", "TEXT"}, [][]any{{int64(3), int64(1), time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), "new1b"}, {int64(5), int64(2), time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), "new2"}, {int64(7), nil, time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC), "newnull"}})
	queryDistinctBoth(run, full)
	queryDistinctOnHeld(t, p, b, c, full)
	queryDistinctBoth(run, queryDistinctOnHidden())
	for _, prepared := range []bool{false, true} {
		s := queryDistinctOnHidden()
		s.nonContext, s.prepared = true, prepared
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
	s := queryDistinctOnHidden()
	s.query += " LIMIT 1 OFFSET 1"
	s.rows = [][]any{{int64(5), "new2"}}
	queryDistinctBoth(run, s)
	s = queryDistinctOnHidden()
	s.query = "SELECT DISTINCT ON(k) id,payload FROM events WHERE k IS NULL ORDER BY k,stamp DESC,id DESC"
	s.rows = [][]any{{int64(7), "newnull"}}
	queryDistinctBoth(run, s)
	q := "SELECT DISTINCT ON(k) id,payload FROM events WHERE id>=$1 ORDER BY k ASC NULLS LAST,stamp DESC,id DESC"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		arg  any
		rows [][]any
	}{
		{1, queryDistinctOnHidden().rows}, {4, [][]any{{int64(5), "new2"}, {int64(7), "newnull"}}}, {8, [][]any{}}, {nil, [][]any{}}, {1, queryDistinctOnHidden().rows},
	} {
		s := referenceSelect(q, []string{"id", "payload"}, []string{"INT4", "TEXT"}, tc.rows, tc.arg)
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, s)
	}
	for _, args := range [][]any{{}, {struct{}{}}} {
		rows, err := stmt.QueryContext(p.ctx, args...)
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
	order, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer order.Close()
	for _, tc := range []struct {
		arg int
		id  int64
	}{{1, 1}, {-1, 3}, {1, 1}} {
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, order, q}, b.recorder, referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{{tc.id}}, tc.arg))
	}
	run(referenceExec("INSERT INTO events VALUES(8,1,'2025-01-05','fresh')", 1))
	s = referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}, -1)
	referenceRunStep(t, p.ctx, referenceHeldStatement{c, order, q}, b.recorder, s)
}

func TestReferenceQueryDistinctOnAcceptance(t *testing.T) {
	queryDistinctOnScenario(t, queryDistinctOnAcceptance)
}

func TestReferenceQueryDistinctOnMetadata(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE typed(id INT NOT NULL,k INT,s VARCHAR(5),n NUMERIC(7,2))", 0))
		run(referenceExec("INSERT INTO typed VALUES(1,1,'hello',12.50),(2,1,'world',13.50)", 2))
		for _, empty := range []bool{false, true} {
			q := "SELECT DISTINCT ON(k) id AS shown,k,id+1 AS computed,s AS label,n AS price FROM typed"
			want := [][]any{{int64(2), int64(1), int64(3), "world", "13.50"}}
			if empty {
				q += " WHERE FALSE"
				want = [][]any{}
			}
			q += " ORDER BY k,id DESC"
			s := referenceSelect(q, []string{"shown", "k", "computed", "label", "price"}, []string{"INT4", "INT4", "INT4", "VARCHAR", "NUMERIC"}, want)
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				m, err := rows.ColumnTypes()
				if err != nil || len(m) != 5 {
					t.Fatal(m, err)
				}
				for i, col := range m {
					n, ok := col.Nullable()
					wn, wk := i != 0 && i != 2, i != 2
					if b.pg {
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
				if !b.pg {
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
			queryDistinctBoth(run, s)
			queryDistinctOnHeld(t, p, b, c, s)
		}
	})
}

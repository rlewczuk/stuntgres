package reference_test

import (
	"database/sql"
	"testing"
)

func queryDistinctHeldFrames(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	q := "SELECT DISTINCT a+$1 AS sum FROM t WHERE id<=$2 ORDER BY sum"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		args []any
		want [][]any
	}{
		{[]any{10, 3}, [][]any{{int64(11)}}}, {[]any{20, 3}, [][]any{{int64(21)}}},
		{[]any{nil, 3}, [][]any{{nil}}}, {[]any{10, 0}, [][]any{}}, {[]any{10, 3}, [][]any{{int64(11)}}},
	} {
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, q}, b.recorder, referenceSelect(q, []string{"sum"}, []string{"INT4"}, tc.want, tc.args...))
	}
	for _, args := range [][]any{{10}, {struct{}{}, 3}} {
		rows, err := stmt.QueryContext(p.ctx, args...)
		if rows != nil {
			rows.Close()
			t.Fatal("argument failure exposed rows")
		}
		if err == nil {
			t.Fatal("invalid arguments succeeded")
		}
	}
}

func TestReferenceQueryDistinctMetadata(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctSetup(t, run)
		queryDistinctHeldFrames(t, p, b, c)
		for _, empty := range []bool{false, true} {
			q := "SELECT DISTINCT id AS shown,a,id+1 AS computed FROM t ORDER BY shown"
			want := [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(1), int64(3)}, {int64(3), int64(1), int64(4)}, {int64(4), nil, int64(5)}, {int64(5), nil, int64(6)}, {int64(6), nil, int64(7)}, {int64(7), nil, int64(8)}}
			if empty {
				q = "SELECT DISTINCT id AS shown,a,id+1 AS computed FROM t WHERE FALSE"
				want = [][]any{}
			}
			s := referenceSelect(q, []string{"shown", "a", "computed"}, []string{"INT4", "INT4", "INT4"}, want)
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 3 {
					t.Fatal(meta, err)
				}
				for i, col := range meta {
					nullable, known := col.Nullable()
					wn, wk := i == 1, i < 2
					if b.pg {
						wn, wk = false, false
					}
					if nullable != wn || known != wk {
						t.Fatal(i, nullable, known, wn, wk)
					}
				}
			}
			queryDistinctBoth(run, s)
		}
		run(referenceExec("CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))", 0))
		run(referenceExec("INSERT INTO typed VALUES('hello',12.50),('hello',12.50)", 2))
		for _, empty := range []bool{false, true} {
			q := "SELECT DISTINCT s AS label,n AS price FROM typed"
			want := [][]any{{"hello", "12.50"}}
			if empty {
				q += " WHERE FALSE"
				want = [][]any{}
			}
			s := referenceSelect(q, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want)
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				m, err := rows.ColumnTypes()
				if err != nil || len(m) != 2 {
					t.Fatal(m, err)
				}
				if n, ok := m[0].Length(); !ok || n != 5 {
					t.Fatal(n, ok)
				}
				if precision, scale, ok := m[1].DecimalSize(); !ok || precision != 7 || scale != 2 {
					t.Fatal(precision, scale, ok)
				}
			}
			queryDistinctBoth(run, s)
		}
	})
}

package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceDateBindingRawMetadataAndNull(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			for _, prepared := range []bool{false, true} {
				run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"DATE"}, nil))
				for i, arg := range []any{[]byte("2000-02-29"), time.Date(2000, 1, 1, 0, 1, 0, 0, time.FixedZone("east", 14*3600)), time.Date(1999, 12, 31, 23, 59, 0, 0, time.FixedZone("west", -12*3600)), time.Date(0, 2, 29, 0, 0, 0, 0, time.UTC), "infinity", "-infinity", nil} {
					s := referenceExec("INSERT INTO d VALUES($1,$2)", 1, i, arg)
					s.prepared = prepared
					run(s)
				}
				bc := "0001-02-29 BC"
				if b.pg {
					bc = "0001-03-01 BC"
				}
				want := [][]any{{referenceDateExpected(b.pg, "2000-02-29")}, {referenceDateExpected(b.pg, "2000-01-01")}, {referenceDateExpected(b.pg, "1999-12-31")}, {referenceDateExpected(b.pg, bc)}, {"infinity"}, {"-infinity"}, {nil}}
				run(referenceSelect("SELECT v FROM d ORDER BY id", []string{"v"}, []string{"DATE"}, want))
				run(referenceSelect("SELECT v=NULL FROM d WHERE id=0", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}}))
				run(referenceExec("DROP TABLE d", 0))
				run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			}
		})
	}
}

func TestReferenceDateHeldBindingsAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE,w DATE)", 0))
			insertQuery := "INSERT INTO d VALUES($1,$1)"
			ins, err := b.db.PrepareContext(p.ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := ins.Close(); err != nil {
					t.Error(err)
				}
			}()
			insert := referenceHeldStatement{referenceExecutor: b.db, statement: ins, query: insertQuery}
			bytes := []byte("2000-02-29")
			for _, value := range []any{"epoch", bytes, time.Date(2000, 1, 1, 0, 1, 0, 0, time.FixedZone("east", 14*3600)), time.Date(1999, 12, 31, 23, 59, 0, 0, time.FixedZone("west", -12*3600)), time.Date(0, 2, 29, 23, 0, 0, 0, time.UTC), nil} {
				referenceDateRunStep(t, p.ctx, insert, b.recorder, referenceExec(insertQuery, 1, value))
			}
			bytes[0] = '9'
			bc := "0001-02-29 BC"
			if b.pg {
				bc = "0001-03-01 BC"
			} // Pinned pq's BC AddDate normalization.
			want := [][]any{{referenceDateExpected(b.pg, bc)}, {referenceDateExpected(b.pg, "1970-01-01")}, {referenceDateExpected(b.pg, "1999-12-31")}, {referenceDateExpected(b.pg, "2000-01-01")}, {referenceDateExpected(b.pg, "2000-02-29")}, {nil}}
			run(referenceSelect("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, want))
			query := "SELECT v FROM d WHERE v=$1 ORDER BY v"
			held, err := b.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := referenceHeldStatement{referenceExecutor: b.db, statement: held, query: query}
			for _, value := range []any{"2000-02-29", "epoch", nil, "2000-02-29"} {
				var expected [][]any
				if value != nil {
					text := value.(string)
					if text == "epoch" {
						text = "1970-01-01"
					}
					expected = [][]any{{referenceDateExpected(b.pg, text)}}
				}
				referenceDateRunStep(t, p.ctx, x, b.recorder, referenceSelect(query, []string{"v"}, []string{"DATE"}, expected, value))
			}
			run(referenceSelect("SELECT v=NULL FROM d WHERE v='2000-02-29'", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}}))
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT v=TIMESTAMP '2000-02-29 00:00',TIMESTAMP '2000-02-29 00:00'=v FROM d WHERE v=DATE '2000-02-29'", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
				s.prepared = prepared
				run(s)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if _, err := ins.ExecContext(canceled, "2000-01-01"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			rows, err := held.QueryContext(canceled, "2000-01-01")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

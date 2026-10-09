package reference_test

import (
	"testing"
	"time"
)

func TestReferenceDateDocumentedInputsAndCivilBounds(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			forms := []string{"1999-01-08", "January 8, 1999", "1999-Jan-08", "Jan-08-1999", "08-Jan-1999", "19990108", "990108", "1999.008", "J2451187", "1999-01-08"}
			insertQuery := "INSERT INTO d VALUES($1,$2)"
			insert, err := b.db.PrepareContext(p.ctx, insertQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := insert.Close(); err != nil {
					t.Error(err)
				}
			}()
			for _, prepared := range []bool{false, true} {
				for i, input := range forms {
					x := referenceExecutor(b.db)
					if prepared {
						x = referenceHeldStatement{referenceExecutor: b.db, statement: insert, query: insertQuery}
					}
					referenceDateRunStep(t, p.ctx, x, b.recorder, referenceExec(insertQuery, 1, i, input))
				}
			}
			var finite any = "1999-01-08"
			if b.pg {
				finite = time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC)
			}
			want := make([][]any, 2*len(forms))
			for i := range want {
				want[i] = []any{finite}
			}
			for _, q := range []string{"SELECT v FROM d ORDER BY id", "SELECT DATE '1999-01-08' FROM d WHERE v='1999-01-08'"} {
				name := "v"
				if q != "SELECT v FROM d ORDER BY id" {
					name = "date"
				}
				s := referenceSelect(q, []string{name}, []string{"DATE"}, want)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec(`CREATE TABLE date(date "date")`, 0))
			run(referenceSelect("SELECT date FROM date", []string{"date"}, []string{"DATE"}, nil))
			run(referenceExec("INSERT INTO date VALUES(NULL)", 1))
			run(referenceSelect("SELECT date FROM date", []string{"date"}, []string{"DATE"}, [][]any{{nil}}))
			run(referenceExec("CREATE TABLE bounds(id INTEGER,v DATE)", 0))
			cases := []struct {
				input, canonical string
				year             int
				month            time.Month
				day              int
			}{
				{"4714-11-24 BC", "4714-11-24 BC", -4713, 11, 24},
				{"5874897-12-31", "5874897-12-31", 5874897, 12, 31},
				{"300000-01-01", "300000-01-01", 300000, 1, 1},
				{"0001-02-29 BC", "0001-02-29 BC", 0, 2, 29},
				{"1600-02-29", "1600-02-29", 1600, 2, 29},
				{"2000-02-29", "2000-02-29", 2000, 2, 29},
				{"2400-02-29", "2400-02-29", 2400, 2, 29},
				{"epoch", "1970-01-01", 1970, 1, 1},
				{"infinity", "infinity", 0, 0, 0}, {"-infinity", "-infinity", 0, 0, 0},
				{"January 8, 99 BC", "0099-01-08 BC", -98, 1, 8}, {"01/02/03", "2003-01-02", 2003, 1, 2},
				{"Jan 8 69", "2069-01-08", 2069, 1, 8}, {"Jan 8 70", "1970-01-08", 1970, 1, 8},
			}
			boundsQuery := "INSERT INTO bounds VALUES($1,$2)"
			boundsInsert, err := b.db.PrepareContext(p.ctx, boundsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := boundsInsert.Close(); err != nil {
					t.Error(err)
				}
			}()
			bounds := referenceHeldStatement{referenceExecutor: b.db, statement: boundsInsert, query: boundsQuery}
			want = nil
			for i, tc := range cases {
				run(referenceExec(boundsQuery, 1, 2*i, tc.input))
				referenceDateRunStep(t, p.ctx, bounds, b.recorder, referenceExec(boundsQuery, 1, 2*i+1, tc.input))
				var value any = tc.canonical
				if b.pg {
					if tc.month != 0 {
						value = time.Date(tc.year, tc.month, tc.day, 0, 0, 0, 0, time.UTC)
					}
				}
				want = append(want, []any{value}, []any{value})
			}
			run(referenceSelect("SELECT v FROM bounds ORDER BY id", []string{"v"}, []string{"DATE"}, want))
		})
	}
}

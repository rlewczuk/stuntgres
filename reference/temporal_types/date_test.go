package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDateDocumentedInputsAndCivilBounds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			forms := []string{"1999-01-08", "January 8, 1999", "1999-Jan-08", "Jan-08-1999", "08-Jan-1999", "19990108", "990108", "1999.008", "J2451187", "1999-01-08"}
			insertQuery := "INSERT INTO d VALUES($1,$2)"
			insert, err := b.DB.PrepareContext(p.Ctx, insertQuery)
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
					x := fixture.Executor(b.DB)
					if prepared {
						x = fixture.HeldStatement{Executor: b.DB, Statement: insert, SQL: insertQuery}
					}
					fixture.DateRunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(insertQuery, 1, i, input))
				}
			}
			var finite any = "1999-01-08"
			if b.PG {
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
				s := fixture.Select(q, []string{name}, []string{"DATE"}, want)
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec(`CREATE TABLE date(date "date")`, 0))
			run(fixture.Select("SELECT date FROM date", []string{"date"}, []string{"DATE"}, nil))
			run(fixture.Exec("INSERT INTO date VALUES(NULL)", 1))
			run(fixture.Select("SELECT date FROM date", []string{"date"}, []string{"DATE"}, [][]any{{nil}}))
			run(fixture.Exec("CREATE TABLE bounds(id INTEGER,v DATE)", 0))
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
			boundsInsert, err := b.DB.PrepareContext(p.Ctx, boundsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := boundsInsert.Close(); err != nil {
					t.Error(err)
				}
			}()
			bounds := fixture.HeldStatement{Executor: b.DB, Statement: boundsInsert, SQL: boundsQuery}
			want = nil
			for i, tc := range cases {
				run(fixture.Exec(boundsQuery, 1, 2*i, tc.input))
				fixture.DateRunStep(t, p.Ctx, bounds, b.Recorder, fixture.Exec(boundsQuery, 1, 2*i+1, tc.input))
				var value any = tc.canonical
				if b.PG {
					if tc.month != 0 {
						value = time.Date(tc.year, tc.month, tc.day, 0, 0, 0, 0, time.UTC)
					}
				}
				want = append(want, []any{value}, []any{value})
			}
			run(fixture.Select("SELECT v FROM bounds ORDER BY id", []string{"v"}, []string{"DATE"}, want))
		})
	}
}

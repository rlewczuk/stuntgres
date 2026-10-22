package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePointValues(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE point_values(id integer,p point)", 0))
		run(fixture.Exec("INSERT INTO point_values VALUES(1,'(1,2)'),(2,'1,2'),(3,NULL)", 3))
		for _, prepared := range []bool{false, true} {
			s := boxSelect(b, "SELECT id,p FROM point_values ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, [][]any{{int64(1), "(1,2)"}, {int64(2), "(1,2)"}, {int64(3), nil}})
			s.Prepared = prepared
			run(s)
		}
		for _, tc := range [][2]string{
			{"(1,2)", "(1,2)"}, {"1,2", "(1,2)"}, {" \t( -2.5 , 4e1 )\n", "(-2.5,40)"},
			{"1.0000000000000002,0.10000000000000002", "(1.0000000000000002,0.10000000000000002)"},
			{"-0,-0", "(-0,-0)"}, {"5e-324,0", "(5e-324,0)"}, {"Infinity,-Infinity", "(Infinity,-Infinity)"},
			{"NaN,-NaN", "(NaN,NaN)"}, {"0x1.8p1,0x1p-1", "(3,0.5)"}, {"nan(payload),+infinity", "(NaN,Infinity)"},
		} {
			t.Run(tc[0], func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"SELECT point " + boxQuote(tc[0]) + " AS p", "SELECT " + boxQuote(tc[0]) + "::point AS p", "SELECT " + boxQuote(tc[0]) + "::text::point AS p"} {
					s := boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{tc[1]}})
					run(s)
					run(fixture.Prepared(s))
				}
			})
		}
		q := "SELECT $1::point AS p"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ arg, want any }{{"1,2", "(1,2)"}, {[]byte("(1,2)"), "(1,2)"}, {boxValuer{value: "1,2"}, "(1,2)"}, {nil, nil}, {[]byte(nil), nil}, {"3,4", "(3,4)"}} {
			s := boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{tc.want}}, tc.arg)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		buf := []byte("1,2")
		rows, err := stmt.QueryContext(p.Ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		copy(buf, "9,9")
		boxMetadata(t, b.PG, rows, []string{"POINT"}, nil)
		if !rows.Next() {
			t.Fatalf("bound row: %v", rows.Err())
		}
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "(1,2)" {
			t.Fatalf("caller bytes changed point: %q", value)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatalf("bound row termination: %v", rows.Err())
		}
		rows.Close()
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Select("SELECT (point '0.10000000000000002,1')::text AS p", []string{"p"}, []string{"TEXT"}, [][]any{{"(0.1,1)"}}))
	})
}

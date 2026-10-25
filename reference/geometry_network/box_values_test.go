package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBoxValues(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE box_values(id integer NOT NULL,b box)", 0))
		run(fixture.Exec("INSERT INTO box_values VALUES(1,'(1,4),(3,2)'),(2,'(3,2),(1,4)')", 2))
		run(boxSelect(b, "SELECT id,b,b[0] AS hi,b[1] AS lo FROM box_values ORDER BY id", []string{"id", "b", "hi", "lo"}, []string{"INT4", "BOX", "POINT", "POINT"}, [][]any{{int64(1), "(3,4),(1,2)", "(3,4)", "(1,2)"}, {int64(2), "(3,4),(1,2)", "(3,4)", "(1,2)"}}))
		for _, tc := range [][2]string{
			{"((1,4),(3,2))", "(3,4),(1,2)"}, {"(1,4),(3,2)", "(3,4),(1,2)"}, {"1,4,3,2", "(3,4),(1,2)"},
			{"(1,4,3,2)", "(3,4),(1,2)"}, {"(1,4)(3,2)", "(3,4),(1,2)"}, {"(1,4),(3,2),", "(3,4),(1,2)"}, {"1,4,3,2,", "(3,4),(1,2)"},
			{" ( ( -2.5 , 4e1 ) , ( 3.25 , -6 ) ) ", "(3.25,40),(-2.5,-6)"}, {"2,2,2,2", "(2,2),(2,2)"},
			{"-0,-0,-0,-0", "(-0,-0),(-0,-0)"}, {"5e-324,0,0,0", "(5e-324,0),(0,0)"},
			{"Infinity,2,-Infinity,1", "(Infinity,2),(-Infinity,1)"}, {"NaN,2,1,1", "(NaN,2),(1,1)"},
		} {
			t.Run(tc[0], func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, query := range []string{
						"SELECT " + boxQuote(tc[0]) + "::box AS b", "SELECT box " + boxQuote(tc[0]) + " AS b",
						"SELECT pg_catalog.box " + boxQuote(tc[0]) + " AS b", "SELECT CAST(" + boxQuote(tc[0]) + " AS box) AS b",
						"SELECT " + boxQuote(tc[0]) + "::text::box AS b",
					} {
						s := boxSelect(b, query, []string{"b"}, []string{"BOX"}, [][]any{{tc[1]}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		}
		q := "SELECT $1::box AS b,($1::box)[0] AS hi,($1::box)[1] AS lo"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, arg := range []any{"(1,4),(3,2)", "(3,2),(1,4)", []byte("1,4,3,2"), boxValuer{value: "1,4,3,2"}, "(3,2),(1,4)", nil, []byte(nil)} {
			want := [][]any{{"(3,4),(1,2)", "(3,4)", "(1,2)"}}
			if arg == nil {
				want = [][]any{{nil, nil, nil}}
			} else if v, ok := arg.([]byte); ok && v == nil {
				want = [][]any{{nil, nil, nil}}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"b", "hi", "lo"}, []string{"BOX", "POINT", "POINT"}, want, arg))
		}
		insert := "INSERT INTO box_values VALUES($1,$2)"
		ins, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer ins.Close()
		for i, arg := range []any{"1,4,3,2", []byte("3,2,1,4"), boxValuer{value: "1,4,3,2"}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ins, SQL: insert}, b.Recorder, fixture.Exec(insert, 1, i+3, arg))
		}
		buf := []byte("1,4,3,2")
		rows, err := stmt.QueryContext(p.Ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		copy(buf, "9,9,8,8")
		if !rows.Next() {
			t.Fatalf("retained bind row: %v", rows.Err())
		}
		var value, hi, lo string
		if err := rows.Scan(&value, &hi, &lo); err != nil {
			t.Fatal(err)
		}
		if value != "(3,4),(1,2)" || hi != "(3,4)" || lo != "(1,2)" {
			t.Fatalf("caller buffer mutated retained value %q %q %q", value, hi, lo)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatalf("retained bind termination: %v", rows.Err())
		}
		rows.Close()
		sentinel := errors.New("box valuer failure")
		_, err = stmt.QueryContext(p.Ctx, boxValuer{err: sentinel})
		if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
			t.Fatalf("Valuer error %v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("ordinary Valuer error gained SQLSTATE %s", state.SQLState())
		}
	})
}

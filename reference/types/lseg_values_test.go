package reference_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLsegValues(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE lseg_values(id integer NOT NULL,s lseg)", 0))
		run(fixture.Exec("INSERT INTO lseg_values VALUES(1,'[(1,4),(3,2)]'),(2,'((1,4),(3,2))'),(3,'(1,4),(3,2)'),(4,'1,4,3,2'),(5,'[(3,2),(1,4)]')", 5))
		run(boxSelect(b, "SELECT id,s FROM lseg_values ORDER BY id", []string{"id", "s"}, []string{"INT4", "LSEG"}, [][]any{{int64(1), "[(1,4),(3,2)]"}, {int64(2), "[(1,4),(3,2)]"}, {int64(3), "[(1,4),(3,2)]"}, {int64(4), "[(1,4),(3,2)]"}, {int64(5), "[(3,2),(1,4)]"}}))
		for _, tc := range [][2]string{
			{"[(1,4),(3,2)]", "[(1,4),(3,2)]"}, {"((1,4),(3,2))", "[(1,4),(3,2)]"}, {"(1,4),(3,2)", "[(1,4),(3,2)]"}, {"1,4,3,2", "[(1,4),(3,2)]"},
			{"[(3,2),(1,4)]", "[(3,2),(1,4)]"}, {"0,0,0,0", "[(0,0),(0,0)]"},
			// Historical optional delimiters accepted by PostgreSQL.
			{"(1,4,3,2)", "[(1,4),(3,2)]"}, {"(1,4)(3,2)", "[(1,4),(3,2)]"}, {"(1,4),(3,2),", "[(1,4),(3,2)]"}, {"1,4,3,2,", "[(1,4),(3,2)]"}, {"[(1,4),(3,2))", "[(1,4),(3,2)]"},
			{" [ ( -2.5 , 4e1 ) , ( 3.25 , -6 ) ] ", "[(-2.5,40),(3.25,-6)]"}, {"-0,-0,-0,-0", "[(-0,-0),(-0,-0)]"},
			{"5e-324,0,0,0", "[(5e-324,0),(0,0)]"}, {"0.10000000000000002,1,0,0", "[(0.10000000000000002,1),(0,0)]"},
			{"Infinity,NaN,-Infinity,0", "[(Infinity,NaN),(-Infinity,0)]"},
		} {
			t.Run(tc[0], func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SELECT lseg " + boxQuote(tc[0]) + " AS s", "SELECT pg_catalog.lseg " + boxQuote(tc[0]) + " AS s", "SELECT " + boxQuote(tc[0]) + "::lseg AS s", "SELECT CAST(" + boxQuote(tc[0]) + " AS lseg) AS s", "SELECT " + boxQuote(tc[0]) + "::text::lseg AS s"} {
						s := boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{tc[1]}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		}
		q := "SELECT $1::lseg AS s"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ arg, want any }{{"1,4,3,2", "[(1,4),(3,2)]"}, {[]byte("1,4,3,2"), "[(1,4),(3,2)]"}, {boxValuer{value: "1,4,3,2"}, "[(1,4),(3,2)]"}, {boxValuer{value: []byte("1,4,3,2")}, "[(1,4),(3,2)]"}, {nil, nil}, {[]byte(nil), nil}, {"3,2,1,4", "[(3,2),(1,4)]"}} {
			s := boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{tc.want}}, tc.arg)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		buf := []byte("1,4,3,2")
		rows, err := stmt.QueryContext(p.Ctx, buf)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		copy(buf, "9,9,8,8")
		boxMetadata(t, b.PG, rows, []string{"LSEG"}, nil)
		if !rows.Next() {
			t.Fatalf("retained bind row: %v", rows.Err())
		}
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "[(1,4),(3,2)]" {
			t.Fatalf("mutated bound bytes changed value: %q", value)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatalf("retained bind termination: %v", rows.Err())
		}
		rows.Close()
		sentinel := errors.New("lseg valuer failure")
		_, err = stmt.QueryContext(p.Ctx, boxValuer{err: sentinel})
		if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
			t.Fatalf("Valuer error: %v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("ordinary Valuer error gained SQLSTATE: %v", err)
		}
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Select("SELECT (lseg '0.10000000000000002,1,0,0')::text AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"[(0.1,1),(0,0)]"}}))
	})
}

package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSegIntervalPredicates(t *testing.T) {
	for _, tc := range segIntervalCases {
		t.Run(tc.name, func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				a, z := queryIntLiteral(tc.a)+"::seg", queryIntLiteral(tc.b)+"::seg"
				segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection(a, z), segIntervalNames, segIntervalTypes, [][]any{segIntervalBools(tc.want)}, false))
				segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection("$1::seg", "$2::seg"), segIntervalNames, segIntervalTypes, [][]any{segIntervalBools(tc.want)}, false, tc.a, tc.b))
				for i, name := range segIntervalRoutines {
					segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT public."+name+"("+a+","+z+") AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[i]}}, false))
					segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+a+" OPERATOR(public."+segIntervalOperators[i]+") "+z+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want[i]}}, false))
				}
			})
		})
	}
}

func TestReferenceSegIntervalBindings(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		segIntervalSeed(t, p, b, c)
		segIntervalBoth(t, p, b, c, segIntervalTableCheck(b))
		segIntervalHeldRoles(t, p, b, c)
		segIntervalBoth(t, p, b, c, fixture.Select("SELECT id FROM seg_interval_data WHERE a && b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
		for _, q := range []string{"SELECT seg_overlap('0 .. 1','1 .. 2') AS v", "SELECT CAST('0 .. 1'::text AS seg) && '1 .. 2'::seg AS v", "SELECT $1::seg = $1::seg AS v"} {
			var args []any
			if q == "SELECT $1::seg = $1::seg AS v" {
				args = []any{"1.0"}
			}
			segIntervalBoth(t, p, b, c, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, args...))
		}
		q := "SELECT $1::seg && $2::seg AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, b any
			want any
		}{{"0 .. 1", "1 .. 2", true}, {"0 .. 1", "2 .. 3", false}, {nil, "1", nil}, {"1", "1", true}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, false, tc.a, tc.b))
		}
		want := queryIntParameterDiagnostic("42601", "bad seg representation", "ABC", 1)
		want.Detail = `syntax error at or near "A"`
		s := fixture.ErrorDiagnostic(q, want, "ABC", "1")
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, "1", "1"))
		insert := "INSERT INTO seg_interval_data VALUES ($1,$2,$3)"
		ins, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer ins.Close()
		for i, a := range []any{[]byte("1.0"), nil, "1"} {
			result, err := ins.ExecContext(p.Ctx, 6+i, a, "1")
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal("insert count", count, err)
			}
			if _, err := result.LastInsertId(); err == nil {
				t.Fatal("LastInsertId supported")
			}
			if bytes, ok := a.([]byte); ok {
				for j := range bytes {
					bytes[j] = 'x'
				}
			}
		}
		segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT id,a=b AS v FROM seg_interval_data WHERE id>=6 ORDER BY id", []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(6), false}, {int64(7), nil}, {int64(8), true}}, false))
	})
}

func TestReferenceSegIntervalNulls(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, operands := range [][2]string{{"NULL::seg", "'1'::seg"}, {"'1'::seg", "NULL::seg"}, {"NULL::seg", "NULL::seg"}} {
			segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection(operands[0], operands[1]), segIntervalNames, segIntervalTypes, [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}}, false))
		}
		for _, args := range [][2]any{{nil, "1"}, {"1", nil}, {nil, nil}} {
			segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection("$1::seg", "$2::seg"), segIntervalNames, segIntervalTypes, [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}}, false, args[0], args[1]))
		}
	})
}

func TestReferenceSegIntervalMetadata(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		segIntervalSeed(t, p, b, c)
		for _, suffix := range []string{" WHERE id=99", " WHERE id=5", ""} {
			var want [][]any
			if suffix == " WHERE id=5" {
				want = [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}}
			} else if suffix == "" {
				for _, r := range segIntervalSeedRows() {
					want = append(want, r[1:])
				}
			}
			if want == nil {
				want = [][]any{}
			}
			segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection("a", "b")+" FROM seg_interval_data"+suffix+" ORDER BY id", segIntervalNames, segIntervalTypes, want, false))
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT "+segIntervalProjection("a", "b")+" FROM seg_interval_data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		segIntervalMetadata(t, b.PG, cols, segIntervalNames, segIntervalTypes, false)
		// Independent marker/digit changes leave every geometric predicate intact.
		for i, pair := range [][2]string{{"<1 .. 2", "1 .. 2"}, {">1 .. 2", "1 .. 2"}, {"~1 .. 2", "1 .. 2"}, {"1 .. <2", "1 .. 2"}, {"1 .. >2", "1 .. 2"}, {"1 .. ~2", "1 .. 2"}, {"1.00 .. 2", "1 .. 2"}, {"1 .. 2.00", "1 .. 2"}} {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT "+segIntervalProjection(queryIntLiteral(pair[0])+"::seg", queryIntLiteral(pair[1])+"::seg"), segIntervalNames, segIntervalTypes, [][]any{{false, false, true, true, false, true, true, true}}, false))
			})
		}
	})
}

func segIntervalHeldRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// True and false inputs are independently chosen for each released formula.
	for i, tc := range []struct{ yes, no [2]string }{
		{[2]string{"0 .. 1", "2 .. 3"}, [2]string{"1", "1"}},
		{[2]string{"2 .. 3", "0 .. 1"}, [2]string{"1", "1"}},
		{[2]string{"1", "1"}, [2]string{"2 .. 3", "0 .. 1"}},
		{[2]string{"1", "1"}, [2]string{"0 .. 1", "2 .. 3"}},
		{[2]string{"1", "1"}, [2]string{"1.0", "1.00"}},
		{[2]string{"0 .. 1", "1 .. 2"}, [2]string{"0 .. 1", "2 .. 3"}},
		{[2]string{"0 .. 2", "1"}, [2]string{"0 .. 1", "1 .. 2"}},
		{[2]string{"1", "0 .. 2"}, [2]string{"0 .. 1", "1 .. 2"}},
	} {
		for _, operator := range []bool{false, true} {
			q := "SELECT " + segIntervalRoutines[i] + "($1::seg,$2::seg) AS v"
			if operator {
				q = "SELECT $1::seg " + segIntervalOperators[i] + " $2::seg AS v"
			}
			func() {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, input := range []struct {
					a, z any
					want any
				}{{tc.yes[0], tc.yes[1], true}, {tc.no[0], tc.no[1], false}, {nil, tc.yes[1], nil}, {tc.yes[0], tc.yes[1], true}} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{input.want}}, false, input.a, input.z))
				}
			}()
		}
	}
}

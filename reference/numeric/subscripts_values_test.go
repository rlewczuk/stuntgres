package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const subscriptsMatrix = "'[-1:0][3:5]={{1,NULL,3},{4,5,6}}'::integer[]"

func subscriptsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func TestReferenceSubscriptsValues(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args   string
			values []int64
		}{
			{"'{NULL,1,NULL,2}'::integer[],1", []int64{1, 2, 3, 4}},
			{"'[-2:0]={10,NULL,30}'::integer[],1", []int64{-2, -1, 0}},
			{"'[-2:0]={10,NULL,30}'::integer[],1,false", []int64{-2, -1, 0}},
			{"'[-2:0]={10,NULL,30}'::integer[],1,true", []int64{-2, -1, 0}},
			{subscriptsMatrix + ",1", []int64{-1, 0}},
			{subscriptsMatrix + ",2", []int64{3, 4, 5}},
			{subscriptsMatrix + ",2,true", []int64{3, 4, 5}},
			{subscriptsMatrix + ",0", nil}, {subscriptsMatrix + ",-1", nil}, {subscriptsMatrix + ",3", nil},
			{subscriptsMatrix + ",(-2147483648)::integer", nil}, {subscriptsMatrix + ",2147483647", nil},
			{"'{}'::integer[],1", nil}, {"NULL::integer[],1", nil},
			{"'{1}'::integer[],NULL::integer", nil}, {"'{1}'::integer[],1,NULL::boolean", nil},
			{"'{a,NULL,c}'::text[],1", []int64{1, 2, 3}},
			{"'{true,NULL,false}'::boolean[],1", []int64{1, 2, 3}},
			{"'{1.2,NULL,3.4}'::numeric[],1", []int64{1, 2, 3}},
			// PG18.0 reverse at the exact INT32_MIN lower endpoint does not
			// terminate; use MIN+1 here and prove safe exact-MIN native iteration
			// independently in the internal cursor unit.
			{"'[-2147483647:-2147483646]={1,2}'::integer[],1,true", []int64{-2147483647, -2147483646}},
			{"'[2147483645:2147483646]={1,2}'::integer[],1", []int64{2147483645, 2147483646}},
			{"'[-2147483648:-2147483647]={1,2}'::integer[],1", []int64{-2147483648, -2147483647}},
			{"'[2147483645:2147483646]={1,2}'::integer[],1,true", []int64{2147483645, 2147483646}},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := fixture.Select("SELECT generate_subscripts("+tc.args+") AS s ORDER BY s", []string{"s"}, []string{"INT4"}, seriesRows(tc.values...))
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceSubscriptsMetadata(t *testing.T) {
	subscriptsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args   string
			values []int64
		}{
			{"'{1,2}'::integer[],1", []int64{1, 2}}, {"NULL::integer[],1", nil},
			{"'{}'::integer[],1", nil}, {subscriptsMatrix + ",3", nil}, {subscriptsMatrix + ",2,NULL", nil},
		} {
			for _, name := range []string{"generate_subscripts", "s"} {
				q := "SELECT generate_subscripts(" + tc.args + ")"
				if name == "s" {
					q += " AS s"
				}
				q += " ORDER BY " + name
				s := fixture.Select(q, []string{name}, []string{"INT4"}, seriesRows(tc.values...))
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(cols) != 1 || cols[0].Name() != name || cols[0].DatabaseTypeName() != "INT4" {
						t.Fatal("metadata before Next", cols)
					}
					if !b.PG {
						if _, known := cols[0].Nullable(); known {
							t.Fatal("computed nullability must be unknown")
						}
					}
				}
				seriesBoth(t, p, b, c, s)
			}
		}
	})
}

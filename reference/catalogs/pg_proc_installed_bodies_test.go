package reference_test

import (
	"sort"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGProcInstalledBodies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer pgProcInstalledCleanup(t, c)
			pgProcInstalledSetup(t, p, b, c)
			pgProcInstalledMembership(t, p, b, c)
			pgProcInstalledCheck(t, p, b, c, pgProcInstalledBodies())
			pgProcInstalledExecute(t, p, b, c)
		})
	}
}

func pgProcInstalledMembership(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	facts := pgProcInstalledBodies()
	for _, schema := range []string{pgProcCitextSchema, pgProcEarthSchema} {
		var want [][]any
		var names []string
		seen := make(map[string]bool)
		for _, f := range facts {
			if f.schema == schema {
				nargs := int64(0)
				if f.args != "" {
					nargs = int64(strings.Count(f.args, ",") + 1)
				}
				want = append(want, []any{f.name, nargs})
				if !seen[f.name] {
					names = append(names, "'"+f.name+"'")
					seen[f.name] = true
				}
			}
		}
		sort.Slice(want, func(i, j int) bool {
			if want[i][0] == want[j][0] {
				return want[i][1].(int64) < want[j][1].(int64)
			}
			return want[i][0].(string) < want[j][0].(string)
		})
		pgProcBoth(t, p, b, x, "SELECT proname,pronargs FROM pg_proc WHERE pronamespace=$1::regnamespace::oid AND proname IN ("+strings.Join(names, ",")+") ORDER BY proname,pronargs", []string{"proname", "pronargs"}, []string{"NAME", "INT2"}, want, schema)
	}
}

func pgProcInstalledExecute(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	ct := pgProcCitextSchema + ".citext"
	for _, tc := range []struct {
		name, args, typ string
		rows            [][]any
	}{
		{"regexp_match", "'AbA'::" + ct + ",'a'::" + ct, "_TEXT", [][]any{{"{A}"}}},
		{"regexp_match", "'AbA'::" + ct + ",'a'::" + ct + ",'c'", "_TEXT", [][]any{{nil}}},
		{"regexp_matches", "'AbA'::" + ct + ",'a'::" + ct, "_TEXT", [][]any{{"{A}"}}},
		{"regexp_matches", "'AbA'::" + ct + ",'a'::" + ct + ",'g'", "_TEXT", [][]any{{"{A}"}, {"{A}"}}},
		{"regexp_replace", "'AbA'::" + ct + ",'a'::" + ct + ",'x'", "TEXT", [][]any{{"xbA"}}},
		{"regexp_replace", "'AbA'::" + ct + ",'a'::" + ct + ",'x','g'", "TEXT", [][]any{{"xbx"}}},
		{"regexp_split_to_array", "'AbA'::" + ct + ",'b'::" + ct, "_TEXT", [][]any{{"{A,A}"}}},
		{"regexp_split_to_array", "'AbA'::" + ct + ",'b'::" + ct + ",'c'", "_TEXT", [][]any{{"{A,A}"}}},
		{"regexp_split_to_table", "'AbA'::" + ct + ",'b'::" + ct, "TEXT", [][]any{{"A"}, {"A"}}},
		{"regexp_split_to_table", "'AbA'::" + ct + ",'b'::" + ct + ",'c'", "TEXT", [][]any{{"A"}, {"A"}}},
		{"strpos", "'AbA'::" + ct + ",'ba'::" + ct, "INT4", [][]any{{int64(2)}}},
		{"replace", "'AbA'::" + ct + ",'a'::" + ct + ",'x'::" + ct, "TEXT", [][]any{{"xbx"}}},
		{"split_part", "'AbA'::" + ct + ",'b'::" + ct + ",2", "TEXT", [][]any{{"A"}}},
		{"translate", "'AbA'::" + ct + ",'a'::" + ct + ",'x'", "TEXT", [][]any{{"xbx"}}},
	} {
		pgProcBoth(t, p, b, x, "SELECT "+pgProcCitextSchema+"."+tc.name+"("+tc.args+") AS value", []string{"value"}, []string{tc.typ}, tc.rows)
	}
	e := pgProcEarthSchema + "."
	pgProcBoth(t, p, b, x, "SELECT "+e+"earth() AS radius,"+e+"sec_to_gc(0::float8) AS arc,"+e+"gc_to_sec(0::float8) AS chord,"+e+"latitude("+e+"ll_to_earth(0::float8,0::float8)) AS latitude,"+e+"longitude("+e+"ll_to_earth(0::float8,0::float8)) AS longitude", []string{"radius", "arc", "chord", "latitude", "longitude"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0), float64(0), float64(0), float64(0)}})
}

package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySortValues(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		for _, tc := range intarraySortCases {
			t.Run(tc.label, func(t *testing.T) {
				literal := tc.expression(tc.literals(tc.args))
				intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+literal+" AS v", tc.want))
				intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT v FROM "+literal+" AS result(v)", tc.want))
				nulls := append([]any(nil), tc.args...)
				nulls[0] = nil
				intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT v FROM "+tc.expression(tc.literals(nulls))+" AS result(v)", nil))
				for _, typed := range []bool{false, true} {
					q := "SELECT " + tc.expression(tc.parameters(typed)) + " AS v"
					intarrayBoth(t, p, b, c, intarraySortResult(b, q, tc.want, tc.args...))
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, bytes := range []bool{false, true} {
						args := append([]any(nil), tc.args...)
						if bytes {
							args[0] = []byte(intarraySortA)
							if len(args) > 1 {
								args[1] = []byte("dEsC")
							}
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, tc.want, args...))
					}
					for _, input := range []any{nil, []byte(nil)} {
						args := append([]any(nil), tc.args...)
						args[0] = input
						fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, nil, args...))
					}
					bad := append([]any(nil), tc.args...)
					bad[0] = "{1,NULL}"
					s := fixture.ErrorDiagnostic(q, intarraySetNullError(), bad...)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					changed := append([]any(nil), tc.args...)
					changed[0] = []byte("{5,4,4}")
					want := "{4,4,5}"
					if tc.label == "sort_binary" || tc.label == "sort_desc" {
						want = "{5,4,4}"
					}
					if tc.name == "uniq" {
						want = "{5,4}"
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, want, changed...))
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
		// Flat numeric order and adjacent compaction, independently specified
		// for every overload, with shape witnesses confirmed on pinned PG18.6.
		for i, boundary := range []struct{ input, asc, desc, uniq string }{
			{"{}", "{}", "{}", "{}"},
			{"{7}", "{7}", "{7}", "{7}"},
			{"{1,2,3}", "{1,2,3}", "{3,2,1}", "{1,2,3}"},
			{"{3,2,1}", "{1,2,3}", "{3,2,1}", "{3,2,1}"},
			{"{2,2,2}", "{2,2,2}", "{2,2,2}", "{2}"},
			{"{2,1,2}", "{1,2,2}", "{2,2,1}", "{2,1,2}"},
			{"{1,2,2,3,1,1}", "{1,1,1,2,2,3}", "{3,2,2,1,1,1}", "{1,2,3,1}"},
			{"{2147483647,-2147483648,0,-1}", "{-2147483648,-1,0,2147483647}", "{2147483647,0,-1,-2147483648}", "{2147483647,-2147483648,0,-1}"},
			{"[0:2]={3,1,2}", "[0:2]={1,2,3}", "[0:2]={3,2,1}", "[0:2]={3,1,2}"},
			{"{{4,1},{3,2}}", "{{1,2},{3,4}}", "{{4,3},{2,1}}", "{{4,1},{3,2}}"},
			{"{{1,1},{2,2}}", "{{1,1},{2,2}}", "{{2,2},{1,1}}", "{{1},{2}}"},
			{"[0:1][-2:-1]={{1,1},{2,2}}", "[0:1][-2:-1]={{1,1},{2,2}}", "[0:1][-2:-1]={{2,2},{1,1}}", "[0:1][-2:-2]={{1},{2}}"},
			{"[0:3]={1,1,2,2}", "[0:3]={1,1,2,2}", "[0:3]={2,2,1,1}", "[0:1]={1,2}"},
			{"{{1,2},{2,3}}", "{{1,2},{2,3}}", "{{3,2},{2,1}}", "{{1},{2},{3}}"},
		} {
			for _, tc := range intarraySortCases {
				t.Run("boundary_"+strconv.Itoa(i)+"_"+tc.label, func(t *testing.T) {
					args := append([]any(nil), tc.args...)
					args[0] = boundary.input
					want := boundary.asc
					if tc.label == "sort_binary" || tc.label == "sort_desc" {
						want = boundary.desc
					}
					if tc.name == "uniq" {
						want = boundary.uniq
					}
					intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(tc.literals(args))+" AS v", want))
					intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(tc.parameters(true))+" AS v", want, args...))
				})
			}
		}
		for _, direction := range []string{"asc", "ASC", "aSc", "desc", "DESC", "dEsC"} {
			want := "{1,1,2,3}"
			if direction == "desc" || direction == "DESC" || direction == "dEsC" {
				want = "{3,2,1,1}"
			}
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT ia_provider.sort($1::integer[],$2::text) AS v", want, intarraySortA, direction))
		}
		for _, input := range []string{"{}", "{7}"} {
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT ia_provider.sort($1::integer[],$2::text) AS v", input, input, "invalid"))
		}
		intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT ia_provider.uniq(ia_provider.sort('{1,2,3,2,1}'::integer[])) AS v", "{1,2,3}"))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN ia_sort_array AS integer[]", 0),
			fixture.Exec("CREATE DOMAIN ia_sort_direction AS text", 0),
			fixture.Exec("CREATE TABLE ia_sort_inputs(id integer NOT NULL,a integer[])", 0),
			fixture.Exec("INSERT INTO ia_sort_inputs VALUES (1,'{3,1,1,2}'),(2,'{}'),(3,NULL)", 3),
			fixture.Exec("SET search_path=ia_provider,public", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range intarraySortCases {
			args := tc.literals(tc.args)
			args[0] += "::ia_sort_array"
			if len(args) > 1 {
				args[1] += "::ia_sort_direction"
			}
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(args)+" AS v", tc.want))
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.name+"("+joinIntarraySortLiterals(tc)+") AS v", tc.want))
			args = tc.literals(tc.args)
			args[0] = "a"
			q := "SELECT " + tc.expression(args) + " AS v FROM ia_sort_inputs"
			intarrayBoth(t, p, b, c, intarraySelect(b, q+" ORDER BY id", []string{"v"}, []string{"_INT4"}, nil, [][]any{{tc.want}, {"{}"}, {nil}}))
			intarrayBoth(t, p, b, c, intarraySelect(b, q+" WHERE id=99", []string{"v"}, []string{"_INT4"}, nil, [][]any{}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
	})
}

func joinIntarraySortLiterals(tc intarraySortCase) string {
	args := tc.literals(tc.args)
	if len(args) == 1 {
		return args[0]
	}
	return args[0] + "," + args[1]
}

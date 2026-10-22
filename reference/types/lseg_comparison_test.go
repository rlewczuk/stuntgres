package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func lsegComparison(left, right string) string {
	parts := make([]string, 0, 6)
	for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		parts = append(parts, left+op+right+" AS "+[]string{"eq", "ne", "lt", "le", "gt", "ge"}[i])
	}
	return "SELECT " + strings.Join(parts, ",")
}

func TestReferenceLsegComparison(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		cols := []string{"eq", "ne", "lt", "le", "gt", "ge"}
		types := []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}
		q := lsegComparison("$1::lseg", "$2::lseg")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			left, right string
			want        []any
		}{
			{"0,0,3,4", "0,0,3,4", []any{true, false, false, true, false, true}},
			{"0,0,3,4", "3,4,0,0", []any{false, true, false, true, false, true}},
			{"0,0,3,4", "10,10,13,14", []any{false, true, false, true, false, true}},
			{"0,0,3,4", "0,0,0,6", []any{false, true, true, true, false, false}},
			{"0,0,1,0", "0,0,1.0000005,0", []any{true, false, false, true, false, true}},
			{"0,0,1,0", "0,0,1.000002,0", []any{false, true, true, true, false, false}},
			{"NaN,0,1,1", "NaN,0,1,1", []any{true, false, false, false, false, false}},
			{"NaN,1,0,0", "NaN,1.0000005,0,0", []any{false, true, false, false, false, false}},
			{"0,0,Infinity,0", "0,0,Infinity,0", []any{true, false, false, true, false, true}},
			{"Infinity,1,Infinity,0", "Infinity,1,Infinity,0", []any{true, false, false, false, false, false}},
			{"0,0,0,0", "0,0,0,0", []any{true, false, false, true, false, true}},
		} {
			t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, s := range []fixture.Step{
						fixture.Select(q, cols, types, [][]any{tc.want}, tc.left, tc.right),
						fixture.Select(lsegComparison("lseg "+boxQuote(tc.left), "lseg "+boxQuote(tc.right)), cols, types, [][]any{tc.want}),
					} {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, cols, types, [][]any{tc.want}, tc.left, tc.right))
			})
		}
		for _, args := range [][]any{{nil, "0,0,3,4"}, {"0,0,3,4", nil}, {nil, nil}} {
			s := fixture.Select(q, cols, types, [][]any{{nil, nil, nil, nil, nil, nil}}, args...)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Select("SELECT 1 AS id WHERE NULL::lseg=lseg '0,0,3,4'", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT lseg '0,0,3,4'!=lseg '3,4,0,0' AS ne", []string{"ne"}, []string{"BOOL"}, [][]any{{true}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT lseg '1e308,0,-1e308,0'=lseg '1e308,0,-1e308,0' AS eq,lseg '1e308,0,-1e308,0'<>lseg '1e308,0,-1e308,0' AS ne", []string{"eq", "ne"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}})
			s.Prepared = prepared
			run(s)
			run(boxError("SELECT lseg '1e308,0,-1e308,0'<lseg '0,0,1,0'", "22003", "value out of range: overflow", "", "", prepared, true))
		}
	})
}

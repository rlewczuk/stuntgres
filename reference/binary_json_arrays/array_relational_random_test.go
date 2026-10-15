package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalRandomInvariants(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr     string
			n        int
			complete bool
		}{
			{"array_sample($1::int[],0)", 0, false}, {"array_sample($1::int[],1)", 1, false},
			{"array_sample($1::int[],3)", 3, false}, {"array_sample($1::int[],5)", 5, true},
			{"array_shuffle($1::int[])", 5, true},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for range 8 {
						arrayRelationalRows(t, p, c, "SELECT "+tc.expr+" AS v", prepared, []any{"{1,1,2,NULL,3}"}, func(rows *sql.Rows) {
							arrayRelationalArrayMetadata(t, rows, b.PG)
							if !rows.Next() {
								t.Fatal("missing random result")
							}
							var value string
							if err := rows.Scan(&value); err != nil {
								t.Fatal(err)
							}
							got := map[string]int{}
							if value != "{}" {
								if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
									t.Fatalf("invalid array %q", value)
								}
								for _, cell := range strings.Split(value[1:len(value)-1], ",") {
									got[cell]++
								}
							}
							want := map[string]int{"1": 2, "2": 1, "NULL": 1, "3": 1}
							count := 0
							for cell, n := range got {
								count += n
								if n > want[cell] {
									t.Fatalf("not a submultiset: %q", value)
								}
							}
							if count != tc.n || (tc.complete && !reflect.DeepEqual(got, want)) {
								t.Fatalf("wrong sample size/multiplicity: %q", value)
							}
							if rows.Next() {
								t.Fatal("extra random result")
							}
						})
					}
				}
			})
		}
		for _, expr := range []string{"array_shuffle('{}'::int[])", "array_sample('{}'::int[],0)", "array_shuffle(ARRAY[7])", "array_sample(ARRAY[7],1)"} {
			want := "{}"
			if strings.Contains(expr, "ARRAY[7]") {
				want = "{7}"
			}
			arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{want}}))
		}
	})
}

func TestReferenceArrayRelationalSlicesAndBounds(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input, call, bounds string
			n                   int
			slices              []string
		}{
			{"[-2:0][5:6]={{1,NULL},{2,20},{3,30}}", "array_shuffle($1::int[])", "[-2:0][5:6]", 3, []string{"{1,NULL}", "{2,20}", "{3,30}"}},
			{"[-2:0][5:6]={{1,NULL},{2,20},{3,30}}", "array_sample($1::int[],2)", "[1:2][5:6]", 2, []string{"{1,NULL}", "{2,20}", "{3,30}"}},
			{"[-2:0][5:6]={{1,NULL},{2,20},{3,30}}", "array_sample($1::int[],3)", "[1:3][5:6]", 3, []string{"{1,NULL}", "{2,20}", "{3,30}"}},
			{"[-2:-1][5:5][8:9]={{{1,2}},{{3,NULL}}}", "array_sample($1::int[],1)", "[1:1][5:5][8:9]", 1, []string{"{{1,2}}", "{{3,NULL}}"}},
		} {
			t.Run(tc.call+tc.input, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for range 8 {
						arrayRelationalRows(t, p, c, "SELECT "+tc.call+" AS v", prepared, []any{tc.input}, func(rows *sql.Rows) {
							arrayRelationalArrayMetadata(t, rows, b.PG)
							if !rows.Next() {
								t.Fatal("missing slice result")
							}
							var value string
							if err := rows.Scan(&value); err != nil {
								t.Fatal(err)
							}
							prefix := tc.bounds + "="
							if !strings.HasPrefix(value, prefix) {
								t.Fatalf("bounds %q want %q", value, prefix)
							}
							payload := strings.TrimPrefix(value, prefix)
							counts := make(map[string]int)
							// Remove whole original slices only; no element comparison or random order expectation.
							for _, slice := range tc.slices {
								counts[slice] = strings.Count(payload, slice)
								payload = strings.ReplaceAll(payload, slice, "")
							}
							total := 0
							for _, n := range counts {
								if n > 1 {
									t.Fatalf("repeated first-axis occurrence: %q", value)
								}
								total += n
							}
							if total != tc.n || payload != "{"+strings.Repeat(",", tc.n-1)+"}" {
								t.Fatalf("broken slice: %q remainder %q", value, payload)
							}
							if rows.Next() {
								t.Fatal("extra result")
							}
						})
					}
				}
			})
		}
		for _, tc := range []struct{ q, want string }{
			{"SELECT array_shuffle('[-2:-2][5:6]={{1,NULL}}'::int[]) AS v", "[-2:-2][5:6]={{1,NULL}}"},
			{"SELECT array_sample('[-2:0][5:6]={{1,NULL},{2,20},{3,30}}'::int[],0) AS v", "{}"},
		} {
			arrayRelationalBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"_INT4"}, [][]any{{tc.want}}))
		}
	})
}

func TestReferenceArrayRelationalCountDiagnostics(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input  string
			n, max int
		}{
			{"{1,2}", -1, 2}, {"{1,2}", 3, 2}, {"{}", 1, 0}, {"{{1,2,3},{4,5,6}}", 3, 2},
			{"{1,2}", -2147483648, 2}, {"{1,2}", 2147483647, 2},
		} {
			t.Run(fmt.Sprint(tc), func(t *testing.T) {
				q := fmt.Sprintf("SELECT array_sample('%s'::int[],%d)", tc.input, tc.n)
				// Pinned PostgreSQL18 confirms absent position/detail/hint.
				d := fixture.ControlsReferenceDiagnostic("22023", fmt.Sprintf("sample size must be between 0 and %d", tc.max))
				arrayRelationalError(t, p, b, c, q, d, true)
			})
		}
		for _, q := range []string{
			"SELECT array_sample(NULL::int[], -1) AS v", "SELECT array_sample(ARRAY[1],NULL::int) AS v",
			"SELECT array_shuffle(NULL::int[]) AS v", "SELECT array_sample(NULL::int[],NULL::int) AS v",
		} {
			arrayRelationalBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"_INT4"}, [][]any{{nil}}))
		}
	})
}

func TestReferenceArrayRelationalEntropyIsolation(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		draw := func() float64 {
			var v float64
			if err := c.QueryRowContext(p.Ctx, "SELECT random()").Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		seed := func() {
			if _, err := c.ExecContext(p.Ctx, "SELECT setseed(0.314159)"); err != nil {
				t.Fatal(err)
			}
		}
		seed()
		first, second := draw(), draw()
		for _, prepared := range []bool{false, true} {
			seed()
			if got := draw(); got != first {
				t.Fatalf("first seeded draw %v want %v", got, first)
			}
			arrayRelationalRows(t, p, c, "SELECT cardinality(array_sample(ARRAY[1,2,3,4,5],3)) AS n,cardinality(array_shuffle(ARRAY[1,2,3,4,5])) AS m", prepared, nil, func(rows *sql.Rows) {
				if !rows.Next() {
					t.Fatal("missing entropy witness")
				}
				var n, m int
				if err := rows.Scan(&n, &m); err != nil {
					t.Fatal(err)
				}
				if n != 3 || m != 5 {
					t.Fatalf("counts %d %d", n, m)
				}
				if rows.Next() {
					t.Fatal("extra entropy witness")
				}
			})
			if got := draw(); got != second {
				t.Fatalf("array operations perturbed random(): %v want %v", got, second)
			}
		}
	})
}

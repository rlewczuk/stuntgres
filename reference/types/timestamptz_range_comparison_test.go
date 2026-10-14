package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL has real range operators; native intentionally admits storage only.
// Independent unequal witnesses catch the old same-descriptor/constant-zero path.
func TestReferenceTimestamptzRangeComparisonBoundary(t *testing.T) {
	for _, shape := range []string{"builtin", "domain", "nested_domain"} {
		t.Run(shape, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				operand := "tstzrange"
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				if shape != "builtin" {
					run(fixture.Exec("CREATE DOMAIN span AS tstzrange", 0))
					operand = "span"
					if shape == "nested_domain" {
						run(fixture.Exec("CREATE DOMAIN chain AS span", 0))
						operand = "chain"
					}
				}
				run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,a "+operand+",b "+operand+")", 0))
				run(fixture.Exec("CREATE TABLE vacant(a "+operand+",b "+operand+")", 0))
				run(fixture.Exec("CREATE TABLE nulls(a "+operand+",b "+operand+")", 0))
				run(fixture.Exec("INSERT INTO nulls VALUES (NULL,NULL)", 1))
				run(fixture.Exec("INSERT INTO r VALUES (1,'[2000-01-01,2000-01-02)','[2001-01-01,2001-01-02)'),(2,'[2001-01-01,2001-01-02)','[2000-01-01,2000-01-02)'),(3,'[2000-01-01,2000-01-02)','[2000-01-01,2000-01-02)'),(4,'empty','[2000-01-01,2000-01-02)'),(5,NULL,'[2000-01-01,2000-01-02)'),(6,'[2000-01-01,2000-01-02)',NULL)", 6))
				for _, tc := range []struct {
					op                   string
					less, greater, equal bool
				}{
					{"=", false, false, true}, {"<>", true, true, false},
					{"<", true, false, false}, {"<=", true, false, true},
					{">", false, true, false}, {">=", false, true, true},
				} {
					literal := "'[2000-01-01,2000-01-02)'"
					same := [][]any{{tc.less}, {tc.greater}, {tc.equal}, {tc.less}, {nil}, {nil}}
					right := [][]any{{tc.equal}, {tc.greater}, {tc.equal}, {tc.less}, {nil}, {tc.equal}}
					left := [][]any{{tc.equal}, {tc.less}, {tc.equal}, {tc.greater}, {nil}, {tc.equal}}
					allNull := [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}}
					for _, route := range []struct {
						name, left, right, leftType, rightType, relation string
						rows                                             [][]any
						args                                             []any
					}{
						{name: "columns", left: "a", right: "b", leftType: operand, rightType: operand, relation: "r ORDER BY k", rows: same},
						{name: "empty_table", left: "a", right: "b", leftType: operand, rightType: operand, relation: "vacant", rows: [][]any{}},
						{name: "null_table", left: "a", right: "b", leftType: operand, rightType: operand, relation: "nulls", rows: [][]any{{nil}}},
						{name: "unknown_right", left: "a", right: literal, leftType: operand, rightType: "unknown", relation: "r ORDER BY k", rows: right},
						{name: "unknown_left", left: literal, right: "a", leftType: "unknown", rightType: operand, relation: "r ORDER BY k", rows: left},
						{name: "null_right", left: "a", right: "NULL", leftType: operand, rightType: "unknown", relation: "r ORDER BY k", rows: allNull},
						{name: "null_left", left: "NULL", right: "a", leftType: "unknown", rightType: operand, relation: "r ORDER BY k", rows: allNull},
						{name: "bind_right", left: "a", right: "$1", leftType: operand, rightType: "unknown", relation: "r ORDER BY k", rows: right, args: []any{"[2000-01-01,2000-01-02)"}},
						{name: "bind_left", left: "$1", right: "a", leftType: "unknown", rightType: operand, relation: "r ORDER BY k", rows: left, args: []any{"[2000-01-01,2000-01-02)"}},
						{name: "nil_bind_right", left: "a", right: "$1", leftType: operand, rightType: "unknown", relation: "r ORDER BY k", rows: allNull, args: []any{nil}},
						{name: "nil_bind_left", left: "$1", right: "a", leftType: "unknown", rightType: operand, relation: "r ORDER BY k", rows: allNull, args: []any{nil}},
						{name: "empty_unknown", left: "a", right: literal, leftType: operand, rightType: "unknown", relation: "vacant", rows: [][]any{}},
						{name: "empty_null", left: "NULL", right: "b", leftType: "unknown", rightType: operand, relation: "vacant", rows: [][]any{}},
					} {
						t.Run(tc.op+"/"+route.name, func(t *testing.T) {
							q := "SELECT " + route.left + " " + tc.op + " " + route.right + " AS v FROM " + route.relation
							for _, prepared := range []bool{false, true} {
								var s fixture.Step
								if b.PG {
									// Independently expected: empty precedes nonempty; these finite ranges
									// differ first by lower endpoint, and NULL propagates.
									s = fixture.Select(q, []string{"v"}, []string{"BOOL"}, route.rows, route.args...)
								} else {
									d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+route.leftType+" "+tc.op+" "+route.rightType)
									d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
									at := strings.Index(q, " "+tc.op+" ") + 1
									d.Position = timestampRangeComparisonPosition(q, at)
									s = fixture.ErrorDiagnostic(q, d, route.args...)
									s.SelectRows, s.ErrorStage = true, "execute"
									if prepared {
										s.ErrorStage = "prepare"
									}
								}
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
							}
						})
					}
				}
				// The predicate route must also reject analysis instead of matching
				// unrelated timestamp datums. PostgreSQL matches only the equal finite row.
				q := "SELECT k FROM r WHERE a = b ORDER BY k"
				for _, prepared := range []bool{false, true} {
					var s fixture.Step
					if b.PG {
						s = fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}})
					} else {
						d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+operand+" = "+operand)
						d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
						d.Position = timestampRangeComparisonPosition(q, strings.Index(q, " = ")+1)
						s = fixture.ErrorDiagnostic(q, d)
						s.SelectRows, s.ErrorStage = true, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
					}
					s.Prepared = prepared
					run(s)
				}
				run(rangeSelect(b, "SELECT a,b FROM r WHERE k=1", []string{"a", "b"}, []string{"TSTZRANGE", "TSTZRANGE"}, [][]any{{`["2000-01-01 00:00:00+00","2000-01-02 00:00:00+00")`, `["2001-01-01 00:00:00+00","2001-01-02 00:00:00+00")`}}, []bool{true, true}))
			}
		})
	}
}

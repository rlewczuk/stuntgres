package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorSets(t *testing.T) {
	for _, f := range operatorFamilies {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.name+",b "+f.name+")", 0))
				ab, bc, ac := f.span(f.one, f.three), f.span(f.three, f.seven), f.span(f.one, f.seven)
				run(fixture.Exec("INSERT INTO op VALUES (1,"+rangeLiteral(ab)+","+rangeLiteral(bc)+"),(2,"+rangeLiteral(ac)+","+rangeLiteral(bc)+"),(3,"+rangeLiteral(ac)+","+rangeLiteral(ab)+"),(4,"+rangeLiteral(ab)+","+rangeLiteral(ab)+"),(5,"+rangeLiteral(ab)+",'empty'),(6,'empty',"+rangeLiteral(ab)+"),(7,NULL,"+rangeLiteral(ab)+")", 7))
				operatorValue(t, p, b, "a + b", "op ORDER BY k", f.name, [][]any{{operatorRangeOutput(f, ac)}, {operatorRangeOutput(f, ac)}, {operatorRangeOutput(f, ac)}, {operatorRangeOutput(f, ab)}, {operatorRangeOutput(f, ab)}, {operatorRangeOutput(f, ab)}, {nil}})
				operatorValue(t, p, b, "a * b", "op ORDER BY k", f.name, [][]any{{"empty"}, {operatorRangeOutput(f, bc)}, {operatorRangeOutput(f, ab)}, {operatorRangeOutput(f, ab)}, {"empty"}, {"empty"}, {nil}})
				operatorValue(t, p, b, "a - b", "op ORDER BY k", f.name, [][]any{{operatorRangeOutput(f, ab)}, {operatorRangeOutput(f, ab)}, {operatorRangeOutput(f, bc)}, {"empty"}, {operatorRangeOutput(f, ab)}, {"empty"}, {nil}})
				run(fixture.Exec("CREATE TABLE failures(a "+f.name+",b "+f.name+",c "+f.name+")", 0))
				run(fixture.Exec("INSERT INTO failures VALUES ("+rangeLiteral(f.span(f.one, f.nine))+","+rangeLiteral(f.span(f.three, f.seven))+","+rangeLiteral(f.span(f.seven, f.nine))+")", 1))
				for _, tc := range []struct{ expr, message string }{{"a - b", "result of range difference would not be contiguous"}, {"b + '[" + f.one + "," + f.two + ")'", "result of range union would not be contiguous"}} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("SELECT "+tc.expr+" FROM failures", fixture.ControlsReferenceDiagnostic("22000", tc.message))
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
				}
			})
		})
	}
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE ties(a numrange,b numrange,c numrange,d numrange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ties VALUES ('[1.0,2.00]','[1.000,2.0]','[1,2]','(1,2)')", 1))
		for _, tc := range []struct{ expr, want string }{{"a + b", "[1.000,2.0]"}, {"b + a", "[1.0,2.00]"}, {"a * b", "[1.0,2.00]"}, {"b * a", "[1.000,2.0]"}} {
			operatorValue(t, p, b, tc.expr, "ties", "numrange", [][]any{{tc.want}})
		}
		s := fixture.ErrorDiagnostic("SELECT c - d FROM ties", fixture.ControlsReferenceDiagnostic("22000", "result of range difference would not be contiguous"))
		s.SelectRows = true
		s.ErrorStage = "execute"
		operatorBoth(t, p, b, s)
	})
}

// Text formatting is specified independently of the provider under test.
func operatorRangeOutput(f operatorFamily, input string) string {
	if f.name != "tsrange" && f.name != "tstzrange" {
		return input
	}
	// All generated finite witnesses here use inclusive lower/exclusive upper.
	for i, c := range input {
		if c == ',' {
			return "[\"" + input[1:i] + "\",\"" + input[i+1:len(input)-1] + "\")"
		}
	}
	panic("invalid independent witness")
}

func TestReferenceMultirangeOperatorSets(t *testing.T) {
	for _, f := range operatorFamilies[:3] {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.multi()+",b "+f.multi()+")", 0))
				run(fixture.Exec("INSERT INTO op VALUES (1,'{[1,3)}','{[7,9)}'),(2,'{[1,9)}','{[3,7)}'),(3,'{[1,9)}','{[2,3),[4,5),[6,7)}'),(4,'{[1,3),[7,9)}','{[2,8)}'),(5,'{[1,3)}','{}'),(6,'{}','{[1,3)}'),(7,'{}','{}'),(8,NULL,'{[1,3)}'),(9,'{(,3)}','{[3,)}')", 9))
				for _, tc := range []struct {
					op string
					v  []any
				}{
					{"+", []any{"{[1,3),[7,9)}", "{[1,9)}", "{[1,9)}", "{[1,9)}", "{[1,3)}", "{[1,3)}", "{}", nil, "{(,)}"}},
					{"*", []any{"{}", "{[3,7)}", "{[2,3),[4,5),[6,7)}", "{[2,3),[7,8)}", "{}", "{}", "{}", nil, "{}"}},
					{"-", []any{"{[1,3)}", "{[1,3),[7,9)}", "{[1,2),[3,4),[5,6),[7,9)}", "{[1,2),[8,9)}", "{[1,3)}", "{}", "{}", nil, "{(,3)}"}},
				} {
					rows := make([][]any, len(tc.v))
					for i, v := range tc.v {
						rows[i] = []any{v}
					}
					operatorValue(t, p, b, "a "+tc.op+" b", "op ORDER BY k", f.multi(), rows)
				}
				// Re-reading operands detects accidental in-place normalization/mutation.
				operatorBoth(t, p, b, rangeSelect(b, "SELECT a AS v FROM op WHERE k=4", []string{"v"}, []string{upperOperatorName(f.multi())}, [][]any{{"{[1,3),[7,9)}"}}, []bool{true}))
			})
		})
	}
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE points(a nummultirange,b nummultirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO points VALUES ('{[1,2]}','{(1,2)}')", 1))
		operatorValue(t, p, b, "a - b", "points", "nummultirange", [][]any{{"{[1,1],[2,2]}"}})
	})
}

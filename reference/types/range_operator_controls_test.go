package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func operatorStoredRecipes(t *testing.T, p *fixture.Pair, b fixture.Backend) ([]fixture.Step, [][]bool) {
	t.Helper()
	checks := []fixture.Step{}
	nullable := [][]bool{}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN operator_span AS int4range", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN operator_chain AS operator_span", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_domains(k integer,a operator_chain,b operator_chain,g boolean GENERATED ALWAYS AS (a @> b) STORED,u int4range GENERATED ALWAYS AS (a + b) STORED)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO operator_domains(k,a,b) VALUES (1,'[1,9)','[3,7)'),(2,'[1,3)','[2,8)'),(3,NULL,NULL)", 3))
	domainCheck := rangeSelect(b, "SELECT a,g,u FROM operator_domains WHERE a @> b", []string{"a", "g", "u"}, []string{"INT4RANGE", "BOOL", "INT4RANGE"}, [][]any{{"[1,9)", true, "[1,9)"}}, []bool{true, true, true})
	operatorBoth(t, p, b, domainCheck)
	checks = append(checks, domainCheck)
	nullable = append(nullable, []bool{true, true, true})
	for i, f := range operatorFamilies {
		name := fmt.Sprintf("operator_store_%d", i)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE "+name+"(k integer,a "+f.name+",b "+f.name+",g boolean GENERATED ALWAYS AS (a @> b) STORED,u "+f.name+" GENERATED ALWAYS AS (a + b) STORED,CONSTRAINT contained CHECK (a @> b))", 0))
		a, c := f.span(f.one, f.nine), f.span(f.three, f.seven)
		run(fixture.Exec("INSERT INTO "+name+"(k,a,b) VALUES (1,"+rangeLiteral(a)+","+rangeLiteral(c)+"),(2,NULL,NULL)", 2))
		run(fixture.Exec("CREATE VIEW "+name+"_view AS SELECT k,a OPERATOR(pg_catalog.*) b AS v,a = b AS eq,g,u FROM "+name, 0))
		check := rangeSelect(b, "SELECT v,eq,g,u FROM "+name+"_view WHERE k=1", []string{"v", "eq", "g", "u"}, []string{upperOperatorName(f.name), "BOOL", "BOOL", upperOperatorName(f.name)}, [][]any{{operatorRangeOutput(f, c), false, true, operatorRangeOutput(f, a)}}, []bool{true, true, true, true})
		operatorBoth(t, p, b, check)
		checks = append(checks, check)
		nullable = append(nullable, []bool{true, true, true, true})
		// Predicate enforcement is real, and later-row failure is statement atomic.
		q := "INSERT INTO " + name + "(k,a,b) VALUES (3," + rangeLiteral(a) + "," + rangeLiteral(c) + "),(4," + rangeLiteral(c) + "," + rangeLiteral(a) + ")"
		d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \""+name+"\" violates check constraint \"contained\"")
		// Failing-row detail includes generated values, independently specified.
		d.Detail = "Failing row contains (4, " + operatorRangeOutput(f, c) + ", " + operatorRangeOutput(f, a) + ", f, " + operatorRangeOutput(f, a) + ")."
		d.Schema = "public"
		d.Table = name
		d.Constraint = "contained"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		q = "INSERT INTO " + name + "(k,a,b) VALUES (5," + rangeLiteral(a) + "," + rangeLiteral(c) + "),(6," + rangeLiteral(f.span(f.one, f.three)) + "," + rangeLiteral(f.span(f.seven, f.nine)) + ")"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22000", "result of range union would not be contiguous"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		operatorBoth(t, p, b, fixture.Select("SELECT k FROM "+name+" ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	}
	for i, f := range operatorFamilies[:3] {
		name := fmt.Sprintf("operator_multi_store_%d", i)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE "+name+"(a "+f.multi()+",b "+f.multi()+",g boolean GENERATED ALWAYS AS (a @> b) STORED,u "+f.multi()+" GENERATED ALWAYS AS (a - b) STORED,CHECK (a @> b))", 0))
		run(fixture.Exec("INSERT INTO "+name+"(a,b) VALUES ('{[1,9)}','{[3,7)}')", 1))
		run(fixture.Exec("CREATE VIEW "+name+"_view AS SELECT g,u,a && b AS has_overlap,a = b AS eq FROM "+name, 0))
		check := rangeSelect(b, "SELECT g,u,has_overlap,eq FROM "+name+"_view", []string{"g", "u", "has_overlap", "eq"}, []string{"BOOL", upperOperatorName(f.multi()), "BOOL", "BOOL"}, [][]any{{true, "{[1,3),[7,9)}", true, false}}, []bool{true, true, true, true})
		operatorBoth(t, p, b, check)
		checks = append(checks, check)
		nullable = append(nullable, []bool{true, true, true, true})
	}
	return checks, nullable
}
func upperOperatorName(name string) string {
	// Keep logical descriptor spelling independent of pq's OID inventory.
	switch name {
	case "int4range":
		return "INT4RANGE"
	case "int8range":
		return "INT8RANGE"
	case "numrange":
		return "NUMRANGE"
	case "daterange":
		return "DATERANGE"
	case "tsrange":
		return "TSRANGE"
	case "tstzrange":
		return "TSTZRANGE"
	case "int4multirange":
		return "INT4MULTIRANGE"
	case "int8multirange":
		return "INT8MULTIRANGE"
	case "nummultirange":
		return "NUMMULTIRANGE"
	}
	panic("unlisted range family")
}
func TestReferenceRangeOperatorControls(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		checks, nullable := operatorStoredRecipes(t, p, b)
		failures := []rangeStagedFailure{{"CREATE TABLE staged(a int4range,b int4range,u int4range GENERATED ALWAYS AS (a + b) STORED); INSERT INTO staged(a,b) VALUES ('[1,3)','[3,7)'); INSERT INTO staged(a,b) VALUES ('[1,3)','[7,9)');", fixture.ControlsReferenceDiagnostic("22000", "result of range union would not be contiguous")}}
		operatorControlCycle(t, p, b, checks, nullable, failures)
	})
}

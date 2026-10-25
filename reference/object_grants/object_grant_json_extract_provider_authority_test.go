package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantJSONBExtractProviderAuthority(t *testing.T) {
	objectGrantExtractProviderAuthority(t, "JSONB")
}

func TestReferenceObjectGrantJSONExtractSharedProviderAuthority(t *testing.T) {
	objectGrantExtractProviderAuthority(t, "JSON")
}

func objectGrantExtractProviderAuthority(t *testing.T, documentType string) {
	t.Helper()
	variants := []struct {
		suffix, formal, op, rhs string
		text, path              bool
	}{
		{"extract_path", "text[]", "#>", "'{s}'::TEXT[]", false, true},
		{"extract_path_text", "text[]", "#>>", "'{s}'::TEXT[]", true, true},
		{"object_field", "text", "->", "'s'::TEXT", false, false},
		{"object_field_text", "text", "->>", "'s'::TEXT", true, false},
		{"array_element", "integer", "->", "0", false, false},
		{"array_element_text", "integer", "->>", "0", true, false},
	}
	prefix, formalDocument := "jsonb_", "jsonb"
	if documentType == "JSON" {
		prefix, formalDocument = "json_", "json"
	}
	for _, tc := range variants {
		name := prefix + tc.suffix
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_jextract_source(id INT,j "+documentType+")", 0))
				doc := `{"s":"x","n":null,"a":[10,20]}`
				if tc.formal == "integer" {
					doc = `["x",null,20]`
				}
				run(fixture.Exec("INSERT INTO og_jextract_source VALUES(1,$1),(2,NULL)", 2, doc))
				run(fixture.Exec("GRANT SELECT ON og_jextract_source TO og_object_reader", 0))
				typ, value := documentType, `"x"`
				if tc.text {
					typ, value = "TEXT", "x"
				}
				selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_jextract_source"+tail, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
				}
				expr := "j " + tc.op + " " + tc.rhs
				nullExpr := "NULL::" + documentType + " " + tc.op + " " + tc.rhs
				cases := []objectGrantProviderCase{
					{selectValue(expr, " WHERE id=1", value), true, true},
					{selectValue(expr, " WHERE id=2", nil), true, true},
					{selectValue("$1::"+documentType+" "+tc.op+" "+tc.rhs, " WHERE id=1", value, doc), true, true},
					{selectValue("$1::"+documentType+" "+tc.op+" "+tc.rhs, " WHERE id=1", nil, nil), false, false},
					{selectValue(nullExpr, " WHERE id=1", nil), false, true},
					{selectValue("j "+tc.op+" $1::"+tc.formal, " WHERE id=1", nil, nil), false, false},
					{selectValue("j "+tc.op+" NULL::"+tc.formal, " WHERE id=1", nil), false, true},
					{selectValue("CASE WHEN FALSE THEN "+expr+" ELSE NULL END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_jextract_source WHERE id>99", []string{"v"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT "+expr+" AS v FROM og_jextract_source WHERE FALSE", []string{"v"}, []string{typ}, nil), true, true},
				}
				if tc.path {
					var jsonNull any = "null"
					if tc.text {
						jsonNull = nil
					}
					cases = append(cases,
						objectGrantProviderCase{selectValue(name+"(j,'s')", " WHERE id=1", value), true, true},
						objectGrantProviderCase{selectValue(name+"(j,'a','-1')", " WHERE id=1", "20"), true, true},
						objectGrantProviderCase{selectValue(name+"(j,VARIADIC $1::TEXT[])", " WHERE id=1", value, "{s}"), true, true},
						objectGrantProviderCase{selectValue(name+"(from_json=>j,VARIADIC path_elems=>$1::TEXT[])", " WHERE id=1", value, "{s}"), true, true},
						objectGrantProviderCase{selectValue(name+"(j,VARIADIC $1::TEXT[])", " WHERE id=1", nil, nil), false, false},
						objectGrantProviderCase{selectValue(name+"(j,VARIADIC NULL::TEXT[])", " WHERE id=1", nil), false, true},
						objectGrantProviderCase{selectValue(name+"(j,'s',NULL::TEXT)", " WHERE id=1", nil), true, true},
						objectGrantProviderCase{selectValue(name+"(j,$1::TEXT)", " WHERE id=1", nil, nil), true, true},
						objectGrantProviderCase{selectValue(name+"(j,VARIADIC '{s,NULL}'::TEXT[])", " WHERE id=1", nil), true, true},
						objectGrantProviderCase{selectValue(name+"(j,'missing')", " WHERE id=1", nil), true, true},
						objectGrantProviderCase{selectValue(name+"(j,'n')", " WHERE id=1", jsonNull), true, true},
						objectGrantProviderCase{selectValue(name+"($1::"+documentType+",VARIADIC '{}'::TEXT[])", " WHERE id=1", value, `"x"`), true, true},
					)
				}
				otherOp, otherType, otherValue := "->>", "TEXT", "x"
				if tc.text {
					otherOp, otherType, otherValue = "->", documentType, `"x"`
				}
				otherRHS := "'s'::TEXT"
				if tc.formal == "integer" {
					otherRHS = "0"
				}
				if tc.path {
					otherOp = "#>>"
					if tc.text {
						otherOp = "#>"
					}
					otherRHS = "'{s}'::TEXT[]"
				}
				control := fixture.Select("SELECT j "+otherOp+" "+otherRHS+" AS v FROM og_jextract_source WHERE id=1", []string{"v"}, []string{otherType}, [][]any{{otherValue}})
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+name+"("+formalDocument+","+tc.formal+")", "function", name, cases, []fixture.Step{control})
			})
		})
	}
}

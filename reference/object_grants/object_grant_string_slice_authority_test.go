package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantStringSliceAuthority(t *testing.T) {
	// These are the eighteen released numeric/end declarations, not defaults
	// expanded from a longest signature. The twelve BYTEA/BIT/overlay entries
	// reuse their existing providers; only six TEXT declarations are newly live.
	type declaration struct {
		target, family string
		args           []string
		want           any
		forms          []string
		siblings       []int
	}
	declarations := []declaration{
		{"substring(text,integer,integer)", "text", []string{"s", "i", "n"}, "é🙂", []string{"substring(s FROM i FOR n)", "substring(s FOR n FROM i)", `"substring"(s,i,n)`}, []int{1, 2, 4, 8}},
		{"substring(text,integer)", "text", []string{"s", "i"}, "é🙂Z", []string{"substring(s FROM i)", `"substring"(s,i)`}, []int{0, 3, 5, 9}},
		{"substr(text,integer,integer)", "text", []string{"s", "i", "n"}, "é🙂", []string{`"substr"(s,i,n)`}, []int{0, 3, 6}},
		{"substr(text,integer)", "text", []string{"s", "i"}, "é🙂Z", []string{`"substr"(s,i)`}, []int{1, 2, 7}},
		{"substring(bytea,integer,integer)", "bytea", []string{"s", "i", "n"}, "\xc3\xa9", []string{"substring(s FROM i FOR n)", "substring(s FOR n FROM i)"}, []int{0, 5, 6, 8}},
		{"substring(bytea,integer)", "bytea", []string{"s", "i"}, "\xc3\xa9\xff\x00AB\x00", []string{"substring(s FROM i)"}, []int{1, 4, 7, 9}},
		{"substr(bytea,integer,integer)", "bytea", []string{"s", "i", "n"}, "\xc3\xa9", []string{`"substr"(s,i,n)`}, []int{2, 4, 7}},
		{"substr(bytea,integer)", "bytea", []string{"s", "i"}, "\xc3\xa9\xff\x00AB\x00", []string{`"substr"(s,i)`}, []int{3, 5, 6}},
		{"substring(bit,integer,integer)", "bit", []string{"s", "i", "n"}, "01", []string{"substring(s FROM i FOR n)", "substring(s FOR n FROM i)", `"substring"(s,i,n)`}, []int{0, 4, 9}},
		{"substring(bit,integer)", "bit", []string{"s", "i"}, "0110", []string{"substring(s FROM i)", `"substring"(s,i)`}, []int{1, 5, 8}},
		{"overlay(text,text,integer,integer)", "text", []string{"s", "r", "i", "n"}, "abXYef", []string{"overlay(s PLACING r FROM i FOR n)", `"overlay"(s,r,i,n)`}, []int{11, 12, 14}},
		{"overlay(text,text,integer)", "text", []string{"s", "r", "i"}, "abXYef", []string{"overlay(s PLACING r FROM i)", `"overlay"(s,r,i)`}, []int{10, 13, 15}},
		{"overlay(bytea,bytea,integer,integer)", "bytea", []string{"s", "r", "i", "n"}, "\x00\xc3XY\x00AB\x00", []string{"overlay(s PLACING r FROM i FOR n)"}, []int{10, 13, 14}},
		{"overlay(bytea,bytea,integer)", "bytea", []string{"s", "r", "i"}, "\x00\xc3XY\x00AB\x00", []string{"overlay(s PLACING r FROM i)"}, []int{11, 12, 15}},
		{"overlay(bit,bit,integer,integer)", "bit", []string{"s", "r", "i", "n"}, "10010", []string{"overlay(s PLACING r FROM i FOR n)", `"overlay"(s,r,i,n)`}, []int{10, 12, 15}},
		{"overlay(bit,bit,integer)", "bit", []string{"s", "r", "i"}, "10010", []string{"overlay(s PLACING r FROM i)", `"overlay"(s,r,i)`}, []int{11, 13, 14}},
		{"left(text,integer)", "text", []string{"s", "n"}, "Aé", []string{`"left"(s,n)`}, []int{0, 1, 17}},
		{"right(text,integer)", "text", []string{"s", "n"}, "🙂Z", []string{`"right"(s,n)`}, []int{0, 1, 16}},
	}
	// Physical VARCHAR/SMALLINT/VARBIT inputs must select the released
	// TEXT/INT4/BIT formals. In particular, no VARBIT slicing declaration exists.
	for _, tc := range declarations {
		t.Run(tc.target, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				name := strings.Split(tc.target, "(")[0]
				overlay := name == "overlay"
				sourceType, resultType := "VARCHAR", "TEXT"
				source, replacement, sourceArg := "'Aé🙂Z'", "'XY'", any("Aé🙂Z")
				start := "2"
				if overlay {
					source, sourceArg, start = "'abcdef'", "abcdef", "3"
				}
				if tc.family == "bytea" {
					sourceType, resultType = "BYTEA", "BYTEA"
					source, replacement = "'\\x00c3a9ff00414200'::BYTEA", "'\\x5859'::BYTEA"
					sourceArg = []byte{0, 0xc3, 0xa9, 0xff, 0, 0x41, 0x42, 0}
				} else if tc.family == "bit" {
					sourceType, resultType = "VARBIT", "BIT"
					source, replacement, sourceArg, start = "B'10110'", "B'00'", "10110", "2"
				}
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_slice_authority(id INTEGER,s "+sourceType+",r "+sourceType+",i SMALLINT,n SMALLINT)", 0))
				run(fixture.Exec("INSERT INTO og_slice_authority VALUES(1,"+source+","+replacement+","+start+",2)", 1))
				// One runtime-NULL row per actual IN formal; NULLs in a row are
				// not a planning-time proof that the selected call disappears.
				for j, arg := range tc.args {
					values := []string{source, replacement, start, "2"}
					pos := map[string]int{"s": 0, "r": 1, "i": 2, "n": 3}[arg]
					values[pos] = "NULL"
					run(fixture.Exec("INSERT INTO og_slice_authority VALUES("+strconv.Itoa(j+2)+","+strings.Join(values, ",")+")", 1))
				}
				run(fixture.Exec("GRANT SELECT ON og_slice_authority TO og_object_reader", 0))
				call := name + "(" + strings.Join(tc.args, ",") + ")"
				run(fixture.Exec("CREATE VIEW og_slice_authority_view AS SELECT "+call+" AS v FROM og_slice_authority WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_slice_authority_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_slice_authority"+tail, []string{"v"}, []string{resultType}, [][]any{{want}}, args...)
				}
				nullSource, nullResult, parameterSource := "NULL::"+sourceType, "NULL::"+resultType, "$1::"+sourceType
				if tc.family == "bit" {
					// Infer logical BIT types without enabling unsupported explicit casts.
					nullSource, nullResult, parameterSource = "CASE WHEN FALSE THEN s ELSE NULL END", "NULL", "$1"
				}
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", tc.want), true, true},
					{value("pg_catalog."+call, " WHERE id=1", tc.want), true, true},
					{fixture.Select("SELECT v FROM og_slice_authority_view", []string{"v"}, []string{resultType}, [][]any{{tc.want}}), true, true},
					{value("CASE WHEN id=1 THEN "+call+" ELSE "+nullResult+" END", " WHERE id=1", tc.want), true, true},
					{value("CASE WHEN FALSE THEN "+call+" ELSE "+nullResult+" END", " WHERE id=1", nil), false, true},
					{value("COALESCE("+nullResult+","+call+")", " WHERE id=1", tc.want), true, true},
					{value("COALESCE("+source+","+call+")", " WHERE id=1", fixture.Normalize(sourceArg)), false, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_slice_authority WHERE id>99", []string{"v"}, []string{resultType}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_slice_authority WHERE FALSE", []string{"v"}, []string{resultType}, nil), true, true},
				}
				for _, form := range tc.forms {
					cases = append(cases, objectGrantProviderCase{value(form, " WHERE id=1", tc.want), true, true})
				}
				literal := map[string]string{"s": source, "r": replacement, "i": start + "::INT4", "n": "2::INT4"}
				constantArgs := make([]string, len(tc.args))
				for j, arg := range tc.args {
					constantArgs[j] = literal[arg]
					cases = append(cases, objectGrantProviderCase{value(call, " WHERE id="+strconv.Itoa(j+2), nil), true, true})
					nullArgs := append([]string(nil), tc.args...)
					typ := "INT4"
					if arg == "s" || arg == "r" {
						typ = sourceType
					}
					nullArgs[j] = "NULL::" + typ
					if tc.family == "bit" && (arg == "s" || arg == "r") {
						nullArgs[j] = "CASE WHEN FALSE THEN " + arg + " ELSE NULL END"
					}
					cases = append(cases, objectGrantProviderCase{value(name+"("+strings.Join(nullArgs, ",")+")", " WHERE id=1", nil), false, true})
				}
				// Fresh immutable constants are deliberately not SAME-held tests:
				// PostgreSQL may retain a legitimately folded old result.
				cases = append(cases, objectGrantProviderCase{value(name+"("+strings.Join(constantArgs, ",")+")", " WHERE id=1", tc.want), true, false})
				parameterArgs := append([]string(nil), tc.args...)
				parameterArgs[0] = "CASE WHEN id=1 THEN " + parameterSource + " ELSE s END"
				cases = append(cases, objectGrantProviderCase{value(name+"("+strings.Join(parameterArgs, ",")+")", " WHERE id=1", tc.want, sourceArg), true, true})
				parameterArgs[0] = parameterSource
				if tc.family == "bit" {
					parameterArgs[0] = "CASE WHEN TRUE THEN $1 ELSE s END"
				}
				cases = append(cases, objectGrantProviderCase{value(name+"("+strings.Join(parameterArgs, ",")+")", " WHERE id=1", nil, nil), false, false})
				// FOR-only supplies start=1 and selects the long declaration. A
				// revoked short overload must not deny it, or conversely authorize it.
				if name == "substring" {
					var first any = "Aé"
					if tc.family == "bytea" {
						first = "\x00\xc3"
					} else if tc.family == "bit" {
						first = "10"
					}
					cases = append(cases, objectGrantProviderCase{value("substring(s FOR n)", " WHERE id=1", first), len(tc.args) == 3, true})
				}
				if name == "left" || name == "right" {
					negative := "Aé"
					if name == "right" {
						negative = "🙂Z"
					}
					cases = append(cases, objectGrantProviderCase{value(name+"(s,-n)", " WHERE id=1", negative), true, true})
				}
				if overlay && len(tc.args) == 4 {
					var zero, negative any = "abXYcdef", "abXYabcdef"
					negativeCall := "overlay(s,r,i,-n)"
					if tc.family == "bytea" {
						zero = "\x00\xc3XY\xa9\xff\x00AB\x00"
						negative = "\x00\xc3XY\x00\xc3\xa9\xff\x00AB\x00"
					} else if tc.family == "bit" {
						zero, negative, negativeCall = "1000110", "10000110", "overlay(s,r,i+1,-1)"
					}
					cases = append(cases,
						objectGrantProviderCase{value("overlay(s,r,i,0)", " WHERE id=1", zero), true, true},
						objectGrantProviderCase{value(negativeCall, " WHERE id=1", negative), true, true},
					)
				}
				// Real kernel errors return after granting EXECUTE; admission
				// precedes a surviving row call's invalid count/start.
				badArgs := append([]string(nil), tc.args...)
				badCall := ""
				if (name == "substring" || name == "substr") && len(tc.args) == 3 {
					badArgs[2] = "-n"
					badCall = name + "(" + strings.Join(badArgs, ",") + ")"
				} else if overlay {
					badArgs[2] = "i-i"
					badCall = name + "(" + strings.Join(badArgs, ",") + ")"
				}
				if badCall != "" {
					s := fixture.ImplicitError("SELECT "+badCall+" AS v FROM og_slice_authority WHERE id=1", fixture.Diagnostic{Code: "22011", Message: "negative substring length not allowed"})
					s.SelectRows, s.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{s, true, true})
				}
				if overlay && len(tc.args) == 4 {
					s := fixture.ImplicitError("SELECT overlay(s,r,2147483647,n) AS v FROM og_slice_authority WHERE id=1", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
					s.SelectRows, s.ErrorStage = true, "execute"
					cases = append(cases, objectGrantProviderCase{s, true, true})
				}
				if (name == "substring" || name == "substr") && len(tc.args) == 3 {
					s := fixture.ImplicitError("SELECT "+name+"("+source+",1::INT4,-1::INT4) AS v WHERE FALSE", fixture.Diagnostic{Code: "22011", Message: "negative substring length not allowed"})
					s.SelectRows = true
					cases = append(cases, objectGrantProviderCase{s, true, false})
				}
				// Runtime NULL cannot suppress demanded argument evaluation. A
				// constant child error is planned even inside a literal-NULL call;
				// unlike a runtime error, it can precede the outer EXECUTE check.
				childArgs := append([]string(nil), tc.args...)
				childArgs[len(childArgs)-1] = "1/(i-i)"
				child := fixture.ImplicitError("SELECT "+name+"("+strings.Join(childArgs, ",")+") AS v FROM og_slice_authority WHERE id=2", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				child.SelectRows, child.ErrorStage = true, "execute"
				cases = append(cases, objectGrantProviderCase{child, true, true})
				childArgs[0], childArgs[len(childArgs)-1] = nullSource, "1/0"
				constantChild := fixture.ImplicitError("SELECT "+name+"("+strings.Join(childArgs, ",")+") AS v FROM og_slice_authority WHERE id=1", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				constantChild.SelectRows = true
				cases = append(cases, objectGrantProviderCase{constantChild, false, false})
				controls := []fixture.Step{}
				for _, sibling := range tc.siblings {
					other := declarations[sibling]
					otherName := strings.Split(other.target, "(")[0]
					s, r, i := "'Aé🙂Z'::TEXT", "'XY'::TEXT", "2::INT4"
					typ := "TEXT"
					if otherName == "overlay" {
						s, i = "'abcdef'::TEXT", "3::INT4"
					}
					if other.family == "bytea" {
						s, r, typ = "'\\x00c3a9ff00414200'::BYTEA", "'\\x5859'::BYTEA", "BYTEA"
					} else if other.family == "bit" {
						s, r, i, typ = "B'10110'", "B'00'", "2::INT4", "BIT"
					}
					actuals := make([]string, len(other.args))
					for j, arg := range other.args {
						actuals[j] = map[string]string{"s": s, "r": r, "i": i, "n": "2::INT4"}[arg]
					}
					controls = append(controls, fixture.Select("SELECT pg_catalog."+otherName+"("+strings.Join(actuals, ",")+") AS v", []string{"v"}, []string{typ}, [][]any{{other.want}}))
				}
				// Existing regex and POSITION identities are selection controls,
				// not another copy of their owning comprehensive value suites.
				controls = append(controls, fixture.Select("SELECT substring('abc'::TEXT,'b'::TEXT) AS regex,position('cd' IN 'abcdef') AS text_pos,pg_catalog.position('\\x1234567890'::BYTEA,'\\x5678'::BYTEA) AS byte_pos,position(B'010' IN B'000001101011') AS bit_pos", []string{"regex", "text_pos", "byte_pos", "bit_pos"}, []string{"TEXT", "INT4", "INT4", "INT4"}, [][]any{{"b", int64(3), int64(3), int64(8)}}))
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+tc.target, "function", name, cases, controls)
			})
		})
	}

	// Overlay's C slicing/length/catenation helpers do not become SQL ACL
	// dependencies. Each genuine SQL helper is denied independently, while
	// row-backed overlays of all families keep their own authority.
	for _, target := range []string{
		"length(text)", "length(bytea)", "length(bit)",
		"substring(text,integer,integer)", "substring(bytea,integer,integer)", "substring(bit,integer,integer)",
		"textcat(text,text)", "byteacat(bytea,bytea)", "bitcat(bit varying,bit varying)",
	} {
		t.Run("C_helper_independence/"+target, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog." + target})
				run(fixture.Exec("CREATE TABLE og_overlay_helpers(s TEXT,b BYTEA,v VARBIT)", 0))
				run(fixture.Exec("INSERT INTO og_overlay_helpers VALUES('abcdef','\\x00c3a9ff00414200',B'10110')", 1))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("GRANT SELECT ON og_overlay_helpers TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+target+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(fixture.Select("SELECT overlay(s,'XY',3) AS t,overlay(b,'\\x5859'::BYTEA,3) AS b,overlay(v,B'00',2) AS v FROM og_overlay_helpers", []string{"t", "b", "v"}, []string{"TEXT", "BYTEA", "BIT"}, [][]any{{"abXYef", "\x00\xc3XY\x00AB\x00", "10010"}}))
				})
			})
		})
	}

	// Reuse the original SQL-body consumer: the released three-TEXT wrapper
	// really invokes 1986 then 2073. Numeric forms must remain independent.
	for _, tc := range []objectGrantBodyCase{
		{
			name: "slice_similar_escape_isolation", columns: "s TEXT,p TEXT,e TEXT", values: `'abc','a#"b#"c','#'`,
			expression: "substring(s,p,e)", nullExpression: `substring(NULL::TEXT,'a#"b#"c','#')`, deadExpression: "CASE WHEN FALSE THEN substring(s,p,e) ELSE 'b' END",
			resultType: "TEXT", value: "b", target: "pg_catalog.similar_to_escape(text,text)", deniedName: "similar_to_escape",
			primitive: "s SIMILAR TO '%' ESCAPE e", primitiveType: "BOOL", primitiveValue: true,
			independent: "substring(s,2::INT4,1::INT4)", independentType: "TEXT", independentValue: "b",
		},
		{
			name: "slice_regex_body_isolation", columns: "s TEXT,p TEXT,e TEXT", values: `'abc','a#"b#"c','#'`,
			expression: "substring(s,p,e)", nullExpression: `substring(NULL::TEXT,'a#"b#"c','#')`, deadExpression: "CASE WHEN FALSE THEN substring(s,p,e) ELSE 'b' END",
			resultType: "TEXT", value: "b", target: "pg_catalog.substring(text,text)", deniedName: "substring",
			primitive: "substring(s,'b')", primitiveType: "TEXT", primitiveValue: "b",
			independent: "substring(s,2::INT4)", independentType: "TEXT", independentValue: "bc",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}

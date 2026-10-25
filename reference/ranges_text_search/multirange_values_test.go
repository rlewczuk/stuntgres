package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeValues(t *testing.T) {
	integer := [][2]string{
		{"{[1,3),[2,4),empty,[8,9)}", "{[1,4),[8,9)}"},
		{"{[8,9),[2,4),[1,3)}", "{[1,4),[8,9)}"},
		{"{[1,3),[3,5),[5,6)}", "{[1,6)}"},
		{"{[1,10),[3,4),[1,10)}", "{[1,10)}"},
		{"{[1,2),[3,4)}", "{[1,2),[3,4)}"},
		{"{[1,2],[3,4]}", "{[1,5)}"},
		{"{[4,4),empty,(4,5)}", "{}"}, {"{}", "{}"},
		{" \t{ EMPTY , empty }\r\n", "{}"},
		{"{[,],[1,3)}", "{(,)}"}, {"{(,2),[2,)}", "{(,)}"},
		{"{(,2),[3,)}", "{(,2),[3,)}"},
		{`{["1","4"]}`, "{[1,5)}"}, {`{["1"2,14)}`, "{[12,14)}"},
		{`{[\1,4]}`, "{[1,5)}"},
	}
	numeric := [][2]string{
		{"{(4,5)}", "{(4,5)}"},
		{"{[1.0,2.00),[2.000,3.0)}", "{[1.0,3.0)}"},
		{"{[1,2),[2,3)}", "{[1,3)}"}, {"{[1,2],(2,3]}", "{[1,3]}"},
		{"{[1,2],[2,3]}", "{[1,3]}"}, {"{[1,2),(2,3]}", "{[1,2),(2,3]}"},
		{"{[1.0,1.00]}", "{[1.0,1.00]}"}, {"{[1.0,1.00)}", "{}"},
		{"{(1,4),[1,3)}", "{[1,4)}"},
		{"{[1.0,5.0),[1.00,3.00)}", "{[1.0,5.0)}"},
		{"{[1.0,5.0),[2.00,5.00)}", "{[1.0,5.00)}"},
		{"{[1.0,3.00),[1.000,4.0)}", "{[1.000,4.0)}"},
		{"{[1.000,4.0),[1.0,3.00)}", "{[1.000,4.0)}"},
		{"{[1.0,4.00),[2.000,4.000)}", "{[1.0,4.000)}"},
		{"{[2.000,4.000),[1.0,4.00)}", "{[1.0,4.000)}"},
		{"{[1.0,4.00),[2.000,3.0)}", "{[1.0,4.00)}"},
		{"{[9007199254740992.2,9007199254740992.3),[9007199254740992.1,9007199254740992.2)}", "{[9007199254740992.1,9007199254740992.3)}"},
		{"{[9007199254740992.1,9007199254740992.2),[9007199254740992.2,9007199254740992.3)}", "{[9007199254740992.1,9007199254740992.3)}"},
		{"{[9007199254740992.1,9007199254740992.2),[9007199254740992.3,9007199254740992.4)}", "{[9007199254740992.1,9007199254740992.2),[9007199254740992.3,9007199254740992.4)}"},
		{"{[-Infinity,Infinity]}", "{[-Infinity,Infinity]}"},
		{"{[Infinity,NaN]}", "{[Infinity,NaN]}"}, {"{[NaN,NaN]}", "{[NaN,NaN]}"},
		{"{(,),[-Infinity,NaN]}", "{(,)}"},
	}
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", typ, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					create := fixture.Exec("CREATE TABLE r(k integer,a pg_catalog."+typ+")", 0)
					create.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, create)
					cases := append([][2]string(nil), integer...)
					if typ == "nummultirange" {
						cases = numeric
					} else {
						min, max := "-2147483648", "2147483647"
						if typ == "int8multirange" {
							min, max = "-9223372036854775808", "9223372036854775807"
						}
						for _, member := range []string{"[" + min + "," + max + ")", "[" + max + ",)", "(," + min + ")"} {
							cases = append(cases, [2]string{"{" + member + "}", "{" + member + "}"})
						}
						cases = append(cases, [2]string{"{[" + max + "," + max + ")}", "{}"})
					}
					want := make([][]any, 0, len(cases)+1)
					for k, tc := range cases {
						s := fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES (%d,%s)", k, rangeLiteral(tc[0])), 1)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						want = append(want, []any{int64(k), tc[1]})
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (100,NULL)", 1))
					want = append(want, []any{int64(100), nil})
					s := rangeSelect(backend, "SELECT k,a FROM r ORDER BY k", []string{"k", "a"}, []string{"INT4", strings.ToUpper(typ)}, want, []bool{true, true})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			})
		}
	}
}

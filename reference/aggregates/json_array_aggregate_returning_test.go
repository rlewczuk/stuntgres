package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateReturningCore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ clause, typ string }{
		{"json", "JSON"}, {"jsonb", "JSONB"}, {"text", "TEXT"}, {"bytea", "BYTEA"},
		{"bytea FORMAT JSON ENCODING UTF8", "BYTEA"},
	} {
		t.Run(tc.clause, func(t *testing.T) {
			for _, c := range []struct{ policy, where string; want any }{
				{"", "g='a'", "[2, 1]"}, {" NULL ON NULL", "g='a'", "[2, null, 1]"},
				{"", "g='b'", "[]"}, {" NULL ON NULL", "g='b'", "[null]"},
				{"", "FALSE", nil}, {" NULL ON NULL", "FALSE", nil},
			} {
				t.Run(c.policy+"/"+c.where, func(t *testing.T) {
					q := "SELECT json_arrayagg(v ORDER BY id"+c.policy+" RETURNING "+tc.clause+") AS v FROM ja_input WHERE "+c.where
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, tc.typ, c.want))
				})
			}
		})
	}
	for _, tc := range []struct{ expr, typ, want string }{
		{`json_arrayagg('[1]'::text)`, "JSON", `["[1]"]`},
		{`json_arrayagg('[1]'::text FORMAT JSON)`, "JSON", `[[1]]`},
		{`json_arrayagg('[1]'::bytea FORMAT JSON)`, "JSON", `[[1]]`},
		{`json_arrayagg('[1]'::bytea FORMAT JSON ENCODING UTF8)`, "JSON", `[[1]]`},
		{`json_arrayagg('[1]'::json FORMAT JSON)`, "JSON", `[[1]]`},
		{`json_arrayagg('[1]'::jsonb FORMAT JSON)`, "JSONB", `[[1]]`},
	} {
		t.Run(tc.expr, func(t *testing.T) { jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.want)) })
	}
}

func TestReferenceJSONArrayAggregateReturning(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonArrayAggregateSetup(t, p, b)
		jsonArrayAggregateReturningCore(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN ja_json AS json", 0),
			fixture.Exec("CREATE DOMAIN ja_jsonb AS jsonb", 0),
			fixture.Exec("CREATE DOMAIN ja_text AS text", 0),
			fixture.Exec("CREATE DOMAIN ja_varchar AS varchar(3)", 0),
		} { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, tc := range []struct{ expr, from, typ string; want any }{
			{`json_arrayagg(b ORDER BY id)`, " FROM ja_input WHERE g='a'", "JSONB", `[{"a": 2, "z": 1}, null]`},
			{`json_arrayagg(b::ja_jsonb ORDER BY id)`, " FROM ja_input WHERE g='a'", "JSON", `[{"a": 2, "z": 1}, null]`},
			{`json_arrayagg(j ORDER BY id)`, " FROM ja_input WHERE g='a'", "JSON", `[ {"z":1,"a":1,"a":2} , null]`},
			{`json_arrayagg('x')`, "", "JSON", `["x"]`},
			{`json_arrayagg(NULL)`, "", "JSON", `[]`},
			{`json_arrayagg(NULL NULL ON NULL)`, "", "JSON", `[null]`},
			{`json_arrayagg(b ORDER BY id RETURNING json)`, " FROM ja_input WHERE g='a'", "JSON", `[{"a": 2, "z": 1}, null]`},
			{`json_arrayagg(j ORDER BY id RETURNING jsonb FORMAT JSON)`, " FROM ja_input WHERE g='a'", "JSONB", `[{"a": 2, "z": 1}, null]`},
			{`json_arrayagg(1 RETURNING varchar(3))`, "", "VARCHAR", `[1]`},
			{`json_arrayagg(1 RETURNING char(5))`, "", "BPCHAR", `[1]  `},
			{`json_arrayagg(1 RETURNING name)`, "", "NAME", `[1]`},
			{`json_arrayagg(1 RETURNING ja_json)`, "", "JSON", `[1]`},
			{`json_arrayagg(1 RETURNING ja_jsonb)`, "", "JSONB", `[1]`},
			{`json_arrayagg(1 RETURNING ja_text)`, "", "TEXT", `[1]`},
			{`json_arrayagg(1 RETURNING ja_varchar)`, "", "VARCHAR", `[1]`},
			{`json_arrayagg(1 RETURNING varchar(2))`, " WHERE FALSE", "VARCHAR", nil},
		} {
			t.Run(tc.expr+tc.from, func(t *testing.T) { jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+tc.expr+" AS v"+tc.from, tc.typ, tc.want)) })
		}
		for _, tc := range []struct{ expr, code, message string }{
			{`json_arrayagg(1 RETURNING varchar(2))`, "22001", "value too long for type character varying(2)"},
			{`json_arrayagg(1 RETURNING char(2))`, "22001", "value too long for type character(2)"},
		} {
			t.Run(tc.expr+"/failure", func(t *testing.T) {
				q := "SELECT "+tc.expr+" AS v"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				jsonArrayAggregateError(t, p, b, q, d, false)
			})
		}
		jsonArrayAggregateConstrainedReturning(t,p,b)
	})
}

func jsonArrayAggregateConstrainedReturning(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _, declaration:=range []struct{q,token string}{
		{"CREATE DOMAIN ja_notnull AS json NOT NULL","NOT NULL"},
		{"CREATE DOMAIN ja_only_empty AS text CHECK(VALUE='[]')","CHECK"},
	} {
		if b.PG {
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec(declaration.q,0))
		} else {
			d:=fixture.ControlsReferenceDiagnostic("0A000","domain constraints and defaults are unsupported")
			d.Position=jsonArrayAggregatePosition(declaration.q,declaration.token)
			jsonArrayAggregateError(t,p,b,declaration.q,d,true)
		}
	}
	if !b.PG {
		// This independently witnesses the preexisting declaration boundary,
		// not a skip or a claim of native domain constraint validation.
		for _, name:=range []string{"ja_notnull","ja_only_empty"} {
			q:="SELECT NULL::"+name+" AS v"
			d:=fixture.ControlsReferenceDiagnostic("42704",`type "`+name+`" does not exist`)
			d.Position=jsonArrayAggregatePosition(q,name)
			jsonArrayAggregateError(t,p,b,q,d,true)
		}
		return
	}
	for _, tc:=range []struct{expr,from string;want any}{
		{"json_arrayagg(1 RETURNING ja_only_empty)"," WHERE FALSE",nil},
		{"json_arrayagg(NULL RETURNING ja_only_empty)","","[]"},
	} {
		jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect("SELECT "+tc.expr+" AS v"+tc.from,"TEXT",tc.want))
	}
	for _, tc:=range []struct{q,code,message,datatype,constraint string}{
		{"SELECT json_arrayagg(1 RETURNING ja_only_empty) AS v","23514",`value for domain ja_only_empty violates check constraint "ja_only_empty_check"`,"ja_only_empty","ja_only_empty_check"},
		{"SELECT json_arrayagg(1 RETURNING ja_notnull) AS v WHERE FALSE","23502","domain ja_notnull does not allow null values","ja_notnull",""},
	} {
		d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
		d.Schema,d.DataType,d.Constraint="public",tc.datatype,tc.constraint
		jsonArrayAggregateError(t,p,b,tc.q,d,false)
	}
}

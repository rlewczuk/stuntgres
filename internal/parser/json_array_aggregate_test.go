package parser

import (
	"strings"
	"strconv"
	"testing"
	"unicode/utf8"
	"stuntgres/internal/sqlstate"
)

func TestJSONArrayAggregateOrdinarySyntax(t *testing.T) {
	for _, name:=range []string{"json_agg","jsonb_agg","json_agg_strict","jsonb_agg_strict"} {
		for _, prefix:=range []string{"","pg_catalog.","public."} {
			for _, suffix:=range []string{"()","(*)","(1)","(1,2)","(DISTINCT v ORDER BY v) FILTER(WHERE TRUE)","(v) OVER()"} {
				q:="/*é😀*/ SELECT "+prefix+name+suffix+" FROM t"
				statements,err:=Parse(q);if err!=nil||len(statements)!=1{t.Fatal(q,err)}
				call,ok:=statements[0].(*Select).Targets[0].(AggregateExpr)
				if !ok||call.Name!=name||call.JSON!=nil||call.Pos.Character!=15||(prefix!=""&&call.Schema==nil){t.Fatal("ordinary provenance",q,call)}
			}
		}
		for _, call:=range []string{name+"(missing)",name+"(1,2)",name+"(1) FILTER(WHERE 2)"} {
			if statements,err:=Parse("INSERT INTO t VALUES("+call+")");err!=nil||len(statements)!=1{t.Fatal("VALUES analysis lost",call,err)}
		}
	}
}
func TestJSONArrayAggregateQuotedCallIdentity(t *testing.T) {
	for _, name:=range []string{"JSON_AGG","Json_Agg","JSONB_AGG","Jsonb_Agg","JSON_AGG_STRICT","Json_Agg_Strict","JSONB_AGG_STRICT","Jsonb_Agg_Strict"} {
		for _, suffix:=range []string{"(7)","(ROW(7)::ja_projection)","(DISTINCT v ORDER BY v)"} {
			q:=`SELECT "`+name+`"`+suffix
			statements,err:=Parse(q)
			if err!=nil||len(statements)!=1{t.Fatal(q,statements,err)}
			call,ok:=statements[0].(*Select).Targets[0].(FunctionExpr)
			// CompositeCandidate governs the generic scalar capability diagnostic,
			// not ordinary exact-name field fallback eligibility.
			if !ok||len(call.Name.Parts)!=1||call.Name.Parts[0].Text!=name||call.ResolutionOnly||call.CompositeCandidate {
				t.Fatal("quoted exact-name ordinary resolution lost",q,call)
			}
		}
	}
}
func TestJSONArrayAggregateKeywordNameContexts(t *testing.T) {
	for _, q:=range []string{
		"CREATE TABLE json_arrayagg (x INT)",
		"CREATE TABLE json_arrayagg (format JSON, encoding TEXT, absent INT)",
		"CREATE TABLE json_arrayagg.t (x INT)",
		"CREATE TABLE t (json_arrayagg INT)",
		"CREATE INDEX json_arrayagg ON t (json_arrayagg)",
		"INSERT INTO json_arrayagg (format,encoding,absent) VALUES('{}','UTF8',1)",
		"SELECT json_arrayagg, format FROM json_arrayagg",
		"SELECT x AS json_arrayagg FROM t",
		"SELECT x FROM t AS json_arrayagg (x)",
		"SELECT json_arrayagg(format FORMAT JSON RETURNING json) FROM json_arrayagg",
		"SELECT json_arrayagg(json_arrayagg(format FORMAT JSON) FORMAT JSON) FROM json_arrayagg",
	} {
		statements,err:=Parse(q)
		if err!=nil||len(statements)!=1{t.Fatal(q,statements,err)}
	}
}
func TestJSONArrayAggregateSQLJSONSyntax(t *testing.T) {
	const q="/*é😀*/ SELECT json_arrayagg($1::text FORMAT JSON ORDER BY id DESC NULLS LAST NULL ON NULL RETURNING varchar(12) FORMAT JSON) FILTER(WHERE flag) FROM t"
	statements,err:=Parse(q);if err!=nil||len(statements)!=1{t.Fatal(err)}
	call,ok:=statements[0].(*Select).Targets[0].(AggregateExpr)
	if !ok||call.JSON==nil||call.Name!="json_arrayagg"||len(call.Args)!=1||len(call.Order)!=1||call.Filter==nil{t.Fatal("missing SQL/JSON children",call)}
	options:=call.JSON
	if options.Absent||options.Input==nil||options.Output==nil||options.Returning==nil||options.Returning.Name!="varchar"||len(options.Returning.Modifiers)!=1||*options.Returning.Modifiers[0].Integer!=12{t.Fatal("missing positioned clauses",options)}
	for _, tc:=range []struct{p Position;token string}{{call.Pos,"json_arrayagg"},{options.Input.Pos,"FORMAT JSON ORDER"},{options.NullPos,"NULL ON NULL"},{options.Returning.Pos,"varchar"},{options.Output.Pos,"FORMAT JSON)"}} {
		want:=utf8.RuneCountInString(q[:strings.Index(q,tc.token)])+1
		if tc.p.Character!=want{t.Fatal("Unicode clause location",tc.token,tc.p,want)}
	}
	if !call.Order[0].Desc||call.Order[0].Nulls!=NullsLast{t.Fatal("local order lost")}
	for _, q:=range []string{"SELECT json_arrayagg(NULL)","SELECT json_arrayagg(v ABSENT ON NULL)","SELECT json_arrayagg(v FORMAT JSON ENCODING UTF8 RETURNING bytea FORMAT JSON ENCODING UTF8)","SELECT json_arrayagg(json_arrayagg(v))","SELECT json_arrayagg(v RETURNING SETOF json)"} {
		statements,err:=Parse(q);if err!=nil||len(statements)!=1{t.Fatal(q,err)}
		call:=statements[0].(*Select).Targets[0].(AggregateExpr)
		if call.JSON==nil||!call.JSON.Absent{t.Fatal("default absence lost",q,call)}
	}
	for _, name:=range []string{"json_agg","jsonb_agg","json_agg_strict","jsonb_agg_strict","json_arrayagg"} {
		q:="CREATE TABLE t(v int,j json GENERATED ALWAYS AS("+name+"(v)) STORED)"
		statements,err:=Parse(q);if err!=nil||len(statements)!=1{t.Fatal(q,err)}
		raw:=statements[0].(*CreateTable).Elements[1].Column.Constraints[0].Generation.Expression
		forbidden,ok:=raw.(ForbiddenGenerationExpr)
		if !ok||forbidden.Code!="42803"||forbidden.Pos.Character!=strings.Index(q,name)+1{t.Fatal("generated restriction lost",raw)}
	}
	for _, q:=range []string{
		"CREATE TABLE t(v text,j json GENERATED ALWAYS AS(json_arrayagg(v FORMAT JSON NULL ON NULL RETURNING json)) STORED)",
	} {
		statements,err:=Parse(q);if err!=nil||len(statements)!=1{t.Fatal(q,err)}
		raw:=statements[0].(*CreateTable).Elements[1].Column.Constraints[0].Generation.Expression
		if restricted,ok:=raw.(ForbiddenGenerationExpr);!ok||restricted.Code!="42803"{t.Fatal("formatted generation restriction lost",raw)}
	}
}
func TestJSONArrayAggregateSQLJSONSyntaxDiagnostics(t *testing.T) {
	for _, tc:=range []struct{q,token,code,message string}{
		{"SELECT json_arrayagg()", ")","42601",`syntax error at or near ")"`},
		{"SELECT json_arrayagg(1,2)", ",","42601",`syntax error at or near ","`},
		{"SELECT json_arrayagg(DISTINCT v)", "DISTINCT","42601",`syntax error at or near "DISTINCT"`},
		{"SELECT json_arrayagg(1 FORMAT XML)", "FORMAT","42601",`syntax error at or near "FORMAT"`},
		{"SELECT json_arrayagg(1 RETURNING bytea FORMAT JSON ENCODING latin1)", "latin1","22023","unrecognized JSON encoding: latin1"},
	} {
		statements,err:=Parse(tc.q);d,ok:=err.(*sqlstate.Error)
		if len(statements)!=0||!ok||d.Code!=tc.code||d.Message!=tc.message||d.Position!=sqlPositionForJSONArray(tc.q,tc.token){t.Fatal(tc.q,statements,err)}
	}
	for _, q:=range []string{"SELECT json_arrayagg(1) trailing junk","SELECT json_arrayagg(1); SELECT FROM"} {
		if statements,err:=Parse(q);err==nil||len(statements)!=0{t.Fatal("valid prefix executed",q,statements,err)}
	}
}
func sqlPositionForJSONArray(q,token string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,token)])+1)
}

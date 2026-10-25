package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// These complete records were settled against the pinned PostgreSQL 18.0
// discovery run. Empty fields are assertions, not ignored diagnostics.
func TestReferenceRangeMoreDiagnosticContracts(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_discovery(k integer,a int4range,m int4multirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_discovery VALUES (1,'[1,4]','{[8,9),[1,3)}')", 1))
		for _, tc := range []struct {
			query string
			args []any
			prepareStage string
			want fixture.Diagnostic
		}{
		{
			query: "SELECT multirange(NULL)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM multirange(NULL)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange('empty')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM multirange('empty')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange('{}')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM multirange('{}')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange($1)",
			args: []any{nil}, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM multirange($1)",
			args: []any{nil}, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange() FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange() does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(*) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange() does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a,a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange(int4range, int4range) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange(int4multirange) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(x => a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange(x => int4range) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(x => a,x => a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:"argument name \"x\" used more than once", Detail:"", Hint:"", Position:"32", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(x => a,a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:"positional argument cannot follow named argument", Detail:"", Hint:"", Position:"32", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ \"MULTIRANGE\"(a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function MULTIRANGE(int4range) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.multirange(a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"3F000", Message:"schema \"absent\" does not exist", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ public.multirange(a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function public.multirange(int4range) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(DISTINCT a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"DISTINCT specified, but multirange is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a ORDER BY 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"ORDER BY specified, but multirange is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a) FILTER (WHERE true) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"FILTER specified, but multirange is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a) OVER () FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"OVER specified, but multirange is not a window function nor an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(missing) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"25", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.multirange(missing) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"32", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(missing) FILTER (WHERE missing_filter) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"25", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a) FILTER (WHERE missing_filter) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing_filter\" does not exist", Detail:"", Hint:"", Position:"42", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a) FILTER (WHERE 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"argument of FILTER must be type boolean, not type integer", Detail:"", Hint:"", Position:"42", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.multirange(a) FILTER (WHERE 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"argument of FILTER must be type boolean, not type integer", Detail:"", Hint:"", Position:"49", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(1) FILTER (WHERE true) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function multirange(integer) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(VARIADIC NULL) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"could not determine polymorphic type because input has type unknown", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"54023", Message:"cannot pass more than 100 arguments to a function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ multirange(missing,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"25", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN $1 ELSE a END) FROM more_discovery",
			args: []any{"bad"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22P02", Message:"malformed range literal: \"bad\"", Detail:"Missing left parenthesis or bracket.", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN 'bad' ELSE a END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22P02", Message:"malformed range literal: \"bad\"", Detail:"Missing left parenthesis or bracket.", Hint:"", Position:"39", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN $1 ELSE a END) FROM more_discovery",
			args: []any{"[4,1)"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22000", Message:"range lower bound must be less than or equal to range upper bound", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN '[4,1)' ELSE a END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22000", Message:"range lower bound must be less than or equal to range upper bound", Detail:"", Hint:"", Position:"39", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN $1 ELSE a END) FROM more_discovery",
			args: []any{"[1,2147483647]"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22003", Message:"integer out of range", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT multirange(CASE WHEN true THEN '[1,2147483647]' ELSE a END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22003", Message:"integer out of range", Detail:"", Hint:"", Position:"39", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(NULL)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"8", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM unnest(NULL)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"15", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest('empty')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"8", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM unnest('empty')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"15", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest('{}')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"8", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM unnest('{}')",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"15", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest($1)",
			args: []any{nil}, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"8", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM unnest($1)",
			args: []any{nil}, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"15", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest() FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest() does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(*) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest() does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m,m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest(int4multirange, int4multirange) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest(int4range) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(x => m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest(x => int4multirange) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(x => m,x => m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:"argument name \"x\" used more than once", Detail:"", Hint:"", Position:"28", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(x => m,m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:"positional argument cannot follow named argument", Detail:"", Hint:"", Position:"28", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ \"UNNEST\"(m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function UNNEST(int4multirange) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.unnest(m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"3F000", Message:"schema \"absent\" does not exist", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ public.unnest(m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function public.unnest(int4multirange) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(DISTINCT m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"DISTINCT specified, but unnest is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m ORDER BY 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"ORDER BY specified, but unnest is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m) FILTER (WHERE true) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"FILTER specified, but unnest is not an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m) OVER () FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:"OVER specified, but unnest is not a window function nor an aggregate function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(missing) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"21", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.unnest(missing) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"28", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(missing) FILTER (WHERE missing_filter) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"21", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m) FILTER (WHERE missing_filter) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing_filter\" does not exist", Detail:"", Hint:"", Position:"38", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m) FILTER (WHERE 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"argument of FILTER must be type boolean, not type integer", Detail:"", Hint:"", Position:"38", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ absent.unnest(m) FILTER (WHERE 1) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42804", Message:"argument of FILTER must be type boolean, not type integer", Detail:"", Hint:"", Position:"45", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(1) FILTER (WHERE true) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42883", Message:"function unnest(integer) does not exist", Detail:"", Hint:"No function matches the given name and argument types. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(VARIADIC NULL) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"54023", Message:"cannot pass more than 100 arguments to a function", Detail:"", Hint:"", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT /*é*/ unnest(missing,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m,m) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:"column \"missing\" does not exist", Detail:"", Hint:"", Position:"21", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN $1 ELSE m END) FROM more_discovery",
			args: []any{"bad"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22P02", Message:"malformed multirange literal: \"bad\"", Detail:"Missing left brace.", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN 'bad' ELSE m END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22P02", Message:"malformed multirange literal: \"bad\"", Detail:"Missing left brace.", Hint:"", Position:"35", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN $1 ELSE m END) FROM more_discovery",
			args: []any{"{[4,1)}"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22000", Message:"range lower bound must be less than or equal to range upper bound", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN '{[4,1)}' ELSE m END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22000", Message:"range lower bound must be less than or equal to range upper bound", Detail:"", Hint:"", Position:"35", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN $1 ELSE m END) FROM more_discovery",
			args: []any{"{[1,2147483647]}"}, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22003", Message:"integer out of range", Detail:"", Hint:"", Position:"", InternalPosition:"", InternalQuery:"", Context:"unnamed portal parameter $1 = '...'", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT unnest(CASE WHEN true THEN '{[1,2147483647]}' ELSE m END) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"22003", Message:"integer out of range", Detail:"", Hint:"", Position:"35", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT 1 FROM more_discovery WHERE unnest(m) IS NULL",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in WHERE", Detail:"", Hint:"", Position:"36", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT 1 FROM more_discovery x JOIN more_discovery y ON unnest(x.m) IS NULL",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in JOIN conditions", Detail:"", Hint:"", Position:"57", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT count(*) FROM more_discovery HAVING unnest(m) IS NULL",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in HAVING", Detail:"", Hint:"", Position:"44", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT count(*) FILTER (WHERE unnest(m) IS NULL) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in FILTER", Detail:"", Hint:"", Position:"31", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT count(unnest(m)) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"aggregate function calls cannot contain set-returning function calls", Detail:"", Hint:"You might be able to move the set-returning function into a LATERAL FROM item.", Position:"14", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT first_value(unnest(m)) OVER () FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"window function calls cannot contain set-returning function calls", Detail:"", Hint:"You might be able to move the set-returning function into a LATERAL FROM item.", Position:"20", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT CASE WHEN true THEN unnest(m) ELSE a END FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in CASE", Detail:"", Hint:"You might be able to move the set-returning function into a LATERAL FROM item.", Position:"28", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT coalesce(unnest(m),a) FROM more_discovery",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in COALESCE", Detail:"", Hint:"You might be able to move the set-returning function into a LATERAL FROM item.", Position:"17", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT 1 FROM more_discovery LIMIT lower(unnest(m))",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in LIMIT", Detail:"", Hint:"", Position:"42", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT 1 FROM more_discovery OFFSET lower(unnest(m))",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in OFFSET", Detail:"", Hint:"", Position:"43", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "UPDATE more_discovery SET a=unnest(m)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in UPDATE", Detail:"", Hint:"", Position:"29", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "CREATE TABLE more_bad_default(a int4range DEFAULT unnest(NULL))",
			args: nil, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42725", Message:"function unnest(unknown) is not unique", Detail:"", Hint:"Could not choose a best candidate function. You might need to add explicit type casts.", Position:"51", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "CREATE TABLE more_bad_check(m int4multirange CHECK (isempty(unnest(m))))",
			args: nil, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in check constraints", Detail:"", Hint:"", Position:"61", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "CREATE TABLE more_bad_generated(m int4multirange,a int4range GENERATED ALWAYS AS (unnest(m)) STORED)",
			args: nil, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in column generation expressions", Detail:"", Hint:"", Position:"83", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "CREATE INDEX more_bad_index ON more_discovery ((lower(unnest(m))))",
			args: nil, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in index expressions", Detail:"", Hint:"", Position:"55", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "CREATE INDEX more_bad_predicate ON more_discovery(k) WHERE isempty(unnest(m))",
			args: nil, prepareStage: "execute",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions are not allowed in index predicates", Detail:"", Hint:"", Position:"68", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		{
			query: "SELECT * FROM more_discovery,unnest(multirange(unnest(m))) AS u(r)",
			args: nil, prepareStage: "prepare",
			want: fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"set-returning functions must appear at top level of FROM", Detail:"", Hint:"", Position:"48", InternalPosition:"", InternalQuery:"", Context:"", Schema:"", Table:"", Column:"", DataType:"", Constraint:""},
		},
		} {
			t.Run(tc.query, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, tc.want, tc.args...)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared { s.ErrorStage = tc.prepareStage }
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		}
	})
}

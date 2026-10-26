package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsOutputs(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsMultiSetup(t, p, b, c)
		resultsMultiCheck(t, p, b, c)
		resultsBoth(t, p, b, c, "SELECT (rr_multi(4)).inc,(rr_multi(4)).twice", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
		resultsBoth(t, p, b, c, "SELECT rr_multi(4)", []string{"rr_multi"}, []string{"RECORD"}, [][]any{{"(5,8)"}})
		resultsExec(t, p, b, c,
			"CREATE FUNCTION rr_inter(OUT before integer,IN x integer,INOUT y integer,OUT after integer) LANGUAGE SQL AS 'SELECT $1+1,$2+2,$1+$2'",
			"CREATE FUNCTION rr_unnamed(OUT integer,IN x integer,OUT integer) LANGUAGE SQL AS 'SELECT $1+1,$1*2'",
			"CREATE FUNCTION rr_one(IN x integer,OUT answer integer) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rr_inout(INOUT answer integer) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rr_assignment(IN x integer,OUT big bigint,OUT small integer) LANGUAGE SQL AS 'SELECT $1,$1+1'")
		resultsBoth(t, p, b, c, "SELECT * FROM rr_inter($1::integer,$2::integer)", []string{"before", "y", "after"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(8), int64(10)}}, 4, 6)
		resultsBoth(t, p, b, c, "SELECT * FROM rr_unnamed(4)", []string{"column1", "column2"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
		for _, name := range []string{"rr_one", "rr_inout"} {
			resultsBoth(t, p, b, c, "SELECT "+name+"(4)", []string{name}, []string{"INT4"}, [][]any{{int64(5)}})
			resultsBoth(t, p, b, c, "SELECT * FROM "+name+"(4)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}})
		}
		resultsBoth(t, p, b, c, "SELECT * FROM rr_assignment(4)", []string{"big", "small"}, []string{"INT8", "INT4"}, [][]any{{int64(4), int64(5)}})
		resultsBothMetadata(t, p, b, c, "SELECT proargnames::text AS names,proargmodes::text AS modes,pronargs::integer AS inputs,proretset AS sets FROM pg_proc WHERE proname='rr_inter'", []string{"names", "modes", "inputs", "sets"}, []string{"TEXT", "TEXT", "INT4", "BOOL"}, [][]any{{"{before,x,y,after}", "{o,i,b,o}", int64(2), false}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata})
	})
}

func TestReferenceRoutineResultsIdentity(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsMultiSetup(t, p, b, c)
		want := fixture.ControlsReferenceDiagnostic("42883", "function rr_multi(integer, integer, integer) does not exist")
		want.Hint, want.Position = resultsNoMatchHint, "8"
		resultsError(t, p, c, "SELECT rr_multi(4,5,8)", true, "prepare", want)
		want = fixture.ControlsReferenceDiagnostic("42723", `function "rr_multi" already exists with same argument types`)
		resultsError(t, p, c, "CREATE FUNCTION rr_multi(IN x integer,OUT other text,OUT label text) LANGUAGE SQL AS 'SELECT $1::text,$1::text'", false, "execute", want)
		resultsMultiCheck(t, p, b, c)
		resultsExec(t, p, b, c, "CREATE FUNCTION rr_identity(INOUT y integer,OUT doubled integer) LANGUAGE SQL AS 'SELECT $1+1,$1*2'")
		resultsBoth(t, p, b, c, "SELECT * FROM rr_identity(y=>$1::integer)", []string{"y", "doubled"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}}, 4)
		want = fixture.ControlsReferenceDiagnostic("42883", "function rr_identity() does not exist")
		want.Hint, want.Position = resultsNoMatchHint, "8"
		resultsError(t, p, c, "SELECT rr_identity()", true, "prepare", want)
		resultsBothMetadata(t, p, b, c, "SELECT pronargs::integer AS inputs,proargtypes::text AS types,proallargtypes[1]=proargtypes[0] AS input_identity FROM pg_proc WHERE proname='rr_multi'", []string{"inputs", "types", "input_identity"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "23", true}}, []resultsNativeColumn{{}, resultsTextMetadata, {}})
		for _, declaration := range []string{
			"(IN x integer,OUT renamed integer,OUT twice integer) LANGUAGE SQL AS 'SELECT $1+1,$1*2'",
			"(IN x integer,OUT twice integer,OUT inc integer) LANGUAGE SQL AS 'SELECT $1*2,$1+1'",
			"(IN x integer,OUT inc bigint,OUT twice integer) LANGUAGE SQL AS 'SELECT ($1+1)::bigint,$1*2'",
			"(IN x integer,OUT inc integer,OUT twice integer) RETURNS SETOF record LANGUAGE SQL AS 'SELECT $1+1,$1*2'",
		} {
			want = fixture.ControlsReferenceDiagnostic("42P13", "cannot change return type of existing function")
			want.Hint = "Use DROP FUNCTION rr_multi(integer) first."
			if declaration != "(IN x integer,OUT inc integer,OUT twice integer) RETURNS SETOF record LANGUAGE SQL AS 'SELECT $1+1,$1*2'" {
				want.Detail = "Row type defined by OUT parameters is different."
			}
			resultsError(t, p, c, "CREATE OR REPLACE FUNCTION rr_multi"+declaration, false, "execute", want)
			resultsMultiCheck(t, p, b, c)
		}
		resultsExec(t, p, b, c, "CREATE FUNCTION rr_replace(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
		want = fixture.ControlsReferenceDiagnostic("42P13", "cannot change return type of existing function")
		want.Hint = "Use DROP FUNCTION rr_replace(integer) first."
		resultsError(t, p, c, "CREATE OR REPLACE FUNCTION rr_replace(integer) RETURNS SETOF integer LANGUAGE SQL AS 'SELECT $1'", false, "execute", want)
		resultsBoth(t, p, b, c, "SELECT rr_replace(4)", []string{"rr_replace"}, []string{"INT4"}, [][]any{{int64(4)}})
		resultsExec(t, p, b, c, "CREATE FUNCTION rr_vm(VARIADIC x integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
		resultsBothMetadata(t, p, b, c, "SELECT proargmodes::text AS modes,proallargtypes::text AS types FROM pg_proc WHERE proname='rr_vm'", []string{"modes", "types"}, []string{"TEXT", "TEXT"}, [][]any{{"{v}", "{1007}"}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata})
	})
}

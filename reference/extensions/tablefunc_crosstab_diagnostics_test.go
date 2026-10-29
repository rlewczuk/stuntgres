package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabDiagnostics(t *testing.T) {
	for _, name := range []string{"source_two", "source_four", "return_one", "row_type", "value_type", "missing", "syntax", "unicode", "mutation", "late_failure", "inner_parameter", "record_definition", "redundant_definition", "target_record", "wrong_arity", "named_argument", "filter", "scalar_context"} {
		t.Run(name, func(t *testing.T) {
			crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				crosstabSeed(t, p, b, c)
				source := fixture.CrosstabSource
				decl := "k text,a text,b text"
				d := fixture.ControlsReferenceDiagnostic("22023", "invalid crosstab source data query")
				d.Detail = "The query must return 3 columns: row_name, category, and value."
				stage := "execute"
				q := ""
				switch name {
				case "source_two":
					source = "SELECT k,v FROM ct_source ORDER BY seq"
				case "source_four":
					source = "SELECT seq,k,category,v FROM ct_source ORDER BY seq"
				case "return_one":
					decl = "k text"
					d = crosstabReturnError("Return row must have at least two columns.")
				case "row_type":
					decl = "k integer,a text,b text"
					d = crosstabReturnError("Source row_name datatype text does not match return row_name datatype integer.")
				case "value_type":
					decl = "k text,a text,b integer"
					d = crosstabReturnError("Source value datatype text does not match return value datatype integer in column 3.")
				case "missing", "unicode", "late_failure":
					source = "SELECT missing_ct"
					d = fixture.ControlsReferenceDiagnostic("42703", "column \"missing_ct\" does not exist")
					d.InternalPosition = fixture.Position(8)
					if name == "unicode" {
						source = "/*é*/ SELECT missing_ct"
						d.InternalPosition = fixture.Position(14)
					}
					if name == "late_failure" {
						source = fixture.CrosstabSource + "; SELECT missing_ct"
						d.InternalPosition = fixture.Position(len(fixture.CrosstabSource) + 10)
					}
					d.InternalQuery = source
				case "syntax":
					source = "SELECT )"
					d = fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \")\"")
					d.InternalPosition, d.InternalQuery = fixture.Position(8), source
				case "mutation":
					source = "DELETE FROM ct_source"
					d = fixture.ControlsReferenceDiagnostic("0A000", "DELETE is not allowed in a non-volatile function")
					d.Context = "SQL statement \"DELETE FROM ct_source\""
				case "inner_parameter":
					source = "SELECT $1,0,1"
					d = fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
					d.InternalPosition, d.InternalQuery = fixture.Position(8), source
				case "record_definition":
					q = "SELECT * FROM crosstab(NULL::text)"
					d = fixture.ControlsReferenceDiagnostic("42601", "a column definition list is required for functions returning \"record\"")
					d.Position = fixture.Position(15)
					stage = "prepare"
				case "redundant_definition":
					q = "SELECT * FROM crosstab2(NULL::text) AS t(k text,a text,b text)"
					d = fixture.ControlsReferenceDiagnostic("42601", "a column definition list is redundant for a function returning a named composite type")
					d.Position = fixture.Position(42)
					stage = "prepare"
				case "target_record":
					q = "SELECT crosstab('SELECT 1,2,3')"
					d = fixture.ControlsReferenceDiagnostic("0A000", "function returning record called in context that cannot accept type record")
				case "wrong_arity", "named_argument":
					q = "SELECT crosstab2()"
					signature := "crosstab2()"
					if name == "named_argument" {
						q = "SELECT crosstab2(source => NULL::text)"
						signature = "crosstab2(source => text)"
					}
					d = fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8)
					stage = "prepare"
				case "filter":
					q = "SELECT crosstab2(NULL::text) FILTER (WHERE true)"
					d = fixture.ControlsReferenceDiagnostic("42809", "FILTER specified, but crosstab2 is not an aggregate function")
					d.Position = fixture.Position(8)
					stage = "prepare"
				case "scalar_context":
					q = "SELECT 1 WHERE crosstab2(NULL::text) IS NULL"
					d = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in WHERE")
					d.Position = fixture.Position(16)
					stage = "prepare"
				}
				if q == "" {
					q = "SELECT * FROM crosstab(" + fixture.CrosstabQuote(source) + ") AS t(" + decl + ")"
				}
				crosstabFailure(t, p, b, c, q, d, stage)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM ct_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.CrosstabWant("SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k", fixture.CrosstabSource))
			})
		})
	}
}

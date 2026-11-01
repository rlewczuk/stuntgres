package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesDiagnostics(t *testing.T) {
	minimum := crosstabReturnError("Return row must have at least two columns.")
	categoryWidth := crosstabCategoriesDiagnostic("22023", "invalid crosstab categories query", "The query must return one column.")
	nullCategory := fixture.ControlsReferenceDiagnostic("22004", "crosstab category value must not be null")
	duplicate := fixture.ControlsReferenceDiagnostic("42710", "duplicate category name")
	empty := fixture.ControlsReferenceDiagnostic("21000", "crosstab categories query must return at least one row")
	sourceWidth := crosstabCategoriesDiagnostic("22023", "invalid crosstab source data query", "The query must return at least 3 columns: row_name, category, and value.")
	for _, tc := range []struct {
		name, source, categories, declaration string
		diagnostic                           fixture.Diagnostic
	}{
		{"initial_minimum_before_broken_queries", "SELECT )", "SELECT missing_ct", "k text", minimum},
		{"category_width", fixture.CrosstabSource, "SELECT 1,2", "k text,a text", categoryWidth},
		{"category_width_before_empty_source", "SELECT 1 WHERE false", "SELECT 1,2", "k text,a text", categoryWidth},
		{"null_category", fixture.CrosstabSource, "SELECT NULL::text", "k text,a text", nullCategory},
		{"null_before_empty_source", "SELECT 1 WHERE false", "SELECT NULL::text", "k text,a text", nullCategory},
		{"duplicate_category", fixture.CrosstabSource, crosstabCategoriesOrder("a", "a"), "k text,a text,b text", duplicate},
		{"duplicate_before_source_parse", "SELECT )", crosstabCategoriesOrder("a", "a"), "k text,a text,b text", duplicate},
		{"duplicate_before_null", fixture.CrosstabSource, "SELECT category FROM (VALUES(1,'a'::text),(2,'a'),(3,NULL)) AS s(seq,category) ORDER BY seq", "k text,a text,b text", duplicate},
		{"null_before_duplicate", fixture.CrosstabSource, "SELECT category FROM (VALUES(1,NULL::text),(2,'a'),(3,'a')) AS s(seq,category) ORDER BY seq", "k text,a text,b text", nullCategory},
		{"empty_categories", fixture.CrosstabSource, "SELECT 1 WHERE false", "k text,a text", empty},
		{"empty_categories_before_source_width", "SELECT 1,2", "SELECT 1 WHERE false", "k text,a text", empty},
		{"source_width_one", "SELECT 1", "SELECT 'a'::text", "k text,a text", sourceWidth},
		{"source_width_two", "SELECT 1,2", "SELECT 'a'::text", "k text,a text", sourceWidth},
		{"result_width", "SELECT 'g'::text,'first'::text,'a'::text,'v'::text", crosstabCategoriesOrder("a", "b", "c"), "k text,extra text,a text,b text", crosstabReturnError("Return row must have 5 columns, not 4.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				crosstabSeed(t, p, b, c)
				crosstabFailure(t, p, b, c, crosstabCategoriesSQL("crosstab", tc.declaration), tc.diagnostic, "execute", tc.source, tc.categories)
				crosstabCategoriesIntact(t, p, b, c)
			})
		})
	}
	for _, side := range []string{"category", "source"} {
		for _, kind := range []string{"missing", "syntax", "unicode", "late_missing", "late_syntax", "parameter", "mutation", "late_mutation"} {
			t.Run(side+"_"+kind, func(t *testing.T) {
				crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					crosstabSeed(t, p, b, c)
					inner := "SELECT missing_ct"
					d := fixture.ControlsReferenceDiagnostic("42703", "column \"missing_ct\" does not exist")
					token := "missing_ct"
					switch kind {
					case "syntax", "late_syntax":
						inner, token = "SELECT )", ")"
						d = fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \")\"")
					case "unicode":
						inner = "/*é*/ SELECT missing_ct"
					case "parameter":
						inner, token = "SELECT $1", "$1"
						d = fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
					case "mutation", "late_mutation":
						inner = "DELETE FROM ct_source"
						d = fixture.ControlsReferenceDiagnostic("0A000", "DELETE is not allowed in a non-volatile function")
					}
					if strings.HasPrefix(kind, "late_") {
						inner = "SELECT 'a'::text; " + inner
					}
					if strings.Contains(kind, "mutation") {
						d.Context = "SQL statement \"" + inner + "\""
					} else {
						d.InternalQuery = inner
						d.InternalPosition = fixture.Position(utf8.RuneCountInString(inner[:strings.Index(inner, token)]) + 1)
					}
					source, categories := fixture.CrosstabSource, crosstabCategoriesOrder("z", "a", "x")
					if side == "category" {
						categories = inner
					} else {
						source = inner
					}
					crosstabFailure(t, p, b, c, crosstabCategoriesSQL("crosstab", "k text,z text,a text,x text"), d, "execute", source, categories)
					crosstabCategoriesIntact(t, p, b, c)
				})
			})
		}
	}
	for _, name := range []string{"both_broken", "empty_categories_broken_source", "late_source_before_shape", "late_source_before_destination", "record_definition", "target_record", "where_context"} {
		t.Run(name, func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				crosstabSeed(t, p, b, c)
				q := crosstabCategoriesSQL("crosstab", "k text,a integer")
				source, categories := "SELECT )", "SELECT missing_ct"
				d := fixture.ControlsReferenceDiagnostic("42703", "column \"missing_ct\" does not exist")
				d.InternalQuery, d.InternalPosition = categories, fixture.Position(8)
				stage := "execute"
				switch name {
				case "empty_categories_broken_source":
					source, categories = "SELECT missing_ct", "SELECT 1 WHERE false"
					d.InternalQuery = source
				case "late_source_before_shape", "late_source_before_destination":
					source = "SELECT k,v::integer FROM (VALUES(1,'g'::text,'10'::text),(2,'g','bad')) AS s(seq,k,v) ORDER BY seq"
					if name == "late_source_before_destination" {
						source = "SELECT k,'a'::text,v::integer FROM (VALUES(1,'g'::text,'10'::text),(2,'g','bad')) AS s(seq,k,v) ORDER BY seq"
						q = crosstabCategoriesSQL("crosstab", "k integer,a integer")
					}
					categories = "SELECT 'a'::text"
					d = fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type integer: \"bad\"")
					d.Context = "SQL statement \"" + source + "\""
				case "record_definition":
					q = "SELECT * FROM crosstab(NULL::text,NULL::text)"
					d = fixture.ControlsReferenceDiagnostic("42601", "a column definition list is required for functions returning \"record\"")
					d.Position, stage = fixture.Position(15), "prepare"
				case "target_record":
					q = "SELECT crosstab('SELECT 1,2,3'::text,'SELECT 2'::text)"
					d = fixture.ControlsReferenceDiagnostic("0A000", "materialize mode required, but it is not allowed in this context")
				case "where_context":
					q = "SELECT 1 WHERE crosstab(NULL::text,NULL::text) IS NULL"
					d = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in WHERE")
					d.Position, stage = fixture.Position(16), "prepare"
				}
				if name == "record_definition" || name == "target_record" || name == "where_context" {
					crosstabFailure(t, p, b, c, q, d, stage)
				} else {
					crosstabFailure(t, p, b, c, q, d, stage, source, categories)
				}
				crosstabCategoriesIntact(t, p, b, c)
			})
		})
	}
	t.Run("late_group_failure_is_statement_atomic", func(t *testing.T) {
		crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			crosstabSeed(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ct_category_atomic(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ct_category_atomic VALUES(99)", 1))
			source := "SELECT k,category,v FROM (VALUES(1,'first'::text,'a'::text,'10'::text),(2,'last','a','bad')) AS s(seq,k,category,v) ORDER BY seq"
			q := "INSERT INTO ct_category_atomic SELECT v FROM crosstab($1::text,$2::text) AS t(k text,v integer)"
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type integer: \"bad\"")
			s := fixture.ErrorDiagnostic(q, d, source, "SELECT 'a'::text")
			s.ErrorStage = "execute"
			crosstabCategoriesBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM ct_category_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			crosstabCategoriesIntact(t, p, b, c)
		})
	})
}

func crosstabCategoriesIntact(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM ct_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
	q := crosstabCategoriesSQL("crosstab", "k text,z text,a text,x text") + " ORDER BY k"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"k", "z", "a", "x"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}, fixture.CrosstabSource, crosstabCategoriesOrder("z", "a", "x")))
}

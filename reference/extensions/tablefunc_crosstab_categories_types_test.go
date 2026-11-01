package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesTypes(t *testing.T) {
	t.Run("mixed_destination_inputs", func(t *testing.T) {
		crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE EXTENSION tablefunc", 0),
				fixture.Exec("SET DateStyle TO 'ISO, MDY'", 0),
				fixture.Exec("SET TimeZone TO 'UTC'", 0),
				fixture.Exec("SET extra_float_digits TO 1", 0),
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			q := crosstabCategoriesSQL("crosstab", "k integer,flag boolean,n integer,label text,stamp timestamp,f double precision")
			source := "SELECT k,flag,category,v FROM (VALUES(1,'7'::text,'true'::text,'n'::text,'10'::text),(2,'7','bad-later-extra','label','label'),(3,'7','false','stamp','2026-01-02 03:04:05'),(4,'7','false','f','1.5')) AS s(seq,k,flag,category,v) ORDER BY seq"
			crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "flag", "n", "label", "stamp", "f"}, []string{"INT4", "BOOL", "INT4", "TEXT", "TIMESTAMP", "FLOAT8"}, [][]any{{int64(7), true, int64(10), "label", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), float64(1.5)}}, source, crosstabCategoriesOrder("n", "label", "stamp", "f")))
			q = crosstabCategoriesSQL("crosstab", "k text,v integer")
			crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "v"}, []string{"TEXT", "INT4"}, [][]any{{"g", int64(9)}}, "SELECT 'g'::text,'7'::text,'9'::text", "SELECT 7::integer"))
			// Distinct full source strings remain distinct groups even when the
			// destination integer codec maps both keys to the same value.
			q = crosstabCategoriesSQL("crosstab", "k integer,v integer")
			s := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}, "SELECT k,category,v FROM (VALUES(1,'01'::text,'a'::text,'10'::text),(2,'1','a','20')) AS s(seq,k,category,v) ORDER BY seq", "SELECT 'a'::text")
			s.Bag = true
			crosstabCategoriesBoth(t, p, b, c, s)
			// Source type output and destination input share the caller's settings.
			q = crosstabCategoriesSQL("crosstab", "k text,v text")
			crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "2026-01-02 03:04:05"}}, "SELECT 'g'::text,'a'::text,'2026-01-02 03:04:05'::timestamp", "SELECT 'a'::text"))
			crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "1.5"}}, "SELECT 'g'::text,'a'::text,1.5::double precision", "SELECT 'a'::text"))
		})
	})
	for _, tc := range []struct {
		name, setup, source, declaration string
		rows                             [][]any
		diagnostic                       *fixture.Diagnostic
	}{
		{"overwritten_bad_input", "", "SELECT k,category,v FROM (VALUES(1,'g'::text,'a'::text,'bad'::text),(2,'g','a','10')) AS s(seq,k,category,v) ORDER BY seq", "k text,v integer", [][]any{{"g", int64(10)}}, nil},
		{"ignored_bad_input", "", "SELECT k,category,v FROM (VALUES(1,'g'::text,'ignored'::text,'bad'::text),(2,'g','a','10')) AS s(seq,k,category,v) ORDER BY seq", "k text,v integer", [][]any{{"g", int64(10)}}, nil},
		{"final_bad_input", "", "SELECT k,category,v FROM (VALUES(1,'g'::text,'a'::text,'10'::text),(2,'g','a','bad')) AS s(seq,k,category,v) ORDER BY seq", "k text,v integer", nil, crosstabCategoriesDiagnosticPointer("22P02", "invalid input syntax for type integer: \"bad\"")},
		{"single_bad_input", "", "SELECT 'g'::text,'a'::text,'bad'::text", "k text,v integer", nil, crosstabCategoriesDiagnosticPointer("22P02", "invalid input syntax for type integer: \"bad\"")},
		{"key_conversion_first", "", "SELECT 'bad-key'::text,'a'::text,'bad-value'::text", "k integer,v integer", nil, crosstabCategoriesDiagnosticPointer("22P02", "invalid input syntax for type integer: \"bad-key\"")},
		{"extra_conversion_before_value", "", "SELECT 'g'::text,'bad-extra'::text,'a'::text,'bad-value'::text", "k text,extra integer,v integer", nil, crosstabCategoriesDiagnosticPointer("22P02", "invalid input syntax for type integer: \"bad-extra\"")},
		{"varchar_typmod", "", "SELECT 'g'::text,'a'::text,'long'::text", "k text,v varchar(3)", nil, crosstabCategoriesDiagnosticPointer("22001", "value too long for type character varying(3)")},
		{"domain_check", "CREATE DOMAIN ct_positive AS integer CONSTRAINT ct_positive_check CHECK(VALUE>0)", "SELECT 'g'::text,'a'::text,'-1'::text", "k text,v ct_positive", nil, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "value for domain ct_positive violates check constraint \"ct_positive_check\"", Schema: "public", DataType: "ct_positive", Constraint: "ct_positive_check"}},
		{"domain_missing_null", "CREATE DOMAIN ct_required AS integer NOT NULL", "SELECT 'g'::text,'ignored'::text,'bad'::text", "k text,v ct_required", nil, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: "domain ct_required does not allow null values", Schema: "public", DataType: "ct_required"}},
		{"domain_matched_null", "CREATE DOMAIN ct_required AS integer NOT NULL", "SELECT 'g'::text,'a'::text,NULL::text", "k text,v ct_required", nil, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: "domain ct_required does not allow null values", Schema: "public", DataType: "ct_required"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION tablefunc", 0))
				if tc.setup != "" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.setup, 0))
				}
				q := crosstabCategoriesSQL("crosstab", tc.declaration)
				if tc.diagnostic != nil {
					crosstabFailure(t, p, b, c, q, *tc.diagnostic, "execute", tc.source, "SELECT 'a'::text")
				} else {
					crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"k", "v"}, []string{"TEXT", "INT4"}, tc.rows, tc.source, "SELECT 'a'::text"))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 19 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(19)}}))
			})
		})
	}
}

func crosstabCategoriesDiagnosticPointer(code, message string) *fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	return &d
}

package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceFTSStatDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, inner, code, message string
		location                   string
		context                    bool
	}{
		{"one_integer", "SELECT 1", "22023", "ts_stat query must return one tsvector column", "", false},
		{"zero_columns", "SELECT", "22023", "ts_stat query must return one tsvector column", "", false},
		{"two_columns", "SELECT ''::tsvector,1", "22023", "ts_stat query must return one tsvector column", "", false},
		{"zero_wrong_type", "SELECT 1 WHERE FALSE", "22023", "ts_stat query must return one tsvector column", "", false},
		{"text_not_vector", "SELECT 'cat:1'::text", "22023", "ts_stat query must return one tsvector column", "", false},
		{"missing", "SELECT missing::tsvector", "42703", `column "missing" does not exist`, "missing", false},
		{"parameter", "SELECT $1::tsvector", "42P02", "there is no parameter $1", "$1", false},
		{"empty", "", "42P11", "cannot open multi-query plan as cursor", "", false},
		{"comment", "/* only a comment */", "42P11", "cannot open multi-query plan as cursor", "", false},
		{"multiple", "SELECT ''::tsvector; SELECT ''::tsvector", "42P11", "cannot open multi-query plan as cursor", "", false},
		{"insert", "INSERT INTO fts_stat_errors VALUES(2,'dog:1')", "42P11", "cannot open INSERT query as cursor", "", false},
		{"insert_returning", "INSERT INTO fts_stat_errors VALUES(2,'dog:1') RETURNING v", "0A000", "INSERT is not allowed in a non-volatile function", "", true},
		{"modifying_cte", "WITH changed AS (INSERT INTO fts_stat_errors VALUES(2,'dog:1') RETURNING v) SELECT v FROM changed", "0A000", "SELECT is not allowed in a non-volatile function", "", true},
		{"row_lock", "SELECT v FROM fts_stat_errors FOR UPDATE", "0A000", "SELECT FOR UPDATE is not allowed in a non-volatile function", "", true},
		{"constant_division", "SELECT v FROM fts_stat_errors WHERE 1/0=0", "22012", "division by zero", "", true},
		{"fetch_division", "SELECT v FROM fts_stat_errors WHERE 1/(ord-1)=0", "22012", "division by zero", "", false},
		{"fetch_before_shape", "SELECT 1/(ord-1) FROM fts_stat_errors", "22012", "division by zero", "", false},
		{"unicode", "/*é猫*/ SELECT missing::tsvector", "42703", `column "missing" does not exist`, "missing", false},
		{"suffix", "SELECT ''::tsvector; garbage", "42601", `syntax error at or near "garbage"`, "garbage", false},
		{"multi_analysis", "SELECT ''::tsvector; SELECT missing FROM fts_stat_errors", "42703", `column "missing" does not exist`, "missing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := ftsInspectionConnection(t, p, b)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE fts_stat_errors(ord integer,v tsvector)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fts_stat_errors VALUES(1,'cat:1')", 1))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.location != "" {
					d.InternalPosition = fixture.Position(utf8.RuneCountInString(tc.inner[:strings.Index(tc.inner, tc.location)]) + 1)
					d.InternalQuery = tc.inner
				}
				if tc.context {
					d.Context = "SQL statement \"" + tc.inner + "\""
				}
				ftsInspectionDiagnostic(t, p, b, c, "SELECT * FROM ts_stat("+ftsTypesQuote(tc.inner)+")", d, false)
				ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT ord,v FROM fts_stat_errors ORDER BY ord", []string{"ord", "v"}, []string{"INT4", "TSVECTOR"}, [][]any{{int64(1), "'cat':1"}}))
				ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat('SELECT v FROM fts_stat_errors')", ftsStatColumns, ftsStatTypes, [][]any{{"cat", int64(1), int64(1)}}))
			})
		})
	}
	t.Run("late_fetch", func(t *testing.T) {
		ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := ftsInspectionConnection(t, p, b)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE fts_stat_late(ord integer,v tsvector)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fts_stat_late SELECT n,'x:1'::tsvector FROM generate_series(1,101) n", 101))
			d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			ftsInspectionDiagnostic(t, p, b, c, "SELECT * FROM ts_stat('SELECT v FROM fts_stat_late WHERE 1/(101-ord)>=0')", d, false)
			ftsInspectionBoth(t, p, b, c, fixture.Select("SELECT * FROM ts_stat('SELECT v FROM fts_stat_late')", ftsStatColumns, ftsStatTypes, [][]any{{"x", int64(101), int64(101)}}))
		})
	})
}

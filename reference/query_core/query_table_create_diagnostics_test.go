package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceQueryTableCreateDiagnostics(t *testing.T) {
	cases := []struct {
		name, query, code, message, at, hint string
		prepare                              bool
	}{
		{"exists", "CREATE TABLE qc_existing AS SELECT 1/0 AS x", "42P07", `relation "qc_existing" already exists`, "", "", false},
		{"duplicate", "CREATE TABLE qc_bad AS SELECT 1 AS x,2 AS x", "42701", `column "x" specified more than once`, "", "", false},
		{"implicit_duplicate", "CREATE TABLE qc_bad AS SELECT 1+1,2+2", "42701", `column "?column?" specified more than once`, "", "", false},
		{"prefix_duplicate", "CREATE TABLE qc_bad(x,x) AS SELECT 1 AS a,2 AS b", "42701", `column "x" specified more than once`, "", "", false},
		{"clipped", "CREATE TABLE qc_bad AS SELECT 1 AS " + strings.Repeat("a", 63) + "x,2 AS " + strings.Repeat("a", 63) + "y", "42701", `column "` + strings.Repeat("a", 63) + `" specified more than once`, "", "", false},
		{"duplicate_nodata", "CREATE TABLE qc_bad AS SELECT 1 AS x,2 AS x WITH NO DATA", "42701", `column "x" specified more than once`, "", "", false},
		{"prefix_duplicate_nodata", "CREATE TABLE qc_bad(x,x) AS SELECT 1 AS a,2 AS b WITH NO DATA", "42701", `column "x" specified more than once`, "", "", false},
		{"system", "CREATE TABLE qc_bad AS SELECT 1 AS ctid", "42701", `column name "ctid" conflicts with a system column name`, "", "", false},
		{"too_many", "CREATE TABLE qc_bad(a,b) AS SELECT 1 AS x", "42601", "too many column names were specified", "", "", false},
		{"too_many_nodata", "CREATE TABLE qc_bad(a,b) AS SELECT 1 AS x WITH NO DATA", "42601", "too many column names were specified", "", "", false},
		{"missing_source", "/*é*/ CREATE TABLE qc_bad AS SELECT i FROM qc_missing", "42P01", `relation "qc_missing" does not exist`, "qc_missing", "", true},
		{"skip_analysis", "CREATE TABLE IF NOT EXISTS qc_existing AS SELECT i FROM qc_missing", "42P01", `relation "qc_missing" does not exist`, "qc_missing", "", true},
		{"nodata_column", "CREATE TABLE qc_bad AS SELECT absent FROM qc_source WITH NO DATA", "42703", `column "absent" does not exist`, "absent", "", true},
		{"nodata_operator", "CREATE TABLE qc_bad AS SELECT true+1 AS x WITH NO DATA", "42883", "operator does not exist: boolean + integer", "+", "No operator matches the given name and argument types. You might need to add explicit type casts.", true},
		{"nodata_literal_cast", "CREATE TABLE qc_bad AS SELECT 'bad'::integer AS x WITH NO DATA", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", "", true},
		{"nested", "CREATE TABLE qc_bad AS SELECT * FROM (SELECT i INTO qc_inner FROM qc_source) d", "42601", "SELECT ... INTO is not allowed here", "qc_inner", "", true},
		{"view", "CREATE VIEW qc_bad AS SELECT i INTO qc_inner FROM qc_source", "0A000", "views must not contain SELECT INTO", "", "", false},
		{"void", "CREATE TABLE qc_bad AS SELECT setseed(0) AS x", "42P16", `column "x" has pseudo-type void`, "", "", false},
		{"collation", "CREATE TABLE qc_bad AS SELECT CASE WHEN i>0 THEN a ELSE b END AS x FROM qc_collation", "42P22", `no collation was derived for column "x" with collatable type text`, "", "Use the COLLATE clause to set the collation explicitly.", false},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						_, run := fixture.QueryTableCreateSession(t, p, b)
						fixture.QueryTableCreateSource(run)
						run(fixture.Exec(`CREATE TABLE qc_collation(i INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
						run(fixture.Exec("CREATE TABLE qc_existing(v TEXT)", 0))
						run(fixture.Exec("INSERT INTO qc_existing VALUES('sentinel')", 1))
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Hint = tc.hint
						if tc.at != "" {
							want.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.at)]) + 1)
						}
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && tc.prepare {
							s.ErrorStage = "prepare"
						}
						run(s)
						fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_bad")
						fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_inner")
						run(fixture.QueryTableCreateBag("qc_source"))
						run(fixture.Select("SELECT * FROM qc_existing", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
					})
				}
			})
		}
	}
}

func TestReferenceQueryTableCreateAtomicity(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE qc_late AS SELECT 10/(2-i) AS v FROM qc_source ORDER BY i",
		"SELECT 10/(2-i) AS v INTO qc_late FROM qc_source ORDER BY i",
		"CREATE TABLE qc_late AS SELECT (SELECT i FROM qc_source) AS v",
	} {
		t.Run(q, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					fixture.QueryTableCreateSource(run)
					want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					if strings.Contains(q, "(SELECT") {
						want = fixture.ControlsReferenceDiagnostic("21000", "more than one row returned by a subquery used as an expression")
					}
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows = true
					run(s)
					fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_late")
					run(fixture.QueryTableCreateBag("qc_source"))
					run(fixture.Exec("CREATE TABLE qc_late AS SELECT i,s FROM qc_source", 3))
					run(fixture.QueryTableCreateBag("qc_late"))
				})
			}
		})
	}
}

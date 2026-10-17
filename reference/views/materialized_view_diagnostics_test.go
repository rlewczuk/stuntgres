package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceMaterializedViewDiagnostics(t *testing.T) {
	cases := []struct {
		name, query, code, message, at, hint string
		prepare                              bool
	}{
		{"syntax", "/*é*/ CREATE MATERIALIZED VIEW mv_bad AS", "42601", "syntax error at end of input", "", "", true},
		{"duplicate", "CREATE MATERIALIZED VIEW mv_bad AS SELECT 1 AS x,2 AS x", "42701", `column "x" specified more than once`, "", "", false},
		{"system", "CREATE MATERIALIZED VIEW mv_bad AS SELECT 1 AS ctid", "42701", `column name "ctid" conflicts with a system column name`, "", "", false},
		{"aliases", "CREATE MATERIALIZED VIEW mv_bad(a,b) AS SELECT 1 AS x", "42601", "too many column names were specified", "", "", false},
		{"missing", "/*é*/ CREATE MATERIALIZED VIEW mv_bad AS SELECT i FROM mv_missing", "42P01", `relation "mv_missing" does not exist`, "mv_missing", "", true},
		{"skip_analysis", "CREATE MATERIALIZED VIEW IF NOT EXISTS mv_existing AS SELECT i FROM mv_missing", "42P01", `relation "mv_missing" does not exist`, "mv_missing", "", true},
		{"column", "CREATE MATERIALIZED VIEW mv_bad AS SELECT absent FROM mv_base WITH NO DATA", "42703", `column "absent" does not exist`, "absent", "", true},
		{"cast", "CREATE MATERIALIZED VIEW mv_bad AS SELECT 'bad'::integer AS x WITH NO DATA", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'", "", true},
		{"void", "CREATE MATERIALIZED VIEW mv_bad AS SELECT setseed(0) AS x", "42P16", `column "x" has pseudo-type void`, "", "", false},
		{"collation", `CREATE MATERIALIZED VIEW mv_bad AS SELECT CASE WHEN i>0 THEN a ELSE b END AS x FROM mv_collation`, "42P22", `no collation was derived for column "x" with collatable type text`, "", "Use the COLLATE clause to set the collation explicitly.", false},
		{"temp", "CREATE MATERIALIZED VIEW mv_bad AS SELECT i FROM mv_temp", "0A000", "materialized views must not use temporary tables or views", "", "", true},
		{"exists", "CREATE MATERIALIZED VIEW mv_existing AS SELECT 1/0 AS x", "42P07", `relation "mv_existing" already exists`, "", "", false},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						_, run := fixture.QueryTableCreateSession(t, p, b)
						materializedViewSeed(run)
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv_existing AS SELECT i,s FROM mv_base", 3))
						run(fixture.Exec("CREATE TEMP TABLE mv_temp(i INT)", 0))
						run(fixture.Exec(`CREATE TABLE mv_collation(i INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Hint = tc.hint
						if tc.at != "" {
							want.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.at)]) + 1)
						}
						if tc.name == "syntax" {
							want.Position = fixture.Position(utf8.RuneCountInString(tc.query) + 1)
						}
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared && tc.prepare {
							s.ErrorStage = "prepare"
						}
						run(s)
						fixture.QueryTableCreateAbsent(t, run, "mv_bad")
						run(materializedViewBag("mv_base"))
						run(materializedViewBag("mv_existing"))
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv_bad AS SELECT i,s FROM mv_base", 3))
						run(materializedViewBag("mv_bad"))
					})
				}
			})
		}
	}
	t.Run("bound_parameter", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				_, run := fixture.QueryTableCreateSession(t, p, b)
				want := fixture.ControlsReferenceDiagnostic("0A000", "materialized views may not be defined using bound parameters")
				s := fixture.Prepared(fixture.ErrorDiagnostic("CREATE MATERIALIZED VIEW mv_parameter AS SELECT $1::integer AS i", want, 7))
				s.ErrorStage = "prepare"
				run(s)
				fixture.QueryTableCreateAbsent(t, run, "mv_parameter")
			})
		}
	})
}

func TestReferenceMaterializedViewAtomicity(t *testing.T) {
	for _, tc := range []struct{ body, code, message string }{
		{"SELECT 10/(2-i) AS x FROM mv_base ORDER BY i", "22012", "division by zero"},
		{"SELECT (SELECT i FROM mv_base) AS x", "21000", "more than one row returned by a subquery used as an expression"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					materializedViewSeed(run)
					run(fixture.Exec("CREATE MATERIALIZED VIEW mv_sentinel AS SELECT i,s FROM mv_base", 3))
					run(fixture.ImplicitError("CREATE MATERIALIZED VIEW mv_late AS "+tc.body, fixture.Diagnostic{Code: tc.code, Message: tc.message}))
					fixture.QueryTableCreateAbsent(t, run, "mv_late")
					run(materializedViewBag("mv_base"))
					run(materializedViewBag("mv_sentinel"))
					fixture.MaterializedViewPopulation(run, "mv_sentinel", true)
					run(fixture.Exec("CREATE MATERIALIZED VIEW mv_late AS SELECT i,s FROM mv_base", 3))
					run(materializedViewBag("mv_late"))
				})
			}
		})
	}
}

func TestReferenceMaterializedViewDML(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					materializedViewSeed(run)
					run(fixture.Exec("CREATE MATERIALIZED VIEW mv_readonly AS SELECT i,s FROM mv_base", 3))
					for _, q := range []string{"INSERT INTO mv_readonly VALUES(9,'z')", "UPDATE mv_readonly SET i=9", "DELETE FROM mv_readonly WHERE i=1"} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42809", Message: `cannot change materialized view "mv_readonly"`})
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						run(materializedViewBag("mv_readonly"))
						run(materializedViewBag("mv_base"))
					}
					lock := fixture.ImplicitError("SELECT * FROM mv_readonly FOR UPDATE", fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in materialized view "mv_readonly"`})
					lock.SelectRows, lock.Prepared, lock.ErrorStage = true, prepared, "execute"
					run(lock)
					q := "UPDATE mv_readonly SET absent=9"
					want := fixture.ControlsReferenceDiagnostic("42703", `column "absent" of relation "mv_readonly" does not exist`)
					want.Position = fixture.Position(strings.Index(q, "absent") + 1)
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				})
			}
		})
	}
}

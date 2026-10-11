package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The oracle entry point runs the same permanent expectations on PostgreSQL
// alone while LIKE is being implemented. The paired entry point retains them
// as independent expectations before backend comparison.

func TestReferenceLikePhases(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) {
				s.CheckMetadata = likeReferenceMetadata
				p.Step(t, s)
			}
			run(fixture.Exec("CREATE TABLE t(v TEXT,p TEXT,e TEXT,i INTEGER)", 0))
			if populated {
				run(fixture.Exec("INSERT INTO t VALUES(NULL,'x','ab',1)", 1))
			}
			invalid := fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}
			terminal := fixture.Diagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"}
			for _, tc := range []struct {
				query string
				args  []any
				want  fixture.Diagnostic
			}{
				{"SELECT v LIKE 'x' ESCAPE 'ab' FROM t", nil, invalid},
				{"SELECT NULL LIKE 'x' ESCAPE 'ab' FROM t", nil, invalid},
				{"SELECT v LIKE $1 ESCAPE $2 FROM t", []any{"x", "ab"}, invalid},
				{"SELECT 'x' LIKE '\\' FROM t", nil, terminal},
				{"SELECT v LIKE 'x' ESCAPE 1 FROM t", nil, fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
				{"SELECT v NOT ILIKE 'x' ESCAPE 1 FROM t", nil, fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, tc.want, tc.args...)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if tc.want.Code == "42883" {
							s.ErrorStage = "prepare"
						}
					}
					run(s)
				}
			}
			var rows [][]any
			if populated {
				rows = [][]any{{nil}}
			}
			for _, q := range []string{
				"SELECT v LIKE p ESCAPE e AS matched FROM t",
				"SELECT v LIKE '\\' AS matched FROM t",
				"SELECT v LIKE 'x' ESCAPE e AS matched FROM t",
				"SELECT NULL::text LIKE p ESCAPE e AS matched FROM t",
			} {
				t.Run(q, func(t *testing.T) {
					s := fixture.Select(q, []string{"matched"}, []string{"BOOL"}, rows)
					if populated && (q == "SELECT v LIKE p ESCAPE e AS matched FROM t" || q == "SELECT v LIKE 'x' ESCAPE e AS matched FROM t") {
						s = fixture.ImplicitError(q, invalid)
						s.SelectRows = true
						s.ErrorStage = "execute"
					}
					for _, step := range []fixture.Step{s, fixture.Prepared(s)} {
						p.Step(t, step)
					}
				})
			}
		})
	}
}

func TestReferenceLikeLazyEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	run(fixture.Exec("CREATE TABLE seed(i INTEGER)", 0))
	run(fixture.Exec("INSERT INTO seed VALUES(1)", 1))
	for _, op := range []string{"LIKE", "ILIKE", "NOT LIKE", "NOT ILIKE"} {
		for _, tc := range []struct {
			lhs, pattern string
			fails        bool
		}{
			{"", "\\", false},
			{"x", "\\", true},
			{"x", "z\\", false},
			{"x", "x\\", false},
			{"xx", "x\\", true},
			{"x", "%\\", true},
			{"", "%\\", false},
		} {
			q := "SELECT '" + tc.lhs + "' " + op + " '" + tc.pattern + "' AS matched FROM seed"
			var s fixture.Step
			if tc.fails {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
				s.SelectRows = true
				s.ErrorStage = "execute"
			} else {
				negated := op == "NOT LIKE" || op == "NOT ILIKE"
				s = fixture.Select(q, []string{"matched"}, []string{"BOOL"}, [][]any{{negated}})
			}
			run(s)
			run(fixture.Prepared(s))
		}
	}
}

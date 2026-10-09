package reference_test

import "testing"

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
			p := newReferenceScenario(t)
			run := func(s referenceStep) {
				s.checkMetadata = likeReferenceMetadata
				p.step(t, s)
			}
			run(referenceExec("CREATE TABLE t(v TEXT,p TEXT,e TEXT,i INTEGER)", 0))
			if populated {
				run(referenceExec("INSERT INTO t VALUES(NULL,'x','ab',1)", 1))
			}
			invalid := referenceDiagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}
			terminal := referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"}
			for _, tc := range []struct {
				query string
				args  []any
				want  referenceDiagnostic
			}{
				{"SELECT v LIKE 'x' ESCAPE 'ab' FROM t", nil, invalid},
				{"SELECT NULL LIKE 'x' ESCAPE 'ab' FROM t", nil, invalid},
				{"SELECT v LIKE $1 ESCAPE $2 FROM t", []any{"x", "ab"}, invalid},
				{"SELECT 'x' LIKE '\\' FROM t", nil, terminal},
				{"SELECT v LIKE 'x' ESCAPE 1 FROM t", nil, referenceDiagnostic{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
				{"SELECT v NOT ILIKE 'x' ESCAPE 1 FROM t", nil, referenceDiagnostic{Code: "42883", Message: "function pg_catalog.like_escape(unknown, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "10"}},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.query, tc.want, tc.args...)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						if tc.want.Code == "42883" {
							s.errorStage = "prepare"
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
					s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, rows)
					if populated && (q == "SELECT v LIKE p ESCAPE e AS matched FROM t" || q == "SELECT v LIKE 'x' ESCAPE e AS matched FROM t") {
						s = referenceImplicitError(q, invalid)
						s.selectRows = true
						s.errorStage = "execute"
					}
					for _, step := range []referenceStep{s, referencePrepared(s)} {
						p.step(t, step)
					}
				})
			}
		})
	}
}

func TestReferenceLikeLazyEscapes(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	run(referenceExec("CREATE TABLE seed(i INTEGER)", 0))
	run(referenceExec("INSERT INTO seed VALUES(1)", 1))
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
			var s referenceStep
			if tc.fails {
				s = referenceImplicitError(q, referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
				s.selectRows = true
				s.errorStage = "execute"
			} else {
				negated := op == "NOT LIKE" || op == "NOT ILIKE"
				s = referenceSelect(q, []string{"matched"}, []string{"BOOL"}, [][]any{{negated}})
			}
			run(s)
			run(referencePrepared(s))
		}
	}
}

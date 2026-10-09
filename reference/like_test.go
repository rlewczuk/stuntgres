package reference_test

import "testing"

func TestReferenceLikeInference(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	run(referenceExec("CREATE TABLE t(n NAME,c CHAR(3),v VARCHAR(3),s TEXT,b BYTEA)", 0))
	run(referenceExec("INSERT INTO t VALUES('a','a','a','a','\\x61')", 1))
	for _, tc := range []struct {
		query string
		value bool
	}{
		{"SELECT n LIKE $1 AS matched FROM t", true},
		{"SELECT $1 LIKE n AS matched FROM t", true},
		{"SELECT c LIKE $1 AS matched FROM t", false},
		{"SELECT $1 LIKE c AS matched FROM t", true},
		{"SELECT v LIKE $1 AS matched FROM t", true},
		{"SELECT $1 LIKE v AS matched FROM t", true},
	} {
		s := referenceSelect(tc.query, []string{"matched"}, []string{"BOOL"}, [][]any{{tc.value}}, "a")
		run(s)
		run(referencePrepared(s))
	}
	for _, s := range []referenceStep{
		referenceSelect("SELECT $1 AS value,s LIKE $1 AS matched FROM t", []string{"value", "matched"}, []string{"TEXT", "BOOL"}, [][]any{{"a", true}}, "a"),
		referenceSelect("SELECT s LIKE $1 AS matched,$1 AS value FROM t", []string{"matched", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, "a"}}, "a"),
		referenceSelect("SELECT $1 LIKE $2 ESCAPE $3 AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{true}}, "a", "a", "!"),
	} {
		run(s)
		run(referencePrepared(s))
	}
	for _, q := range []string{
		"SELECT s LIKE $1 ESCAPE $2,b LIKE $1 ESCAPE $2 FROM t",
		"SELECT b LIKE $1 ESCAPE $2,s LIKE $1 ESCAPE $2 FROM t",
	} {
		position := "30"
		if q[7] == 'b' {
			position = "10"
		}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "operator does not exist: bytea ~~ text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: position}, "a", "!")
		s.selectRows = true
		s.errorStage = "execute"
		run(s)
		s.errorStage = "prepare"
		run(referencePrepared(s))
	}

}

func TestReferenceLikeCollation(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	run(referenceExec("CREATE TABLE t(d TEXT,c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\",e TEXT COLLATE \"POSIX\")", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			run(referenceExec("INSERT INTO t VALUES('Ab','Ab','Ab','!')", 1))
		}
		for _, lhs := range []string{"d", "c", "p"} {
			for _, rhs := range []string{"d", "c", "p"} {
				for _, op := range []string{"LIKE", "ILIKE", "NOT LIKE", "NOT ILIKE"} {
					q := "SELECT " + lhs + " " + op + " " + rhs + " AS matched FROM t"
					var want [][]any
					if populated {
						want = [][]any{{op == "LIKE" || op == "ILIKE"}}
					}
					step := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, want)
					if populated && (lhs == "c" && rhs == "p" || lhs == "p" && rhs == "c") {
						operation := "LIKE"
						if op == "ILIKE" || op == "NOT ILIKE" {
							operation = "ILIKE"
						}
						step = referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for " + operation, Hint: "Use the COLLATE clause to set the collation explicitly."})
						step.selectRows, step.errorStage = true, "execute"
					}
					run(step)
					run(referencePrepared(step))
				}
			}
		}
		for _, op := range []string{"LIKE", "NOT LIKE", "ILIKE", "NOT ILIKE"} {
			operation := "LIKE"
			if op == "ILIKE" || op == "NOT ILIKE" {
				operation = "ILIKE"
			}
			for _, rhs := range []string{"p", "'a' ESCAPE e"} {
				q := "SELECT c " + op + " " + rhs + " AS matched FROM t"
				s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, nil)
				if populated {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for " + operation, Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.selectRows = true
					s.errorStage = "execute"
				}
				run(s)
				run(referencePrepared(s))
			}
		}
		for _, q := range []string{
			"SELECT c LIKE p AS matched FROM t WHERE FALSE",
			"SELECT c LIKE NULL::text AS matched FROM t",
			"SELECT NULL::text LIKE p ESCAPE e AS matched FROM t",
			"SELECT c LIKE p ESCAPE NULL::text AS matched FROM t",
		} {
			var rows [][]any
			if populated && q != "SELECT c LIKE p AS matched FROM t WHERE FALSE" {
				rows = [][]any{{nil}}
			}
			s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, rows)
			run(s)
			run(referencePrepared(s))
		}
	}
}

func TestReferenceLikeIntegratedExplicitCollation(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(t *testing.T, s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	run(t, referenceExec(`CREATE TABLE t(c TEXT COLLATE "C",p TEXT COLLATE "POSIX",e TEXT COLLATE "POSIX")`, 0))
	for _, state := range []string{"empty", "nonnull", "null"} {
		if state == "nonnull" {
			run(t, referenceExec("INSERT INTO t VALUES('a','a','!')", 1))
		} else if state == "null" {
			run(t, referenceExec("UPDATE t SET c=NULL,p=NULL,e=NULL", 1))
		}
		for _, expression := range []string{
			`c LIKE p COLLATE "C"`,
			`c COLLATE "POSIX" LIKE p`,
			`c COLLATE "default" LIKE p`,
			`c COLLATE "C" LIKE p COLLATE "C"`,
			`c LIKE COALESCE(p,c) COLLATE "C"`,
			`COALESCE(c,p) LIKE p COLLATE "POSIX"`,
			`c LIKE COALESCE(p COLLATE "C",c)`,
			`c COLLATE "C" LIKE 'a' ESCAPE e`,
			`c LIKE p ESCAPE e COLLATE "C"`,
			`c LIKE btrim(p COLLATE "C")`,
			`c LIKE btrim(p COLLATE "default")`,
			"c LIKE btrim(p)",
			"c LIKE (p || '')",
		} {
			t.Run(state+"/"+expression, func(t *testing.T) {
				q := "SELECT " + expression + " AS matched FROM t"
				var want [][]any
				if state == "nonnull" {
					want = [][]any{{true}}
				} else if state == "null" {
					want = [][]any{{nil}}
				}
				s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, want)
				if state == "nonnull" && (expression == "c LIKE btrim(p)" || expression == "c LIKE (p || '')") {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.selectRows = true
					s.errorStage = "execute"
				}
				run(t, s)
				run(t, referencePrepared(s))
			})
		}
		for _, tc := range []struct {
			expression, position string
		}{
			{`c COLLATE "C" LIKE p COLLATE "POSIX"`, "29"},
			{`c COLLATE "C" LIKE 'a' ESCAPE e COLLATE "POSIX"`, "40"},
			{`c LIKE COALESCE(c COLLATE "C",p COLLATE "POSIX")`, "40"},
			{`c LIKE btrim(c COLLATE "C",p COLLATE "POSIX")`, "37"},
		} {
			t.Run(state+"/"+tc.expression, func(t *testing.T) {
				q := "SELECT " + tc.expression + " FROM t"
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: tc.position})
				s.selectRows = true
				s.errorStage = "execute"
				run(t, s)
				s.errorStage = "prepare"
				run(t, referencePrepared(s))
			})
		}
	}
}

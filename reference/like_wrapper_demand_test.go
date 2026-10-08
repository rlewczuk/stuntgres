//go:build reference

package reference_test

import "testing"

func TestReferenceLikeOracleWrapperDemand(t *testing.T) { likeReferenceWrapperDemand(t, true) }
func TestReferenceLikeWrapperDemand(t *testing.T)       { likeReferenceWrapperDemand(t, false) }
func likeReferenceWrapperDemand(t *testing.T, oracleOnly bool) {
	p := newReferencePair(t)
	run := func(t *testing.T, s referenceStep) {
		if oracleOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(t, referenceExec("CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")", 0))
	run(t, referenceExec("INSERT INTO t VALUES('a','z')", 1))
	for state := range 3 {
		if state == 1 {
			run(t, referenceExec("UPDATE t SET c=NULL", 1))
		}
		if state == 2 {
			run(t, referenceExec("UPDATE t SET p=NULL", 1))
		}
		for _, function := range []string{"NULLIF", "GREATEST", "LEAST"} {
			q := "SELECT 'a' LIKE " + function + "(c,p) AS matched FROM t"
			t.Run(q, func(t *testing.T) {
				s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, [][]any{{nil}})
				if state == 0 || state == 1 && function != "NULLIF" {
					message := "could not determine which collation to use for string comparison"
					if state == 1 {
						message = "could not determine which collation to use for LIKE"
					}
					s = referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: message, Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.selectRows = true
					s.errorStage = "execute"
				}
				run(t, s)
				run(t, referencePrepared(s))
			})
		}
	}
	for _, expr := range []string{"COALESCE(pg_catalog.like_escape(c,p),'a')", "NULLIF(c,p)", "GREATEST(c,p)", "LEAST(c,p)"} {
		q := "CREATE VIEW escaped_pattern AS SELECT " + expr + " AS pattern FROM t"
		t.Run(q, func(t *testing.T) {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
			s.errorStage = "execute"
			run(t, s)
			run(t, referencePrepared(s))
		})
	}
	run(t, referenceExec("INSERT INTO t VALUES('a','a')", 1))
	run(t, referenceExec("CREATE VIEW cast_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t", 0))
	s := referenceSelect("SELECT 'a' LIKE pattern AS matched FROM cast_pattern ORDER BY pattern", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {nil}})
	run(t, s)
	run(t, referencePrepared(s))
	conflict := referenceImplicitError("SELECT a.c LIKE b.pattern FROM t a JOIN cast_pattern b ON a.c IS NOT NULL AND b.pattern IS NOT NULL", referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
	conflict.selectRows = true
	conflict.errorStage = "execute"
	run(t, conflict)
	run(t, referencePrepared(conflict))
	// Physical collation identity is a PostgreSQL catalog witness, not a new
	// native catalog surface. LIKE consuming the native view is paired above.
	if oracleOnly {
		run(t, referenceSelect("SELECT attcollation::bigint AS collation_oid FROM pg_attribute WHERE attrelid='cast_pattern'::regclass AND attname='pattern'", []string{"collation_oid"}, []string{"INT8"}, [][]any{{int64(951)}}))
	}
}

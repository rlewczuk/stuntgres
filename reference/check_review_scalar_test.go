//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceCheckReviewScalarCommentDML(t *testing.T) {
	for _, provider := range []struct{ name, expr, target string }{
		{"object", "obj_description('anchor'::regclass,'pg_class')", "TABLE anchor"},
		{"legacy", "obj_description('anchor'::regclass)", "TABLE anchor"},
		{"column", "col_description('anchor'::regclass,1)", "COLUMN anchor.a"},
		{"shared", "", "ROLE postgres"},
	} {
		for _, prepared := range []bool{false, true} {
			for _, backend := range []string{"postgres", "memory"} {
				t.Run(fmt.Sprintf("%s/prepared=%v/%s", provider.name, prepared, backend), func(t *testing.T) {
					p := newReferencePair(t)
					db, rec := p.postgres, p.postgresRecord
					if backend == "memory" {
						db, rec = p.memory, p.memoryRecord
					}
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, rec, s) }
					expr := provider.expr
					if provider.name == "shared" {
						roleOID := int64(10)
						if backend == "postgres" {
							if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&roleOID); err != nil {
								t.Fatal(err)
							}
						}
						expr = fmt.Sprintf("shobj_description(%d,'pg_authid')", roleOID)
					}
					run(referenceExec("CREATE TABLE anchor(a INT)", 0))
					run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
					run(referenceExec("CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
					run(referenceExec("INSERT INTO t VALUES($1)", 1, "old"))
					run(referenceExec("COMMENT ON "+provider.target+" IS 'new'", 0))
					run(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "old"), "old"))
					run(referenceExec("INSERT INTO t VALUES($1)", 1, "new"))
					run(referenceExec("UPDATE t SET a=$1", 2, "new"))
					run(referenceImplicitError("UPDATE t SET a=$1", referenceCheckViolation("t", "c", "old"), "old"))
					run(referenceExec("INSERT INTO t SELECT 'new'", 1))
					run(referenceExec("INSERT INTO t VALUES('new') ORDER BY 1", 1))
					run(referenceImplicitError("INSERT INTO t SELECT 'old'", referenceCheckViolation("t", "c", "old")))
					run(referenceImplicitError("INSERT INTO t VALUES('old') ORDER BY 1", referenceCheckViolation("t", "c", "old")))
					run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"new"}, {"new"}, {"new"}, {"new"}}))
					run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
					run(referenceExec("INSERT INTO t VALUES($1)", 1, "old"))
					run(referenceExec("UPDATE t SET a=$1", 5, "old"))
					run(referenceExec("INSERT INTO t DEFAULT VALUES", 1))
					run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}}))
				})
			}
		}
	}
}

func TestReferenceCheckReviewScalarNonNullMissingOID(t *testing.T) {
	for _, expr := range []string{"obj_description(0,'pg_class')", "obj_description(0)", "col_description(0,1)", "shobj_description(0,'pg_authid')"} {
		t.Run(expr, func(t *testing.T) {
			referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))", 0))
				run(referencePrepared(referenceExec("INSERT INTO t DEFAULT VALUES", 1)))
				run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "2"), 2)))
				run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceCheckReviewScalarPrivateCommentBoundary(t *testing.T) {
	for _, target := range []string{"TABLE anchor", "COLUMN anchor.a"} {
		for _, backend := range []string{"postgres", "memory"} {
			t.Run(target+"/"+backend, func(t *testing.T) {
				p := newReferencePair(t)
				db, recorder := p.postgres, p.postgresRecord
				if backend == "memory" {
					db, recorder = p.memory, p.memoryRecord
				}
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, recorder, s) }
				expr := "obj_description('anchor'::regclass,'pg_class')"
				if target == "COLUMN anchor.a" {
					expr = "col_description('anchor'::regclass,1)"
				}
				run(referenceExec("CREATE TABLE anchor(a INT)", 0))
				run(referenceExec("COMMENT ON "+target+" IS 'old'", 0))
				run(referenceExec("CREATE TABLE t(a TEXT CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
				run(referenceExec("BEGIN", 0))
				if backend == "postgres" {
					run(referenceExec("COMMENT ON "+target+" IS 'new'", 0))
					run(referencePrepared(referenceExec("INSERT INTO t VALUES($1)", 1, "new")))
					run(referencePrepared(referenceExec("UPDATE t SET a=$1", 1, "new")))
				} else {
					run(referenceImplicitError("COMMENT ON "+target+" IS 'new'", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				}
				run(referenceExec("ROLLBACK", 0))
				run(referenceImplicitError("INSERT INTO t VALUES('new')", referenceCheckViolation("t", "c", "new")))
				run(referencePrepared(referenceExec("INSERT INTO t VALUES($1)", 1, "old")))
				run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}}))
			})
		}
	}
}

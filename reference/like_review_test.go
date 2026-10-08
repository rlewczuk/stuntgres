//go:build reference

package reference_test

import (
	"bytes"
	"encoding/hex"
	"stuntgres"
	"testing"
)

func TestReferenceLikeOracleReview(t *testing.T) { likeReferenceReview(t, true) }
func TestReferenceLikeReview(t *testing.T)       { likeReferenceReview(t, false) }

func likeReferenceReview(t *testing.T, oracleOnly bool) {
	p := newReferencePair(t)
	run := func(t *testing.T, s referenceStep) {
		if oracleOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(t, referenceExec("CREATE TABLE seed(i INTEGER)", 0))
	run(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	for _, op := range []string{"LIKE", "ILIKE", "NOT LIKE", "NOT ILIKE"} {
		for _, binary := range []bool{false, true} {
			if binary && (op == "ILIKE" || op == "NOT ILIKE") {
				continue
			}
			for _, tc := range []struct {
				lhs, pattern string
				fails        bool
			}{
				{"x", "%_\\", true}, {"x", "%__\\", false}, {"", "%_\\", false}, {"", "%__\\", false}, {"x", "x\\", false},
				{"xx", "%x\\", true}, {"yx", "%x\\", false}, {"xx", "%_%x\\", false}, {"xxx", "%_%x\\", true},
			} {
				lhs, pattern := "'"+tc.lhs+"'", "'"+tc.pattern+"'"
				if binary {
					lhs = "CAST('\\x" + hex.EncodeToString([]byte(tc.lhs)) + "' AS BYTEA)"
					pattern = "CAST('\\x" + hex.EncodeToString([]byte(tc.pattern)) + "' AS BYTEA)"
				}
				q := "SELECT " + lhs + " " + op + " " + pattern + " AS matched FROM seed"
				t.Run(q, func(t *testing.T) {
					s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, [][]any{{op == "NOT LIKE" || op == "NOT ILIKE"}})
					if tc.fails {
						s = referenceImplicitError(q, referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
						s.selectRows = true
						s.errorStage = "execute"
					}
					run(t, s)
					run(t, referencePrepared(s))
				})
			}
		}
	}
	run(t, referenceExec("CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")", 0))
	expressions := []string{"c LIKE CAST(p AS TEXT)", "c LIKE COALESCE('a'::text,p)", "'a' LIKE COALESCE(pg_catalog.like_escape(c,p),'a')", "c LIKE CASE WHEN TRUE THEN 'a' ELSE p END", "c LIKE NULLIF(p,'z')", "c LIKE GREATEST('a',p)", "c LIKE LEAST('a',p)"}
	for _, populated := range []bool{false, true} {
		if populated {
			run(t, referenceExec("INSERT INTO t VALUES('a','a')", 1))
		}
		for _, expr := range expressions {
			q := "SELECT " + expr + " AS matched FROM t"
			t.Run(q, func(t *testing.T) {
				s := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, nil)
				if populated {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.selectRows = true
					s.errorStage = "execute"
				}
				run(t, s)
				run(t, referencePrepared(s))
				filtered := referenceSelect(q+" WHERE FALSE", []string{"matched"}, []string{"BOOL"}, nil)
				run(t, filtered)
				run(t, referencePrepared(filtered))
			})
		}
	}
	for _, rhs := range []string{"CAST(p AS TEXT)", "COALESCE('a'::text,p)", "COALESCE(pg_catalog.like_escape(c,p),'a')"} {
		s := referenceSelect("SELECT NULL::text LIKE "+rhs+" AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{nil}})
		run(t, s)
		run(t, referencePrepared(s))
	}
	run(t, referenceExec("CREATE VIEW wrapped AS SELECT c LIKE CAST(p AS TEXT) AS casted,c LIKE COALESCE('a'::text,p) AS conditional,'a' LIKE COALESCE(pg_catalog.like_escape(c,p),'a') AS escaped FROM t", 0))
	for _, column := range []string{"casted", "conditional", "escaped"} {
		s := referenceImplicitError("SELECT "+column+" FROM wrapped", referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.selectRows = true
		s.errorStage = "execute"
		run(t, s)
		run(t, referencePrepared(s))
	}
	if oracleOnly || t.Failed() {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"casted", "conditional", "escaped"} {
		s := referenceImplicitError("SELECT "+column+" FROM wrapped", referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.selectRows = true
		s.errorStage = "execute"
		run(t, s)
	}
}

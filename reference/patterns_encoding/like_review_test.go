package reference_test

import (
	"bytes"
	"encoding/hex"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeReview(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(t *testing.T, s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	run(t, fixture.Exec("CREATE TABLE seed(i INTEGER)", 0))
	run(t, fixture.Exec("INSERT INTO seed VALUES(1)", 1))
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
					s := fixture.Select(q, []string{"matched"}, []string{"BOOL"}, [][]any{{op == "NOT LIKE" || op == "NOT ILIKE"}})
					if tc.fails {
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
						s.SelectRows = true
						s.ErrorStage = "execute"
					}
					run(t, s)
					run(t, fixture.Prepared(s))
				})
			}
		}
	}
	run(t, fixture.Exec("CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")", 0))
	expressions := []string{"c LIKE CAST(p AS TEXT)", "c LIKE COALESCE('a'::text,p)", "'a' LIKE COALESCE(pg_catalog.like_escape(c,p),'a')", "c LIKE CASE WHEN TRUE THEN 'a' ELSE p END", "c LIKE NULLIF(p,'z')", "c LIKE GREATEST('a',p)", "c LIKE LEAST('a',p)"}
	for _, populated := range []bool{false, true} {
		if populated {
			run(t, fixture.Exec("INSERT INTO t VALUES('a','a')", 1))
		}
		for _, expr := range expressions {
			q := "SELECT " + expr + " AS matched FROM t"
			t.Run(q, func(t *testing.T) {
				s := fixture.Select(q, []string{"matched"}, []string{"BOOL"}, nil)
				if populated {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
					s.SelectRows = true
					s.ErrorStage = "execute"
				}
				run(t, s)
				run(t, fixture.Prepared(s))
				filtered := fixture.Select(q+" WHERE FALSE", []string{"matched"}, []string{"BOOL"}, nil)
				run(t, filtered)
				run(t, fixture.Prepared(filtered))
			})
		}
	}
	for _, rhs := range []string{"CAST(p AS TEXT)", "COALESCE('a'::text,p)", "COALESCE(pg_catalog.like_escape(c,p),'a')"} {
		s := fixture.Select("SELECT NULL::text LIKE "+rhs+" AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{nil}})
		run(t, s)
		run(t, fixture.Prepared(s))
	}
	run(t, fixture.Exec("CREATE VIEW wrapped AS SELECT c LIKE CAST(p AS TEXT) AS casted,c LIKE COALESCE('a'::text,p) AS conditional,'a' LIKE COALESCE(pg_catalog.like_escape(c,p),'a') AS escaped FROM t", 0))
	for _, column := range []string{"casted", "conditional", "escaped"} {
		s := fixture.ImplicitError("SELECT "+column+" FROM wrapped", fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(t, s)
		run(t, fixture.Prepared(s))
	}
	if t.Failed() {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"casted", "conditional", "escaped"} {
		s := fixture.ImplicitError("SELECT "+column+" FROM wrapped", fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(t, s)
	}
}

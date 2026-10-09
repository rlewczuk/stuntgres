package reference_test

import (
	"encoding/hex"
	"testing"
)

// Original consumer methods complement, rather than replace, recorded Context lanes.
func TestReferenceLikeReviewPercentExhaustionSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE seed(i INTEGER)")
			stringCoreControlExec(t, db, "INSERT INTO seed VALUES(1)")
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
						q := "SELECT " + lhs + " " + op + " " + pattern + " FROM seed"
						t.Run(q, func(t *testing.T) {
							rows, err := db.Query(q)
							if tc.fails {
								if rows != nil {
									rows.Close()
									t.Fatal("bad escape exposed cursor")
								}
								stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
							} else {
								stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{op == "NOT LIKE" || op == "NOT ILIKE"}}, false)
							}
							stmt, err := db.Prepare(q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							rows, err = stmt.Query()
							if tc.fails {
								if rows != nil {
									rows.Close()
									t.Fatal("bad escape exposed cursor")
								}
								stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
							} else {
								stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{op == "NOT LIKE" || op == "NOT ILIKE"}}, false)
							}
						})
					}
				}
			}
		})
	}
}

func TestReferenceLikeReviewCollatableWrappersSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")")
			for _, populated := range []bool{false, true} {
				if populated {
					stringCoreControlExec(t, db, "INSERT INTO t VALUES('a','a')")
				}
				for _, expr := range []string{
					"c LIKE CAST(p AS TEXT)",
					"c LIKE COALESCE('a'::text,p)",
					"'a' LIKE COALESCE(pg_catalog.like_escape(c,p),'a')",
					"c LIKE CASE WHEN TRUE THEN 'a' ELSE p END",
					"c LIKE NULLIF(p,'z')",
					"c LIKE GREATEST('a',p)",
					"c LIKE LEAST('a',p)",
				} {
					q := "SELECT " + expr + " FROM t"
					t.Run(q, func(t *testing.T) {
						rows, err := db.Query(q)
						if populated {
							if rows != nil {
								rows.Close()
								t.Fatal("collation conflict exposed cursor")
							}
							stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
						} else {
							stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{}, false)
						}
					})
				}
			}
		})
	}
}

package reference_test

import (
	"database/sql"
	"testing"
)

// Original consumer methods complement, rather than replace, recorded Context lanes.
func TestReferenceLikeCollationAndDemandSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(d TEXT,c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\",e TEXT COLLATE \"POSIX\")")
			for _, populated := range []bool{false, true} {
				if populated {
					stringCoreControlExec(t, db, "INSERT INTO t VALUES('Ab','Ab','Ab','!')")
				}
				for _, lhs := range []string{"d", "c", "p"} {
					for _, rhs := range []string{"d", "c", "p"} {
						for _, op := range []string{"LIKE", "ILIKE", "NOT LIKE", "NOT ILIKE"} {
							q := "SELECT " + lhs + " " + op + " " + rhs + " FROM t"
							rows, err := db.Query(q)
							conflict := lhs == "c" && rhs == "p" || lhs == "p" && rhs == "c"
							if populated && conflict {
								if rows != nil {
									rows.Close()
									t.Fatal("conflict exposed cursor")
								}
								name := "LIKE"
								if op == "ILIKE" || op == "NOT ILIKE" {
									name = "ILIKE"
								}
								stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for " + name, Hint: "Use the COLLATE clause to set the collation explicitly."})
							} else {
								want := [][]any{}
								if populated {
									want = [][]any{{op == "LIKE" || op == "ILIKE"}}
								}
								stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, want, false)
							}
						}
					}
				}
			}
			for _, q := range []string{"SELECT NULL::text LIKE p ESCAPE e FROM t", "SELECT c LIKE NULL::text FROM t", "SELECT c LIKE p ESCAPE NULL::text FROM t"} {
				rows, err := db.Query(q)
				stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{nil}}, false)
			}
			_, err := db.Query("SELECT c LIKE 'x' ESCAPE e FROM t")
			stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		})
	}
}

func TestReferenceLikeIntegratedExplicitCollationSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, `CREATE TABLE t(c TEXT COLLATE "C",p TEXT COLLATE "POSIX",e TEXT COLLATE "POSIX")`)
			for _, state := range []string{"empty", "nonnull", "null"} {
				if state == "nonnull" {
					stringCoreControlExec(t, db, "INSERT INTO t VALUES('a','a','!')")
				} else if state == "null" {
					stringCoreControlExec(t, db, "UPDATE t SET c=NULL,p=NULL,e=NULL")
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
				} {
					t.Run(state+"/"+expression, func(t *testing.T) {
						q := "SELECT " + expression + " AS matched FROM t"
						want := [][]any{}
						if state == "nonnull" {
							want = [][]any{{true}}
						} else if state == "null" {
							want = [][]any{{nil}}
						}
						rows, err := db.Query(q)
						stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, want, false)
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err = stmt.Query()
						stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, want, false)
					})
				}
				for _, expression := range []string{"c LIKE btrim(p)", "c LIKE (p || '')"} {
					t.Run(state+"/"+expression, func(t *testing.T) {
						q := "SELECT " + expression + " AS matched FROM t"
						check := func() {
							rows, err := db.Query(q)
							if state == "nonnull" {
								if rows != nil {
									rows.Close()
									t.Fatal("collation conflict exposed cursor")
								}
								stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
							} else {
								want := [][]any{}
								if state == "null" {
									want = [][]any{{nil}}
								}
								stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, want, false)
							}
						}
						check()
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err := stmt.Query()
						if state == "nonnull" {
							if rows != nil {
								rows.Close()
								t.Fatal("prepared collation conflict exposed cursor")
							}
							stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
						} else {
							want := [][]any{}
							if state == "null" {
								want = [][]any{{nil}}
							}
							stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, want, false)
						}
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
						want := referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: tc.position}
						rows, err := db.Query(q)
						if rows != nil {
							rows.Close()
							t.Fatal("explicit conflict exposed cursor")
						}
						stringCoreControlDiagnostic(t, err, want)
						stmt, err := db.Prepare(q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("explicit conflict prepared successfully")
						}
						stringCoreControlDiagnostic(t, err, want)
					})
				}
			}
		})
	}
}

func TestReferenceLikeWrapperComparisonDemandSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")")
			stringCoreControlExec(t, db, "INSERT INTO t VALUES('a','z')")
			for state := range 3 {
				if state == 1 {
					stringCoreControlExec(t, db, "UPDATE t SET c=NULL")
				}
				if state == 2 {
					stringCoreControlExec(t, db, "UPDATE t SET p=NULL")
				}
				for _, function := range []string{"NULLIF", "GREATEST", "LEAST"} {
					q := "SELECT 'a' LIKE " + function + "(c,p) FROM t"
					t.Run(q, func(t *testing.T) {
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, prepared := range []bool{false, true} {
							var rows *sql.Rows
							var err error
							if prepared {
								rows, err = stmt.Query()
							} else {
								rows, err = db.Query(q)
							}
							if state == 0 || state == 1 && function != "NULLIF" {
								if rows != nil {
									rows.Close()
									t.Fatal("conflict exposed row cursor")
								}
								message := "could not determine which collation to use for string comparison"
								if state == 1 {
									message = "could not determine which collation to use for LIKE"
								}
								stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: message, Hint: "Use the COLLATE clause to set the collation explicitly."})
							} else {
								stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{nil}}, false)
							}
						}
					})
				}
			}
		})
	}
}

func TestReferenceLikeViewCollationPublicationSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\")")
			stringCoreControlExec(t, db, "INSERT INTO t VALUES('a','a')")
			for _, expr := range []string{"COALESCE(pg_catalog.like_escape(c,p),'a')", "NULLIF(c,p)", "GREATEST(c,p)", "LEAST(c,p)"} {
				q := "CREATE VIEW escaped_pattern AS SELECT " + expr + " AS pattern FROM t"
				t.Run(q, func(t *testing.T) {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal("view output conflict rejected Prepare", err)
					}
					defer stmt.Close()
					_, err = db.Exec(q)
					stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
					_, err = stmt.Exec()
					stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for view column \"pattern\"", Hint: "Use the COLLATE clause to set the collation explicitly."})
				})
			}
			// The same name is available after every failed publication, and the
			// surviving determinate output retains POSIX rather than default identity.
			stringCoreControlExec(t, db, "CREATE VIEW escaped_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t")
			rows, err := db.Query("SELECT 'a' LIKE pattern FROM escaped_pattern")
			stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}}, false)
			_, err = db.Query("SELECT a.c LIKE b.pattern FROM t a JOIN escaped_pattern b ON TRUE")
			stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		})
	}
}

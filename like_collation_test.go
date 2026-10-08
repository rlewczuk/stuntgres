package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestLikeCollationAndDemand(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(d TEXT,c TEXT COLLATE \"C\",p TEXT COLLATE \"POSIX\",e TEXT COLLATE \"POSIX\")")
	for _, populated := range []bool{false, true} {
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES('Ab','Ab','Ab','!')")
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
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for " + name, Hint: "Use the COLLATE clause to set the collation explicitly."})
					} else {
						want := [][]any{}
						if populated {
							want = [][]any{{op == "LIKE" || op == "ILIKE"}}
						}
						compareOpsRows(t, rows, err, []string{"BOOL"}, want)
					}
				}
			}
		}
	}
	for _, q := range []string{"SELECT NULL::text LIKE p ESCAPE e FROM t", "SELECT c LIKE NULL::text FROM t", "SELECT c LIKE p ESCAPE NULL::text FROM t"} {
		rows, err := db.Query(q)
		compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{nil}})
	}
	_, err := db.Query("SELECT c LIKE 'x' ESCAPE e FROM t")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
}

func TestLikeIntegratedExplicitCollation(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE t(c TEXT COLLATE "C",p TEXT COLLATE "POSIX",e TEXT COLLATE "POSIX")`)
	for _, state := range []string{"empty", "nonnull", "null"} {
		if state == "nonnull" {
			execSQL(t, db, "INSERT INTO t VALUES('a','a','!')")
		} else if state == "null" {
			execSQL(t, db, "UPDATE t SET c=NULL,p=NULL,e=NULL")
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
				compareOpsRows(t, rows, err, []string{"BOOL"}, want)
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
				compareOpsRows(t, rows, err, []string{"BOOL"}, want)
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
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
					} else {
						want := [][]any{}
						if state == "null" {
							want = [][]any{{nil}}
						}
						compareOpsRows(t, rows, err, []string{"BOOL"}, want)
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
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
				} else {
					want := [][]any{}
					if state == "null" {
						want = [][]any{{nil}}
					}
					compareOpsRows(t, rows, err, []string{"BOOL"}, want)
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
				want := stuntgres.Error{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: tc.position}
				rows, err := db.Query(q)
				if rows != nil {
					rows.Close()
					t.Fatal("explicit conflict exposed cursor")
				}
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("explicit conflict prepared successfully")
				}
				typeCoreDiagnostic(t, err, want)
			})
		}
	}
}

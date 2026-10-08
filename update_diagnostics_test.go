package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestUpdateCoreDiagnosticPhases(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,n SMALLINT NOT NULL)")
	for _, population := range []bool{false, true} {
		if population {
			execSQL(t, db, "INSERT INTO t VALUES(1,1)")
		}
		for _, tc := range []struct {
			q string
			d stuntgres.Error
		}{
			{"UPDATE t SET a=TRUE", stuntgres.Error{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Position: "16", Hint: "You will need to rewrite or cast the expression."}},
			{"UPDATE t SET a=missing", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: "16"}},
			{"UPDATE t SET a=1,a=2", stuntgres.Error{Code: "42601", Message: `multiple assignments to same column "a"`}},
			{"UPDATE t SET a=1 WHERE", stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
			{"UPDATE /*é*/ missing SET a=1", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "14"}},
		} {
			_, err := db.Exec(tc.q)
			typeCoreDiagnostic(t, err, tc.d)
			rows, err := db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("invalid UPDATE returned rows")
			}
			typeCoreDiagnostic(t, err, tc.d)
			stmt, err := db.Prepare(tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid UPDATE prepared")
			}
			typeCoreDiagnostic(t, err, tc.d)
		}
		for _, q := range []string{"UPDATE t SET n=40000", "UPDATE t SET n=40000 WHERE FALSE"} {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("constant destination cast executed in Prepare", err)
			}
			_, err = stmt.Exec()
			stmt.Close()
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "smallint out of range"})
		}
	}
	for _, q := range []string{"UPDATE t SET a=9 FROM t", "UPDATE t SET a=9 trailing", "UPDATE t SET a=9; SELECT a FROM t"} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("excluded suffix executed", q)
		}
		wantTuples(t, db, "SELECT a,n FROM t", [][]any{{int64(1), int64(1)}})
	}
}

func TestUpdateCoreArithmeticAssignmentDiagnostic(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(f BOOL)")
	for _, populated := range []bool{false, true} {
		wantRows := [][]any{}
		if populated {
			execSQL(t, db, "INSERT INTO t VALUES(TRUE),(FALSE)")
			wantRows = [][]any{{false}, {true}}
		}
		for _, tc := range []struct {
			name, q, position, typ string
		}{
			{"plain", "UPDATE t SET f=1+2", "16", "integer"},
			{"grouped", "UPDATE t SET f=(1+2)", "17", "integer"},
			{"utf8_grouped", "/*é*/ UPDATE t SET f=(1+2)", "23", "integer"},
			{"postfix", "UPDATE t SET f=(1+2)::bigint", "17", "bigint"},
		} {
			for _, protocol := range []string{"exec", "query", "prepare"} {
				t.Run(map[bool]string{false: "empty", true: "populated"}[populated]+"/"+tc.name+"/"+protocol, func(t *testing.T) {
					defer wantTuples(t, db, "SELECT f FROM t ORDER BY f", wantRows)
					want := stuntgres.Error{Code: "42804", Message: `column "f" is of type boolean but expression is of type ` + tc.typ, Hint: "You will need to rewrite or cast the expression.", Position: tc.position}
					switch protocol {
					case "exec":
						result, err := db.Exec(tc.q)
						if result != nil {
							t.Fatal("invalid assignment returned a result")
						}
						typeCoreDiagnostic(t, err, want)
					case "query":
						rows, err := db.Query(tc.q)
						if rows != nil {
							rows.Close()
							t.Fatal("invalid assignment exposed rows")
						}
						typeCoreDiagnostic(t, err, want)
					case "prepare":
						stmt, err := db.Prepare(tc.q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid assignment prepared")
						}
						typeCoreDiagnostic(t, err, want)
					}
				})
			}
		}
	}
}

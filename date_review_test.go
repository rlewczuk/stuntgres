package stuntgres_test

import (
	"testing"

	"stuntgres"
)

// REL_18_0 parse_target.c transformAssignedExpr names the source with
// format_type_be and reports the transformed constant's quoted-input location.
func TestDateTypedAssignmentDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE i(v INTEGER)")
			execSQL(t, db, "INSERT INTO i VALUES(7)")
			query := "INSERT INTO i VALUES(DATE '2000-01-01')"
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(query)
				err = prepareErr
				if stmt != nil {
					stmt.Close()
					t.Fatal("incompatible typed DATE assignment prepared successfully")
				}
			} else {
				_, err = db.Exec(query)
			}
			rows, rowErr := db.Query("SELECT v FROM i ORDER BY v")
			if rowErr != nil {
				t.Fatal(rowErr)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("sentinel row missing", rows.Err())
			}
			var sentinel int
			if rowErr := rows.Scan(&sentinel); rowErr != nil || sentinel != 7 {
				t.Fatalf("retained sentinel = %d, error = %v", sentinel, rowErr)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("failed assignment published extra rows", rows.Err())
			}
			timeDiagnostic(t, err, stuntgres.Error{
				Code: "42804", Message: `column "v" is of type integer but expression is of type date`,
				Hint: "You will need to rewrite or cast the expression.", Position: "27",
			})
		})
	}
}

// REL_18_0 parse_type.c typenameTypeMod rejects DATE's absent typmodin
// before examining raw modifier expressions (including unary-plus nodes).
func TestDateModifierConversionPrecedence(t *testing.T) {
	for _, typ := range []string{"date", `"date"`} {
		for _, modifier := range []string{"3.0", "2147483648", "999999999999999999999999999999", "+3", "+3.0", "+2147483648"} {
			for _, prepared := range []bool{false, true} {
				mode := "direct"
				if prepared {
					mode = "prepared"
				}
				t.Run(typ+"/"+modifier+"/"+mode, func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					execSQL(t, db, "CREATE TABLE d(v DATE)")
					execSQL(t, db, "INSERT INTO d VALUES('2000-01-01')")
					declaration := "CREATE TABLE bad(v " + typ + "(" + modifier + "))"
					t.Run("create", func(t *testing.T) {
						var err error
						if prepared {
							stmt, prepareErr := db.Prepare(declaration)
							if prepareErr != nil {
								t.Fatal("utility Prepare must not resolve modifiers", prepareErr)
							}
							defer stmt.Close()
							_, err = stmt.Exec()
						} else {
							_, err = db.Exec(declaration)
						}
						// A rejected CREATE must not publish a partial relation.
						execSQL(t, db, "CREATE TABLE bad(v DATE)")
						execSQL(t, db, "DROP TABLE bad")
						timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20"})
					})
					t.Run("typed_literal", func(t *testing.T) {
						query := "SELECT " + typ + "(" + modifier + ") '1999-01-08' FROM d"
						var err error
						if prepared {
							stmt, prepareErr := db.Prepare(query)
							err = prepareErr
							if stmt != nil {
								stmt.Close()
								t.Fatal("DATE modifier prepared successfully")
							}
						} else {
							rows, queryErr := db.Query(query)
							err = queryErr
							if rows != nil {
								rows.Close()
								t.Fatal("invalid typed literal returned rows")
							}
						}
						timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8"})
					})
					rows, err := db.Query("SELECT v FROM d")
					if err != nil {
						t.Fatal(err)
					}
					dateCheckRows(t, rows, []any{"2000-01-01"})
				})
			}
		}
	}
}

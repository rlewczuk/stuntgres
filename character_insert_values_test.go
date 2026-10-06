package stuntgres_test

import (
	"strconv"
	"stuntgres"
	"testing"
)

// Independent expectations confirmed by root's pinned18 source VALUES capture.
func TestCharacterSourceValuesCommonTypes(t *testing.T) {
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b  ' AS text)", "CAST('a ' AS varchar)", [][]any{{"a "}, {"b  "}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS text)", [][]any{{"a "}, {"b  "}}},
		{"CAST('b  ' AS text)", "CAST('a ' AS char(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a ' AS char(2))", "CAST('b  ' AS text)", [][]any{{"a"}, {"b"}}},
		{"CAST('b  ' AS char(3))", "CAST('a ' AS varchar)", [][]any{{"a"}, {"b"}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS char(3))", [][]any{{"a "}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b ' AS varchar(2))", "CAST('a ' AS varchar(2))", [][]any{{"a "}, {"b "}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.first+"/"+tc.second+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE dst(v TEXT)")
				q := "INSERT INTO dst VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					result, err := stmt.Exec()
					insertSelectCount(t, result, err, 2)
				} else {
					result, err := db.Exec(q)
					insertSelectCount(t, result, err, 2)
				}
				wantTuples(t, db, "SELECT v FROM dst ORDER BY v", tc.want)
				characterMetadata(t, db, "SELECT v FROM dst", []string{"TEXT"}, []int64{-1})
			})
		}
	}
}

func TestCharacterSourceValuesHeldAndAtomic(t *testing.T) {
	for _, source := range []string{"bpchar", "varchar"} {
		for _, prepared := range []bool{false, true} {
			t.Run(source+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE dst(v TEXT)")
				q := "INSERT INTO dst VALUES(CAST($1 AS text)),(CAST($2 AS " + source + ")) ORDER BY 1"
				want := [][]any{{"a "}, {"b  "}, {"c "}, {nil}}
				if source == "bpchar" {
					want = [][]any{{"a"}, {"b  "}, {"c"}, {nil}}
				}
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					for _, args := range [][]any{{"b  ", "a "}, {nil, "c "}} {
						result, err := stmt.Exec(args...)
						insertSelectCount(t, result, err, 2)
					}
				} else {
					for _, args := range [][]any{{"b  ", "a "}, {nil, "c "}} {
						result, err := db.Exec(q, args...)
						insertSelectCount(t, result, err, 2)
					}
				}
				wantTuples(t, db, "SELECT v FROM dst ORDER BY v", want)
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("atomic/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE dst(v VARCHAR(2))")
			execSQL(t, db, "INSERT INTO dst VALUES('s')")
			q := "INSERT INTO dst VALUES(CAST('aa' AS text)),(CAST('zzx' AS varchar)) ORDER BY 1"
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal("assignment sizing rejected at Prepare", e)
				}
				defer stmt.Close()
				result, e := stmt.Exec()
				err = e
				if result != nil {
					t.Fatal("failed source insert exposed result")
				}
			} else {
				result, e := db.Exec(q)
				err = e
				if result != nil {
					t.Fatal("failed source insert exposed result")
				}
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type character varying(2)"})
			wantTuples(t, db, "SELECT v FROM dst", [][]any{{"s"}})
		})
	}
}

// Root's independent pinned18 oracle confirmed these exact physical rows.
// Equal raw widths across different identities must not add BPCHAR pads.
func TestCharacterSourceValuesCrossTypmodPhysical(t *testing.T) {
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b' AS char(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(2))", [][]any{{"a "}, {"b "}}},
		{"CAST('b' AS varchar(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(3))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "NULL", [][]any{{"b "}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.first+"/"+tc.second+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE dst(v BPCHAR)")
				q := "INSERT INTO dst VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					result, err := stmt.Exec()
					insertSelectCount(t, result, err, 2)
				} else {
					result, err := db.Exec(q)
					insertSelectCount(t, result, err, 2)
				}
				wantTuples(t, db, "SELECT v FROM dst ORDER BY v", tc.want)
				characterMetadata(t, db, "SELECT v FROM dst", []string{"BPCHAR"}, []int64{-1})
			})
		}
	}
}

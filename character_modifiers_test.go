package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestCharacterModifierGrammarDiagnostics(t *testing.T) {
	// Complete diagnostic primaries/locations are root-observed pinned18
	// fixtures. Utility Prepare accepts semantic modifiers; Execute validates.
	for _, base := range []string{"char", "character", "varchar", "character varying", "bpchar", `"bpchar"`, `"varchar"`} {
		for _, mod := range []string{"-1", "+2", "2,3", "2.5", "'2'", "'oops'", "2147483648"} {
			t.Run(base+"("+mod+")", func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				q := "/*é🙂*/ CREATE TABLE modifier_capture(v " + base + "(" + mod + "))"
				generic := base == "bpchar" || strings.HasPrefix(base, `"`)
				if generic && mod == "'2'" {
					s, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					if _, err = s.Exec(); err != nil {
						t.Fatal(err)
					}
					name := "BPCHAR"
					if base == `"varchar"` {
						name = "VARCHAR"
					}
					characterMetadata(t, db, "SELECT v FROM modifier_capture", []string{name}, []int64{2})
					execSQL(t, db, "DROP TABLE modifier_capture")
					execSQL(t, db, q)
					characterMetadata(t, db, "SELECT v FROM modifier_capture", []string{name}, []int64{2})
					return
				}
				want := stuntgres.Error{}
				if !generic {
					token := mod
					if mod == "-1" {
						token = "-"
					}
					if mod == "+2" {
						token = "+"
					}
					if mod == "2,3" {
						token = ","
					}
					position := utf8.RuneCountInString(q[:strings.LastIndex(q, "(")+1]) + 1
					if mod == "2,3" {
						position++
					}
					want = stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: strconv.Itoa(position)}
				} else {
					position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, base)]) + 1)
					switch mod {
					case "-1":
						name := "char"
						if base == `"varchar"` {
							name = "varchar"
						}
						want = stuntgres.Error{Code: "22023", Message: "length for type " + name + " must be at least 1", Position: position}
					case "+2":
						want = stuntgres.Error{Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: position}
					case "2,3":
						want = stuntgres.Error{Code: "22023", Message: "invalid type modifier", Position: position}
					case "2.5":
						want = stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "2.5"`, Position: position}
					case "'oops'":
						want = stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: position}
					case "2147483648":
						want = stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: position}
					}
				}
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if generic {
							if e != nil {
								t.Fatal("semantic modifier rejected at Prepare", e)
							}
							_, err = s.Exec()
							s.Close()
						} else {
							err = e
							if s != nil {
								s.Close()
								t.Fatal("keyword syntax error exposed a prepared statement")
							}
						}
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, want)
				}
			})
		}
	}
}

// Root's pinned18 review capture independently confirmed these complete
// records and phases: text rejects modifiers before interpreting them, while
// simple generic character identifiers reach the typmod input function.
func TestCharacterReviewModifierDiagnostics(t *testing.T) {
	for _, base := range []string{"text", `"text"`, "bpchar", `"bpchar"`, `"varchar"`} {
		modifiers := []string{"foo", `"2"`}
		if base == "text" || base == `"text"` {
			modifiers = []string{"'oops'", "2147483648", "+2"}
		}
		for _, modifier := range modifiers {
			for _, prepared := range []bool{false, true} {
				t.Run(base+"("+modifier+")/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					q := "/*é🙂*/ CREATE TABLE invalid(v " + base + "(" + modifier + "))"
					var err error
					if modifier == `"2"` {
						if prepared {
							stmt, e := db.Prepare(q)
							if e != nil {
								t.Fatal("simple identifier rejected at Prepare", e)
							}
							defer stmt.Close()
							_, err = stmt.Exec()
						} else {
							_, err = db.Exec(q)
						}
						if err != nil {
							t.Fatal(err)
						}
						typ, value := "BPCHAR", "a "
						if base == `"varchar"` {
							typ, value = "VARCHAR", "a"
						}
						characterMetadata(t, db, "SELECT v FROM invalid", []string{typ}, []int64{2})
						execSQL(t, db, "INSERT INTO invalid VALUES('a')")
						wantTuples(t, db, "SELECT v FROM invalid", [][]any{{value}})
						return
					}
					want := stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "foo"`}
					if base == "text" || base == `"text"` {
						want.Code, want.Message = "42601", `type modifier is not allowed for type "text"`
					}
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, base)]) + 1)
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal("semantic modifier rejected at Prepare", e)
						}
						defer stmt.Close()
						_, err = stmt.Exec()
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, want)
					_, err = db.Query("SELECT v FROM invalid")
					sqlState(t, err, "42P01")
				})
			}
		}
	}
}

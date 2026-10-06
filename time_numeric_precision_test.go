package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestTimeNumericPrecision(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, head := range []string{"TIME", `"time"`, `U&"t\0069me"`} {
		for _, tc := range []struct{ number, token, primary string }{
			{"1.5foo", "1.5foo", "trailing junk after numeric literal"}, {".5foo", ".5foo", "trailing junk after numeric literal"}, {"1.5e+", "1.5e+", "trailing junk after numeric literal"}, {"1..2", "..", "syntax error"}, {".5e-", ".5e-", "trailing junk after numeric literal"}, {"1.5e", "1.5e", "trailing junk after numeric literal"}, {"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal"}, {"1.5_", "1.5_", "trailing junk after numeric literal"}, {"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal"}, {"1e+", "1e+", "trailing junk after numeric literal"}, {"1efoo", "1efoo", "trailing junk after numeric literal"}, {"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal"},
		} {
			q := "/*é*/ CREATE TABLE bad(v " + head + "(" + tc.number + "))"
			want := stuntgres.Error{Code: "42601", Message: tc.primary + " at or near \"" + tc.token + "\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)}
			_, err := db.Exec(q)
			timeDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed syntax exposed statement")
			}
			timeDiagnostic(t, err, want)
			execSQL(t, db, "CREATE TABLE bad(v TIME)")
			execSQL(t, db, "DROP TABLE bad")
		}
		for _, number := range []string{".5", "1.5", "1e+2", "2147483648", "999999999999999999999999", "-2147483648"} {
			q := "CREATE TABLE bad(v " + head + "(" + number + "))"
			want := stuntgres.Error{Code: "22P02", Message: "invalid input syntax for type integer: \"" + number + "\"", Position: "20"}
			if head == "TIME" {
				token := number
				if strings.HasPrefix(number, "-") {
					token = "-"
				}
				want = stuntgres.Error{Code: "42601", Message: "syntax error at or near \"" + token + "\"", Position: strconv.Itoa(strings.Index(q, number) + 1)}
			} else if number == "2147483648" || number == "999999999999999999999999" {
				want.Code = "22003"
				want.Message = "value \"" + number + "\" is out of range for type integer"
			} else if number == "-2147483648" {
				want.Code = "22023"
				want.Message = "TIME(-2147483648) precision must not be negative"
			}
			_, err := db.Exec(q)
			timeDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if head == "TIME" {
				if stmt != nil {
					stmt.Close()
					t.Fatal("bad syntax prepared")
				}
				timeDiagnostic(t, err, want)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.Exec()
				stmt.Close()
				timeDiagnostic(t, err, want)
			}
			execSQL(t, db, "CREATE TABLE bad(v TIME)")
			execSQL(t, db, "DROP TABLE bad")
		}
		for _, number := range []string{"0x3", "0b11", "0o3", "0_3"} {
			execSQL(t, db, "CREATE TABLE radix(v "+head+"("+number+"))")
			execSQL(t, db, "DROP TABLE radix")
		}
	}
}

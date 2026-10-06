package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestTimestampGenericNumericPrecisionExecution(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
		for _, tc := range []struct{ number, raw, code string }{
			{".5", ".5", "22P02"}, {"1.5e+3", "1.5e+3", "22P02"}, {"1e+2", "1e+2", "22P02"}, {"1.", "1.", "22P02"},
			{"1_0.5", "1_0.5", "22P02"}, {"1.5e+3_0", "1.5e+3_0", "22P02"},
			{"1.5", "1.5", "22P02"}, {"1e2", "1e2", "22P02"},
			{"+.5", ".5", "42601"}, {"-1.5e+3", "-1.5e+3", "22P02"}, {"+1e+2", "1e+2", "42601"}, {"-1.", "-1.", "22P02"},
			{"+3", "3", "42601"}, {"+0", "0", "42601"}, {"+2147483648", "2147483648", "42601"},
			{"+7", "7", "42601"},
			{"2147483648", "2147483648", "22003"}, {"2147483649", "2147483649", "22003"},
			{"-2147483649", "-2147483649", "22003"}, {"999999999999999999999999999999", "999999999999999999999999999999", "22003"},
			{"2_147_483_648", "2_147_483_648", "22003"}, {"0x80000000", "0x80000000", "22003"},
			{"0o20000000000", "0o20000000000", "22003"}, {"0b10000000000000000000000000000000", "0b10000000000000000000000000000000", "22003"},
			{"-2147483648", "-2147483648", "22023"},
		} {
			for _, table := range []string{"bad", `"é"`} {
				q := "CREATE TABLE " + table + "(v " + head + "(" + tc.number + "))"
				message := `invalid input syntax for type integer: "` + tc.raw + `"`
				if tc.code == "22003" {
					message = `value "` + tc.raw + `" is out of range for type integer`
				}
				if tc.code == "22023" {
					message = "TIMESTAMP(-2147483648) precision must not be negative"
				}
				if tc.code == "42601" {
					message = "type modifiers must be simple constants or identifiers"
				}
				want := stuntgres.Error{Code: tc.code, Message: message, Position: "20"}
				_, err := db.Exec(q)
				typeCoreDiagnostic(t, err, want)
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal("valid generic numeric syntax failed Prepare", q, err)
				}
				_, err = stmt.Exec()
				stmt.Close()
				typeCoreDiagnostic(t, err, want)
				execSQL(t, db, "CREATE TABLE "+table+"(v TIMESTAMP)")
				for _, prepared := range []bool{false, true} {
					skip := "CREATE TABLE IF NOT EXISTS " + table + "(v " + head + "(" + tc.number + "))"
					if prepared {
						s, e := db.Prepare(skip)
						if e != nil {
							t.Fatal(e)
						}
						_, e = s.Exec()
						s.Close()
						if e != nil {
							t.Fatal(e)
						}
					} else {
						execSQL(t, db, skip)
					}
				}
				timestampQuery(t, db, "SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, nil)
				if tc.number == ".5" {
					junk := "CREATE TABLE IF NOT EXISTS " + table + "(v " + head + "(1.5foo))"
					position := "46"
					if head != `"timestamp"` {
						position = "52"
					}
					want := stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: position}
					_, err := db.Exec(junk)
					typeCoreDiagnostic(t, err, want)
					s, err := db.Prepare(junk)
					if s != nil {
						s.Close()
						t.Fatal("IF NOT EXISTS hid scanner junk")
					}
					typeCoreDiagnostic(t, err, want)
				}
				execSQL(t, db, "DROP TABLE "+table)
			}
		}
	}
}

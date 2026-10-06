package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func timestampNumericPrecisionSmoke(t *testing.T, ctx context.Context, db *sql.DB,
	exec func(string, int64, ...any), check func(string, []string, []string, [][]any, ...any)) {
	t.Helper()
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
				_, err := db.ExecContext(ctx, q)
				if e := typeCoreDiagnostic(err, want); e != nil {
					t.Fatal(q, e)
				}
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal("generic numeric modifier Prepare", q, err)
				}
				_, err = stmt.ExecContext(ctx)
				stmt.Close()
				if e := typeCoreDiagnostic(err, want); e != nil {
					t.Fatal(q, e)
				}
				exec("CREATE TABLE "+table+"(v TIMESTAMP)", 0)
				for _, prepared := range []bool{false, true} {
					skip := "CREATE TABLE IF NOT EXISTS " + table + "(v " + head + "(" + tc.number + "))"
					if prepared {
						s, e := db.PrepareContext(ctx, skip)
						if e != nil {
							t.Fatal(e)
						}
						r, e := s.ExecContext(ctx)
						s.Close()
						if e != nil {
							t.Fatal(e)
						}
						n, e := r.RowsAffected()
						if e != nil || n != 0 {
							t.Fatal(n, e)
						}
					} else {
						exec(skip, 0)
					}
				}
				check("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, nil)
				if tc.number == ".5" {
					junk := "CREATE TABLE IF NOT EXISTS " + table + "(v " + head + "(1.5foo))"
					position := "46"
					if head != `"timestamp"` {
						position = "52"
					}
					want := stuntgres.Error{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: position}
					_, err := db.ExecContext(ctx, junk)
					if e := typeCoreDiagnostic(err, want); e != nil {
						t.Fatal(e)
					}
					s, err := db.PrepareContext(ctx, junk)
					if s != nil {
						s.Close()
						t.Fatal("IF NOT EXISTS hid scanner junk")
					}
					if e := typeCoreDiagnostic(err, want); e != nil {
						t.Fatal(e)
					}
				}
				exec("DROP TABLE "+table, 0)
			}
		}
	}
}

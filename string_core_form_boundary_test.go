package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestStringCoreReviewForms(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, function := range []string{`pg_catalog.normalize`, `pg_catalog.is_normalized`} {
		for _, form := range []string{"NFKC", "NFKD"} {
			for _, parameters := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/parameters=%t", function, form, parameters), func(t *testing.T) {
					q := "SELECT " + function + "('x','" + form + "')"
					var args []any
					if parameters {
						q = "SELECT " + function + "($1,$2)"
						args = []any{"x", form}
					}
					want := stuntgres.Error{Code: "22023", Message: "invalid normalization form: " + form}
					_, err := db.Exec(q, args...)
					typeCoreDiagnostic(t, err, want)
					s, err := db.Prepare(q)
					if err != nil {
						t.Fatal("runtime form validation failed at Prepare", err)
					}
					defer s.Close()
					_, err = s.Exec(args...)
					typeCoreDiagnostic(t, err, want)
				})
			}
		}
		for _, tc := range []struct {
			source, form, typ string
			want              any
		}{
			{"é", "nfKc", "TEXT", "é"}, {"é", "NfKd", "TEXT", "é"},
		} {
			want, typ := tc.want, tc.typ
			if function == "pg_catalog.is_normalized" {
				want, typ = false, "BOOL"
			}
			for _, prepared := range []bool{false, true} {
				q := "SELECT " + function + "($1,$2) AS v"
				if prepared {
					s, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := s.Query(tc.source, tc.form)
					queryCoreRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{want}}, false)
					s.Close()
				} else {
					rows, err := db.Query(q, tc.source, tc.form)
					queryCoreRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{want}}, false)
				}
			}
		}
		for _, args := range [][]any{{nil, "NFKC"}, {"x", nil}} {
			typ := "TEXT"
			if function == "pg_catalog.is_normalized" {
				typ = "BOOL"
			}
			q := "SELECT " + function + "($1,$2) AS v"
			rows, err := db.Query(q, args...)
			queryCoreRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{nil}}, false)
			s, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = s.Query(args...)
			queryCoreRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{nil}}, false)
			s.Close()
		}
	}
}

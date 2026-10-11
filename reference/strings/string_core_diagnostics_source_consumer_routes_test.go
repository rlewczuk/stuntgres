package reference_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreDiagnosticsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			for _, tc := range stringCoreDiagnosticCases() {
				t.Run(tc.q, func(t *testing.T) {
					_, err := db.Exec(tc.q)
					fixture.StringCoreControlDiagnostic(t, err, tc.want)
					stmt, err := db.Prepare(tc.q)
					if tc.prepare {
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare returned statement")
						}
						fixture.StringCoreControlDiagnostic(t, err, tc.want)
						return
					}
					if err != nil {
						t.Fatal("runtime error at Prepare", err)
					}
					defer stmt.Close()
					_, err = stmt.Exec()
					fixture.StringCoreControlDiagnostic(t, err, tc.want)
				})
			}
			_, err := db.Exec("/*é🙂*/ SELECT 1 || 2")
			fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer || integer", Position: "17", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
			stmt, err := db.Prepare(`SELECT "normalize"($1,$2)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			_, err = stmt.Exec("x", "bad")
			fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"})
			for _, tc := range []struct{ input, bytes string }{{string([]byte{0}), "0x00"}, {string([]byte{255}), "0xff"}} {
				_, err = stmt.Exec(tc.input, "NFC")
				fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes, Context: "unnamed portal parameter $1"})
			}
		})
	}
}

func TestReferenceStringCoreCancellationAndEncodingSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			var encoding string
			if err := db.QueryRow("SHOW server_encoding").Scan(&encoding); err != nil || encoding != "UTF8" {
				t.Fatal(encoding, err)
			}
			for _, q := range []string{"SET server_encoding='LATIN1'", "RESET server_encoding"} {
				_, err := db.Exec(q)
				fixture.State(t, err, "55P02")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rows, err := db.QueryContext(ctx, "SELECT normalize('é')")
			if rows != nil {
				rows.Close()
				t.Fatal("cancelled query exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("context error became SQL error", err)
			}
			stmt, err := db.PrepareContext(ctx, "SELECT normalize($1)")
			if stmt != nil {
				stmt.Close()
				t.Fatal("cancelled prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceStringCoreReviewFormsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
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
							want := fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: " + form}
							_, err := db.Exec(q, args...)
							fixture.StringCoreControlDiagnostic(t, err, want)
							s, err := db.Prepare(q)
							if err != nil {
								t.Fatal("runtime form validation failed at Prepare", err)
							}
							defer s.Close()
							_, err = s.Exec(args...)
							fixture.StringCoreControlDiagnostic(t, err, want)
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
							fixture.StringCoreRetainedRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{want}}, false)
							s.Close()
						} else {
							rows, err := db.Query(q, tc.source, tc.form)
							fixture.StringCoreRetainedRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{want}}, false)
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
					fixture.StringCoreRetainedRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{nil}}, false)
					s, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = s.Query(args...)
					fixture.StringCoreRetainedRows(t, rows, err, []string{"v"}, []string{typ}, [][]any{{nil}}, false)
					s.Close()
				}
			}
		})
	}
}

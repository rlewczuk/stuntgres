package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONBDiagnostics(t *testing.T) {
	for _, name := range []string{"hstore_to_jsonb", "hstore_to_jsonb_loose"} {
		t.Run(name, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, tc := range []struct{ args, signature string }{{"1", "integer"}, {"'a=>1'::text", "text"}, {"", ""}, {"'a=>1'::hstore,1", "hstore, integer"}} {
					want := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+tc.signature+") does not exist")
					want.Position = "8"
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					for _, prepared := range []bool{false, true} {
						hstoreError(t, p, b, c, "SELECT "+name+"("+tc.args+")", prepared, true, true, want)
					}
				}
				for _, tc := range []struct{ input, message string }{{"a=b", `syntax error in hstore, near "b" at position 2`}, {"a=>", "syntax error in hstore: unexpected end of string"}} {
					q := "/*é*/ SELECT " + name + "(" + hstoreLiteral(tc.input) + "::hstore)"
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
					for _, prepared := range []bool{false, true} {
						hstoreError(t, p, b, c, q, prepared, true, true, want)
					}
				}
				q := "SELECT " + name + "($1::hstore) AS j"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					input, code, message string
					preview              bool
				}{
					{"a=b", "42601", `syntax error in hstore, near "b" at position 2`, true},
					{"a=>", "42601", "syntax error in hstore: unexpected end of string", true},
					{"a=>\x00", "22021", `invalid byte sequence for encoding "UTF8": 0x00`, false},
					{"a=>\xff", "22021", `invalid byte sequence for encoding "UTF8": 0xff`, false},
				} {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Context = "unnamed portal parameter $1"
					if tc.preview {
						want.Context += " = '...'"
					}
					for _, arg := range []any{tc.input, []byte(tc.input)} {
						for _, prepared := range []bool{false, true} {
							hstoreError(t, p, b, c, q, prepared, true, false, want, arg)
						}
						hstoreError(t, p, b, held, q, false, true, false, want, arg)
					}
				}
				value := `{"a": "1"}`
				if name == "hstore_to_jsonb_loose" {
					value = `{"a": 1}`
				}
				hstoreRun(t, p, b, held, hstoreJSONBSelect(b, q, []string{"j"}, [][]any{{value}}, false, "a=>1"))
				hstoreJSONBAtomicity(t, p, b, c, name)
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if rows, err := c.QueryContext(ctx, q, "a=>1"); !errors.Is(err, context.Canceled) || rows != nil {
						t.Fatalf("canceled direct %v/%v", rows, err)
					}
					if rows, err := stmt.QueryContext(ctx, "a=>1"); !errors.Is(err, context.Canceled) || rows != nil {
						t.Fatalf("canceled held %v/%v", rows, err)
					}
					if s, err := c.PrepareContext(ctx, q); !errors.Is(err, context.Canceled) || s != nil {
						t.Fatalf("canceled prepare %v/%v", s, err)
					}
					insert := "INSERT INTO hs_jsonb_atomic VALUES (9," + name + "($1::hstore))"
					insertStmt, err := c.PrepareContext(p.Ctx, insert)
					if err != nil {
						t.Fatal(err)
					}
					defer insertStmt.Close()
					if _, err := c.ExecContext(ctx, insert, "a=>1"); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if _, err := insertStmt.ExecContext(ctx, "a=>1"); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					hstoreRun(t, p, b, c, hstoreJSONBSelect(b, "SELECT j FROM hs_jsonb_atomic ORDER BY id", []string{"j"}, [][]any{{`{"sentinel": true}`}, {value}, {value}}, true))
				}
			})
		})
	}
}

func hstoreJSONBAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_jsonb_atomic(id integer,j jsonb)", 0))
	hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_jsonb_atomic VALUES (1,'{"sentinel": true}')`, 1))
	q := "INSERT INTO hs_jsonb_atomic VALUES (2," + name + "($1::hstore)),(3," + name + "($2::hstore))"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	want := hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 2)
	unchanged := hstoreJSONBSelect(b, "SELECT j FROM hs_jsonb_atomic ORDER BY id", []string{"j"}, [][]any{{`{"sentinel": true}`}}, true)
	for _, prepared := range []bool{false, true} {
		hstoreError(t, p, b, c, q, prepared, false, false, want, "a=>1", "a=b")
		hstoreRun(t, p, b, c, unchanged)
	}
	hstoreError(t, p, b, held, q, false, false, false, want, "a=>1", "a=b")
	hstoreRun(t, p, b, c, unchanged)
	hstoreRun(t, p, b, held, fixture.Exec(q, 2, "a=>1", "a=>1"))
	value := `{"a": "1"}`
	if name == "hstore_to_jsonb_loose" {
		value = `{"a": 1}`
	}
	hstoreRun(t, p, b, c, hstoreJSONBSelect(b, "SELECT j FROM hs_jsonb_atomic ORDER BY id", []string{"j"}, [][]any{{`{"sentinel": true}`}, {value}, {value}}, true))
}

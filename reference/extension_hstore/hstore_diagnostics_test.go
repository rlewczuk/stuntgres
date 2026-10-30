package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func hstoreSeedDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_diag(id integer NOT NULL,v hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_diag VALUES (1,'a=>1'),(2,NULL)", 2))
}

func hstoreDiagnosticRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT id,v FROM hs_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), `"a"=>"1"`}, {int64(2), nil}}, []bool{false, true}))
}

func TestReferenceHstoreDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, input, message string }{
		{"equals", "a=b", `syntax error in hstore, near "b" at position 2`},
		{"missing_key", " =>b", `syntax error in hstore, near "=" at position 1`},
		{"spaces_missing_key", "  =>null", `syntax error in hstore, near "=" at position 2`},
		{"value_quote", `aa=>"`, "syntax error in hstore: unexpected end of string"},
		{"missing_value", "a=>", "syntax error in hstore: unexpected end of string"},
		{"key_quote", `"aa`, "syntax error in hstore: unexpected end of string"},
		{"key_escape", `a\`, "syntax error in hstore: unexpected end of string"},
		{"value_escape", `a=>b\`, "syntax error in hstore: unexpected end of string"},
		{"quoted_escape", `a=>"b\`, "syntax error in hstore: unexpected end of string"},
		{"multibyte", "é=雪", `syntax error in hstore, near "雪" at position 3`},
		{"json_object", "{}", "syntax error in hstore: unexpected end of string"},
	} {
		for _, route := range []string{"direct", "fresh", "held"} {
			for _, operation := range []string{"cast", "cast_function", "assignment", "multirow", "bound_cast", "bound_multirow", "bound_update"} {
				if route == "held" && operation != "bound_cast" && operation != "bound_multirow" && operation != "bound_update" {
					continue
				}
				t.Run(tc.name+"/"+route+"/"+operation, func(t *testing.T) {
					hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						hstoreSeedDiagnostic(t, p, b, c)
						prefix, suffix := "", ""
						query, bound, parameter := false, false, 2
						var args []any
						switch operation {
						case "cast":
							prefix, suffix, query = "/*é*/ SELECT ", "::hstore AS v", true
						case "cast_function":
							prefix, suffix, query = "/*é*/ SELECT CAST(", " AS hstore) AS v", true
						case "assignment":
							prefix, suffix = "/*é*/ INSERT INTO hs_diag VALUES (3,", ")"
						case "multirow":
							prefix, suffix = "/*é*/ INSERT INTO hs_diag VALUES (3,'b=>2'),(4,", ")"
						case "bound_cast":
							prefix, query, bound, parameter, args = "SELECT $1::hstore AS v", true, true, 1, []any{tc.input}
						case "bound_multirow":
							prefix, bound, args = "INSERT INTO hs_diag VALUES (3,$1),(4,$2)", true, []any{"b=>2", tc.input}
						case "bound_update":
							prefix, bound, args = "UPDATE hs_diag SET v=CASE WHEN id=1 THEN $1::hstore ELSE $2::hstore END", true, []any{"b=>2", tc.input}
						}
						q := prefix
						want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
						if bound {
							want = hstoreParameterDiagnostic("42601", tc.message, parameter)
						} else {
							q += hstoreLiteral(tc.input) + suffix
							want.Position = strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
						}
						var db fixture.Executor = c
						if route == "held" {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatalf("bound Prepare must succeed: %v", err)
							}
							defer stmt.Close()
							db = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						hstoreError(t, p, b, db, q, route == "fresh", query, !bound, want, args...)
						hstoreDiagnosticRows(t, p, b, c)
						if route == "held" {
							if query {
								hstoreRun(t, p, b, db, hstoreSelect(b, q, []string{"v"}, [][]any{{`"z"=>"3"`}}, nil, "z=>3"))
							} else if operation == "bound_multirow" {
								hstoreRun(t, p, b, db, fixture.Exec(q, 2, "b=>2", "z=>3"))
								hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT id,v FROM hs_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), `"a"=>"1"`}, {int64(2), nil}, {int64(3), `"b"=>"2"`}, {int64(4), `"z"=>"3"`}}, []bool{false, true}))
							} else {
								hstoreRun(t, p, b, db, fixture.Exec(q, 2, "b=>2", "z=>3"))
								hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT id,v FROM hs_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), `"b"=>"2"`}, {int64(2), `"z"=>"3"`}}, []bool{false, true}))
							}
						}
					})
				})
			}
		}
	}
	for _, tc := range []struct{ name, input, bytes string }{{"nul", "a=>\x00", "0x00"}, {"invalid_utf8", "a=>\xff", "0xff"}} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("transport/%s/prepared_%v", tc.name, prepared), func(t *testing.T) {
				hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					hstoreSeedDiagnostic(t, p, b, c)
					want := hstoreParameterDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+tc.bytes, 2)
					want.Context = "unnamed portal parameter $2"
					hstoreError(t, p, b, c, "INSERT INTO hs_diag VALUES (3,$1),(4,$2)", prepared, false, false, want, "b=>2", tc.input)
					hstoreDiagnosticRows(t, p, b, c)
				})
			})
		}
	}
}

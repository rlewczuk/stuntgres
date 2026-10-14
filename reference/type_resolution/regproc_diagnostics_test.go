package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegProcDiagnostics(t *testing.T) {
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, alias := range []string{"regproc", "regprocedure"} {
			frameQuery := "SELECT $1::" + alias + " AS r"
			runtimeQuery := "SELECT $1::text::" + alias + " AS r"
			frame, err := c.PrepareContext(p.Ctx, frameQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer frame.Close()
			runtime, err := c.PrepareContext(p.Ctx, runtimeQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			facts := []struct{ input, code, message string }{
				{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
				{"08", "22P02", `invalid input syntax for type oid: "08"`},
			}
			if alias == "regproc" {
				facts = append(facts, []struct{ input, code, message string }{
					{"abs", "42725", `more than one function named "abs"`},
					{"lower", "42725", `more than one function named "lower"`},
					{"missing_regproc_name", "42883", `function "missing_regproc_name" does not exist`},
					{"absent_regproc_schema.boolin", "42883", `function "absent_regproc_schema.boolin" does not exist`},
					{"RI_FKey_check_ins", "42883", `function "RI_FKey_check_ins" does not exist`},
					{" 1242 ", "42883", `function " 1242 " does not exist`},
					{"a..b", "42602", "invalid name syntax"},
				}...)
			} else {
				facts = append(facts, []struct{ input, code, message string }{
					{"missing_regproc_name(integer)", "42883", `function "missing_regproc_name(integer)" does not exist`},
					{" abs ( text ) ", "42883", `function " abs ( text ) " does not exist`},
					{"absent_regproc_schema.abs(integer)", "42883", `function "absent_regproc_schema.abs(integer)" does not exist`},
					{"RI_FKey_check_ins()", "42883", `function "RI_FKey_check_ins()" does not exist`},
					{"now", "22P02", "expected a left parenthesis"},
					{"now(", "22P02", "expected a right parenthesis"},
					{"abs(integer,)", "22P02", "expected a type name"},
					{"abs(integer[)", "22P02", "improper type name"},
					{"abs(numeric(8,2)", "22P02", "improper type name"},
					{"a..b(integer)", "42602", "invalid name syntax"},
					{"abs(missing_regproc_type)", "42704", `type "missing_regproc_type" does not exist`},
					{"abs(integer(4))", "42601", `syntax error at or near "("`},
					{"abs(numeric(0))", "22023", "NUMERIC precision 0 must be between 1 and 1000"},
					{"abs(NONE)", "42601", `syntax error at or near "NONE"`},
					{"now(" + strings.Repeat("integer,", 100) + "integer)", "54023", "too many arguments"},
					{"now(" + strings.Repeat("integer,", 99) + "integer)", "42883", `function "now(` + strings.Repeat("integer,", 99) + `integer)" does not exist`},
				}...)
			}
			for _, fact := range facts {
				t.Run(alias+"/"+fact.input, func(t *testing.T) {
					for _, route := range []string{"literal-query", "literal-prepare", "direct-frame", "held-frame", "runtime-text", "held-runtime-text"} {
						t.Run(route, func(t *testing.T) {
							q := frameQuery
							var rows *sql.Rows
							var err error
							switch route {
							case "literal-query":
								q = "SELECT /* é🙂 */ " + fixture.RegtypeLiteral(fact.input) + "::" + alias + " AS r"
								rows, err = c.QueryContext(p.Ctx, q)
							case "literal-prepare":
								q = "SELECT /* é🙂 */ " + fixture.RegtypeLiteral(fact.input) + "::" + alias + " AS r"
								var statement *sql.Stmt
								statement, err = c.PrepareContext(p.Ctx, q)
								if statement != nil {
									statement.Close()
									t.Error("invalid literal Prepare exposed statement")
								}
							case "direct-frame":
								rows, err = c.QueryContext(p.Ctx, q, fact.input)
							case "held-frame":
								rows, err = frame.QueryContext(p.Ctx, fact.input)
							case "runtime-text":
								q = runtimeQuery
								rows, err = c.QueryContext(p.Ctx, q, fact.input)
							case "held-runtime-text":
								q = runtimeQuery
								rows, err = runtime.QueryContext(p.Ctx, fact.input)
							}
							if rows != nil {
								rows.Close()
								t.Error("invalid query exposed cursor")
							}
							regprocError(t, err, fact.code, fact.message, q, route, fact.input)
						})
					}
				})
			}
			valid := "boolin"
			if alias == "regprocedure" {
				valid = "boolin(cstring)"
			}
			for _, value := range []any{valid, nil, []byte(valid)} {
				want := [][]any{{valid}}
				if value == nil {
					want = [][]any{{nil}}
				}
				for _, held := range []struct {
					query string
					stmt  *sql.Stmt
				}{{frameQuery, frame}, {runtimeQuery, runtime}} {
					regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held.stmt, SQL: held.query}, r, fixture.Select(held.query, []string{"r"}, []string{strings.ToUpper(alias)}, want, value))
				}
			}
		}
	})
}

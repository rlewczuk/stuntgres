package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsTyping(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"target => $1", jsonStripDefault}, {"$1,strip_in_arrays => true", jsonStripArrays},
			{"strip_in_arrays => true,target => $1", jsonStripArrays}, {"target := $1,strip_in_arrays := false", jsonStripDefault},
			{"strip_in_arrays := true,target := $1", jsonStripArrays},
		} {
			jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls("+tc.args+") AS stripped", tc.want, jsonStripInput))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN js_doc AS json", 0))
		jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls($1::js_doc,true) AS stripped", jsonStripArrays, jsonStripInput))
	})
}

func TestReferenceJSONStripNullsTypingVariadic(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{jsonTypeQuote(jsonStripInput) + "::json,VARIADIC true", jsonStripArrays},
			{"VARIADIC " + jsonTypeQuote(jsonStripInput) + "::json", jsonStripDefault},
			{"target => " + jsonTypeQuote(jsonStripInput) + "::json,VARIADIC strip_in_arrays => true", jsonStripArrays},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls("+tc.args+") AS stripped", tc.want))
			})
		}
		t.Run("reordered_named_variadic", func(t *testing.T) {
			q := "SELECT json_strip_nulls(strip_in_arrays => true,VARIADIC target => '{}'::json)"
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature(q, "json_strip_nulls(strip_in_arrays => boolean, target => json)", 8))
		})
	})
}

func TestReferenceJSONStripNullsParameters(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT json_strip_nulls($1,$2) AS stripped", "SELECT json_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct{ input, flag, want any }{
					{jsonStripInput, true, jsonStripArrays}, {[]byte(jsonStripInput), false, jsonStripDefault}, {nil, true, nil}, {[]byte(nil), false, nil}, {jsonStripInput, nil, nil}, {`null`, true, `null`},
				} {
					jsonTypeRun(t, p, b, held, jsonStripSelect(q, tc.want, tc.input, tc.flag))
				}
				s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 0, "oops", true)
				s.Diagnostic.Detail = `Token "oops" is invalid.`
				s.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $1 = '...'"
				jsonTypeRun(t, p, b, held, s)
				jsonTypeRun(t, p, b, held, jsonStripSelect(q, jsonStripArrays, jsonStripInput, true))
			}()
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_bytes(v json)", 0))
		input := []byte(jsonStripInput)
		jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO js_bytes VALUES($1)", 1, input))
		input[2] = 'z'
		jsonTypeBoth(t, p, b, c, jsonStripSelect("SELECT json_strip_nulls(v) AS stripped FROM js_bytes", jsonStripDefault))
	})
}

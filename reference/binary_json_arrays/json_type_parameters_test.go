package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeParameters(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, suffix := range []string{"typeof", "array_length", "object_keys"} {
			q := "SELECT " + family + "_" + suffix + "($1) AS v"
			if suffix == "object_keys" {
				q = jsonTypeKeySelect(family, nil, nil).Query
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				if stmt != nil {
					stmt.Close()
				}
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				if !b.PG {
					// Native precancellation is an ordinary API error, not a fabricated
					// PostgreSQL cancellation diagnostic. Reuse this same handle below.
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := stmt.QueryContext(ctx, `{}`)
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled Query published a cursor")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("precanceled Query=%v", err)
					}
				}
				if suffix == "typeof" {
					for _, tc := range []struct{ input, value any }{{`{}`, "object"}, {`[]`, "array"}, {`null`, "null"}, {nil, nil}, {[]byte(`true`), "boolean"}, {`"s"`, "string"}, {`{}`, "object"}} {
						jsonTypeRun(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.value}}, tc.input))
					}
				} else if suffix == "array_length" {
					for _, tc := range []struct{ input, value any }{{`[]`, int64(0)}, {`[1,{},null]`, int64(3)}, {nil, nil}} {
						jsonTypeRun(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.value}}, tc.input))
					}
					jsonTypeRun(t, p, b, held, jsonTypeError(q, "22023", "cannot get array length of a non-array", 0, `{}`))
					jsonTypeRun(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}, `[1,2]`))
				} else {
					jsonTypeRun(t, p, b, held, jsonTypeKeySelect(family, jsonTypeOrderedObject, jsonTypeKeyRows(family)))
					jsonTypeRun(t, p, b, held, jsonTypeKeySelect(family, `{}`, [][]any{}))
					jsonTypeRun(t, p, b, held, jsonTypeKeySelect(family, nil, [][]any{}))
					jsonTypeRun(t, p, b, held, jsonTypeError(q, "22023", "cannot call "+family+"_object_keys on an array", 0, `[]`))
					jsonTypeRun(t, p, b, held, jsonTypeKeySelect(family, `{"ok":1}`, [][]any{{"ok", int64(1)}}))
				}
			}()
		}
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jt_bytes(j json,b jsonb)", 0))
	input := []byte(`{"a":1}`)
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jt_bytes VALUES($1,$2)", 1, input, input))
	copy(input, []byte(`{"z":9}`))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_object_keys(j) AS j, jsonb_object_keys(b) AS b FROM jt_bytes", []string{"j", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}}))
}

func TestReferenceJSONTypeParameters(t *testing.T) { jsonTypeScenario(t, jsonTypeParameters) }

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBExistsTyping(t *testing.T) {
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, op := range []string{"?", "?|", "?&"} {
			t.Run(op, func(t *testing.T) {
				q := "SELECT $1 " + op + " $2 AS v"
				if op == "?|" {
					q = "SELECT $1::jsonb ?| $2 AS v"
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				key := "a"
				if op != "?" {
					key = "{a}"
				}
				for _, tc := range []struct {
					doc  any
					want any
				}{{`{"a":1}`, true}, {`["a"]`, true}, {`"a"`, true}, {`{"outer":{"a":1}}`, false}, {nil, nil}} {
					s := jsonbExistsSelect(q, tc.want, tc.doc, key)
					s.CheckMetadata = jsonbExistsMetadata
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, nil, `{}`, nil))
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Token "bad" is invalid.`
				d.Context = "JSON data, line 1: bad\nunnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic(q, d, "bad", key)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, true, `{"a":1}`, key))
				if op != "?" {
					d = fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
					d.Detail, d.Context = `Unexpected "," character.`, "unnamed portal parameter $2 = '...'"
					for _, doc := range []any{`{}`, nil} {
						s = fixture.ErrorDiagnostic(q, d, doc, "{a,,b}")
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbExistsSelect(q, true, `{"a":1}`, key))
				}
			})
		}
		for _, q := range []string{"CREATE DOMAIN jbe_doc AS jsonb", "CREATE DOMAIN jbe_key AS text", "CREATE DOMAIN jbe_keys AS text[]", "CREATE DOMAIN jbe_char AS varchar"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, expr := range []string{
			`'{"a":1}' ? 'a'`, `'{"a":1}'::jsonb ?| '{a}'`, `'{"a":1}' ?& '{a}'`,
			`'{"a":1}'::jsonb ? 'a'::varchar`, `'{"a":1}'::jsonb ? 'a'::char(1)`,
			`'{"a":1}'::jsonb ?| '{a}'::varchar[]`, `'{"a":1}'::jsonb ?& '{a}'::char(1)[]`,
			`'{"a":1}'::jbe_doc ? 'a'::jbe_key`, `'{"a":1}'::jbe_doc ?& '{a}'::jbe_keys`,
			`'{"a":1}'::jsonb ?| ARRAY['a'::jbe_char]`,
		} {
			jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT "+expr+" AS v", true))
		}
		for _, q := range []string{`SELECT $1 ? $2 AND $1 ? $2 AS v`, `SELECT $1::jsonb ?| $2 AND $1 ?& $2 AS v`} {
			key := "a"
			if q == `SELECT $1::jsonb ?| $2 AND $1 ?& $2 AS v` {
				key = "{a}"
			}
			jsonbExistsBoth(t, p, b, c, jsonbExistsSelect(q, true, `{"a":1}`, key))
		}
		for _, tc := range []struct {
			doc  any
			want any
		}{{`{"a":1}`, true}, {`{"outer":{"a":1}}`, false}, {nil, nil}} {
			jsonbExistsBoth(t, p, b, c, jsonbExistsSelect(`SELECT $1 ?| $2::text[] AS v`, tc.want, tc.doc, "{a}"))
		}
		d := fixture.ControlsReferenceDiagnostic("42P08", "inconsistent types deduced for parameter $1")
		d.Detail, d.Position = "jsonb versus text", "13"
		stmt, err := c.PrepareContext(p.Ctx, "SELECT $1 ? $1")
		if stmt != nil {
			stmt.Close()
			t.Fatal("invalid repeated parameter published statement")
		}
		fixture.AssertDiagnostic(t, err, &d)
		for _, q := range []string{`SELECT '{}'::jsonb ? 'a' AS v`, `SELECT NULL::jsonb ? 'a' AS v`, `SELECT '{}'::jsonb ? 'a' AS v WHERE FALSE`} {
			rows := [][]any{{false}}
			if q == `SELECT NULL::jsonb ? 'a' AS v` {
				rows = [][]any{{nil}}
			}
			if q == `SELECT '{}'::jsonb ? 'a' AS v WHERE FALSE` {
				rows = nil
			}
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, rows)
			s.CheckMetadata = jsonbExistsMetadata
			jsonbExistsBoth(t, p, b, c, s)
		}
	})
}

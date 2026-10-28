package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsMetadataHeld(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, op := range jsonbPathOps {
			t.Run(op, func(t *testing.T) {
				for _, expr := range []string{`'true'::jsonb ` + op + ` '$'::jsonpath`, `NULL::jsonb ` + op + ` '$'::jsonpath`, `'{}'::jsonb ` + op + ` 'strict $.x'::jsonpath`} {
					for _, empty := range []bool{false, true} {
						q := `SELECT ` + expr + ` AS changed`
						rows := [][]any{{nil}}
						if expr == `'true'::jsonb `+op+` '$'::jsonpath` {
							rows = [][]any{{true}}
						}
						if empty {
							q += ` WHERE false`
							rows = nil
						}
						fixture.JSONPathNavigateBoth(t, p, b, c, jsonbPathOpsMetadata(jsonpathQueryResult(q, "BOOL", rows)))
					}
				}
				// No explicit casts: Prepare must infer actual operator argument types.
				q := `SELECT $1::jsonb ` + op + ` $2 AS changed`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				want := op == "@?"
				good := jsonbPathOpsMetadata(jsonbPathOpsOne(q, want, `false`, `$`))
				run(good)
				run(jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `true`, `$v`))
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at end of jsonpath input`)
				d.Context = "unnamed portal parameter $2 = '...'"
				bad := fixture.ErrorDiagnostic(q, d, `{}`, `$[`)
				bad.SelectRows = true
				bad.ErrorStage = "execute"
				run(bad)
				run(jsonbPathOpsMetadata(jsonbPathOpsOne(q, nil, nil, `$`)))
				for range 3 {
					run(jsonbPathOpsMetadata(jsonbPathOpsOne(q, true, `true`, `$`)))
					run(good)
				}
				if !b.PG {
					old, err := stmt.QueryContext(p.Ctx, good.Args...)
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					meta, err := old.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					run(jsonbPathOpsOne(q, nil, `{}`, `strict $.x`))
					fixture.ScalarAssertMetadata(t, false, meta, 0)
					fixture.StringCoreRetainedRows(t, old, nil, good.Columns, good.Types, good.Rows, false)
				}
			})
		}
	})
}

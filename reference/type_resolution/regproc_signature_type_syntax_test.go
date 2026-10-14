package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Signature arguments use SQL type syntax, not REGTYPE's whole-input numeric
// OID or dash shortcuts. Existing ValuesMetadata coverage retains those valid
// whole-input shortcuts for both routine aliases.
func TestReferenceRegProcSignatureTypeSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			const frameQuery = "SELECT $1::regprocedure AS r"
			const runtimeQuery = "SELECT $1::text::regprocedure AS r"
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

			for _, argument := range []string{"23", "-"} {
				input := "abs(" + argument + ")"
				t.Run(input, func(t *testing.T) {
					for _, route := range []string{"literal-query", "literal-prepare", "direct-frame", "held-frame", "runtime-text", "held-runtime-text"} {
						t.Run(route, func(t *testing.T) {
							// Pinned PostgreSQL18.0 independently confirms these
							// decoded-type records across all six routes.
							want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+argument+`"`)
							want.Position = "1"
							want.Context = `invalid type name "` + argument + `"`
							q := frameQuery
							var rows *sql.Rows
							var err error
							switch route {
							case "literal-query", "literal-prepare":
								q = "SELECT /* é🙂 */ " + fixture.RegtypeLiteral(input) + "::regprocedure AS r"
								// Character 17 is the opening quote, despite the
								// multibyte prefix and the inner argument location.
								want.Position = "17"
								if route == "literal-query" {
									rows, err = c.QueryContext(p.Ctx, q)
								} else {
									var statement *sql.Stmt
									statement, err = c.PrepareContext(p.Ctx, q)
									if statement != nil {
										statement.Close()
										t.Error("invalid literal Prepare exposed statement")
									}
								}
							case "direct-frame":
								want.Context += "\nunnamed portal parameter $1 = '...'"
								rows, err = c.QueryContext(p.Ctx, q, input)
							case "held-frame":
								want.Context += "\nunnamed portal parameter $1 = '...'"
								rows, err = frame.QueryContext(p.Ctx, input)
							case "runtime-text":
								q = runtimeQuery
								rows, err = c.QueryContext(p.Ctx, q, input)
							case "held-runtime-text":
								q = runtimeQuery
								rows, err = runtime.QueryContext(p.Ctx, input)
							}
							if rows != nil {
								rows.Close()
								t.Error("invalid signature argument exposed cursor")
							}
							if err == nil {
								t.Errorf("route=%s query=%q input=%q unexpectedly succeeded; want=%#v", route, q, input, want)
								return
							}
							got := fixture.ExtractDiagnostic(t, err)
							if got != want {
								t.Errorf("route=%s query=%q input=%q diagnostic got=%#v want=%#v", route, q, input, got, want)
							}
						})
					}
				})
			}

			// Reuse the same held objects after rejected inputs: parsing errors
			// must not poison their alias or runtime input authority.
			for _, held := range []struct {
				query string
				stmt  *sql.Stmt
			}{{frameQuery, frame}, {runtimeQuery, runtime}} {
				for _, value := range []any{"abs(integer)", nil, []byte("abs(integer)")} {
					want := [][]any{{"abs(integer)"}}
					if value == nil {
						want = [][]any{{nil}}
					}
					regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held.stmt, SQL: held.query}, b.Recorder, fixture.Select(held.query, []string{"r"}, []string{"REGPROCEDURE"}, want, value))
				}
			}
			// Whole-input OID/dash admission remains valid for all three
			// aliases; only signature argument segments require type syntax.
			for _, control := range []struct {
				query string
				rows  [][]any
			}{
				{
					query: "SELECT '1242'::regproc::oid::bigint AS r,'1397'::regprocedure::oid::bigint AS p,'23'::regtype::oid::bigint AS t",
					rows:  [][]any{{int64(1242), int64(1397), int64(23)}},
				},
				{
					query: "SELECT '-'::regproc::oid::bigint AS r,'-'::regprocedure::oid::bigint AS p,'-'::regtype::oid::bigint AS t",
					rows:  [][]any{{int64(0), int64(0), int64(0)}},
				},
			} {
				step := fixture.Select(control.query, []string{"r", "p", "t"}, []string{"INT8", "INT8", "INT8"}, control.rows)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			}
		})
	}
}

package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankResolution(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"tsvector", "tsquery", "real[]", "integer"} {
			name := strings.NewReplacer("[]", "array").Replace(typ)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN rank_"+name+" AS "+typ, 0))
		}
		for _, name := range ftsRankNames {
			want := ftsRankExact(.06079271)
			if name == "ts_rank_cd" {
				want = ftsRankExact(.1)
			}
			for _, tc := range []struct {
				expression string
				args       []any
				value      ftsRankWant
			}{
				{name + "($1::tsvector,$2)", []any{"cat:1", "cat"}, want},
				{name + "($1,$2::tsquery)", []any{"cat:1", "cat"}, want},
				{name + "($1::real[],$2::tsvector,$3)", []any{"{0.1,0.2,0.4,1}", "cat:1", "cat"}, want},
				{name + "($1::tsvector,$2::tsquery,$3)", []any{"cat:1", "cat", 0}, want},
				{"pg_catalog." + name + "('cat:1'::rank_tsvector,'cat'::rank_tsquery,0::rank_integer)", nil, want},
				{name + "('{0.1,0.2,0.4,1}'::rank_realarray,'cat:1'::rank_tsvector,'cat'::rank_tsquery)", nil, want},
				{name + "(ARRAY[0,0,0,1]::integer[],'cat:1A'::tsvector,'cat'::tsquery)", nil, ftsRankExact(map[bool]float64{false: .6079271, true: 1}[name == "ts_rank_cd"])},
				{name + "(ARRAY[0.1,0.2,0.4,1]::numeric[],'cat:1'::tsvector,'cat'::tsquery)", nil, want},
				{name + "('cat:1','cat')", nil, want},
				{name + "('cat:1'::tsvector,'cat','0')", nil, want},
				{name + "('{0.1,0.2,0.4,1}'::real[],'cat:1','cat')", nil, want},
			} {
				ftsRankRoutes(t, p, b, c, "SELECT "+tc.expression+" AS score", "score", "FLOAT4", tc.value, tc.args...)
			}
			// Unknown categories differ in both three-argument rank wrappers.
			// This is the independent type-resolution prediction, not an arity
			// shortcut. Root must observe the pinned resolver's exact diagnostic.
			d := fixture.ControlsReferenceDiagnostic("42725", "function "+name+"(unknown, unknown, unknown) is not unique")
			d.Position = "8"
			d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			ftsRankDiagnosticRoutes(t, p, b, c, "SELECT "+name+"('a','a','0')", d, true)
		}
		// A real visible overload, not a second spelling-based provider path.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION public.ts_rank(tsvector,tsquery) RETURNS real LANGUAGE SQL IMMUTABLE AS 'SELECT 0.75::real'", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public,pg_catalog", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Error(err)
			}
		}()
		ftsRankRoutes(t, p, b, c, "SELECT ts_rank('cat:1'::tsvector,'cat'::tsquery)", "ts_rank", "FLOAT4", ftsRankExact(.75))
		ftsRankRoutes(t, p, b, c, "SELECT pg_catalog.ts_rank('cat:1'::tsvector,'cat'::tsquery)", "ts_rank", "FLOAT4", ftsRankExact(.06079271))
	})
}

func TestReferenceFTSRankNullMetadata(t *testing.T) {
	for _, name := range ftsRankNames {
		for form, types := range ftsRankForms {
			t.Run(fmt.Sprintf("%s_%d", name, form), func(t *testing.T) {
				ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					for _, alias := range []string{"", " AS score"} {
						label := name
						if alias != "" {
							label = "score"
						}
						query := "SELECT " + name + "(" + ftsRankParameters(types) + ")" + alias
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer stmt.Close()
							args := ftsRankArguments(types, "cat:1", "cat")
							positive := ftsRankExact(.06079271)
							if name == "ts_rank_cd" {
								positive = ftsRankExact(.1)
							}
							for slot := -1; slot < len(args); slot++ {
								input := append([]any(nil), args...)
								var want any = positive
								if slot >= 0 {
									input[slot], want = nil, nil
								}
								ftsRankRoutes(t, p, b, c, query, label, "FLOAT4", want, input...)
								rows, err := stmt.QueryContext(p.Ctx, input...)
								if err != nil {
									t.Fatal(err)
								}
								ftsRankRead(t, p, b, rows, label, "FLOAT4", want, false)
							}
							for _, vector := range []string{"", "dog:1", "cat:1"} {
								input := ftsRankArguments(types, vector, "cat")
								want := ftsRankBits(0)
								if vector == "cat:1" {
									want = positive
								}
								ftsRankRoutes(t, p, b, c, query, label, "FLOAT4", want, input...)
								rows, err := stmt.QueryContext(p.Ctx, input...)
								if err != nil {
									t.Fatal(err)
								}
								ftsRankRead(t, p, b, rows, label, "FLOAT4", want, false)
							}
						}()
						zero := query + " WHERE false"
						for _, prepared := range []bool{false, true} {
							args := ftsRankArguments(types, "cat:1", "cat")
							var rows *sql.Rows
							if prepared {
								z, err := c.PrepareContext(p.Ctx, zero)
								if err != nil {
									t.Fatal(err)
								}
								for range 2 {
									rows, err = z.QueryContext(p.Ctx, args...)
									if err != nil {
										z.Close()
										t.Fatal(err)
									}
									ftsRankRead(t, p, b, rows, label, "FLOAT4", nil, true)
								}
								z.Close()
							} else {
								rows, err = c.QueryContext(p.Ctx, zero, args...)
								if err != nil {
									t.Fatal(err)
								}
								ftsRankRead(t, p, b, rows, label, "FLOAT4", nil, true)
							}
						}
					}
					// Valid typed NULL suppresses weight validation, not bad input.
					ftsRankRoutes(t, p, b, c, "SELECT "+name+"('{1}'::real[],NULL::tsvector,'cat'::tsquery)", name, "FLOAT4", nil)
				})
			})
		}
	}
}

package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureBindingsMetadata(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryMeasureCases {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.function + "($1::" + tc.formal + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				selectStep := func(query string, wants [][]any, args ...any) fixture.Step {
					s := geometryMeasureSelect(b, query, []string{"v"}, []string{tc.typ}, wants, args...)
					metadata := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						metadata(t, rows)
						cols, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if nullable, known := cols[0].Nullable(); nullable || known {
							t.Fatalf("computed nullability %t/%t want false/false", nullable, known)
						}
					}
					return s
				}
				check := func(arg, want any) {
					t.Helper()
					s := selectStep(q, [][]any{{want}}, arg)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.input, tc.want)
				for _, bad := range []any{"bad", []byte{}, []byte("bad")} {
					text := "bad"
					if v, ok := bad.([]byte); ok {
						text = string(v)
					}
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+tc.formal+": \""+text+"\"")
					d.Context = "unnamed portal parameter $1 = '...'"
					if text == "" {
						d.Context = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ErrorDiagnostic(q, d, bad)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					check(nil, nil)
					check(tc.input, tc.want)
				}
				check([]byte(nil), nil)
				check([]byte(tc.input), tc.want)
				if tc.formal == "path" {
					check("[(0,0),(4,0),(0,3)]", nil)
					check("((0,0),(6,0),(0,3))", float64(9))
					check("[(0,0),(4,0),(0,3)]", nil)
				} else if tc.formal == "box" {
					want := any(float64(12))
					if tc.typ == "POINT" {
						want = "(3,1.5)"
					}
					check("(5,3),(1,0)", want)
				} else {
					want := any(float64(3))
					switch tc.function {
					case "area":
						want = float64(28.274333882308138)
					case "center":
						want = "(7,8)"
					case "diameter":
						want = float64(6)
					}
					check("<(7,8),3>", want)
				}
				for _, arg := range []any{tc.input, nil} {
					s := selectStep(q+" WHERE false", nil, arg)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
				owned := []byte(tc.input)
				rows, err := stmt.QueryContext(p.Ctx, owned)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				s := selectStep(q, [][]any{{tc.want}})
				s.CheckMetadata(t, rows)
				for i := range owned {
					owned[i] = '?'
				}
				if !rows.Next() {
					t.Fatalf("retained row: %v", rows.Err())
				}
				var value any
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				s.CheckRaw(t, 0, 0, value)
				if fixture.Normalize(value) != tc.want {
					t.Fatalf("retained value %T %#v want %#v", value, value, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("exhaustion: %v", rows.Err())
				}
				rows.Close()
				check(tc.input, tc.want)
				if fixture.Normalize(value) != tc.want {
					t.Fatal("later held execution changed retained cell", value)
				}
			})
		}
	})
}

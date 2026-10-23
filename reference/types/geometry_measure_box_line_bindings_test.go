package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureBoxLineBindingsMetadata(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, call, typ string
			args            []any
			want            any
		}{
			{"width", "width($1::box)", "FLOAT8", []any{"(4,6),(1,2)"}, float64(3)},
			{"height", "height($1::box)", "FLOAT8", []any{"(4,6),(1,2)"}, float64(4)},
			{"diagonal", "diagonal($1::box)", "LSEG", []any{"(4,6),(1,2)"}, "[(4,6),(1,2)]"},
			{"slope", "slope($1::point,$2::point)", "FLOAT8", []any{"(0,0)", "(2,1)"}, float64(0.5)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.call + " AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				step := func(query string, wants [][]any, args ...any) fixture.Step {
					s := geometryMeasureSelect(b, query, []string{"v"}, []string{tc.typ}, wants, args...)
					metadata := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						metadata(t, rows)
						cols, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if nullable, known := cols[0].Nullable(); nullable || known {
							t.Fatalf("computed nullability %t/%t", nullable, known)
						}
					}
					return s
				}
				check := func(want any, args ...any) {
					t.Helper()
					s := step(q, [][]any{{want}}, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.want, tc.args...)
				for i := range tc.args {
					for _, arg := range []any{[]byte(tc.args[i].(string)), boxValuer{value: tc.args[i].(string)}, nil, []byte(nil), boxValuer{value: nil}} {
						args := append([]any(nil), tc.args...)
						args[i] = arg
						want := tc.want
						if arg == nil {
							want = nil
						}
						if v, ok := arg.([]byte); ok && v == nil {
							want = nil
						}
						if v, ok := arg.(boxValuer); ok && v.value == nil {
							want = nil
						}
						check(want, args...)
					}
					for _, bad := range []string{"bad", ""} {
						args := append([]any(nil), tc.args...)
						args[i] = []byte(bad)
						formal := "box"
						if tc.name == "slope" {
							formal = "point"
						}
						d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+formal+": \""+bad+"\"")
						d.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", i+1)
						if bad == "" {
							d.Context = fmt.Sprintf("unnamed portal parameter $%d = ''", i+1)
						}
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						args[i] = nil
						check(nil, args...)
						check(tc.want, tc.args...)
					}
				}
				for _, prepared := range []bool{false, true} {
					s := step(q+" WHERE false", nil, tc.args...)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				// Contextual inference has the same public result as explicit casts.
				inferred := "SELECT " + tc.name + "($1) AS v"
				if tc.name == "slope" {
					inferred = "SELECT slope($1,$2) AS v"
				}
				for _, prepared := range []bool{false, true} {
					s := step(inferred, [][]any{{tc.want}}, tc.args...)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				args := make([]any, len(tc.args))
				for i, arg := range tc.args {
					args[i] = []byte(arg.(string))
				}
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				s := step(q, [][]any{{tc.want}})
				s.CheckMetadata(t, rows)
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, arg := range args {
					for i := range arg.([]byte) {
						arg.([]byte)[i] = '?'
					}
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
					t.Fatalf("retained cell %v want %v", value, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("exhaustion: %v", rows.Err())
				}
				rows.Close()
				check(tc.want, tc.args...)
				stmt.Close()
				if fixture.Normalize(value) != tc.want || len(cols) != 1 || cols[0].DatabaseTypeName() != tc.typ {
					t.Fatal("later execution/close changed retained cell or metadata")
				}
			})
		}
	})
}

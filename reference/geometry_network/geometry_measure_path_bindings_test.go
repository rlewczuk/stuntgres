package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasurePathBindingsMetadata(t *testing.T) {
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range geometryMeasurePathCases {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.function + "($1::" + tc.formal + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				selectStep := func(query string, values [][]any, args ...any) fixture.Step {
					return geometryMeasurePathSelect(b, query, []string{"v"}, []string{tc.typ}, values, args...)
				}
				check := func(arg, want any) {
					t.Helper()
					s := selectStep(q, [][]any{{want}}, arg)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				check(tc.input, tc.want)
				switch tc.name {
				case "closed":
					check("((1,2))", true)
				case "open":
					check("((1,2))", false)
				case "close":
					check("[(3,4),(0,0),(0,0)]", "((3,4),(0,0),(0,0))")
				case "reopen":
					check("((3,4),(0,0),(0,0))", "[(3,4),(0,0),(0,0)]")
				case "length_path":
					check("((-1,0),(1,0))", float64(4))
				case "length_lseg":
					check("[(0,0),(0,2)]", float64(2))
				case "count_path":
					check("[(1,2)]", int64(1))
				case "count_polygon":
					check("((1,2))", int64(1))
				}
				check(tc.input, tc.want)
				for _, bad := range []any{"bad", []byte("bad"), []byte{}} {
					text := "bad"
					if bytes, ok := bad.([]byte); ok {
						text = string(bytes)
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
					check(tc.input, tc.want)
				}
				check(nil, nil)
				check([]byte(nil), nil)
				check([]byte(tc.input), tc.want)
				check(boxValuer{value: tc.input}, tc.want)
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
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
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
					t.Fatalf("retained %v want %v", value, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("exhaustion: %v", rows.Err())
				}
				rows.Close()
				check(nil, nil)
				check(tc.input, tc.want)
				if fixture.Normalize(value) != tc.want || len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != tc.typ {
					t.Fatal("later bind changed owned cell/metadata")
				}
			})
		}
		q := "SELECT p AS original,pclose(p) AS closed,popen(pclose(p)) AS reopened,isopen(p) AS unchanged FROM (SELECT $1::path AS p) AS source"
		s := geometryMeasurePathSelect(b, q, []string{"original", "closed", "reopened", "unchanged"}, []string{"PATH", "PATH", "PATH", "BOOL"}, [][]any{{"[(0,0),(4,0),(0,3)]", "((0,0),(4,0),(0,3))", "[(0,0),(4,0),(0,3)]", true}}, "[(0,0),(4,0),(0,3)]")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

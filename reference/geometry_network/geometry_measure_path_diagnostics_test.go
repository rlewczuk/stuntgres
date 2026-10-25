package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasurePathDiagnostics(t *testing.T) {
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ expression, signature string }{
			{"isopen(polygon '((0,0),(1,0),(0,1))')", "isopen(polygon)"},
			{"isclosed(lseg '[(0,0),(1,0)]')", "isclosed(lseg)"},
			{"length(polygon '((0,0),(1,0),(0,1))')", "length(polygon)"},
			{"length(polygon '((0,0),(1,0),(0,1))') OVER ()", "length(polygon)"},
			{"pclose(lseg '[(0,0),(1,0)]')", "pclose(lseg)"},
			{"popen(1)", "popen(integer)"},
			{"npoints(point '(0,0)')", "npoints(point)"},
			{"isopen()", "isopen()"},
			{"pclose(path '[(0,0)]',path '[(0,0)]')", "pclose(path, path)"},
			{"length(path '[(0,0)]',path '[(0,0)]')", "length(path, path)"},
			{"isopen(missing => path '[(0,0)]')", "isopen(missing => path)"},
			{"pg_catalog.isopen(polygon '((0,0))')", "pg_catalog.isopen(polygon)"},
			{"\"ISOPEN\"(path '[(0,0)]')", "ISOPEN(path)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("signature/%s/%t", tc.expression, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42883", "function "+tc.signature+" does not exist", "8", hint, prepared, false))
				})
			}
		}
		for _, operand := range []string{"NULL", "'[(0,0)]'", "$1"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("ambiguous/%s/%t", operand, prepared), func(t *testing.T) {
					var args []any
					if operand == "$1" {
						args = []any{nil}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT npoints("+operand+")", "42725", "function npoints(unknown) is not unique", "8", "Could not choose a best candidate function. You might need to add explicit type casts.", prepared, false, args...))
				})
			}
		}
		for _, tc := range []struct{ expression, message string }{
			{"isopen(DISTINCT path '[(0,0)]')", "DISTINCT specified, but isopen is not an aggregate function"},
			{"npoints(path '[(0,0)]') FILTER (WHERE true)", "FILTER specified, but npoints is not an aggregate function"},
			{"length(path '[(0,0)]') OVER ()", "OVER specified, but length is not a window function nor an aggregate function"},
			{"length('abc'::text) OVER ()", "OVER specified, but length is not a window function nor an aggregate function"},
			{"length('abc'::bytea) OVER ()", "OVER specified, but length is not a window function nor an aggregate function"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("decoration/%s/%t", tc.expression, prepared), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError("SELECT "+tc.expression, "42809", tc.message, "8", "", prepared, false))
				})
			}
		}
		for _, tc := range []struct{ function, formal, input string }{
			{"pclose", "path", "[]"}, {"popen", "path", "()"}, {"isopen", "path", "bad"}, {"npoints", "polygon", "()"},
			{"length", "lseg", "[]"},
		} {
			for _, prefix := range []string{"SELECT ", "SELECT /* é */ "} {
				q := prefix + tc.function + "(" + tc.formal + " '" + tc.input + "')"
				pos := fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("input/%s/%s/%t", q, tc.formal, prepared), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22P02", "invalid input syntax for type "+tc.formal+": \""+tc.input+"\"", pos, "", prepared, false))
					})
				}
			}
			q := "SELECT " + tc.function + "($1::text::" + tc.formal + ")"
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22P02", "invalid input syntax for type "+tc.formal+": \""+tc.input+"\"", "", "", prepared, true, tc.input))
			}
		}
		for _, token := range []string{"1e400", "1e-400"} {
			q := "SELECT /* é */ length(path '[(0,0),(" + token + ",0)]')"
			pos := fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22003", "\""+token+"\" is out of range for type double precision", pos, "", prepared, false))
			}
		}
		for _, tc := range []struct {
			function, formal, input, good, typ string
			want                               any
		}{
			{"length", "path", "[(0,0),(TOKEN,0)]", "[(0,0),(3,4)]", "FLOAT8", float64(5)},
			{"length", "lseg", "[(0,0),(TOKEN,0)]", "[(0,0),(3,4)]", "FLOAT8", float64(5)},
			{"npoints", "polygon", "((TOKEN,0))", "((0,0))", "INT4", int64(1)},
		} {
			for _, token := range []string{"1e400", "1e-400"} {
				t.Run("bound-range/"+tc.formal+"/"+token, func(t *testing.T) {
					q := "SELECT " + tc.function + "($1::" + tc.formal + ") AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					d := fixture.ControlsReferenceDiagnostic("22003", "\""+token+"\" is out of range for type double precision")
					d.Context = "unnamed portal parameter $1 = '...'"
					s := fixture.ErrorDiagnostic(q, d, strings.ReplaceAll(tc.input, "TOKEN", token))
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasurePathSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.good))
				})
			}
		}
		for _, tc := range []struct {
			formal, input, good string
			want                float64
		}{
			{"lseg", "[(-1e308,0),(1e308,0)]", "[(0,0),(3,4)]", 5},
			{"lseg", "[(0,0),(1.3e308,1.3e308)]", "[(0,0),(3,4)]", 5},
			{"path", "[(0,0),(1e308,0),(0,0)]", "[(0,0),(4,0),(0,3)]", 9},
		} {
			t.Run("overflow/"+tc.input, func(t *testing.T) {
				q := "SELECT length(" + tc.formal + " '" + tc.input + "') AS v"
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22003", "value out of range: overflow", "", "", prepared, true))
				}
				q = "SELECT length($1::" + tc.formal + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := boxError(q, "22003", "value out of range: overflow", "", "", false, true, tc.input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasurePathSelect(b, q, []string{"v"}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryMeasurePathSelect(b, q, []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}}, tc.good))
			})
		}
	})
}

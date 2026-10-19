package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeAssignment(t *testing.T) {
	t.Run("bindings_and_copies", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cube_assign(id integer NOT NULL,v cube)", 0))
			q := "INSERT INTO cube_assign VALUES ($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("INSERT INTO cube_assign VALUES (1,'(1,2),(3,4)')", 1))
			run(fixture.Exec(q, 1, 2, "(3,1),(1,4)"))
			run(fixture.Prepared(fixture.Exec(q, 1, 3, "()")))
			buffer := []byte("(1,2),(1,2)")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 4, buffer))
			for i := range buffer {
				buffer[i] = '9'
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 6, []byte(nil)))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 7, "-0"))
			want := [][]any{{int64(1), "(1, 2),(3, 4)"}, {int64(2), "(3, 1),(1, 4)"}, {int64(3), "()"}, {int64(4), "(1, 2)"}, {int64(5), nil}, {int64(6), nil}, {int64(7), "(-0)"}}
			run(fixture.Exec("CREATE TABLE cube_copy(id integer NOT NULL,v cube)", 0))
			run(fixture.Exec("INSERT INTO cube_copy SELECT id,v FROM cube_assign", 7))
			run(fixture.Exec("UPDATE cube_copy SET v=v WHERE id=1", 1))
			run(fixture.Prepared(fixture.Exec("UPDATE cube_copy SET v=$1 WHERE id=3", 1, "(8),(9)")))
			for _, prepared := range []bool{false, true} {
				s := cubeSelect(b, "SELECT id,v FROM cube_assign ORDER BY id", []string{"id", "v"}, want)
				s.Prepared = prepared
				run(s)
				copyWant := append([][]any(nil), want...)
				copyWant[2] = []any{int64(3), "(8),(9)"}
				s = cubeSelect(b, "SELECT id,v FROM cube_copy ORDER BY id", []string{"id", "v"}, copyWant)
				s.Prepared = prepared
				run(s)
				for _, arg := range []any{nil, []byte(nil)} {
					s = cubeSelect(b, "SELECT $1::cube AS v", []string{"v"}, [][]any{{nil}}, arg)
					s.Prepared = prepared
					run(s)
				}
				for _, arg := range []any{"", []byte{}} {
					want := cubeParameterDiagnostic(cubeSyntaxDiagnostic("syntax error at end of input"), "", 1)
					cubeError(t, p, b, c, "SELECT $1::cube AS v", prepared, true, false, want, arg)
				}
			}
		})
	})
	t.Run("not_null_atomicity", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cube_required(id integer,v cube NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO cube_required VALUES (1,'1')", 1))
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "cube_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "cube_required", "v"
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} {
					cubeError(t, p, b, c, "INSERT INTO cube_required VALUES (2,$1)", prepared, false, false, want, arg)
				}
				cubeError(t, p, b, c, "INSERT INTO cube_required VALUES (2,NULL)", prepared, false, false, want)
				run(cubeSelect(b, "SELECT id,v FROM cube_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(1)"}}))
			}
		})
	})
	t.Run("character_io_and_assignment", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cube_character(id integer NOT NULL,txt text,vc varchar)", 0))
			run(fixture.Exec("CREATE TABLE cube_source(id integer NOT NULL,v cube)", 0))
			run(fixture.Exec("INSERT INTO cube_source VALUES (1,'(3,1),(1,4)'),(2,NULL)", 2))
			for _, prepared := range []bool{false, true} {
				apply := func(s fixture.Step) { s.Prepared = prepared; run(s) }
				apply(fixture.Exec("DELETE FROM cube_character", 0))
				apply(fixture.Exec("INSERT INTO cube_character VALUES (1,'(3,1),(1,4)'::cube,'(3,1),(1,4)'::cube),(2,NULL::cube,NULL::cube)", 2))
				apply(fixture.Exec("INSERT INTO cube_character SELECT id+2,v,v FROM cube_source", 2))
				apply(fixture.Select("SELECT id,txt,vc FROM cube_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "(3, 1),(1, 4)", "(3, 1),(1, 4)"}, {int64(2), nil, nil}, {int64(3), "(3, 1),(1, 4)", "(3, 1),(1, 4)"}, {int64(4), nil, nil}}))
				apply(fixture.Exec("UPDATE cube_character SET txt=$1::cube,vc=$1::cube WHERE id=1", 1, "-0"))
				apply(fixture.Exec("UPDATE cube_character SET txt=NULL::cube,vc=NULL::cube WHERE id=3", 1))
				apply(fixture.Select("SELECT id,txt,vc FROM cube_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "(-0)", "(-0)"}, {int64(2), nil, nil}, {int64(3), nil, nil}, {int64(4), nil, nil}}))
				// Restore the scenario so both execution routes have identical inputs.
				run(fixture.Exec("DELETE FROM cube_character", 4))
				for _, q := range []string{"SELECT '(1,2)'::text::cube AS v", "SELECT '(1,2)'::varchar::cube AS v", "SELECT $1::text::cube AS v"} {
					args := []any{}
					if q == "SELECT $1::text::cube AS v" {
						args = []any{"(1,2)"}
					}
					apply(cubeSelect(b, q, []string{"v"}, [][]any{{"(1, 2)"}}, args...))
				}
				for _, typ := range []string{"text", "varchar"} {
					name := "TEXT"
					if typ == "varchar" {
						name = "VARCHAR"
					}
					apply(fixture.Select("SELECT '(1,2)'::cube::"+typ+" AS v", []string{"v"}, []string{name}, [][]any{{"(1, 2)"}}))
				}
				for _, tc := range []struct{ prefix, expression, typ string }{
					{"INSERT INTO cube_source VALUES (3,", "'1'::text)", "text"},
					{"INSERT INTO cube_source VALUES (3,", "'1'::varchar)", "character varying"},
					{"UPDATE cube_source SET v=", "'1'::text", "text"},
				} {
					want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type cube but expression is of type `+tc.typ)
					want.Hint, want.Position = "You will need to rewrite or cast the expression.", strconv.Itoa(len(tc.prefix)+1)
					cubeError(t, p, b, c, tc.prefix+tc.expression, prepared, false, true, want)
				}
				run(cubeSelect(b, "SELECT id,v FROM cube_source ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(3, 1),(1, 4)"}, {int64(2), nil}}))
			}
		})
	})
}

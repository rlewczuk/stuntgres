package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryIntAssignment(t *testing.T) {
	t.Run("bound_and_same_type", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queryIntInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE qi_assign(id integer NOT NULL,v query_int)", 0))
			q := "INSERT INTO qi_assign VALUES ($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec(q, 1, 1, "1&(2|3)"))
			run(fixture.Prepared(fixture.Exec(q, 1, 2, "!!1")))
			buffer := []byte("010")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, buffer))
			copy(buffer, "999")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 4, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, []byte(nil)))
			run(fixture.Exec("CREATE TABLE qi_copy(id integer NOT NULL,v query_int)", 0))
			run(fixture.Exec("INSERT INTO qi_copy SELECT id,v FROM qi_assign", 5))
			run(fixture.Prepared(fixture.Exec("UPDATE qi_copy SET v=$1 WHERE id=2", 1, "!(!1|2)")))
			run(fixture.Exec("UPDATE qi_copy SET v=v WHERE id=1", 1))
			for _, prepared := range []bool{false, true} {
				s := queryIntSelect(b, "SELECT id,v FROM qi_assign ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1 & ( 2 | 3 )"}, {int64(2), "!( !1 )"}, {int64(3), "8"}, {int64(4), nil}, {int64(5), nil}})
				s.Prepared = prepared
				run(s)
				s = queryIntSelect(b, "SELECT id,v FROM qi_copy ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1 & ( 2 | 3 )"}, {int64(2), "!( !1 | 2 )"}, {int64(3), "8"}, {int64(4), nil}, {int64(5), nil}})
				s.Prepared = prepared
				run(s)
			}
		})
	})
	t.Run("typed_nil_and_empty_bytes", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queryIntInstall(t, p, b, c)
			for _, arg := range []any{nil, []byte(nil)} {
				for _, prepared := range []bool{false, true} {
					s := queryIntSelect(b, "SELECT $1::query_int AS v", []string{"v"}, [][]any{{nil}}, arg)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { queryIntMetadata(t, b.PG, rows, s.Columns, nil) }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			for _, arg := range []any{[]byte{}, ""} {
				for _, prepared := range []bool{false, true} {
					want := queryIntParameterDiagnostic("42601", "syntax error", "", 1)
					queryIntError(t, p, b, c, "SELECT $1::query_int AS v", prepared, true, false, want, arg)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE qi_required(id integer,v query_int NOT NULL)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qi_required VALUES (1,'1')", 1))
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "qi_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "qi_required", "v"
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} {
					queryIntError(t, p, b, c, "INSERT INTO qi_required VALUES (2,$1)", prepared, false, false, want, arg)
				}
				queryIntError(t, p, b, c, "INSERT INTO qi_required VALUES (2,NULL)", prepared, false, false, want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, queryIntSelect(b, "SELECT id,v FROM qi_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1"}}))
		})
	})
	t.Run("to_character_assignment", func(t *testing.T) {
		// REL_18_0 parse_coerce.c/find_coercion_pathway admits assignment
		// CoerceViaIO to string-category targets, not the reverse direction.
		// Expected stored strings are independent canonical codec values.
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(route.name, func(t *testing.T) {
				queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					queryIntInstall(t, p, b, c)
					direct := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run := func(s fixture.Step) {
						s.Prepared = route.prepared
						direct(s)
					}
					direct(fixture.Exec("CREATE TABLE qi_character(id integer NOT NULL,txt text,vc varchar)", 0))
					direct(fixture.Exec("CREATE TABLE qi_character_source(id integer NOT NULL,v query_int)", 0))
					direct(fixture.Exec("INSERT INTO qi_character_source VALUES (1,'1&(2|3)'::query_int),(2,NULL::query_int),(3,'!!1'::query_int),(4,NULL::query_int)", 4))

					run(fixture.Exec("INSERT INTO qi_character VALUES (1,'1&(2|3)'::query_int,'1&(2|3)'::query_int)", 1))
					run(fixture.Exec("INSERT INTO qi_character VALUES (2,NULL::query_int,NULL::query_int)", 1))
					run(fixture.Exec("INSERT INTO qi_character VALUES (3,$1::query_int,$1::query_int)", 1, "!(!1|2)"))
					run(fixture.Exec("INSERT INTO qi_character VALUES (4,$1::query_int,$1::query_int)", 1, []byte(nil)))
					run(fixture.Exec("INSERT INTO qi_character SELECT id+4,v,v FROM qi_character_source", 4))
					run(fixture.Select("SELECT id,txt,vc FROM qi_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{
						{int64(1), "1 & ( 2 | 3 )", "1 & ( 2 | 3 )"},
						{int64(2), nil, nil},
						{int64(3), "!( !1 | 2 )", "!( !1 | 2 )"},
						{int64(4), nil, nil},
						{int64(5), "1 & ( 2 | 3 )", "1 & ( 2 | 3 )"},
						{int64(6), nil, nil},
						{int64(7), "!( !1 )", "!( !1 )"},
						{int64(8), nil, nil},
					}))

					run(fixture.Exec("UPDATE qi_character SET txt='010'::query_int,vc='010'::query_int WHERE id=1", 1))
					run(fixture.Exec("UPDATE qi_character SET txt=$1::query_int,vc=$1::query_int WHERE id=2", 1, "!!1"))
					run(fixture.Exec("UPDATE qi_character SET txt=$1::query_int,vc=$1::query_int WHERE id=3", 1, []byte(nil)))
					run(fixture.Exec("UPDATE qi_character SET txt=NULL::query_int,vc=NULL::query_int WHERE id=5", 1))
					run(fixture.Exec("UPDATE qi_character SET txt='010'::query_int,vc='010'::query_int WHERE id=6", 1))
					run(fixture.Select("SELECT id,txt,vc FROM qi_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{
						{int64(1), "8", "8"},
						{int64(2), "!( !1 )", "!( !1 )"},
						{int64(3), nil, nil},
						{int64(4), nil, nil},
						{int64(5), nil, nil},
						{int64(6), "8", "8"},
						{int64(7), "!( !1 )", "!( !1 )"},
						{int64(8), nil, nil},
					}))
				})
			})
		}
	})
	t.Run("cast_matrix", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queryIntInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"SELECT '1&(2|3)'::query_int AS v",
				"SELECT ('1&(2|3)'::text)::query_int AS v",
				"SELECT ('1&(2|3)'::varchar)::query_int AS v",
			} {
				s := queryIntSelect(b, q, []string{"v"}, [][]any{{"1 & ( 2 | 3 )"}})
				run(s)
				run(fixture.Prepared(s))
			}
			for _, tc := range []struct{ q, typ string }{
				{"SELECT ('1&(2|3)'::query_int)::text AS v", "TEXT"},
				{"SELECT ('1&(2|3)'::query_int)::varchar AS v", "VARCHAR"},
			} {
				s := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{"1 & ( 2 | 3 )"}})
				run(s)
				run(fixture.Prepared(s))
			}
			for _, q := range []string{"SELECT NULL::query_int AS v", "SELECT NULL::text::query_int AS v"} {
				s := queryIntSelect(b, q, []string{"v"}, [][]any{{nil}})
				run(s)
				run(fixture.Prepared(s))
			}
			s := queryIntSelect(b, "SELECT $1::text::query_int AS v", []string{"v"}, [][]any{{"1 & ( 2 | 3 )"}}, "1&(2|3)")
			run(s)
			run(fixture.Prepared(s))
			for _, tc := range []struct{ q, message, position string }{
				{"SELECT 1::query_int AS v", "cannot cast type integer to query_int", "9"},
				{"SELECT true::query_int AS v", "cannot cast type boolean to query_int", "12"},
				{"SELECT ('1'::query_int)::integer AS v", "cannot cast type query_int to integer", "24"},
			} {
				want := fixture.ControlsReferenceDiagnostic("42846", tc.message)
				want.Position = tc.position
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE qi_cast(id integer,v query_int)", 0))
			for _, tc := range []struct {
				q, typ, position string
				args             []any
			}{
				{"INSERT INTO qi_cast VALUES (1,'1'::text)", "text", "31", nil},
				{"INSERT INTO qi_cast VALUES (2,'2'::varchar)", "character varying", "31", nil},
				{"INSERT INTO qi_cast VALUES (3,3)", "integer", "31", nil},
				{"INSERT INTO qi_cast VALUES (4,$1::text)", "text", "31", []any{"4"}},
				{"UPDATE qi_cast SET v='1'::text", "text", "22", nil},
			} {
				want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type query_int but expression is of type `+tc.typ)
				want.Hint, want.Position = "You will need to rewrite or cast the expression.", tc.position
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, tc.q, prepared, false, true, want, tc.args...)
				}
			}
			run(queryIntSelect(b, "SELECT id,v FROM qi_cast ORDER BY id", []string{"id", "v"}, [][]any{}))
		})
	})
}

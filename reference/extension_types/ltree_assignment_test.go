package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeAssignment(t *testing.T) {
	t.Run("binding", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			ltreeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lt_assign(id integer NOT NULL,v ltree)", 0))
			q := "INSERT INTO lt_assign VALUES ($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			buffer := []byte("a.bC")
			for _, args := range [][]any{{1, buffer}, {2, nil}, {3, "foo"}, {4, []byte(nil)}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, args...))
			}
			bad := queryIntParameterDiagnostic("42601", "ltree syntax error at character 3", "a..b", 2)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, bad, 5, "a..b"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, ""))
			run(fixture.Exec("DELETE FROM lt_assign WHERE id=5", 1))
			for _, q := range []string{"SELECT ltree 'a.b' AS v", "SELECT public.ltree 'a.b' AS v"} {
				s := ltreeSelect(b, q, []string{"v"}, [][]any{{"a.b"}})
				run(s); run(fixture.Prepared(s))
			}
			copy(buffer, "xxxx")
			run(fixture.Exec("CREATE TABLE lt_copy(id integer NOT NULL,v ltree)", 0))
			run(fixture.Exec("INSERT INTO lt_copy SELECT id,v FROM lt_assign", 4))
			run(fixture.Prepared(fixture.Exec("UPDATE lt_copy SET v=$1 WHERE id=3", 1, "a-b._")))
			run(fixture.Exec("UPDATE lt_copy SET v=v WHERE id=1", 1))
			for _, tc := range []struct{ table string; rows [][]any }{
				{"lt_assign", [][]any{{int64(1), "a.bC"}, {int64(2), nil}, {int64(3), "foo"}, {int64(4), nil}}},
				{"lt_copy", [][]any{{int64(1), "a.bC"}, {int64(2), nil}, {int64(3), "a-b._"}, {int64(4), nil}}},
			} {
				s := ltreeSelect(b, "SELECT id,v FROM "+tc.table+" ORDER BY id", []string{"id", "v"}, tc.rows)
				run(s); run(fixture.Prepared(s))
			}
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} {
					s := ltreeSelect(b, "SELECT $1::ltree AS v", []string{"v"}, [][]any{{nil}}, arg)
					s.Prepared = prepared; run(s)
				}
				for _, arg := range []any{"", []byte{}} {
					s := ltreeSelect(b, "SELECT $1::ltree AS v", []string{"v"}, [][]any{{""}}, arg)
					s.Prepared = prepared; run(s)
				}
			}
			run(fixture.Exec("CREATE TABLE lt_required(id integer,v ltree NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO lt_required VALUES (1,'a')", 1))
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "lt_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "lt_required", "v"
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} { ltreeError(t, p, b, c, "INSERT INTO lt_required VALUES (2,$1)", prepared, false, false, want, arg) }
				ltreeError(t, p, b, c, "INSERT INTO lt_required VALUES (2,NULL)", prepared, false, false, want)
			}
			run(ltreeSelect(b, "SELECT id,v FROM lt_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a"}}))
		})
	})
	t.Run("coercion", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			ltreeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lt_character(id integer,txt text,vc varchar)", 0))
			run(fixture.Exec("CREATE TABLE lt_cast(id integer,v ltree)", 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT 'a.bC'::text::ltree AS v", "SELECT 'a.bC'::varchar::ltree AS v", "SELECT $1::text::ltree AS v"} {
					var args []any
					if q == "SELECT $1::text::ltree AS v" { args = []any{"a.bC"} }
					s := ltreeSelect(b, q, []string{"v"}, [][]any{{"a.bC"}}, args...); s.Prepared = prepared; run(s)
				}
				for _, tc := range []struct{ target, typ string }{{"text", "TEXT"}, {"varchar", "VARCHAR"}} {
					s := fixture.Select("SELECT 'a.bC'::ltree::"+tc.target+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{"a.bC"}}); s.Prepared = prepared; run(s)
				}
				s := fixture.Exec("INSERT INTO lt_character VALUES (1,'a.bC'::ltree,'a.bC'::ltree),(2,NULL::ltree,NULL::ltree)", 2); s.Prepared = prepared; run(s)
				s = fixture.Exec("UPDATE lt_character SET txt=$1::ltree,vc=$1::ltree WHERE id=1", int64(1), "foo")
				if prepared { s.Affected = 2 }; s.Prepared = prepared; run(s)
				for _, tc := range []struct{ q, message, token string }{
					{"SELECT 1::ltree AS v", "cannot cast type integer to ltree", "::ltree"},
					{"SELECT true::ltree AS v", "cannot cast type boolean to ltree", "::ltree"},
					{"SELECT ('a'::ltree)::integer AS v", "cannot cast type ltree to integer", "::integer"},
				} {
					want := fixture.ControlsReferenceDiagnostic("42846", tc.message); want.Position = ltreeSQLPosition(tc.q, tc.token)
					ltreeError(t, p, b, c, tc.q, prepared, true, true, want)
				}
				for _, tc := range []struct{ q, typ, token string }{
					{"INSERT INTO lt_cast VALUES (1,'a'::text)", "text", "'a'"},
					{"INSERT INTO lt_cast VALUES (2,'a'::varchar)", "character varying", "'a'"},
					{"UPDATE lt_cast SET v='a'::text", "text", "'a'"},
				} {
					want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type ltree but expression is of type `+tc.typ)
					want.Hint, want.Position = "You will need to rewrite or cast the expression.", ltreeSQLPosition(tc.q, tc.token)
					ltreeError(t, p, b, c, tc.q, prepared, false, true, want)
				}
				want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type ltree but expression is of type text`)
				want.Hint = "You will need to rewrite or cast the expression."
				typedSQL := "INSERT INTO lt_cast VALUES (3,$1::text)"
				want.Position = ltreeSQLPosition(typedSQL, "$1")
				ltreeError(t, p, b, c, typedSQL, prepared, false, true, want, "a")
			}
			run(fixture.Select("SELECT id,txt,vc FROM lt_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "foo", "foo"}, {int64(1), "foo", "foo"}, {int64(2), nil, nil}, {int64(2), nil, nil}}))
			run(fixture.Exec("CREATE TABLE lt_character_copy(id integer,txt text,vc varchar)", 0))
			run(fixture.Exec("INSERT INTO lt_cast VALUES (1,'a.bC'),(2,NULL)", 2))
			run(fixture.Exec("INSERT INTO lt_character_copy SELECT id,v,v FROM lt_cast", 2))
			run(fixture.Select("SELECT id,txt,vc FROM lt_character_copy ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "a.bC", "a.bC"}, {int64(2), nil, nil}}))
			for _, q := range []string{"SELECT NULL::text::ltree AS v", "SELECT NULL::varchar::ltree AS v"} {
				s := ltreeSelect(b, q, []string{"v"}, [][]any{{nil}})
				run(s); run(fixture.Prepared(s))
			}
			run(ltreeSelect(b, "SELECT id,v FROM lt_cast ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a.bC"}, {int64(2), nil}}))
		})
	})
}
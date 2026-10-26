package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLqueryAssignment(t *testing.T) {
	t.Run("binding", func(t *testing.T) {
		lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lqueryInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lq_assign(id integer NOT NULL,v lquery)", 0))
			q := "INSERT INTO lq_assign VALUES ($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			buffer := []byte("a*@%")
			for _, args := range [][]any{{1, buffer}, {2, nil}, {3, "foo{1}"}, {4, []byte(nil)}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, args...))
			}
			copy(buffer, "xxxx")
			run(fixture.Exec("CREATE TABLE lq_copy(id integer NOT NULL,v lquery)", 0))
			run(fixture.Exec("INSERT INTO lq_copy SELECT id,v FROM lq_assign", 4))
			run(fixture.Prepared(fixture.Exec("UPDATE lq_copy SET v=$1 WHERE id=3", 1, "*{0,2}")))
			run(fixture.Exec("UPDATE lq_copy SET v=v WHERE id=1", 1))
			for _, tc := range []struct{ table string; rows [][]any }{
				{"lq_assign", [][]any{{int64(1), "a%@*"}, {int64(2), nil}, {int64(3), "foo{1}"}, {int64(4), nil}}},
				{"lq_copy", [][]any{{int64(1), "a%@*"}, {int64(2), nil}, {int64(3), "*{,2}"}, {int64(4), nil}}},
			} {
				s := lquerySelect(b, "SELECT id,v FROM "+tc.table+" ORDER BY id", []string{"id", "v"}, tc.rows)
				run(s); run(fixture.Prepared(s))
			}
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} {
					s := lquerySelect(b, "SELECT $1::lquery AS v", []string{"v"}, [][]any{{nil}}, arg)
					s.Prepared = prepared; run(s)
				}
				for _, arg := range []any{"", []byte{}} {
					want := queryIntParameterDiagnostic("42601", "lquery syntax error", "", 1)
					want.Detail = "Unexpected end of input."
					lqueryError(t, p, b, c, "SELECT $1::lquery AS v", prepared, true, false, want, arg)
				}
			}
			run(fixture.Exec("CREATE TABLE lq_required(id integer,v lquery NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO lq_required VALUES (1,'a')", 1))
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "lq_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "lq_required", "v"
			for _, prepared := range []bool{false, true} {
				for _, arg := range []any{nil, []byte(nil)} { lqueryError(t, p, b, c, "INSERT INTO lq_required VALUES (2,$1)", prepared, false, false, want, arg) }
				lqueryError(t, p, b, c, "INSERT INTO lq_required VALUES (2,NULL)", prepared, false, false, want)
			}
			run(lquerySelect(b, "SELECT id,v FROM lq_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a"}}))
		})
	})
	t.Run("coercion", func(t *testing.T) {
		lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lqueryInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lq_character(id integer,txt text,vc varchar)", 0))
			run(fixture.Exec("CREATE TABLE lq_cast(id integer,v lquery)", 0))
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT 'a*@%'::text::lquery AS v", "SELECT 'a*@%'::varchar::lquery AS v", "SELECT $1::text::lquery AS v"} {
					var args []any
					if q == "SELECT $1::text::lquery AS v" { args = []any{"a*@%"} }
					s := lquerySelect(b, q, []string{"v"}, [][]any{{"a%@*"}}, args...); s.Prepared = prepared; run(s)
				}
				for _, tc := range []struct{ target, typ string }{{"text", "TEXT"}, {"varchar", "VARCHAR"}} {
					s := fixture.Select("SELECT 'a*@%'::lquery::"+tc.target+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{"a%@*"}}); s.Prepared = prepared; run(s)
				}
				s := fixture.Exec("INSERT INTO lq_character VALUES (1,'a*@%'::lquery,'a*@%'::lquery),(2,NULL::lquery,NULL::lquery)", 2); s.Prepared = prepared; run(s)
				s = fixture.Exec("UPDATE lq_character SET txt=$1::lquery,vc=$1::lquery WHERE id=1", int64(1), "foo{1}")
				if prepared { s.Affected = 2 }; s.Prepared = prepared; run(s)
				for _, tc := range []struct{ q, message, token string }{
					{"SELECT 1::lquery AS v", "cannot cast type integer to lquery", "::lquery"},
					{"SELECT true::lquery AS v", "cannot cast type boolean to lquery", "::lquery"},
					{"SELECT ('a'::lquery)::integer AS v", "cannot cast type lquery to integer", "::integer"},
				} {
					want := fixture.ControlsReferenceDiagnostic("42846", tc.message); want.Position = lquerySQLPosition(tc.q, tc.token)
					lqueryError(t, p, b, c, tc.q, prepared, true, true, want)
				}
				for _, tc := range []struct{ q, typ, token string }{
					{"INSERT INTO lq_cast VALUES (1,'a'::text)", "text", "'a'"},
					{"INSERT INTO lq_cast VALUES (2,'a'::varchar)", "character varying", "'a'"},
					{"UPDATE lq_cast SET v='a'::text", "text", "'a'"},
				} {
					want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type lquery but expression is of type `+tc.typ)
					want.Hint, want.Position = "You will need to rewrite or cast the expression.", lquerySQLPosition(tc.q, tc.token)
					lqueryError(t, p, b, c, tc.q, prepared, false, true, want)
				}
				want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type lquery but expression is of type text`)
				want.Hint = "You will need to rewrite or cast the expression."
				typedSQL := "INSERT INTO lq_cast VALUES (3,$1::text)"
				want.Position = lquerySQLPosition(typedSQL, "$1")
				lqueryError(t, p, b, c, typedSQL, prepared, false, true, want, "a")
			}
			run(fixture.Select("SELECT id,txt,vc FROM lq_character ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "foo{1}", "foo{1}"}, {int64(1), "foo{1}", "foo{1}"}, {int64(2), nil, nil}, {int64(2), nil, nil}}))
			run(fixture.Exec("CREATE TABLE lq_character_copy(id integer,txt text,vc varchar)", 0))
			run(fixture.Exec("INSERT INTO lq_cast VALUES (1,'a*@%'),(2,NULL)", 2))
			run(fixture.Exec("INSERT INTO lq_character_copy SELECT id,v,v FROM lq_cast", 2))
			run(fixture.Select("SELECT id,txt,vc FROM lq_character_copy ORDER BY id", []string{"id", "txt", "vc"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "a%@*", "a%@*"}, {int64(2), nil, nil}}))
			for _, q := range []string{"SELECT NULL::text::lquery AS v", "SELECT NULL::varchar::lquery AS v"} {
				s := lquerySelect(b, q, []string{"v"}, [][]any{{nil}})
				run(s); run(fixture.Prepared(s))
			}
			run(lquerySelect(b, "SELECT id,v FROM lq_cast ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "a%@*"}, {int64(2), nil}}))
		})
	})
}

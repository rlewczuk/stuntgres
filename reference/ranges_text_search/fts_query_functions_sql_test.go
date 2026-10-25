package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSQueryFunctionsSQLRules(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE "fts QF rules"(ord integer,t tsquery,s tsquery)`, 0))
		run(fixture.Exec(`INSERT INTO "fts QF rules" VALUES(1,'a','b'),(2,'b','c')`, 2))
		for i, tc := range []struct {
			input, sql string
			want       any
		}{
			{"a", `SELECT t,s FROM "fts QF rules" ORDER BY ord`, "'c'"},
			{"a", `SELECT t,s FROM "fts QF rules" ORDER BY ord DESC`, "'b'"},
			{"a", `SELECT t AS arbitrary,s AS names FROM public."fts QF rules" WHERE ord=1`, "'b'"},
			{"a", "VALUES ('a'::tsquery,'b'::tsquery),('b'::tsquery,'c'::tsquery)", "'c'"},
			{"a", "VALUES (NULL::tsquery,'z'::tsquery),('a'::tsquery,NULL::tsquery),(''::tsquery,'z'::tsquery)", "'a'"},
			{"a", "VALUES ('a'::tsquery,''::tsquery)", ""},
			{"", "SELECT malformed !!!", ""},
		} {
			t.Run(fmt.Sprintf("sql_%d", i), func(t *testing.T) {
				ftsQueryFunctionsValue(t, p, b, "ts_rewrite($1::tsquery,$2::text)", "TSQUERY", tc.want, tc.input, tc.sql)
			})
		}
		for i, args := range [][]any{{nil, "SELECT malformed !!!"}, {"a", nil}} {
			t.Run(fmt.Sprintf("strict_%d", i), func(t *testing.T) {
				ftsQueryFunctionsValue(t, p, b, "ts_rewrite($1::tsquery,$2::text)", "TSQUERY", nil, args...)
			})
		}
		t.Run("zero_rows_normalizes", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			s := fixture.Select("SELECT ts_rewrite('d&c&b&a'::tsquery,'SELECT t,s FROM \"fts QF rules\" WHERE FALSE') AS rewritten", []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'c' & 'b' & 'a' & 'd'"}})
			run(s)
			run(fixture.Prepared(s))
		})
		t.Run("more_than_fetch_batch", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			values := make([]string, 0, 102)
			for i := range 101 {
				values = append(values, fmt.Sprintf("(%d,'inert','unused')", i))
			}
			values = append(values, "(101,'a','z')")
			run(fixture.Exec("CREATE TABLE fts_qf_many(ord integer,t tsquery,s tsquery)", 0))
			run(fixture.Exec("INSERT INTO fts_qf_many VALUES"+strings.Join(values, ","), 102))
			ftsQueryFunctionsValue(t, p, b, "ts_rewrite('a'::tsquery,'SELECT t,s FROM fts_qf_many ORDER BY ord')", "TSQUERY", "'z'")
		})
		run(fixture.Select(`SELECT ord,t,s FROM "fts QF rules" ORDER BY ord`, []string{"ord", "t", "s"}, []string{"INT4", "TSQUERY", "TSQUERY"}, [][]any{{int64(1), "'a'", "'b'"}, {int64(2), "'b'", "'c'"}}))
	})
}

func TestReferenceFTSQueryFunctionsSQLState(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_qf_state(ord integer,t tsquery,s tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_qf_state VALUES(1,'a','b'),(2,'b','c')", 2))
		query := "SELECT ts_rewrite('a'::tsquery,'SELECT t,s FROM fts_qf_state ORDER BY ord') AS rewritten"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		check := func(want string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{want}}))
		}
		check("'c'")
		run(fixture.Exec("UPDATE fts_qf_state SET s='d'::tsquery WHERE ord=2", 1))
		check("'d'")
		t.Run("private_transaction", func(t *testing.T) {
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, s := range []fixture.Step{
				fixture.Exec("UPDATE fts_qf_state SET s='e'::tsquery WHERE ord=2", 1),
				fixture.Exec("INSERT INTO fts_qf_state VALUES(3,'e','f')", 1),
				fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'f'"}}),
				fixture.Exec("DELETE FROM fts_qf_state WHERE ord=3", 1),
				fixture.Select(query, []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'e'"}}),
			} {
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
		check("'d'")
		t.Run("stackwise_restore", func(t *testing.T) {
			inner := "SELECT 'a'::tsquery,ts_rewrite('b'::tsquery,'SELECT t,s FROM fts_qf_state WHERE ord=2')"
			q := "SELECT ts_rewrite('a'::tsquery," + ftsTypesQuote(inner) + ") AS first,ts_rewrite('a'::tsquery,'SELECT t,s FROM fts_qf_state ORDER BY ord') AS second,17 AS caller"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(q, []string{"first", "second", "caller"}, []string{"TSQUERY", "TSQUERY", "INT4"}, [][]any{{"'d'", "'d'", int64(17)}}))
		})
		t.Run("same_command_private_rows", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			// Omitted volatility is VOLATILE on both backends; use the existing
			// SQL-body declaration syntax, not an adjacent attribute feature.
			run(fixture.Exec("CREATE FUNCTION fts_qf_stage() RETURNS tsquery LANGUAGE SQL AS $$ INSERT INTO fts_qf_state VALUES(3,'d','z'); SELECT ts_rewrite('a'::tsquery,'SELECT t,s FROM fts_qf_state ORDER BY ord') $$", 0))
			run(fixture.Select("SELECT provolatile FROM pg_catalog.pg_proc WHERE proname='fts_qf_stage'", []string{"provolatile"}, []string{"CHAR"}, [][]any{{"v"}}))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT fts_qf_stage() AS rewritten", []string{"rewritten"}, []string{"TSQUERY"}, [][]any{{"'z'"}}))
		})
	})
}

func TestReferenceFTSQueryFunctionsSQLVolatile(t *testing.T) {
	ftsQueryFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE fts_qf_seq", 0))
		inner := "SELECT 'a'::tsquery,'b'::tsquery WHERE nextval('fts_qf_seq')>0"
		ftsQueryFunctionsValue(t, p, b, "ts_rewrite('a'::tsquery,"+ftsTypesQuote(inner)+")", "TSQUERY", "'b'")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT last_value FROM fts_qf_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}

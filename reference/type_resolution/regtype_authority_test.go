package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegtypeHeldAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regtype_reader"})
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		for _, q := range []string{"CREATE ROLE regtype_reader", "CREATE SCHEMA regtype_private", "CREATE DOMAIN regtype_private.secret AS integer", "SET search_path TO regtype_private,public"} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		const query = "SELECT $1::regtype AS r"
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"secret"}}, "regtype_private.secret"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET ROLE regtype_reader", 0))
		for _, input := range []string{"regtype_private.secret", "secret"} {
			rows, err := held.QueryContext(p.Ctx, input)
			if rows != nil {
				rows.Close()
				t.Error("unauthorized held input exposed rows")
			}
			state := "42501"
			primary := "permission denied for schema regtype_private"
			if input == "secret" {
				state = "42704"
				primary = `type "secret" does not exist`
			}
			fixture.RegtypeAssertError(t, err, state, primary, query, "same-held-role-change", "execute")
		}
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{nil}}, nil))
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"integer"}}, "integer"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("RESET ROLE", 0))
		fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"secret"}}, "regtype_private.secret"))
		// Second inferred slot proves portal field attribution is occurrence-based.
		const secondQuery = "SELECT $1::regtype AS a,$2::regtype AS b"
		second, err := conn.PrepareContext(p.Ctx, secondQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		rows, err := second.QueryContext(p.Ctx, "integer", "missing_regtype_name")
		if rows != nil {
			rows.Close()
			t.Error("bad second frame exposed rows")
		}
		fixture.RegtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, secondQuery, "same-held-second-slot", "execute")
		fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: second, SQL: secondQuery}, recorder, fixture.Select(secondQuery, []string{"a", "b"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"integer", nil}}, "integer", nil))
	})
}

func TestReferenceRegtypeNumericStorage(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE public.regtype_numeric_subject(id integer,r regtype)", 0))
		for _, q := range []string{
			"INSERT INTO public.regtype_numeric_subject VALUES(1,23::smallint)",
			"INSERT INTO public.regtype_numeric_subject VALUES(2,23::integer)",
			"INSERT INTO public.regtype_numeric_subject VALUES(3,23::bigint)",
			"INSERT INTO public.regtype_numeric_subject VALUES(4,23::oid)",
			"INSERT INTO public.regtype_numeric_subject VALUES(5,-1::integer)",
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 1))
		}
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,r,r::bigint AS n FROM public.regtype_numeric_subject ORDER BY id", []string{"id", "r", "n"}, []string{"INT4", "REGTYPE", "INT8"}, [][]any{{int64(1), "integer", int64(23)}, {int64(2), "integer", int64(23)}, {int64(3), "integer", int64(23)}, {int64(4), "integer", int64(23)}, {int64(5), "4294967295", int64(4294967295)}}))
		// Assignment casts out of regtype differ from explicit/implicit comparisons.
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE public.regtype_integer_target(i integer,b bigint)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.regtype_integer_target VALUES('4294967295'::regtype,'4294967295'::regtype)", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT i,b FROM public.regtype_integer_target", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(-1), int64(4294967295)}}))
	})
}

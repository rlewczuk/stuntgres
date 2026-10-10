package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRegtypeHeldAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"regtype_reader"})
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, q := range []string{"CREATE ROLE regtype_reader", "CREATE SCHEMA regtype_private", "CREATE DOMAIN regtype_private.secret AS integer", "SET search_path TO regtype_private,public"} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		const query = "SELECT $1::regtype AS r"
		held, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"secret"}}, "regtype_private.secret"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET ROLE regtype_reader", 0))
		for _, input := range []string{"regtype_private.secret", "secret"} {
			rows, err := held.QueryContext(p.ctx, input)
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
			regtypeAssertError(t, err, state, primary, query, "same-held-role-change", "execute")
		}
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{nil}}, nil))
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"integer"}}, "integer"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("RESET ROLE", 0))
		regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"secret"}}, "regtype_private.secret"))
		// Second inferred slot proves portal field attribution is occurrence-based.
		const secondQuery = "SELECT $1::regtype AS a,$2::regtype AS b"
		second, err := conn.PrepareContext(p.ctx, secondQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		rows, err := second.QueryContext(p.ctx, "integer", "missing_regtype_name")
		if rows != nil {
			rows.Close()
			t.Error("bad second frame exposed rows")
		}
		regtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, secondQuery, "same-held-second-slot", "execute")
		regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: second, query: secondQuery}, recorder, referenceSelect(secondQuery, []string{"a", "b"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"integer", nil}}, "integer", nil))
	})
}

func TestReferenceRegtypeNumericStorage(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE public.regtype_numeric_subject(id integer,r regtype)", 0))
		for _, q := range []string{
			"INSERT INTO public.regtype_numeric_subject VALUES(1,23::smallint)",
			"INSERT INTO public.regtype_numeric_subject VALUES(2,23::integer)",
			"INSERT INTO public.regtype_numeric_subject VALUES(3,23::bigint)",
			"INSERT INTO public.regtype_numeric_subject VALUES(4,23::oid)",
			"INSERT INTO public.regtype_numeric_subject VALUES(5,-1::integer)",
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 1))
		}
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT id,r,r::bigint AS n FROM public.regtype_numeric_subject ORDER BY id", []string{"id", "r", "n"}, []string{"INT4", "REGTYPE", "INT8"}, [][]any{{int64(1), "integer", int64(23)}, {int64(2), "integer", int64(23)}, {int64(3), "integer", int64(23)}, {int64(4), "integer", int64(23)}, {int64(5), "4294967295", int64(4294967295)}}))
		// Assignment casts out of regtype differ from explicit/implicit comparisons.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE public.regtype_integer_target(i integer,b bigint)", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO public.regtype_integer_target VALUES('4294967295'::regtype,'4294967295'::regtype)", 1))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT i,b FROM public.regtype_integer_target", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(-1), int64(4294967295)}}))
	})
}

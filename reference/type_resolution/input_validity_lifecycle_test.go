package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInputValidityLifecycle(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, q := range []string{
			"CREATE SCHEMA regtype_a",
			"CREATE SCHEMA regtype_b",
			"CREATE DOMAIN regtype_a.iv_choice AS integer CHECK(VALUE>0)",
			"CREATE DOMAIN regtype_b.iv_choice AS integer CHECK(VALUE<0)",
			`CREATE DOMAIN public."IV destination" AS integer CHECK(VALUE>0)`,
			`CREATE VIEW public.iv_saved AS SELECT pg_input_is_valid('42','integer') AS ok,(pg_input_error_info('oops','integer')).sql_error_code AS code`,
			"CREATE TABLE public.iv_source(id integer,target text)",
			"INSERT INTO public.iv_source VALUES(1,'integer'),(2,'pg_ddl_command')",
			"CREATE TABLE public.iv_destination(id integer,ok boolean)",
			"INSERT INTO public.iv_destination VALUES(0,true)",
		} {
			_, err := c.ExecContext(p.Ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
		}
		ivBoth(t, p, c, r, ivBool("42", `public."IV destination"`, true))
		ivBoth(t, p, c, r, ivBool("-1", `public."IV destination"`, false))
		ivBoth(t, p, c, r, ivInfo("42", `public."IV destination"`, []any{nil, nil, nil, nil}))
		const view = "SELECT ok,code FROM public.iv_saved"
		step := fixture.Select(view, []string{"ok", "code"}, []string{"BOOL", "TEXT"}, [][]any{{true, "22P02"}})
		step.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, step.Columns, step.Types)
		ivBoth(t, p, c, r, step)
		const heldQuery = "SELECT pg_input_is_valid($1,$2) AS ok"
		held, err := c.PrepareContext(p.Ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: heldQuery}
		fixture.RunStep(t, p.Ctx, executor, r, fixture.Select(heldQuery, []string{"ok"}, []string{"BOOL"}, [][]any{{true}}, "42", `public."IV destination"`))
		for _, f := range []struct {
			path string
			want bool
		}{{"regtype_a,regtype_b,public", true}, {"regtype_b,regtype_a,public", false}, {"regtype_a,regtype_b,public", true}} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO "+f.path, 0))
			fixture.RunStep(t, p.Ctx, executor, r, fixture.Select(heldQuery, []string{"ok"}, []string{"BOOL"}, [][]any{{f.want}}, "1", "iv_choice"))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP DOMAIN regtype_a.iv_choice", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN regtype_a.iv_choice AS integer CHECK(VALUE<0)", 0))
		fixture.RunStep(t, p.Ctx, executor, r, fixture.Select(heldQuery, []string{"ok"}, []string{"BOOL"}, [][]any{{false}}, "1", "iv_choice"))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
		_, err = c.ExecContext(p.Ctx, "INSERT INTO public.iv_destination SELECT id,pg_input_is_valid('42',target) FROM public.iv_source ORDER BY id")
		want := ivDiagnostic("0A000", "cannot accept a value of type pg_ddl_command")
		fixture.AssertDiagnostic(t, err, &want)
		ivBoth(t, p, c, r, fixture.Select("SELECT id,ok FROM public.iv_destination ORDER BY id", []string{"id", "ok"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}}))
		retained, err := c.QueryContext(p.Ctx, view)
		if err != nil {
			t.Fatal(err)
		}
		if b.PG {
			fixture.RegtypeAssertRowsWithMetadata(t, retained, step.Rows, true, step.CheckMetadata)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RegtypeAssertRowsWithMetadata(t, retained, step.Rows, false, step.CheckMetadata)
		ivBoth(t, p, c, r, step)
		ivBoth(t, p, c, r, ivBool("-1", `public."IV destination"`, false))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("input-validity stored expressions changed deterministic dump")
		}
	})
}

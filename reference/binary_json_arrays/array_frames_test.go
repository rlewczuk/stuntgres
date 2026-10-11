package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayFrames(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.array_frames (id INT,a INT[],b INT[])", 0))
		query := "INSERT INTO public.array_frames VALUES ($1,$2,$2)"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		input := []byte("[0:1]={7,NULL}")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, int64(1), input))
		for i := range input {
			input[i] = 'x'
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, int64(2), "{{1,2},{3,4}}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, int64(3), nil))
		want := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		want.Detail = `Unexpected "," character.`
		want.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(query, want, int64(4), "{1,,2}")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, int64(5), "{}"))
		selectQuery := "SELECT $1::INT4[] AS a,$1::INT4[] AS b FROM public.one"
		selectStmt, err := c.PrepareContext(p.Ctx, selectQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		selectHeld := fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: selectQuery}
		for _, tc := range []struct {
			input  any
			output any
		}{{"{7,NULL}", "{7,NULL}"}, {nil, nil}, {"{}", "{}"}, {"[0:1]={8,9}", "[0:1]={8,9}"}} {
			fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, fixture.ArrayReferenceSelect(selectQuery, []string{"a", "b"}, []string{"_INT4", "_INT4"}, []string{"_INT4", "_INT4"}, [][]any{{tc.output, tc.output}}, b.PG, tc.input))
		}
		check := fixture.ArrayReferenceSelect("SELECT id,a,b FROM public.array_frames ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "_INT4", "_INT4"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "[0:1]={7,NULL}", "[0:1]={7,NULL}"}, {int64(2), "{{1,2},{3,4}}", "{{1,2},{3,4}}"}, {int64(3), nil, nil}, {int64(5), "{}", "{}"}}, b.PG)
		run(check)
		run(fixture.Prepared(check))
	})
}

func TestReferenceArrayDomainOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"array_domain_owner"})
	fixture.SchemaReferenceCleanup(t, "array_owner")
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE array_domain_owner", "CREATE SCHEMA array_owner AUTHORIZATION array_domain_owner", "SET ROLE array_domain_owner", "SET search_path TO array_owner,public", "CREATE DOMAIN array_owner.array_owned AS INT", "CREATE TABLE array_owner.array_owned_table (a array_owner.array_owned[])", "RESET ROLE"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO array_owner.array_owned_table VALUES ($1)", 1, "{7,NULL}")))
		name := "_ARRAY_OWNED"
		if b.PG {
			name = ""
		}
		check := fixture.ArrayReferenceSelect("SELECT a FROM array_owner.array_owned_table", []string{"a"}, []string{name}, []string{"_INT4"}, [][]any{{"{7,NULL}"}}, b.PG)
		run(check)
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check, fixture.Exec("CREATE ROLE array_owner_spacer", 0), fixture.Exec("CREATE ROLE array_domain_owner", 0))
		}
		run(fixture.Exec("SET ROLE array_domain_owner", 0))
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type array_owned because other objects depend on it")
		want.Detail = "column a of table array_owned_table depends on type array_owned[]"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP DOMAIN array_owner.array_owned RESTRICT", want))
		run(fixture.Prepared(check))
		run(fixture.Exec("DROP DOMAIN array_owner.array_owned CASCADE", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM array_owner.array_owned_table", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

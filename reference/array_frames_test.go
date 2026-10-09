package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceArrayFrames(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.array_frames (id INT,a INT[],b INT[])", 0))
		query := "INSERT INTO public.array_frames VALUES ($1,$2,$2)"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		input := []byte("[0:1]={7,NULL}")
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(1), input))
		for i := range input {
			input[i] = 'x'
		}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(2), "{{1,2},{3,4}}"))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(3), nil))
		want := controlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		want.Detail = `Unexpected "," character.`
		want.Context = "unnamed portal parameter $2 = '...'"
		s := referenceErrorDiagnostic(query, want, int64(4), "{1,,2}")
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(query, 1, int64(5), "{}"))
		selectQuery := "SELECT $1::INT4[] AS a,$1::INT4[] AS b FROM public.one"
		selectStmt, err := c.PrepareContext(p.ctx, selectQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		selectHeld := referenceHeldStatement{referenceExecutor: c, statement: selectStmt, query: selectQuery}
		for _, tc := range []struct {
			input  any
			output any
		}{{"{7,NULL}", "{7,NULL}"}, {nil, nil}, {"{}", "{}"}, {"[0:1]={8,9}", "[0:1]={8,9}"}} {
			referenceRunStep(t, p.ctx, selectHeld, b.recorder, arrayReferenceSelect(selectQuery, []string{"a", "b"}, []string{"_INT4", "_INT4"}, []string{"_INT4", "_INT4"}, [][]any{{tc.output, tc.output}}, b.pg, tc.input))
		}
		check := arrayReferenceSelect("SELECT id,a,b FROM public.array_frames ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "_INT4", "_INT4"}, []string{"INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "[0:1]={7,NULL}", "[0:1]={7,NULL}"}, {int64(2), "{{1,2},{3,4}}", "{{1,2},{3,4}}"}, {int64(3), nil, nil}, {int64(5), "{}", "{}"}}, b.pg)
		run(check)
		run(referencePrepared(check))
	})
}

func TestReferenceArrayDomainOwnership(t *testing.T) {
	roleReferenceCleanup(t, []string{"array_domain_owner"})
	schemaReferenceCleanup(t, "array_owner")
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{"CREATE ROLE array_domain_owner", "CREATE SCHEMA array_owner AUTHORIZATION array_domain_owner", "SET ROLE array_domain_owner", "SET search_path TO array_owner,public", "CREATE DOMAIN array_owner.array_owned AS INT", "CREATE TABLE array_owner.array_owned_table (a array_owner.array_owned[])", "RESET ROLE"} {
			run(referenceExec(q, 0))
		}
		run(referencePrepared(referenceExec("INSERT INTO array_owner.array_owned_table VALUES ($1)", 1, "{7,NULL}")))
		name := "_ARRAY_OWNED"
		if b.pg {
			name = ""
		}
		check := arrayReferenceSelect("SELECT a FROM array_owner.array_owned_table", []string{"a"}, []string{name}, []string{"_INT4"}, [][]any{{"{7,NULL}"}}, b.pg)
		run(check)
		if !b.pg {
			arrayReferenceNativeRoundtrip(t, p, c, b, check, referenceExec("CREATE ROLE array_owner_spacer", 0), referenceExec("CREATE ROLE array_domain_owner", 0))
		}
		run(referenceExec("SET ROLE array_domain_owner", 0))
		want := controlsReferenceDiagnostic("2BP01", "cannot drop type array_owned because other objects depend on it")
		want.Detail = "column a of table array_owned_table depends on type array_owned[]"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(referenceErrorDiagnostic("DROP DOMAIN array_owner.array_owned RESTRICT", want))
		run(referencePrepared(check))
		run(referenceExec("DROP DOMAIN array_owner.array_owned CASCADE", 0))
		run(referenceExec("RESET ROLE", 0))
		run(referenceSelect("SELECT count(*) AS n FROM array_owner.array_owned_table", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

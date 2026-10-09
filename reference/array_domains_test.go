package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceArrayDomains(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN public.array_di AS INT", "CREATE DOMAIN public.array_dj AS public.array_di",
			"CREATE DOMAIN public.array_da AS INT[]", "CREATE DOMAIN public.array_dd AS public.array_di[]",
			"CREATE DOMAIN public.array_dv AS VARCHAR(3)",
			"CREATE TABLE public.array_domains (id INT,i public.array_di[],j public.array_dj[],a public.array_da,d public.array_dd,n public.array_da[],v public.array_dv[])",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec(`INSERT INTO public.array_domains VALUES (1,'[0:1]={7,NULL}','{8,NULL}','{{1,2},{3,NULL}}','{9,NULL}','{"{1,2}",NULL,"[0:1]={3,4}"}','{abc,NULL}')`, 1))
		run(referencePrepared(referenceExec("INSERT INTO public.array_domains VALUES (2,$1,$2,$3,$4,$5,$6)", 1, "[0:1]={7,NULL}", "{8,NULL}", "{{1,2},{3,NULL}}", "{9,NULL}", `{"{1,2}",NULL,"[0:1]={3,4}"}`, "{abc,NULL}")))
		types := []string{"INT4", "_ARRAY_DI", "_ARRAY_DJ", "_INT4", "_ARRAY_DI", "_ARRAY_DA", "_ARRAY_DV"}
		if b.pg {
			types = []string{"INT4", "", "", "_INT4", "", "", ""}
		}
		row := []any{int64(1), "[0:1]={7,NULL}", "{8,NULL}", "{{1,2},{3,NULL}}", "{9,NULL}", `{"{1,2}",NULL,"[0:1]={3,4}"}`, "{abc,NULL}"}
		row2 := append([]any(nil), row...)
		row2[0] = int64(2)
		s := arrayReferenceSelect("SELECT id,i,j,a,d,n,v FROM public.array_domains ORDER BY id", []string{"id", "i", "j", "a", "d", "n", "v"}, types, []string{"INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_VARCHAR"}, [][]any{row, row2}, b.pg)
		run(s)
		run(referencePrepared(s))
		// Domain element input enforces its constrained base during input, unlike
		// unconstrained varchar-array destination sizing after parameter import.
		arrayReferenceInputError(t, p, b, c, "{abcd}", "public.array_dv[]", controlsReferenceDiagnostic("22001", "value too long for type character varying(3)"))
	})
}

func TestReferenceArrayDomainAssociation(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		defer func() {
			for _, schema := range []string{"array_left", "array_right"} {
				if _, err := c.ExecContext(p.ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		for _, q := range []string{
			"CREATE SCHEMA array_left", "CREATE SCHEMA array_right",
			"CREATE DOMAIN array_left._collision AS TEXT", "CREATE DOMAIN array_left.collision AS INT",
			"CREATE DOMAIN array_right.collision AS TEXT",
			"SET search_path TO array_left,public",
			"CREATE TABLE public.array_collision (id INT,a collision[])",
			"CREATE DOMAIN array_left.whole AS array_left.collision[]",
			"CREATE DOMAIN array_left.nested AS array_left.whole[]",
			"ALTER TABLE public.array_collision ADD COLUMN d array_left.nested",
			"INSERT INTO public.array_collision VALUES (1,'{7,NULL}')",
			"SET search_path TO array_right,public",
			"CREATE TABLE public.array_right_store (a collision[])",
			"INSERT INTO public.array_right_store VALUES ('{word,NULL}')",
		} {
			count := int64(0)
			if q == "INSERT INTO public.array_collision VALUES (1,'{7,NULL}')" || q == "INSERT INTO public.array_right_store VALUES ('{word,NULL}')" {
				count = 1
			}
			run(referenceExec(q, count))
		}
		// Actual association, not an assumption that '_' + element is available.
		collisionName, rightName := "_COLLISION_1", "_COLLISION"
		if b.pg {
			collisionName, rightName = "", ""
		}
		run(arrayReferenceSelect("SELECT a FROM public.array_collision", []string{"a"}, []string{collisionName}, []string{"_INT4"}, [][]any{{"{7,NULL}"}}, b.pg))
		run(referencePrepared(arrayReferenceSelect("SELECT a FROM public.array_right_store", []string{"a"}, []string{rightName}, []string{"_TEXT"}, [][]any{{"{word,NULL}"}}, b.pg)))
		want := controlsReferenceDiagnostic("2BP01", "cannot drop type array_left.collision because other objects depend on it")
		want.Detail = "column a of table array_collision depends on type array_left.collision[]\ntype array_left.whole depends on type array_left.collision[]\ntype array_left.nested depends on type array_left.whole[]\ncolumn d of table array_collision depends on type array_left.nested"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(referenceErrorDiagnostic("DROP DOMAIN array_left.collision RESTRICT", want))
		run(referenceExec("DROP DOMAIN array_left.collision CASCADE", 0))
		run(referenceSelect("SELECT id FROM public.array_collision", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("CREATE DOMAIN array_left.collision AS INT", 0))
		run(referenceExec("ALTER TABLE public.array_collision ADD COLUMN a array_left.collision[]", 0))
		run(referenceExec("UPDATE public.array_collision SET a='{9,NULL}'", 1))
		run(arrayReferenceSelect("SELECT a FROM public.array_collision", []string{"a"}, []string{collisionName}, []string{"_INT4"}, [][]any{{"{9,NULL}"}}, b.pg))
		run(referenceExec("DROP DOMAIN array_left.collision CASCADE", 0))
		run(referenceExec("CREATE DOMAIN array_right.created_after_drop AS INT", 0))
		run(referenceExec("ALTER TABLE public.array_collision ADD COLUMN a array_right.created_after_drop[]", 0))
		run(referenceExec("UPDATE public.array_collision SET a='{11,NULL}'", 1))
		recreated := "_CREATED_AFTER_DROP"
		if b.pg {
			recreated = ""
		}
		run(referencePrepared(arrayReferenceSelect("SELECT a FROM public.array_collision", []string{"a"}, []string{recreated}, []string{"_INT4"}, [][]any{{"{11,NULL}"}}, b.pg)))
	})
}

func TestReferenceArrayRegclassDomains(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.array_regclass_target (id INT)", 0))
		run(referenceExec("CREATE DOMAIN public.array_rc AS REGCLASS", 0))
		run(referenceExec("CREATE TABLE public.array_rc_store (id INT,a public.array_rc[])", 0))
		run(referenceExec("INSERT INTO public.array_rc_store VALUES (1,'{array_regclass_target,NULL,-}')", 1))
		run(referencePrepared(referenceExec("INSERT INTO public.array_rc_store VALUES (2,$1)", 1, "{array_regclass_target,NULL,-}")))
		name := "_ARRAY_RC"
		if b.pg {
			name = ""
		}
		selectRows := func(value string) referenceStep {
			return arrayReferenceSelect("SELECT id,a FROM public.array_rc_store WHERE id<3 ORDER BY id", []string{"id", "a"}, []string{"INT4", name}, []string{"INT4", "_REGCLASS"}, [][]any{{int64(1), value}, {int64(2), value}}, b.pg)
		}
		run(selectRows("{array_regclass_target,NULL,-}"))
		var retained *sql.Rows
		if !b.pg {
			var err error
			retained, err = c.QueryContext(p.ctx, selectRows("{array_regclass_target,NULL,-}").query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(referenceExec("SET search_path TO pg_catalog", 0))
		run(referencePrepared(selectRows("{public.array_regclass_target,NULL,-}")))
		run(referenceExec("SET search_path TO public", 0))
		for _, tc := range []struct{ input, code, message string }{
			{"{missing_array_relation}", "42P01", `relation "missing_array_relation" does not exist`},
			{`{"bad..name"}`, "42602", "invalid name syntax"},
			{"{4294967296}", "22003", `value "4294967296" is out of range for type oid`},
		} {
			arrayReferenceInputError(t, p, b, c, tc.input, "public.array_rc[]", controlsReferenceDiagnostic(tc.code, tc.message))
		}
		run(referenceExec("ALTER TABLE public.array_regclass_target RENAME TO array_regclass_renamed", 0))
		run(selectRows("{array_regclass_renamed,NULL,-}"))
		if retained != nil {
			original := selectRows("{array_regclass_target,NULL,-}")
			arrayReferenceConsumeHeld(t, retained, original.rows, original.types)
		}
		run(referenceExec("CREATE TABLE public.array_regclass_target (id INT)", 0))
		run(referencePrepared(referenceExec("INSERT INTO public.array_rc_store VALUES (3,$1)", 1, "{array_regclass_target,NULL,-}")))
		run(arrayReferenceSelect("SELECT a FROM public.array_rc_store WHERE id=3", []string{"a"}, []string{name}, []string{"_REGCLASS"}, [][]any{{"{array_regclass_target,NULL,-}"}}, b.pg))
		if !b.pg {
			arrayReferenceNativeRoundtrip(t, p, c, b, selectRows("{array_regclass_renamed,NULL,-}"))
		}
	})
}

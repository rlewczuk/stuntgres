//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceViewReplaceCreationNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, qualified := range []bool{false, true} {
			t.Run(fmt.Sprintf("qualified=%v/prepared=%v", qualified, prepared), func(t *testing.T) {
				creator := fmt.Sprintf("namespace_creator_%v_%v", qualified, prepared)
				otherOwner := fmt.Sprintf("namespace_other_%v_%v", qualified, prepared)
				roleReferenceCleanup(t, []string{creator, otherOwner})
				p := newReferencePair(t)
				schemaReferenceCleanup(t, "first_schema", "target_schema")
				c, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
				for _, q := range []string{"CREATE USER " + creator, "CREATE USER " + otherOwner, "CREATE SCHEMA first_schema AUTHORIZATION " + creator, "CREATE SCHEMA target_schema", "CREATE TABLE public.t(id INT)", "CREATE VIEW public.v AS SELECT id FROM public.t", "ALTER VIEW public.v OWNER TO " + otherOwner, "CREATE VIEW target_schema.v AS SELECT id FROM public.t", "ALTER VIEW target_schema.v OWNER TO " + creator, "CREATE VIEW public.w AS SELECT id FROM target_schema.v"} {
					run(referenceExec(q, 0))
				}
				run(referenceExec("INSERT INTO public.t VALUES(1)", 1))
				for _, q := range []string{"GRANT USAGE ON SCHEMA public TO PUBLIC", "GRANT SELECT ON public.t TO " + creator + "," + otherOwner, "REVOKE ALL ON SCHEMA target_schema FROM PUBLIC", "GRANT CREATE ON SCHEMA target_schema TO " + creator} {
					if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
						t.Fatal(q, err)
					}
				}
				var laterOID, targetOID, ruleOID int64
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')").Scan(&laterOID); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='target_schema')").Scan(&targetOID); err != nil {
					t.Fatal(err)
				}
				if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&ruleOID); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("SET SESSION AUTHORIZATION "+creator, 0))
				run(referenceExec("SET search_path TO first_schema,public", 0))
				name := "v"
				if qualified {
					name = "target_schema.v"
				}
				run(referenceExec("CREATE OR REPLACE VIEW "+name+" AS SELECT id+10 AS id FROM public.t", 0))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceSelect("SELECT id FROM public.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if qualified {
					run(referenceSelect("SELECT id FROM public.w", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					var retainedRule int64
					if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&retainedRule); err != nil || retainedRule != ruleOID {
						t.Fatal(retainedRule, err)
					}
				} else {
					run(referenceSelect("SELECT id FROM first_schema.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				}
				run(referenceSelect("SELECT oid::BIGINT AS oid FROM pg_class WHERE oid=$1", []string{"oid"}, []string{"INT8"}, [][]any{{laterOID}}, laterOID))
			})
		}
	}
}

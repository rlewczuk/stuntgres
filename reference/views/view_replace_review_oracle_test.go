package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceCreationNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, qualified := range []bool{false, true} {
			t.Run(fmt.Sprintf("qualified=%v/prepared=%v", qualified, prepared), func(t *testing.T) {
				creator := fmt.Sprintf("namespace_creator_%v_%v", qualified, prepared)
				otherOwner := fmt.Sprintf("namespace_other_%v_%v", qualified, prepared)
				fixture.RoleReferenceCleanup(t, []string{creator, otherOwner})
				fixture.SchemaReferenceCleanup(t, "first_schema", "target_schema")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						for _, q := range []string{"CREATE USER " + creator, "CREATE USER " + otherOwner, "CREATE SCHEMA first_schema AUTHORIZATION " + creator, "CREATE SCHEMA target_schema", "CREATE TABLE public.t(id INT)", "CREATE VIEW public.v AS SELECT id FROM public.t", "ALTER VIEW public.v OWNER TO " + otherOwner, "CREATE VIEW target_schema.v AS SELECT id FROM public.t", "ALTER VIEW target_schema.v OWNER TO " + creator, "CREATE VIEW public.w AS SELECT id FROM target_schema.v"} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO public.t VALUES(1)", 1))
						for _, q := range []string{"GRANT USAGE ON SCHEMA public TO PUBLIC", "GRANT SELECT ON public.t TO " + creator + "," + otherOwner, "REVOKE ALL ON SCHEMA target_schema FROM PUBLIC", "GRANT CREATE ON SCHEMA target_schema TO " + creator} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Fatal(q, err)
							}
						}
						var laterOID, targetOID, ruleOID int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')").Scan(&laterOID); err != nil {
							t.Fatal(err)
						}
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='target_schema')").Scan(&targetOID); err != nil {
							t.Fatal(err)
						}
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&ruleOID); err != nil {
							t.Fatal(err)
						}
						run(fixture.Exec("SET SESSION AUTHORIZATION "+creator, 0))
						run(fixture.Exec("SET search_path TO first_schema,public", 0))
						name := "v"
						if qualified {
							name = "target_schema.v"
						}
						run(fixture.Exec("CREATE OR REPLACE VIEW "+name+" AS SELECT id+10 AS id FROM public.t", 0))
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Select("SELECT id FROM public.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						if qualified {
							run(fixture.Select("SELECT id FROM public.w", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
							var retainedRule int64
							if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", targetOID).Scan(&retainedRule); err != nil || retainedRule != ruleOID {
								t.Fatal(retainedRule, err)
							}
						} else {
							run(fixture.Select("SELECT id FROM first_schema.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
						}
						run(fixture.Select("SELECT oid::BIGINT AS oid FROM pg_class WHERE oid=$1", []string{"oid"}, []string{"INT8"}, [][]any{{laterOID}}, laterOID))
					})
				}
			})
		}
	}
}

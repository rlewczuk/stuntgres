package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified from REL_18_6 system_views.sql and ruleutils layout;
// the pinned oracle resolves formatting before any production implementation.
const pgPoliciesDefinition = ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    pol.polname AS policyname,
        CASE
            WHEN pol.polpermissive THEN 'PERMISSIVE'::text
            ELSE 'RESTRICTIVE'::text
        END AS permissive,
        CASE
            WHEN (pol.polroles = '{0}'::oid[]) THEN (string_to_array('public'::text, ''::text))::name[]
            ELSE ARRAY( SELECT pg_authid.rolname
               FROM pg_authid
              WHERE (pg_authid.oid = ANY (pol.polroles))
              ORDER BY pg_authid.rolname)
        END AS roles,
        CASE pol.polcmd
            WHEN 'r'::"char" THEN 'SELECT'::text
            WHEN 'a'::"char" THEN 'INSERT'::text
            WHEN 'w'::"char" THEN 'UPDATE'::text
            WHEN 'd'::"char" THEN 'DELETE'::text
            WHEN '*'::"char" THEN 'ALL'::text
            ELSE NULL::text
        END AS cmd,
    pg_get_expr(pol.polqual, pol.polrelid) AS qual,
    pg_get_expr(pol.polwithcheck, pol.polrelid) AS with_check
   FROM ((pg_policy pol
     JOIN pg_class c ON ((c.oid = pol.polrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)));`

const pgPoliciesPrettyDefinition = ` SELECT n.nspname AS schemaname,
    c.relname AS tablename,
    pol.polname AS policyname,
        CASE
            WHEN pol.polpermissive THEN 'PERMISSIVE'::text
            ELSE 'RESTRICTIVE'::text
        END AS permissive,
        CASE
            WHEN pol.polroles = '{0}'::oid[] THEN string_to_array('public'::text, ''::text)::name[]
            ELSE ARRAY( SELECT pg_authid.rolname
               FROM pg_authid
              WHERE pg_authid.oid = ANY (pol.polroles)
              ORDER BY pg_authid.rolname)
        END AS roles,
        CASE pol.polcmd
            WHEN 'r'::"char" THEN 'SELECT'::text
            WHEN 'a'::"char" THEN 'INSERT'::text
            WHEN 'w'::"char" THEN 'UPDATE'::text
            WHEN 'd'::"char" THEN 'DELETE'::text
            WHEN '*'::"char" THEN 'ALL'::text
            ELSE NULL::text
        END AS cmd,
    pg_get_expr(pol.polqual, pol.polrelid) AS qual,
    pg_get_expr(pol.polwithcheck, pol.polrelid) AS with_check
   FROM pg_policy pol
     JOIN pg_class c ON c.oid = pol.polrelid
     LEFT JOIN pg_namespace n ON n.oid = c.relnamespace;`

func TestReferencePGPoliciesShape(t *testing.T) {
	pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		pgPoliciesRead(s)
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		s.Exec("CREATE POLICY p ON t")
		row := pgPoliciesRow("t", "p", "PERMISSIVE", "{public}", "ALL", nil, nil)
		pgPoliciesRead(s, row)
		s.Run(pgPoliciesSelect("SELECT * FROM pg_policies WHERE schemaname='public' ORDER BY tablename,policyname", [][]any{row}))
		s.Run(fixture.Select("SELECT policyname,roles,qual FROM pg_policies WHERE schemaname='public' AND tablename='t'", []string{"policyname", "roles", "qual"}, []string{"NAME", "_NAME", "TEXT"}, [][]any{{"p", "{public}", nil}}))
		s.Run(fixture.Select("SELECT c.relkind,c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_policies'::regclass", []string{"relkind", "row_link", "relation_link", "array_link", "element_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true}}))
		s.Run(fixture.Select("SELECT attname,atttypid::regtype::text AS type,attnotnull FROM pg_attribute WHERE attrelid='pg_catalog.pg_policies'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "type", "attnotnull"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"schemaname", "name", false}, {"tablename", "name", false}, {"policyname", "name", false}, {"permissive", "text", false}, {"roles", "name[]", false}, {"cmd", "text", false}, {"qual", "text", false}, {"with_check", "text", false}}))
		for _, tc := range []struct{ arg, want string }{{"", pgPoliciesDefinition}, {",false", pgPoliciesDefinition}, {",true", pgPoliciesPrettyDefinition}} {
			s.Run(fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_policies'::regclass"+tc.arg+") AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		s.Run(fixture.Select("SELECT is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='pg_policies'", []string{"is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}, []string{"VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"NO", "NO", "NO", "NO", "NO"}}))
		s.Exec("DROP POLICY p ON t")
		pgPoliciesRead(s)
	})
}

func TestReferencePGPoliciesDefinitions(t *testing.T) {
	for _, mode := range []string{"PERMISSIVE", "RESTRICTIVE"} {
		for _, command := range []string{"ALL", "SELECT", "INSERT", "UPDATE", "DELETE"} {
			t.Run(mode+"/"+command, func(t *testing.T) {
				pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
					s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
					clause := " USING(id>0) WITH CHECK(allowed)"
					var using, check any = "(id > 0)", "allowed"
					switch command {
					case "INSERT": clause, using = " WITH CHECK(allowed)", nil
					case "SELECT", "DELETE": clause, check = " USING(id>0)", nil
					}
					s.Exec("CREATE POLICY p ON t AS "+mode+" FOR "+command+clause)
					pgPoliciesRead(s, pgPoliciesRow("t", "p", mode, "{public}", command, using, check))
					s.Run(fixture.Select("SELECT relrowsecurity FROM pg_class WHERE oid='t'::regclass", []string{"relrowsecurity"}, []string{"BOOL"}, [][]any{{false}}))
				})
			})
		}
	}
}

func TestReferencePGPoliciesRoles(t *testing.T) {
	pgPoliciesCases(t, []string{"policies_a", "policies_z", "comma,role", `quote"role`, `slash\role`}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policies_z,policies_a,policies_a USING(id>0)")
		pgPoliciesRead(s, pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_a,policies_z}", "UPDATE", "(id > 0)", nil))
		s.Run(fixture.Select("SELECT cardinality(polroles) AS n FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}}))
		s.Exec("ALTER ROLE policies_z RENAME TO policies_0")
		defer func() {
			if _, err := s.C.ExecContext(s.P.Ctx, "ALTER ROLE policies_0 RENAME TO policies_z"); err != nil {
				s.T.Error(err)
			}
		}()
		pgPoliciesRead(s, pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_0,policies_a}", "UPDATE", "(id > 0)", nil))
		s.Exec(`ALTER POLICY p ON t TO "slash\role","quote""role","comma,role"`)
		pgPoliciesRead(s, pgPoliciesRow("t", "p", "RESTRICTIVE", `{"comma,role","quote\"role","slash\\role"}`, "UPDATE", "(id > 0)", nil))
		s.Exec("ALTER POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER")
		pgPoliciesRead(s, pgPoliciesRow("t", "p", "RESTRICTIVE", "{"+s.Bootstrap+"}", "UPDATE", "(id > 0)", nil))
		s.Exec("ALTER POLICY p ON t TO PUBLIC")
		pgPoliciesRead(s, pgPoliciesRow("t", "p", "RESTRICTIVE", "{public}", "UPDATE", "(id > 0)", nil))
	})
}

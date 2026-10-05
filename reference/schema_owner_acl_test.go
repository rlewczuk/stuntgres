//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceSchemaOwnerExplicitACL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"sowner_a", "sowner_b", "sowner_reader", "sowner_table"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "sowner_acl")
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			exec := func(q string) {
				if _, err := c.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			for _, q := range []string{
				"CREATE USER sowner_a", "CREATE USER sowner_b", "CREATE USER sowner_reader", "CREATE USER sowner_table",
				"CREATE SCHEMA sowner_acl AUTHORIZATION sowner_a",
				"SET SESSION AUTHORIZATION sowner_a",
				"GRANT USAGE,CREATE ON SCHEMA sowner_acl TO sowner_a WITH GRANT OPTION",
				"GRANT CREATE ON SCHEMA sowner_acl TO sowner_b WITH GRANT OPTION",
				"GRANT USAGE ON SCHEMA sowner_acl TO sowner_reader",
				"GRANT CREATE ON SCHEMA sowner_acl TO sowner_table WITH GRANT OPTION",
				"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION sowner_table",
				"GRANT CREATE ON SCHEMA sowner_acl TO PUBLIC", "CREATE TABLE sowner_acl.t(id INT)", "RESET SESSION AUTHORIZATION",
			} {
				exec(q)
			}
			type grant struct {
				grantor, grantee, privilege string
				option                      bool
			}
			read := func() []grant {
				rows, err := c.QueryContext(p.ctx, "SELECT g.rolname, COALESCE(r.rolname,'PUBLIC'), a.privilege_type,a.is_grantable FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a JOIN pg_roles g ON g.oid=a.grantor LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE n.nspname='sowner_acl' ORDER BY 1,2,3")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var got []grant
				for rows.Next() {
					var g grant
					if err := rows.Scan(&g.grantor, &g.grantee, &g.privilege, &g.option); err != nil {
						t.Fatal(err)
					}
					got = append(got, g)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return got
			}
			before := []grant{{"sowner_a", "sowner_a", "CREATE", true}, {"sowner_a", "sowner_a", "USAGE", true}, {"sowner_a", "sowner_b", "CREATE", true}, {"sowner_a", "sowner_reader", "USAGE", false}, {"sowner_a", "sowner_table", "CREATE", true}, {"sowner_table", "PUBLIC", "CREATE", false}}
			if got := read(); !reflect.DeepEqual(got, before) {
				t.Fatalf("before ACL %+v want %+v", got, before)
			}
			transfer := referenceExec("ALTER SCHEMA sowner_acl OWNER TO sowner_b", 0)
			transfer.prepared = prepared
			referenceRunStep(t, p.ctx, c, p.postgresRecord, transfer)
			after := []grant{{"sowner_b", "sowner_b", "CREATE", true}, {"sowner_b", "sowner_b", "USAGE", true}, {"sowner_b", "sowner_reader", "USAGE", false}, {"sowner_b", "sowner_table", "CREATE", true}, {"sowner_table", "PUBLIC", "CREATE", false}}
			if got := read(); !reflect.DeepEqual(got, after) {
				t.Fatalf("after ACL %+v want %+v", got, after)
			}
			for _, tc := range []struct {
				role          string
				usage, create bool
			}{{"sowner_a", false, true}, {"sowner_b", true, true}, {"sowner_reader", true, true}, {"sowner_table", false, true}} {
				var usage, create bool
				if err := c.QueryRowContext(p.ctx, "SELECT has_schema_privilege($1,'sowner_acl','USAGE'),has_schema_privilege($1,'sowner_acl','CREATE')", tc.role).Scan(&usage, &create); err != nil || usage != tc.usage || create != tc.create {
					t.Fatal(tc, usage, create, err)
				}
			}
			var owner, tableOwner string
			if err := c.QueryRowContext(p.ctx, "SELECT n.nspowner::regrole::text,c.relowner::regrole::text FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE n.nspname='sowner_acl' AND c.relname='t'").Scan(&owner, &tableOwner); err != nil || owner != "sowner_b" || tableOwner != "sowner_table" {
				t.Fatal(owner, tableOwner, err)
			}
			referenceRunStep(t, p.ctx, c, p.postgresRecord, referencePrepared(referenceExec("DROP ROLE sowner_a", 0)))
		})
	}
}

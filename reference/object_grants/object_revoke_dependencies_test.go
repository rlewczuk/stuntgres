package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectRevokeDependencies(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"or_support"})
	objectGrantScenario(t, []string{"or_dep", "or_types", "or_owned"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		supportCreated := false
		defer func() {
			if !supportCreated {
				return
			}
			for _, query := range []string{"RESET SESSION AUTHORIZATION", "DROP OWNED BY or_support CASCADE", "DROP ROLE or_support"} {
				if _, err := c.ExecContext(p.Ctx, query); err != nil {
					t.Error("support cleanup", query, err)
				}
			}
		}()
		var owner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE SCHEMA or_dep", 0))
		run(fixture.Exec("CREATE SCHEMA or_types", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_dep TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA or_types TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE TYPE or_types.e AS ENUM('a')", 0))
		run(fixture.Exec("CREATE DOMAIN or_types.d AS INT", 0))
		for _, tc := range []struct{ target, privilege, bit, acl, consumer, drop, noun, name, missing, code, message, baseline string }{
			{"SCHEMA or_dep", "CREATE", "C", "SELECT unnest(nspacl) AS acl FROM pg_namespace WHERE nspname='or_dep'", "CREATE TABLE or_dep.probe(x INT)", "DROP TABLE or_dep.probe", "schema", "or_dep", "SCHEMA or_dep,or_missing", "3F000", `schema "or_missing" does not exist`, "UC"},
			{"TYPE or_types.e", "USAGE", "U", "SELECT unnest(typacl) AS acl FROM pg_type WHERE oid='or_types.e'::regtype", "CREATE TABLE or_types.probe(x or_types.e)", "DROP TABLE or_types.probe", "type", "or_types.e", "TYPE or_types.e,or_types.missing", "42704", `type "or_types.missing" does not exist`, "U"},
			{"DOMAIN or_types.d", "USAGE", "U", "SELECT unnest(typacl) AS acl FROM pg_type WHERE oid='or_types.d'::regtype", "CREATE TABLE or_types.probe(x or_types.d)", "DROP TABLE or_types.probe", "type", "or_types.d", "DOMAIN or_types.d,or_types.missing", "42704", `type "or_types.missing" does not exist`, "U"},
		} {
			if tc.noun == "type" {
				run(fixture.Exec("REVOKE USAGE ON "+tc.target+" FROM PUBLIC", 0))
			}
			base := []string{owner + "=" + tc.baseline + "/" + owner}
			if tc.noun == "schema" {
				base = append(base, "og_object_reader=U/"+owner, "og_object_other=U/"+owner)
			}
			acl := func(aOption, delegated, independent bool) {
				entries := append([]string{}, base...)
				// Schema USAGE and CREATE from the same grantor share one ACL item.
				if tc.noun == "schema" {
					entries = entries[:1]
				}
				aBits, bBits := tc.bit, ""
				if aOption {
					aBits += "*"
				}
				if tc.noun == "schema" {
					aBits = "U" + aBits
					bBits = "U"
				}
				entries = append(entries, "og_object_reader="+aBits+"/"+owner)
				if independent {
					bBits += tc.bit
				}
				if bBits != "" {
					entries = append(entries, "og_object_other="+bBits+"/"+owner)
				}
				if delegated {
					entries = append(entries, "og_object_other="+tc.bit+"/og_object_reader")
				}
				rows := make([][]any, len(entries))
				for i, entry := range entries {
					rows[i] = []any{entry}
				}
				s := fixture.Select(tc.acl, []string{"acl"}, []string{"ACLITEM"}, rows)
				s.Bag = true
				run(s)
			}
			consume := func(actor string, allowed bool) {
				fixture.RoleGrantActor(run, actor, func() {
					if allowed {
						run(fixture.Exec(tc.consumer, 0))
						run(fixture.Exec(tc.drop, 0))
					} else {
						fixture.RoleGrantPermissionDenied(run, tc.consumer, tc.noun, tc.name)
					}
				})
			}
			chain := func() {
				run(fixture.Exec("GRANT "+tc.privilege+" ON "+tc.target+" TO og_object_reader WITH GRANT OPTION", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT "+tc.privilege+" ON "+tc.target+" TO og_object_other", 0)) })
			}
			chain()
			consume("og_object_other", true)
			option := "REVOKE GRANT OPTION FOR " + tc.privilege + " ON " + tc.target + " FROM og_object_reader"
			for _, suffix := range []string{"", " RESTRICT"} {
				s := fixture.ImplicitError(option+suffix, fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
				s.ErrorStage = "execute"
				run(s)
				acl(true, true, false)
				consume("og_object_reader", true)
				consume("og_object_other", true)
			}
			run(fixture.Exec(option+" CASCADE", 0))
			acl(false, false, false)
			consume("og_object_reader", true)
			consume("og_object_other", false)
			run(fixture.Exec("REVOKE "+tc.privilege+" ON "+tc.target+" FROM og_object_reader", 0))
			consume("og_object_reader", false)
			chain()
			run(fixture.Exec("GRANT "+tc.privilege+" ON "+tc.target+" TO og_object_other", 0))
			fixture.RoleGrantError(run, "REVOKE "+tc.privilege+" ON "+tc.missing+" FROM og_object_other", tc.code, tc.message)
			acl(true, true, true)
			consume("og_object_other", true)
			run(fixture.Exec(option+" CASCADE", 0))
			acl(false, false, true)
			consume("og_object_other", true)
			// Restore delegation authority before testing origin-scoped ordinary revoke.
			run(fixture.Exec("GRANT "+tc.privilege+" ON "+tc.target+" TO og_object_reader WITH GRANT OPTION", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec("GRANT "+tc.privilege+" ON "+tc.target+" TO og_object_other", 0))
				run(fixture.Exec("REVOKE "+tc.privilege+" ON "+tc.target+" FROM og_object_other", 0))
			})
			acl(true, false, true)
			consume("og_object_other", true)
		}
		// A second option origin supports A's existing downstream grant.
		run(fixture.Exec("CREATE ROLE or_support", 0))
		supportCreated = true
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA or_dep TO or_support WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "or_support", func() { run(fixture.Exec("GRANT CREATE ON SCHEMA or_dep TO og_object_reader WITH GRANT OPTION", 0)) })
		run(fixture.Exec("REVOKE CREATE ON SCHEMA or_dep FROM og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT CREATE ON SCHEMA or_dep TO og_object_other", 0)) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR CREATE ON SCHEMA or_dep FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { run(fixture.Exec("CREATE TABLE or_dep.supported(x INT)", 0)) })
		fixture.RoleGrantActor(run, "or_support", func() {
			run(fixture.Exec("REVOKE GRANT OPTION FOR CREATE ON SCHEMA or_dep FROM og_object_reader CASCADE", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE or_dep.retained(x INT)", 0)) })
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE or_dep.unsupported(x INT)", "schema", "or_dep")
		})
		run(fixture.Exec("CREATE SCHEMA or_owned AUTHORIZATION og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("REVOKE CREATE ON SCHEMA or_owned FROM og_object_reader", 0))
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE or_owned.denied(x INT)", "schema", "or_owned")
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA or_owned TO og_object_other", 0))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() { run(fixture.Exec("CREATE TABLE or_owned.allowed(x INT)", 0)) })
	})
}

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGNamespaceVisibility(t *testing.T) {
	pgNamespaceScenario(t, []string{"ns18_denied", "ns18_shadow"}, []string{"ns18_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, c, "CREATE ROLE ns18_reader", "CREATE SCHEMA ns18_denied", "CREATE TABLE ns18_denied.ns18_hidden(i INT)", "CREATE SCHEMA ns18_shadow", "CREATE TABLE ns18_shadow.pg_namespace(marker INT)", "CREATE TABLE public.ns18_visible(i INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ns18_shadow.pg_namespace VALUES(18)", 1))
		id := pgNamespaceID(t, p, c, "ns18_denied")
		_, owner := pgNamespaceCurrentRole(t, p, b, c)
		pgNamespaceExec(t, p, b, c, "SET ROLE ns18_reader")
		pgNamespaceRow(t, p, b, c, "ns18_denied", id, owner, nil)
		for _, prepared := range []bool{false, true} {
			s := fixture.SchemaReferenceError("SELECT i FROM ns18_denied.ns18_hidden", "42501", "permission denied for schema ns18_denied", "", prepared, "execute", "15")
			s.SelectRows = true
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		pgNamespaceExec(t, p, b, c, "SET search_path TO ns18_missing,ns18_denied,public")
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT nspname FROM pg_namespace WHERE oid=11", []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}))
		pgNamespaceExec(t, p, b, c, "RESET ROLE", "SET search_path TO ns18_shadow,public")
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT nspname FROM pg_namespace WHERE oid=11", []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}))
		pgNamespaceExec(t, p, b, c, "SET search_path TO ns18_shadow,pg_catalog")
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT marker FROM pg_namespace", []string{"marker"}, []string{"INT4"}, [][]any{{int64(18)}}))
		pgNamespaceRow(t, p, b, c, "pg_toast", 99, 10, nil)
		pgNamespaceCheck(t, p, b, c, fixture.Select("SELECT i FROM public.ns18_visible", []string{"i"}, []string{"INT4"}, nil))
		pgNamespaceExec(t, p, b, c, "RESET search_path")
		pgNamespaceFixtureOwnerScope(t, p, b, c)
	})
}

func TestReferencePGNamespaceACL(t *testing.T) {
	roles := []string{"ns18_acl_owner", "ns18_acl_reader", "ns18_acl_renamed", "ns18_acl_next"}
	pgNamespaceScenario(t, []string{"ns18_acl", "ns18_acl_null"}, roles, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, c, "CREATE ROLE ns18_acl_owner", "CREATE ROLE ns18_acl_reader", "CREATE ROLE ns18_acl_next", "CREATE SCHEMA ns18_acl_null AUTHORIZATION ns18_acl_owner")
		readerName := "ns18_acl_reader"
		defer func() {
			pgNamespaceExec(t, p, b, c, "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_acl_owner REVOKE USAGE ON SCHEMAS FROM PUBLIC", "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_acl_owner REVOKE CREATE ON SCHEMAS FROM "+readerName)
		}()
		owner := pgNamespaceRoleID(t, p, c, "ns18_acl_owner")
		pgNamespaceRow(t, p, b, c, "ns18_acl_null", pgNamespaceID(t, p, c, "ns18_acl_null"), owner, nil)
		pgNamespaceExec(t, p, b, c, "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_acl_owner GRANT USAGE ON SCHEMAS TO PUBLIC", "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_acl_owner GRANT CREATE ON SCHEMAS TO ns18_acl_reader WITH GRANT OPTION", "CREATE SCHEMA ns18_acl AUTHORIZATION ns18_acl_owner")
		id := pgNamespaceID(t, p, c, "ns18_acl")
		reader := pgNamespaceRoleID(t, p, c, "ns18_acl_reader")
		acl := "{=U/ns18_acl_owner,ns18_acl_owner=UC/ns18_acl_owner,ns18_acl_reader=C*/ns18_acl_owner}"
		pgNamespaceRow(t, p, b, c, "ns18_acl", id, owner, acl)
		retained, err := c.QueryContext(p.Ctx, "SELECT nspacl FROM pg_namespace WHERE oid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		pgNamespaceExec(t, p, b, b.DB, "ALTER ROLE ns18_acl_reader RENAME TO ns18_acl_renamed")
		readerName = "ns18_acl_renamed"
		if pgNamespaceRoleID(t, p, b.DB, readerName) != reader {
			t.Fatal("role rename changed identity")
		}
		pgNamespaceRow(t, p, b, b.DB, "ns18_acl", id, owner, "{=U/ns18_acl_owner,ns18_acl_owner=UC/ns18_acl_owner,"+readerName+"=C*/ns18_acl_owner}")
		// Retained rows preserve the original ACL across role rename and
		// ownership transfer, while fresh rows project current principal names.
		pgNamespaceExec(t, p, b, b.DB, "ALTER SCHEMA ns18_acl OWNER TO ns18_acl_next")
		if !retained.Next() {
			t.Fatal("held ACL missing", retained.Err())
		}
		var old string
		if err := retained.Scan(&old); err != nil || old != acl {
			t.Fatal(old, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("held ACL extra/error", retained.Err())
		}
		retained.Close()
		pgNamespaceRow(t, p, b, c, "ns18_acl", id, pgNamespaceRoleID(t, p, c, "ns18_acl_next"), "{=U/ns18_acl_next,ns18_acl_next=UC/ns18_acl_next,"+readerName+"=C*/ns18_acl_next}")
		pgNamespaceExec(t, p, b, c, "ALTER SCHEMA ns18_acl OWNER TO "+readerName)
		pgNamespaceRow(t, p, b, c, "ns18_acl", id, reader, "{=U/"+readerName+","+readerName+"=UC*/"+readerName+"}")
	})
}

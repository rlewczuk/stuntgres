package reference_test

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/lib/pq"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"di_schema_owner", "di_owner", "di_successor", "di_actor", "di_foreign", "di Quoted"})
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		domainIdentityDDL(t, p, c, r,
			"CREATE ROLE di_schema_owner", "CREATE ROLE di_owner", "CREATE ROLE di_successor", "CREATE ROLE di_actor", "CREATE ROLE di_foreign", `CREATE ROLE "di Quoted"`,
			"CREATE SCHEMA di_src AUTHORIZATION di_schema_owner", "CREATE SCHEMA di_dst AUTHORIZATION di_schema_owner", "CREATE SCHEMA di_other AUTHORIZATION di_foreign",
			"GRANT USAGE ON SCHEMA di_src,di_dst,di_other TO PUBLIC", "GRANT CREATE ON SCHEMA di_src TO di_owner",
			"SET ROLE di_owner", "CREATE DOMAIN di_src.d AS integer", "RESET ROLE", "REVOKE CREATE ON SCHEMA di_src FROM di_owner",
			"CREATE DOMAIN di_other.child AS di_src.d", "CREATE TABLE di_other.t(id integer,v di_src.d,keep integer)", "ALTER TABLE di_other.t OWNER TO di_foreign",
			"CREATE VIEW di_other.v AS SELECT v FROM di_other.t", "ALTER VIEW di_other.v OWNER TO di_foreign")
		run(fixture.Exec("INSERT INTO di_other.t VALUES(1,7,11),(2,NULL,12)", 2))
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid")
		childOwner := domainAlterOID(t, p, c, "SELECT typowner FROM pg_type WHERE oid='di_other.child'::regtype::oid")
		tableOwner := domainAlterOID(t, p, c, "SELECT relowner FROM pg_class WHERE oid='di_other.t'::regclass::oid")
		domainIdentityDDL(t, p, c, r, "SET ROLE di_owner", "ALTER DOMAIN di_src.d RENAME TO renamed", "RESET ROLE")
		// CREATE on the destination is checked before domain ownership, and
		// same-schema SET SCHEMA is not an unchecked no-op.
		domainIdentityDDL(t, p, c, r, "SET ROLE di_actor")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst", "42501", "permission denied for schema di_dst", false)
		domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT CREATE ON SCHEMA di_dst TO di_actor", "SET ROLE di_actor")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst", "42501", "must be owner of type di_src.renamed", true)
		// A same-owner OWNER request succeeds even for an unrelated actor.
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.renamed OWNER TO di_owner")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed OWNER TO di_absent_role", "42704", `role "di_absent_role" does not exist`, false)
		domainIdentityDDL(t, p, c, r, "RESET ROLE", "SET ROLE di_owner")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed SET SCHEMA di_src", "42501", "permission denied for schema di_src", false)
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed OWNER TO di_successor", "42501", `must be able to SET ROLE "di_successor"`, false)
		domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT di_successor TO di_owner WITH INHERIT FALSE, SET TRUE", "SET ROLE di_owner")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.renamed OWNER TO di_successor", "42501", "permission denied for schema di_src", true)
		domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT CREATE ON SCHEMA di_dst TO di_owner", "SET ROLE di_owner", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst", "RESET ROLE")
		// Successor has no destination CREATE: irrelevant for move, required
		// for changed OWNER. ACL owner/grantor substitution is real catalog state.
		domainIdentityDDL(t, p, c, r, "GRANT CREATE ON SCHEMA di_dst TO di_successor", "SET ROLE di_owner", "GRANT USAGE ON DOMAIN di_dst.renamed TO di_actor", "ALTER DOMAIN di_dst.renamed OWNER TO di_successor", "RESET ROLE")
		domainIdentitySameOID(t, p, c, "SELECT 'di_dst.renamed'::regtype::oid", oid)
		domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_dst.renamed'::regtype::oid", array)
		domainIdentitySameOID(t, p, c, "SELECT typowner FROM pg_type WHERE oid='di_other.child'::regtype::oid", childOwner)
		domainIdentitySameOID(t, p, c, "SELECT relowner FROM pg_class WHERE oid='di_other.t'::regclass::oid", tableOwner)
		run(fixture.Select("SELECT t.typname,pg_get_userbyid(t.typowner) AS owner FROM pg_type t WHERE t.oid='di_dst.renamed'::regtype::oid OR t.oid=(SELECT typarray FROM pg_type WHERE oid='di_dst.renamed'::regtype::oid) ORDER BY t.typname", []string{"typname", "owner"}, []string{"NAME", "NAME"}, [][]any{{"_renamed", "di_successor"}, {"renamed", "di_successor"}}))
		// ACL semantics are a grant multiset. The existing baseline's owner/
		// PUBLIC storage order is not an ownership-transfer contract.
		var acl pq.StringArray
		if err := c.QueryRowContext(p.Ctx, "SELECT typacl FROM pg_type WHERE oid='di_dst.renamed'::regtype::oid").Scan(&acl); err != nil {
			t.Fatal("read transferred domain ACL", err)
		}
		slices.Sort(acl)
		wantACL := []string{"=U/di_successor", "di_actor=U/di_successor", "di_successor=U/di_successor"}
		if !slices.Equal([]string(acl), wantACL) {
			t.Fatalf("transferred domain ACL bag=%q want %q", acl, wantACL)
		}
		domainIdentityDDL(t, p, c, r, "SET ROLE di_actor")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_dst.renamed SET SCHEMA di_dst", "42501", "must be owner of type di_dst.renamed", false)
		domainIdentityDDL(t, p, c, r, "RESET ROLE")
		for _, selector := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER", `"di Quoted"`} {
			run(fixture.Prepared(fixture.Exec("ALTER DOMAIN di_dst.renamed OWNER TO "+selector, 0)))
		}
		// Source schema ownership does NOT confer ALTER authority.
		domainIdentityDDL(t, p, c, r, "SET ROLE di_schema_owner")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_dst.renamed RENAME TO denied", "42501", "must be owner of type di_dst.renamed", false)
		// But it DOES confer DROP authority, including removal of consumers
		// owned by another principal. Surviving table state/ACL/comment is intact.
		domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT SELECT ON di_other.t TO di_actor", "COMMENT ON TABLE di_other.t IS 'survivor'", "SET ROLE di_schema_owner")
		*notices = nil
		run(fixture.Exec("DROP DOMAIN di_dst.renamed CASCADE", 0))
		fixture.DomainReferenceNotice(t, notices, "drop cascades to 3 other objects", "drop cascades to type di_other.child\ndrop cascades to column v of table di_other.t\ndrop cascades to view di_other.v")
		domainIdentityDDL(t, p, c, r, "RESET ROLE")
		domainIdentitySameOID(t, p, c, "SELECT relowner FROM pg_class WHERE oid='di_other.t'::regclass::oid", tableOwner)
		run(fixture.Select("SELECT description AS comment FROM pg_description WHERE objoid='di_other.t'::regclass::oid AND classoid=1259::oid AND objsubid=0", []string{"comment"}, []string{"TEXT"}, [][]any{{"survivor"}}))
		domainIdentityDDL(t, p, c, r, "SET ROLE di_actor")
		run(fixture.Select("SELECT id,keep FROM di_other.t ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}))
		domainIdentityDDL(t, p, c, r, "RESET ROLE")
		run(fixture.Error("SELECT * FROM di_other.v", "42P01"))
	})
}

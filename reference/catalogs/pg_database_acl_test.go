package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGDatabaseACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgdb18_acl_reader", "pgdb18_acl_owner"})
	var original sql.NullString
	var originalOwner string
	var restore bool
	// Teardown only: restore the exact cluster baseline after case borrowers close,
	// before role removal. Public-schema reset does not restore database ACLs.
	t.Cleanup(func() {
		if !restore {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := fixture.Admin.ExecContext(ctx, "ALTER DATABASE stuntgres_reference OWNER TO \""+escapeDatabaseIdentifier(originalOwner)+"\""); err != nil {
			t.Error("restore database owner", err)
			return
		}
		var acl any
		if original.Valid {
			acl = original.String
		}
		result, err := fixture.Admin.ExecContext(ctx, "UPDATE pg_catalog.pg_database SET datacl=$1::aclitem[] WHERE datname=current_database()", acl)
		if err != nil {
			t.Error("restore exact database ACL", err)
			return
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Error("restore database ACL count", n, err)
		}
		var restored sql.NullString
		var restoredOwner string
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT datacl,(SELECT rolname FROM pg_roles WHERE oid=datdba) FROM pg_database WHERE datname=current_database()").Scan(&restored, &restoredOwner); err != nil || restored != original || restoredOwner != originalOwner {
			t.Error("restored database baseline", restored, restoredOwner, err)
		}
	})
	p := fixture.NewScenario(t)
	pgDatabaseBackends(t, p, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if b.PG {
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT datacl,(SELECT rolname FROM pg_roles WHERE oid=datdba) FROM pg_database WHERE datname=current_database()").Scan(&original, &originalOwner); err != nil {
				t.Fatal(err)
			}
			restore = true
		}
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "CREATE ROLE pgdb18_acl_reader", "CREATE ROLE pgdb18_acl_owner")
		var name string
		if err := owner.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&name); err != nil {
			t.Fatal(err)
		}
		database := "\"" + escapeDatabaseIdentifier(name) + "\""
		q := "SELECT datacl FROM pg_database WHERE datname=current_database()"
		held := fixture.ViewHold(t, p, b, q)
		peerHeld := fixture.ViewHold(t, p, b, q)
		ownerQuery := "SELECT datdba=(SELECT oid FROM pg_roles WHERE rolname='pgdb18_acl_owner') AS successor,datname=current_database() AS same_database FROM pg_database WHERE datname=current_database()"
		ownerHeld := fixture.ViewHold(t, p, b, ownerQuery)
		pgDatabaseCheck(t, p, b, ownerHeld, fixture.Select(ownerQuery, []string{"successor", "same_database"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		base := any("{postgres=C*/postgres}")
		if b.PG {
			base = nil
		}
		check := func(db fixture.Executor, acl any) {
			pgDatabaseCheck(t, p, b, db, fixture.Select(q, []string{"datacl"}, []string{"_ACLITEM"}, [][]any{{acl}}))
		}
		check(owner, base)
		check(held, base)
		who := pgTableViewsOwner(b)
		ownerEntry := who + "=C*/" + who
		if b.PG {
			ownerEntry = who + "=CTc/" + who
		}
		prefix := "{" + ownerEntry
		if b.PG {
			prefix = "{=Tc/" + who + "," + ownerEntry
		}
		granted := prefix + ",pgdb18_acl_reader=C/" + who + "}"
		option := prefix + ",pgdb18_acl_reader=C*/" + who + "}"
		materialized := prefix + "}"
		grant := "GRANT CREATE ON DATABASE " + database + " TO pgdb18_acl_reader"
		revoke := "REVOKE CREATE ON DATABASE " + database + " FROM pgdb18_acl_reader"
		pgClassExec(t, p, b, owner, grant)
		check(held, granted)
		pgClassExec(t, p, b, owner, grant+" WITH GRANT OPTION")
		check(held, option)
		pgClassExec(t, p, b, owner, "REVOKE GRANT OPTION FOR CREATE ON DATABASE "+database+" FROM pgdb18_acl_reader")
		check(held, granted)
		pgClassExec(t, p, b, owner, revoke)
		check(held, materialized)
		pgClassExec(t, p, b, owner, "GRANT CREATE ON DATABASE "+database+" TO PUBLIC")
		publicACL := "{" + ownerEntry + ",=C/" + who + "}"
		if b.PG {
			publicACL = "{=CTc/" + who + "," + ownerEntry + "}"
		}
		check(held, publicACL)
		pgClassExec(t, p, b, owner, "REVOKE CREATE ON DATABASE "+database+" FROM PUBLIC")
		check(held, materialized)
		pgClassExec(t, p, b, owner, "BEGIN", grant)
		defer owner.ExecContext(context.Background(), "ROLLBACK")
		check(owner, granted)
		check(peer, materialized)
		check(peerHeld, materialized)
		pgClassExec(t, p, b, owner, "SAVEPOINT pgdb18_acl_save", revoke)
		check(owner, materialized)
		pgClassExec(t, p, b, owner, "ROLLBACK TO pgdb18_acl_save")
		check(owner, granted)
		pgClassExec(t, p, b, owner, "ROLLBACK")
		check(held, materialized)
		pgClassExec(t, p, b, owner, "BEGIN", grant, "COMMIT")
		check(peerHeld, granted)
		pgClassExec(t, p, b, owner, "ALTER DATABASE "+database+" OWNER TO pgdb18_acl_owner")
		pgDatabaseCheck(t, p, b, ownerHeld, fixture.Select(ownerQuery, []string{"successor", "same_database"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		successorACL := strings.ReplaceAll(granted, who, "pgdb18_acl_owner")
		check(held, successorACL)
		pgClassExec(t, p, b, owner, "ALTER DATABASE "+database+" OWNER TO "+who, revoke)
		check(held, materialized)
		pgDatabaseCheck(t, p, b, ownerHeld, fixture.Select(ownerQuery, []string{"successor", "same_database"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
	})
}

func escapeDatabaseIdentifier(name string) string {
	return strings.ReplaceAll(name, "\"", "\"\"")
}

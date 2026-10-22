package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgrowFunctionID(t *testing.T, p *fixture.Pair, x identityExecutor, signature string) int64 {
	t.Helper()
	var id int64
	if err := x.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", signature).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal("installed routine identity", id, err)
	}
	return id
}

func TestReferencePgrowlocksLifecycleAndControls(t *testing.T) {
	// Nonpublic installer grants must be removed before role teardown.
	fixture.RoleReferenceCleanup(t, []string{"pgrow_installer"})
	fixture.SchemaReferenceCleanup(t, "pgrow_addon")
	fixture.SchemaReferenceCleanup(t, "pgrow_install_denied")
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_t(id INT PRIMARY KEY,v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(1,0)", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		pgrowInstall(t, p, a)
		pgrowEmpty(t, p, a)
		q := "SELECT * FROM pgrowlocks(NULL::text)"
		missing := fixture.Diagnostic{Code: "42883", Message: "function pgrowlocks(text) does not exist", Hint: pgrowMissingHint, Position: pgrowPosition(q, "pgrowlocks")}
		pgrowError(t, p, observer, q, "prepare", missing)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		pgrowError(t, p, observer, q, "prepare", missing)
		fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA pgrow_addon", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "SAVEPOINT install_mark", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE EXTENSION pgrowlocks SCHEMA pgrow_addon", 0)
		if got := pgrowQuery(t, p, a, "SELECT * FROM pgrow_addon.pgrowlocks(NULL::text)"); len(got) != 0 {
			t.Fatal(got)
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK TO install_mark", 0)
		fixture.LockExec(t, p.Ctx, a, "RELEASE install_mark", 0)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		identityBoth(t, p, b, observer, fixture.Select("SELECT extname FROM pg_extension WHERE extname='pgrowlocks'", []string{"extname"}, []string{"NAME"}, nil))
		fixture.LockExec(t, p.Ctx, a, "CREATE ROLE pgrow_installer", 0)
		// Isolate the denied installer's explicit namespace ACL from the
		// clean provider/view namespace used by the later controls.
		fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA pgrow_install_denied", 0)
		fixture.LockExec(t, p.Ctx, a, "GRANT USAGE,CREATE ON SCHEMA pgrow_install_denied TO pgrow_installer", 0)
		fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_installer", 0)
		_, err := observer.ExecContext(p.Ctx, "CREATE EXTENSION pgrowlocks SCHEMA pgrow_install_denied")
		want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create extension "pgrowlocks"`)
		want.Hint = "Must be superuser to create this extension."
		fixture.AssertDiagnostic(t, err, &want)
		fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
		fixture.LockExec(t, p.Ctx, a, "DROP SCHEMA pgrow_install_denied", 0)
		identityBoth(t, p, b, observer, fixture.Select("SELECT extname FROM pg_extension WHERE extname='pgrowlocks'", []string{"extname"}, []string{"NAME"}, nil))
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE EXTENSION pgrowlocks SCHEMA pgrow_addon", 0)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		fixture.LockExec(t, p.Ctx, observer, "SET search_path TO pgrow_addon,public", 0)
		pgrowEmpty(t, p, observer)
		original := pgrowFunctionID(t, p, observer, "pgrowlocks(text)")
		fixture.LockExec(t, p.Ctx, observer, "CREATE VIEW public.pgrow_saved AS SELECT * FROM pgrowlocks('public.pgrow_t')", 0)
		fixture.LockExec(t, p.Ctx, observer, "SET search_path TO public", 0)
		if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrow_saved"); len(got) != 0 {
			t.Fatal(got)
		}
		_, err = a.ExecContext(p.Ctx, "DROP EXTENSION pgrowlocks")
		restrict := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension pgrowlocks because other objects depend on it")
		restrict.Detail = "view pgrow_saved depends on function pgrow_addon.pgrowlocks(text)"
		restrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.AssertDiagnostic(t, err, &restrict)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "DROP EXTENSION pgrowlocks CASCADE", 0)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrow_saved"); len(got) != 0 {
			t.Fatal(got)
		}
		if id := pgrowFunctionID(t, p, observer, "pgrow_addon.pgrowlocks(text)"); id != original {
			t.Fatal("rollback changed provider identity", original, id)
		}
		fixture.LockExec(t, p.Ctx, a, "DROP EXTENSION pgrowlocks CASCADE", 0)
		identityBoth(t, p, b, observer, fixture.Select("SELECT relname FROM pg_class WHERE relname='pgrow_saved'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.LockExec(t, p.Ctx, a, "CREATE EXTENSION pgrowlocks SCHEMA pgrow_addon", 0)
		if id := pgrowFunctionID(t, p, observer, "pgrow_addon.pgrowlocks(text)"); id == original {
			t.Fatal("reinstall revived retired routine", id)
		}
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW pgrow_saved AS SELECT * FROM pgrow_addon.pgrowlocks('public.pgrow_t')", 0)
		if !b.PG {
			pgrowNativeControls(t, p, b, a, observer)
		}
		// The shared SQL lifecycle ends callable after actual owner/view changes.
		if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrow_saved"); len(got) != 0 {
			t.Fatal(got)
		}
	})
}

func pgrowNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	owner := pgrowOwner(t, p, a, "For Update")
	identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
	q := "SELECT * FROM pgrow_addon.pgrowlocks('public.pgrow_t')"
	before := pgrowOne(t, pgrowQuery(t, p, observer, q), false, owner)
	oldRows, err := observer.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer oldRows.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
		t.Fatal("malformed Restore succeeded")
	}
	pgrowSameTID(t, before, pgrowOne(t, pgrowQuery(t, p, observer, q), false, owner))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	pgrowSameTID(t, before, pgrowOne(t, pgrowRead(t, oldRows), false, owner))
	if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrow_saved"); len(got) != 0 {
		t.Fatal("Restore resurrected live row locks", got)
	}
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	newOwner := pgrowOwner(t, p, a, "For Share")
	identityLock(t, p, a, "pgrow_t", 1, "SHARE")
	pgrowOne(t, pgrowQuery(t, p, observer, "SELECT * FROM pgrow_saved"), false, newOwner)
	fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
}

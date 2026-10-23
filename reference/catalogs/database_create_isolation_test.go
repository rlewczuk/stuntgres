package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreateIsolation(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		original := c.scalar("SELECT current_database()")
		c.create("dc_isolation_a", "", false)
		c.create("dc_isolation_b", "", false)
		for i, name := range []string{"dc_isolation_a", "dc_isolation_b"} {
			db, _, rec := c.pool(name, "")
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("CREATE TABLE dc_local(v integer)", 0))
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("INSERT INTO dc_local VALUES ($1)", 1, int64(i+1)))
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		a, _, rec := c.pool("dc_isolation_a", "")
		fixture.RunStep(t, c.p.Ctx, a, rec, fixture.Prepared(fixture.Select("SELECT v FROM dc_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})))
		fixture.RunStep(t, c.p.Ctx, a, rec, fixture.Select("SELECT v FROM public.dc_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		cross := "SELECT v FROM dc_isolation_b.public.dc_local"
		d := databaseCreateDiagnostic("0A000", "cross-database references are not implemented: \"dc_isolation_b.public.dc_local\"")
		d.Position = "15"
		if !c.backend.PG {
			d.Message = "three-part relation names are unsupported"
			d.Position = databaseCreatePosition(cross, "dc_local", false)
		}
		fixture.RunStep(t, c.p.Ctx, a, rec, fixture.ErrorDiagnostic(cross, d))
		c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='dc_local'", []string{"relname"}, []string{"NAME"}, nil))
		if c.scalar("SELECT current_database()") != original {
			t.Fatal("default attachment retargeted")
		}
		missing, _, _ := c.pool("dc_no_such_database", "")
		want := databaseCreateDiagnostic("3D000", "database \"dc_no_such_database\" does not exist")
		want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
		fixture.AssertDiagnostic(t, missing.PingContext(c.p.Ctx), &want)
		c.absent("dc_no_such_database")
		if c.backend.PG {
			return
		}
		// PostgreSQL libpq maps an empty dbname to its user default. The native
		// decoded-name selector explicitly distinguishes empty from default.
		empty, _, _ := c.pool("", "")
		want.Message = "database \"\" does not exist"
		freshDefault := sql.OpenDB(c.p.NativeConnector)
		defer freshDefault.Close()
		var freshName string
		if err := freshDefault.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&freshName); err != nil || freshName != original {
			t.Fatalf("connector copy retargeted default to %q: %v", freshName, err)
		}
		fixture.AssertDiagnostic(t, empty.PingContext(c.p.Ctx), &want)
		driverOwner := c.p.NativeConnector.Driver().(*stuntgres.Driver)
		for _, key := range []string{"", " opaque database key ", "postgres://user@host/db?x=1", strings.Repeat("k", 63) + "a", strings.Repeat("k", 63) + "b"} {
			base, err := driverOwner.OpenConnector(key)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(base)
			defer db.Close()
			if _, err := db.ExecContext(c.p.Ctx, "CREATE DATABASE dc_isolation_a"); err != nil {
				t.Fatal(err)
			}
			selected, err := driverOwner.OpenDatabaseConnector(key, "dc_isolation_a")
			if err != nil {
				t.Fatal(err)
			}
			isolated := sql.OpenDB(selected)
			defer isolated.Close()
			var count int
			if err := isolated.QueryRowContext(c.p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='dc_local'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("opaque key %q leaked: %d %v", key, count, err)
			}
			var display string
			if err := db.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&display); err != nil {
				t.Fatal(err)
			}
			wantDisplay := key
			if len(wantDisplay) > 63 {
				wantDisplay = wantDisplay[:63]
			}
			if display != wantDisplay {
				t.Fatalf("opaque key %q became %q", key, display)
			}
		}
		other := stuntgres.NewDriver()
		base, err := other.OpenConnector(c.p.NativeDSN)
		if err != nil {
			t.Fatal(err)
		}
		otherDB := sql.OpenDB(base)
		defer otherDB.Close()
		var count int
		if err := otherDB.QueryRowContext(c.p.Ctx, "SELECT count(*) FROM pg_database WHERE datname='dc_isolation_a'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("Driver isolation: %d %v", count, err)
		}
	})
}

func TestReferenceDatabaseCreateSharedAuthority(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE ROLE dc_owner LOGIN PASSWORD 'role-core-fixture'", 0))
		c.run(fixture.Exec("CREATE ROLE dc_reader LOGIN PASSWORD 'role-core-fixture' CONNECTION LIMIT 1", 0))
		c.create("dc_authority", "OWNER dc_owner", false)
		db, _, rec := c.pool("dc_authority", "")
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, conn, rec, s) }
		q := "SELECT rolcreatedb FROM pg_roles WHERE rolname='dc_reader'"
		stmt, err := conn.PrepareContext(c.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		fixture.RunStep(t, c.p.Ctx, held, rec, fixture.Select(q, []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{false}}))
		c.run(fixture.Exec("ALTER ROLE dc_reader CREATEDB", 0))
		fixture.RunStep(t, c.p.Ctx, held, rec, fixture.Select(q, []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{true}}))
		c.run(fixture.Exec("ALTER ROLE dc_reader NOCREATEDB", 0))
		fixture.RunStep(t, c.p.Ctx, held, rec, fixture.Select(q, []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{false}}))

		// Database ACL changes support transactional publication. Exercise both
		// noncurrent-target and selected-target writers without requiring
		// transactional ALTER ROLE.
		aclQuery := "SELECT datacl FROM pg_database WHERE datname='dc_authority'"
		aclStmt, err := conn.PrepareContext(c.p.Ctx, aclQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer aclStmt.Close()
		aclHeld := fixture.HeldStatement{Executor: conn, Statement: aclStmt, SQL: aclQuery}
		checkACL := func(original, selected any) {
			t.Helper()
			c.run(fixture.Select(aclQuery, []string{"datacl"}, []string{"_ACLITEM"}, [][]any{{original}}))
			fixture.RunStep(t, c.p.Ctx, aclHeld, rec, fixture.Select(aclQuery, []string{"datacl"}, []string{"_ACLITEM"}, [][]any{{selected}}))
		}
		checkACL(nil, nil)
		grant := "GRANT CREATE ON DATABASE dc_authority TO dc_reader"
		revoke := "REVOKE CREATE ON DATABASE dc_authority FROM dc_reader"
		baseACL := "{=Tc/dc_owner,dc_owner=CTc/dc_owner}"
		grantedACL := "{=Tc/dc_owner,dc_owner=CTc/dc_owner,dc_reader=C/dc_owner}"
		c.run(fixture.Exec(grant, 0))
		checkACL(grantedACL, grantedACL)
		c.run(fixture.Exec(revoke, 0))
		checkACL(baseACL, baseACL)
		for _, writer := range []struct {
			name string
			run  func(fixture.Step)
		}{{"original", c.run}, {"selected", run}} {
			checkPrivate := func() {
				t.Helper()
				if writer.name == "original" {
					checkACL(grantedACL, baseACL)
				} else {
					checkACL(baseACL, grantedACL)
				}
			}
			writer.run(fixture.Exec("BEGIN", 0))
			writer.run(fixture.Exec(grant, 0))
			checkPrivate()
			writer.run(fixture.Exec("SAVEPOINT dc_sp", 0))
			writer.run(fixture.Exec(revoke, 0))
			checkACL(baseACL, baseACL)
			writer.run(fixture.Exec("ROLLBACK TO dc_sp", 0))
			checkPrivate()
			writer.run(fixture.Exec("ROLLBACK", 0))
			checkACL(baseACL, baseACL)
			writer.run(fixture.Exec("BEGIN", 0))
			writer.run(fixture.Exec(grant, 0))
			checkPrivate()
			writer.run(fixture.Exec("COMMIT", 0))
			checkACL(grantedACL, grantedACL)
			writer.run(fixture.Exec(revoke, 0))
			checkACL(baseACL, baseACL)
		}
		c.run(fixture.Exec("REVOKE CONNECT ON DATABASE dc_authority FROM PUBLIC", 0))
		reader, _, readerRec := c.pool("dc_authority", "dc_reader")
		denied := databaseCreateDiagnostic("42501", "permission denied for database \"dc_authority\"")
		if c.backend.PG {
			denied.Severity, denied.SeverityUnlocalized = "FATAL", "FATAL"
		}
		denied.Detail = "User does not have CONNECT privilege."
		fixture.AssertDiagnostic(t, reader.PingContext(c.p.Ctx), &denied)
		c.run(fixture.Exec("GRANT CONNECT ON DATABASE dc_authority TO dc_reader", 0))
		if err := reader.PingContext(c.p.Ctx); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, c.p.Ctx, reader, readerRec, fixture.Select("SELECT current_user,current_database()", []string{"current_user", "current_database"}, []string{"NAME", "NAME"}, [][]any{{"dc_reader", "dc_authority"}}))
		c.run(fixture.Exec("ALTER DATABASE dc_authority OWNER TO dc_reader", 0))
		ownerQuery := "SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='dc_authority'"
		want := fixture.Select(ownerQuery, []string{"rolname"}, []string{"NAME"}, [][]any{{"dc_reader"}})
		c.run(want)
		run(want)
		c.run(fixture.Exec("REVOKE CONNECT ON DATABASE dc_authority FROM dc_reader", 0))
		// A database owned in another selected target still blocks DROP ROLE.
		drop := databaseCreateDiagnostic("2BP01", "role \"dc_reader\" cannot be dropped because some objects depend on it")
		drop.Detail = "owner of database dc_authority"
		c.run(fixture.ErrorDiagnostic("DROP ROLE dc_reader", drop))
		// Role connection limits are cluster-wide, not one copy per database.
		other, _, _ := c.pool(c.scalar("SELECT current_database()"), "dc_reader")
		limit := databaseCreateDiagnostic("53300", "too many connections for role \"dc_reader\"")
		limit.Severity, limit.SeverityUnlocalized = "FATAL", "FATAL"
		fixture.AssertDiagnostic(t, other.PingContext(c.p.Ctx), &limit)
	})
}

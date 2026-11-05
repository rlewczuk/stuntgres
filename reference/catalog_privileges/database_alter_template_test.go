package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterTemplate(t *testing.T) {
	for _, held := range []bool{false, true} {
		label := "direct"
		if held {
			label = "held"
		}
		t.Run(label, func(t *testing.T) {
			databaseAlterScenario(t, func(c *databaseCreateCase) {
				c.create("da_source", "OWNER da_owner", false)
				databaseAlterSeed(c, "da_source")
				c.remember("da_copy")
				c.remember("da_denied")
				c.remember("da_owner_copy")
				c.remember("da_disabled_copy")
				c.run(fixture.Exec("SET ROLE da_actor", 0))
				c.error("CREATE DATABASE da_denied TEMPLATE da_source", "42501", "permission denied to copy database \"da_source\"")
				c.absent("da_denied")
				c.run(fixture.Exec("RESET ROLE", 0))
				c.run(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE true CONNECTION LIMIT 4", 0))
				c.run(fixture.Exec("REVOKE CONNECT ON DATABASE da_source FROM PUBLIC", 0))
				c.run(fixture.Exec("ALTER ROLE da_actor IN DATABASE da_source SET application_name TO 'da_source_only'", 0))
				c.run(fixture.Exec("SET ROLE da_actor", 0))
				q := "CREATE DATABASE da_copy TEMPLATE da_source"
				if held {
					databaseAlterHeld(c, c.conn, q, func(h fixture.HeldStatement) {
						c.absent("da_copy")
						databaseAlterRun(c, h, c.backend.Recorder, fixture.Exec(q, 0))
						databaseAlterRun(c, h, c.backend.Recorder, fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("42P04", "database \"da_copy\" already exists")))
					})
				} else {
					c.run(fixture.Exec(q, 0))
				}
				c.run(fixture.Exec("RESET ROLE", 0))
				databaseAlterFlags(c, "da_copy", false, true, -1)
				c.run(fixture.Select("SELECT r.rolname,datacl IS NULL AS default_acl FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE datname='da_copy'", []string{"rolname", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{"da_actor", true}}))
				var copyOID int64
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname='da_copy'").Scan(&copyOID); err != nil {
					t.Fatal(err)
				}
				databaseAlterData(c, "da_copy", copyOID, 42)
				copyDB, copyRaw, copyRec := c.pool("da_copy", "")
				databaseAlterRun(c, copyDB, copyRec, fixture.Exec("UPDATE da_value SET v=99", 1))
				databaseAlterRun(c, copyDB, copyRec, fixture.Select("SELECT nextval('da_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(43)}}))
				databaseAlterClose(c, copyDB, copyRaw)
				fresh, freshRaw, freshRec := c.pool("da_copy", "da_actor")
				databaseAlterRun(c, fresh, freshRec, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
				databaseAlterClose(c, fresh, freshRaw)
				source, sourceRaw, sourceRec := c.pool("da_source", "")
				databaseAlterRun(c, source, sourceRec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				databaseAlterRun(c, source, sourceRec, fixture.Select("SELECT nextval('da_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(42)}}))
				databaseAlterClose(c, source, sourceRaw)
				c.run(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE false", 0))
				c.run(fixture.Exec("SET ROLE da_actor", 0))
				c.error("CREATE DATABASE da_denied TEMPLATE da_source", "42501", "permission denied to copy database \"da_source\"")
				c.run(fixture.Exec("RESET ROLE", 0))
				c.run(fixture.Exec("SET ROLE da_owner", 0))
				c.create("da_owner_copy", "TEMPLATE da_source", true)
				c.run(fixture.Exec("RESET ROLE", 0))
				c.run(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE true ALLOW_CONNECTIONS false", 0))
				c.run(fixture.Exec("SET ROLE da_actor", 0))
				c.create("da_disabled_copy", "TEMPLATE da_source", false)
				c.run(fixture.Exec("RESET ROLE", 0))
				c.create("da_default_seed", "", false)
				empty, emptyRaw, emptyRec := c.pool("da_default_seed", "")
				databaseAlterRun(c, empty, emptyRec, fixture.Select("SELECT relname FROM pg_class WHERE relname='da_value'", []string{"relname"}, []string{"NAME"}, nil))
				databaseAlterClose(c, empty, emptyRaw)
			})
		})
	}
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		databaseAlterSeed(c, "da_source")
		source, raw, rec := c.pool("da_source", "")
		defer databaseAlterClose(c, source, raw)
		conn, err := source.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer conn.Close()
		databaseAlterRun(c, conn, rec, fixture.Exec("CREATE TEMP TABLE da_temporary(v integer)", 0))
		databaseAlterRun(c, conn, rec, fixture.Exec("INSERT INTO da_temporary VALUES (7)", 1))
		databaseAlterRun(c, conn, rec, fixture.Select("SELECT v FROM da_temporary", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		c.remember("da_current_copy")
		databaseAlterRun(c, conn, rec, fixture.Prepared(fixture.Exec("CREATE DATABASE da_current_copy TEMPLATE da_source", 0)))
		copyDB, copyRaw, copyRec := c.pool("da_current_copy", "")
		defer databaseAlterClose(c, copyDB, copyRaw)
		databaseAlterRun(c, copyDB, copyRec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		// PostgreSQL copies pg_class itself, so physical TEMP descriptors can remain
		// in a clone. It does not transfer the source session's temporary namespace
		// or usable TEMP relation storage into the destination session.
		copyConn, err := copyDB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer copyConn.Close()
		missing := databaseCreateDiagnostic("42P01", "relation \"da_temporary\" does not exist")
		missing.Position = databaseCreatePosition("SELECT v FROM da_temporary", "da_temporary", false)
		failed := fixture.ErrorDiagnostic("SELECT v FROM da_temporary", missing)
		failed.SelectRows = true
		databaseAlterRun(c, copyConn, copyRec, failed)
		databaseAlterRun(c, copyConn, copyRec, fixture.Exec("CREATE TEMP TABLE da_temporary(v integer)", 0))
		databaseAlterRun(c, copyConn, copyRec, fixture.Exec("INSERT INTO da_temporary VALUES (99)", 1))
		databaseAlterRun(c, copyConn, copyRec, fixture.Select("SELECT v FROM da_temporary", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		databaseAlterRun(c, conn, rec, fixture.Select("SELECT v FROM da_temporary", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

func TestReferenceDatabaseAlterTemplateBusy(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		databaseAlterSeed(c, "da_source")
		c.run(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE true", 0))
		c.remember("da_copy")
		q := "CREATE DATABASE da_copy TEMPLATE da_source"
		first, firstRaw, firstRec := c.pool("da_source", "")
		conn, err := first.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer conn.Close()
		second, secondRaw, _ := c.pool("da_source", "")
		fixture.State(c.t, second.PingContext(c.p.Ctx), "")
		databaseAlterHeld(c, c.conn, q, func(h fixture.HeldStatement) {
			databaseAlterBusy(c, q, "da_source", 2, 0)
			c.absent("da_copy")
			databaseAlterClose(c, second, secondRaw)
			databaseAlterBusy(c, q, "da_source", 1, 0)
			gid := "da_source_prepared"
			cleanupDB, _, cleanupRec := c.pool("da_source", "")
			// PREPARED completion is database-local. Keep this pool unconnected while
			// proving busy counts, and release its physical session after each command.
			cleanupDB.SetMaxIdleConns(0)
			preparedBackend := c.backend
			preparedBackend.DB = cleanupDB
			fixture.PreparedReferenceCleanup(c.t, preparedBackend, gid)
			databaseAlterRun(c, conn, firstRec, fixture.Exec("BEGIN", 0))
			databaseAlterRun(c, conn, firstRec, fixture.Exec("INSERT INTO da_value VALUES (7)", 1))
			databaseAlterRun(c, conn, firstRec, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
			databaseAlterBusy(c, q, "da_source", 1, 1)
			if err := conn.Close(); err != nil {
				c.t.Fatal(err)
			}
			databaseAlterClose(c, first, firstRaw)
			databaseAlterBusy(c, q, "da_source", 0, 1)
			c.absent("da_copy")
			databaseAlterRun(c, cleanupDB, cleanupRec, fixture.Exec("ROLLBACK PREPARED '"+gid+"'", 0))
			databaseAlterRun(c, h, c.backend.Recorder, fixture.Exec(q, 0))
		})
		copyDB, copyRaw, copyRec := c.pool("da_copy", "")
		defer databaseAlterClose(c, copyDB, copyRaw)
		databaseAlterRun(c, copyDB, copyRec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}

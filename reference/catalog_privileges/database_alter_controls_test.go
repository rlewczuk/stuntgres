package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterSelectors(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		oid := databaseAlterSeed(c, "da_source")
		old, oldRaw, _ := c.pool("da_source", "")
		// Construct immutable selectors before rename, but do not hold a live target session.
		c.remember("da_selected")
		databaseAlterRename(c, "da_source", "da_selected", false)
		want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: "database \"da_source\" does not exist"}
		fixture.AssertDiagnostic(c.t, old.PingContext(c.p.Ctx), &want)
		databaseAlterClose(c, old, oldRaw)
		databaseAlterData(c, "da_selected", oid, 42)
		databaseAlterStartup(c, "da_missing", "", "3D000", "database \"da_missing\" does not exist", "")
		if !c.backend.PG {
			databaseAlterStartup(c, "", "", "3D000", "database \"\" does not exist", "")
			base := c.p.NativeConnector
			copied := base.WithDatabase("da_source")
			selected := copied.WithDatabase("da_selected")
			stale := sql.OpenDB(copied)
			defer stale.Close()
			fixture.AssertDiagnostic(c.t, stale.PingContext(c.p.Ctx), &want)
			live := sql.OpenDB(selected)
			defer live.Close()
			var got int64
			if err := live.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&got); err != nil || got != oid {
				c.t.Fatalf("copied selector identity: %d %v", got, err)
			}
			for _, key := range []string{"", " ", "postgres://opaque/database?name=da_selected", strings.Repeat("k", 63) + "one", strings.Repeat("k", 63) + "two"} {
				driver := base.Driver().(*stuntgres.Driver)
				connector, err := driver.OpenDatabaseConnector(key, "da_selected")
				if err != nil {
					c.t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				absent := want
				absent.Message = "database \"da_selected\" does not exist"
				fixture.AssertDiagnostic(c.t, db.PingContext(c.p.Ctx), &absent)
				attachment, err := driver.OpenConnector(key)
				if err != nil {
					c.t.Fatal(err)
				}
				bootstrap, bootstrapRec := fixture.RecordedNativePool(c.t, attachment)
				databaseAlterRun(c, bootstrap, bootstrapRec, fixture.Exec("CREATE DATABASE da_selected", 0))
				fixture.State(c.t, db.PingContext(c.p.Ctx), "")
				empty := sql.OpenDB(attachment.(*stuntgres.Connector).WithDatabase(""))
				emptyDiagnostic := want
				emptyDiagnostic.Message = `database "" does not exist`
				fixture.AssertDiagnostic(c.t, empty.PingContext(c.p.Ctx), &emptyDiagnostic)
				databaseAlterClose(c, empty)
				databaseAlterClose(c, bootstrap)
				databaseAlterClose(c, db)
			}
			separate := stuntgres.NewDriver()
			connector, err := separate.OpenDatabaseConnector(c.p.NativeDSN, "da_selected")
			if err != nil {
				c.t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			absent := want
			absent.Message = "database \"da_selected\" does not exist"
			fixture.AssertDiagnostic(c.t, db.PingContext(c.p.Ctx), &absent)
			databaseAlterDefaultSelector(c)
		}
	})
}

func databaseAlterDefaultSelector(c *databaseCreateCase) {
	c.t.Helper()
	d := stuntgres.NewDriver()
	base, err := d.OpenConnector("alter default://opaque?x=1")
	if err != nil {
		c.t.Fatal(err)
	}
	original := sql.OpenDB(base)
	defer original.Close()
	for _, q := range []string{"CREATE TABLE da_attachment_value(v integer)", "INSERT INTO da_attachment_value VALUES (42)", "CREATE DATABASE da_admin"} {
		if _, err := original.ExecContext(c.p.Ctx, q); err != nil {
			c.t.Fatal(err)
		}
	}
	var oldName string
	var oid int64
	if err := original.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&oldName); err != nil {
		c.t.Fatal(err)
	}
	if err := original.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil {
		c.t.Fatal(err)
	}
	databaseAlterClose(c, original)
	adminConnector := base.(*stuntgres.Connector).WithDatabase("da_admin")
	admin, adminRec := fixture.RecordedNativePool(c.t, adminConnector)
	defer admin.Close()
	databaseAlterRun(c, admin, adminRec, fixture.Exec("ALTER DATABASE "+pq.QuoteIdentifier(oldName)+" RENAME TO da_attachment_renamed", 0))
	following, followingRec := fixture.RecordedNativePool(c.t, base)
	defer following.Close()
	databaseAlterRun(c, following, followingRec, fixture.Select("SELECT current_database() AS name", []string{"name"}, []string{"NAME"}, [][]any{{"da_attachment_renamed"}}))
	databaseAlterRun(c, following, followingRec, fixture.Select("SELECT v FROM da_attachment_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	var got int64
	if err := following.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&got); err != nil || got != oid {
		c.t.Fatalf("default identity %d -> %d: %v", oid, got, err)
	}
	stale := sql.OpenDB(base.(*stuntgres.Connector).WithDatabase(oldName))
	defer stale.Close()
	want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: "database \"" + oldName + "\" does not exist"}
	fixture.AssertDiagnostic(c.t, stale.PingContext(c.p.Ctx), &want)
}

func TestReferenceDatabaseAlterControls(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		defer databaseAlterBuiltinACLCleanup(c)()
		c.create("da_source", "OWNER da_owner", false)
		oid := databaseAlterSeed(c, "da_source")
		databaseAlterRename(c, "da_source", "da_control", false)
		c.create("da_other", "", false)
		other, otherRaw, otherRec := c.pool("da_other", "")
		defer databaseAlterClose(c, other, otherRaw)
		databaseAlterRun(c, other, otherRec, fixture.Exec("CREATE TABLE da_sentinel(v integer)", 0))
		databaseAlterRun(c, other, otherRec, fixture.Exec("INSERT INTO da_sentinel VALUES (99)", 1))
		c.run(fixture.Exec("CREATE TABLE da_sentinel(v integer)", 0))
		c.run(fixture.Exec("INSERT INTO da_sentinel VALUES (7)", 1))
		c.run(fixture.Exec("ALTER DATABASE da_control OWNER TO da_next", 0))
		c.run(fixture.Exec("GRANT CONNECT ON DATABASE da_control TO da_plain", 0))
		db, raw, rec := c.pool("da_control", "")
		defer databaseAlterClose(c, db, raw)
		fixture.State(c.t, raw.PingContext(c.p.Ctx), "")
		fixture.State(c.t, db.PingContext(c.p.Ctx), "")
		c.run(fixture.Exec("ALTER DATABASE da_control IS_TEMPLATE true CONNECTION LIMIT 3 ALLOW_CONNECTIONS false", 0))
		metadata := "SELECT d.datname,r.rolname,d.datistemplate,d.datallowconn,d.datconnlimit,d.datcollversion,d.datacl::text FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='da_control'"
		acl := c.scalar("SELECT datacl::text FROM pg_database WHERE datname='da_control'")
		want := fixture.Select(metadata, []string{"datname", "rolname", "datistemplate", "datallowconn", "datconnlimit", "datcollversion", "datacl"}, []string{"NAME", "NAME", "BOOL", "BOOL", "INT4", "TEXT", "TEXT"}, [][]any{{"da_control", "da_next", true, false, int64(3), nil, acl}})
		c.run(want)
		privilegeQuery := "SELECT has_tablespace_privilege('da_plain','pg_default','CREATE') AS plain,has_tablespace_privilege('da_owner','pg_default','CREATE') AS owner"
		privilegeStep := func(plain, owner bool) fixture.Step {
			return fixture.Select(privilegeQuery, []string{"plain", "owner"}, []string{"BOOL", "BOOL"}, [][]any{{plain, owner}})
		}
		c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO da_plain", 0))
		for _, target := range []struct {
			db  *sql.DB
			rec *fixture.Recorder
		}{{db, rec}, {other, otherRec}} {
			databaseAlterRun(c, target.db, target.rec, privilegeStep(true, false))
		}
		var dump bytes.Buffer
		if !c.backend.PG {
			if err := stuntgres.Dump(c.p.Ctx, raw, &dump); err != nil {
				c.t.Fatal(err)
			}
			for _, forbidden := range []string{"CREATE DATABASE", "ALTER DATABASE", "CREATE ROLE"} {
				if strings.Contains(dump.String(), forbidden) {
					c.t.Fatal("dump includes cluster authority", forbidden)
				}
			}
		}
		// Current shared authority wins over the older dump's ACL state.
		c.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM da_plain", 0))
		c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO da_owner", 0))
		c.run(privilegeStep(false, true))
		databaseAlterRun(c, db, rec, privilegeStep(false, true))
		databaseAlterRun(c, other, otherRec, privilegeStep(false, true))
		if !c.backend.PG {
			if err := stuntgres.Reset(c.p.Ctx, raw); err != nil {
				c.t.Fatal(err)
			}
			c.run(want)
			databaseAlterRun(c, db, rec, privilegeStep(false, true))
			databaseAlterRun(c, other, otherRec, privilegeStep(false, true))
			databaseAlterRun(c, db, rec, fixture.Select("SELECT relname FROM pg_class WHERE relname='da_value'", []string{"relname"}, []string{"NAME"}, nil))
			if err := stuntgres.Restore(c.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				c.t.Fatal(err)
			}
			c.run(want)
			databaseAlterRun(c, db, rec, privilegeStep(false, true))
			databaseAlterRun(c, other, otherRec, privilegeStep(false, true))
			c.run(privilegeStep(false, true))
		}
		databaseAlterRun(c, db, rec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		var got int64
		if err := db.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&got); err != nil || got != oid {
			c.t.Fatalf("control changed identity %d -> %d: %v", oid, got, err)
		}
		databaseAlterRun(c, other, otherRec, fixture.Select("SELECT v FROM da_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		c.run(fixture.Select("SELECT v FROM da_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if !c.backend.PG {
			databaseAlterExplicitRepairs(c, db, raw, rec, other, otherRec, want)
		}
		c.run(fixture.Exec("ALTER DATABASE da_control ALLOW_CONNECTIONS true", 0))
	})
}

func TestReferenceDatabaseAlterFeatureSmoke(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		oid := databaseAlterSeed(c, "da_source")
		databaseAlterRename(c, "da_source", "da_smoke", true)
		databaseAlterStartup(c, "da_source", "", "3D000", "database \"da_source\" does not exist", "")
		databaseAlterData(c, "da_smoke", oid, 42)
		c.run(fixture.Exec("ALTER DATABASE da_smoke ALLOW_CONNECTIONS false", 0))
		for _, role := range []string{"da_plain", ""} {
			databaseAlterStartup(c, "da_smoke", role, "55000", "database \"da_smoke\" is not currently accepting connections", "")
		}
		c.run(fixture.Exec("ALTER DATABASE da_smoke ALLOW_CONNECTIONS true CONNECTION LIMIT 0", 0))
		databaseAlterStartup(c, "da_smoke", "da_plain", "53300", "too many connections for database \"da_smoke\"", "")
		databaseAlterStartup(c, "da_smoke", "", "", "", "")
		c.run(fixture.Exec("ALTER DATABASE da_smoke CONNECTION LIMIT -1", 0))
		c.run(fixture.Exec("ALTER DATABASE da_smoke OWNER TO da_next", 0))
		successor, successorRaw, successorRec := c.pool("da_smoke", "")
		successorConn, err := successor.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		for _, q := range []string{"SET ROLE da_next", "CREATE TABLE da_successor(v integer)", "RESET ROLE"} {
			databaseAlterRun(c, successorConn, successorRec, fixture.Exec(q, 0))
		}
		if err := successorConn.Close(); err != nil {
			c.t.Fatal(err)
		}
		databaseAlterClose(c, successor, successorRaw)
		c.run(fixture.Exec("SET ROLE da_owner", 0))
		c.error("ALTER DATABASE da_smoke IS_TEMPLATE true", "42501", "must be owner of database da_smoke")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("SET ROLE da_actor", 0))
		c.remember("da_copy")
		c.error("CREATE DATABASE da_copy TEMPLATE da_smoke", "42501", "permission denied to copy database \"da_smoke\"")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("SET ROLE da_next", 0))
		c.run(fixture.Exec("ALTER DATABASE da_smoke IS_TEMPLATE true", 0))
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("SET ROLE da_actor", 0))
		c.create("da_copy", "TEMPLATE da_smoke", true)
		c.run(fixture.Exec("RESET ROLE", 0))
		var copyOID int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_database WHERE datname='da_copy'").Scan(&copyOID); err != nil {
			c.t.Fatal(err)
		}
		databaseAlterData(c, "da_copy", copyOID, 43)
		copyDB, copyRaw, copyRec := c.pool("da_copy", "")
		databaseAlterRun(c, copyDB, copyRec, fixture.Exec("UPDATE da_value SET v=99", 1))
		databaseAlterClose(c, copyDB, copyRaw)
		c.run(fixture.Exec("ALTER DATABASE da_smoke IS_TEMPLATE false", 0))
		c.remember("da_denied")
		c.run(fixture.Exec("SET ROLE da_actor", 0))
		c.error("CREATE DATABASE da_denied TEMPLATE da_smoke", "42501", "permission denied to copy database \"da_smoke\"")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.absent("da_denied")
		c.run(fixture.Exec("ALTER DATABASE da_smoke SET TABLESPACE pg_default", 0))
		records := []fixture.Diagnostic{}
		_, noticeConn, noticeRec := fixture.NoticeReferencePool(c.t, c.p, c.backend.PG, &records)
		databaseAlterRun(c, noticeConn, noticeRec, fixture.Exec("SET ROLE da_next", 0))
		records = nil
		databaseAlterRun(c, noticeConn, noticeRec, fixture.Exec("ALTER DATABASE da_smoke REFRESH COLLATION VERSION", 0))
		notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}
		if len(records) != 1 || records[0] != notice {
			c.t.Fatalf("smoke notice: %#v", records)
		}
		databaseAlterRun(c, noticeConn, noticeRec, fixture.Exec("RESET ROLE", 0))
		c.remember("da_uncommitted")
		c.run(fixture.Exec("BEGIN", 0))
		c.run(fixture.Exec("ALTER DATABASE da_smoke RENAME TO da_uncommitted", 0))
		c.run(fixture.Exec("ROLLBACK", 0))
		c.absent("da_uncommitted")
		databaseAlterData(c, "da_smoke", oid, 43)
	})
}

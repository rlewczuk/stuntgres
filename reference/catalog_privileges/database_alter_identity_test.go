package reference_test

import (
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterRename(t *testing.T) {
	for _, mode := range []string{"direct", "prepared", "held"} {
		t.Run(mode, func(t *testing.T) {
			databaseAlterScenario(t, func(c *databaseCreateCase) {
				c.create("da_source", "OWNER da_owner", false)
				oid := databaseAlterSeed(c, "da_source")
				c.run(fixture.Exec("GRANT CONNECT ON DATABASE da_source TO da_plain", 0))
				acl := c.scalar("SELECT datacl::text FROM pg_database WHERE datname='da_source'")
				c.remember("da_renamed")
				q := "ALTER DATABASE da_source RENAME TO da_renamed"
				if mode == "held" {
					databaseAlterHeld(c, c.conn, q, func(held fixture.HeldStatement) {
						c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname='da_source'", []string{"datname"}, []string{"NAME"}, [][]any{{"da_source"}}))
						c.absent("da_renamed")
						databaseAlterRun(c, held, c.backend.Recorder, fixture.Exec(q, 0))
						databaseAlterRun(c, held, c.backend.Recorder, fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("3D000", "database \"da_source\" does not exist")))
					})
				} else {
					s := fixture.Exec(q, 0)
					if mode == "prepared" {
						s = fixture.Prepared(s)
					}
					c.run(s)
				}
				c.absent("da_source")
				c.run(fixture.Select("SELECT datname,r.rolname,datacl::text FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE datname='da_renamed'", []string{"datname", "rolname", "datacl"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"da_renamed", "da_owner", acl}}))
				databaseAlterStartup(c, "da_source", "", "3D000", "database \"da_source\" does not exist", "")
				databaseAlterData(c, "da_renamed", oid, 42)
				c.error("ALTER DATABASE da_renamed RENAME TO da_renamed", "42P04", "database \"da_renamed\" already exists")
				c.create("da_occupied", "", false)
				c.error("ALTER DATABASE da_renamed RENAME TO da_occupied", "42P04", "database \"da_occupied\" already exists")
				c.run(fixture.Exec("ALTER DATABASE da_renamed IS_TEMPLATE true", 0))
				databaseAlterRename(c, "da_renamed", "da_template_renamed", true)
			})
		})
	}
	for _, name := range []string{"Mixed Case", "quote\"slash\\", "数据库", strings.Repeat("x", 63) + "ignored", strings.Repeat("界", 22), "line\nreturn\r"} {
		t.Run(name, func(t *testing.T) {
			databaseAlterScenario(t, func(c *databaseCreateCase) {
				c.create("da_source", "", false)
				actual := name
				if strings.HasPrefix(name, strings.Repeat("x", 63)) {
					actual = strings.Repeat("x", 63)
				}
				if name == strings.Repeat("界", 22) {
					actual = strings.Repeat("界", 21)
				}
				c.remember(actual)
				c.run(fixture.Exec("ALTER DATABASE da_source RENAME TO "+pq.QuoteIdentifier(name), 0))
				c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{actual}}, actual))
				databaseAlterStartup(c, actual, "", "", "", "")
			})
		})
	}
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		c.remember("da_next_name")
		db, raw, _ := c.pool("da_source", "")
		fixture.State(c.t, db.PingContext(c.p.Ctx), "")
		databaseAlterBusy(c, "ALTER DATABASE da_source RENAME TO da_next_name", "", 1, 0)
		databaseAlterClose(c, db, raw)
		current := c.scalar("SELECT current_database()")
		c.error("ALTER DATABASE "+pq.QuoteIdentifier(current)+" RENAME TO da_next_name", "0A000", "current database cannot be renamed")
		c.error("ALTER DATABASE "+pq.QuoteIdentifier(current)+" RENAME TO "+pq.QuoteIdentifier(current), "42P04", "database \""+current+"\" already exists")
	})
}

func TestReferenceDatabaseAlterTransactions(t *testing.T) {
	for _, finish := range []string{"ROLLBACK", "COMMIT", "COMMIT PREPARED", "ROLLBACK PREPARED"} {
		t.Run(finish, func(t *testing.T) {
			databaseAlterScenario(t, func(c *databaseCreateCase) {
				c.create("da_source", "OWNER da_owner", false)
				oid := databaseAlterSeed(c, "da_source")
				c.create("da_other", "", false)
				other, otherRaw, otherRec := c.pool("da_other", "")
				defer databaseAlterClose(c, other, otherRaw)
				databaseAlterRun(c, other, otherRec, fixture.Exec("CREATE TABLE da_unrelated(v integer)", 0))
				for _, name := range []string{"da_private", "da_final", "da_discarded"} {
					c.remember(name)
				}
				peer, err := c.backend.DB.Conn(c.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				q := "SELECT datname FROM pg_database WHERE datname IN ('da_source','da_final') ORDER BY datname"
				retained, err := c.backend.DB.QueryContext(c.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				databaseAlterHeld(c, peer, q, func(held fixture.HeldStatement) {
					committed := fixture.Select(q, []string{"datname"}, []string{"NAME"}, [][]any{{"da_source"}})
					databaseAlterRun(c, held, c.backend.Recorder, committed)
					c.run(fixture.Exec("BEGIN", 0))
					c.run(fixture.Exec("ALTER DATABASE da_source RENAME TO da_private", 0))
					c.run(fixture.Exec("ALTER DATABASE da_private IS_TEMPLATE true CONNECTION LIMIT 3", 0))
					c.run(fixture.Exec("ALTER DATABASE da_private OWNER TO da_next", 0))
					c.run(fixture.Exec("SAVEPOINT da_sp", 0))
					c.run(fixture.Exec("ALTER DATABASE da_private RENAME TO da_discarded", 0))
					c.run(fixture.Exec("ALTER DATABASE da_discarded CONNECTION LIMIT 9", 0))
					c.run(fixture.Exec("ROLLBACK TO SAVEPOINT da_sp", 0))
					databaseAlterFlags(c, "da_private", true, true, 3)
					c.absent("da_discarded")
					c.run(fixture.Exec("ALTER DATABASE da_private RENAME TO da_final", 0))
					databaseAlterRun(c, held, c.backend.Recorder, committed)
					databaseAlterRun(c, other, otherRec, fixture.Exec("INSERT INTO da_unrelated VALUES (99)", 1))
					if strings.Contains(finish, "PREPARED") {
						gid := "da_alter_identity"
						fixture.PreparedReferenceCleanup(t, c.backend, gid)
						c.run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
						databaseAlterRun(c, held, c.backend.Recorder, committed)
						c.run(fixture.Exec(finish+" '"+gid+"'", 0))
					} else {
						c.run(fixture.Exec(finish, 0))
					}
					name := "da_source"
					if strings.HasPrefix(finish, "COMMIT") {
						name = "da_final"
					}
					databaseAlterRun(c, held, c.backend.Recorder, fixture.Select(q, []string{"datname"}, []string{"NAME"}, [][]any{{name}}))
					databaseAlterData(c, name, oid, 42)
					if name == "da_final" {
						databaseAlterFlags(c, name, true, true, 3)
					} else {
						databaseAlterFlags(c, name, false, true, -1)
					}
					if !retained.Next() {
						t.Fatal("retained catalog row missing", retained.Err())
					}
					var oldName string
					if err := retained.Scan(&oldName); err != nil || oldName != "da_source" {
						t.Fatalf("retained name %q: %v", oldName, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("retained catalog changed", retained.Err())
					}
					databaseAlterRun(c, other, otherRec, fixture.Select("SELECT v FROM da_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
				})
			})
		})
	}
	for _, commit := range []bool{false, true} {
		label := "go-rollback"
		if commit {
			label = "go-commit"
		}
		t.Run(label, func(t *testing.T) {
			databaseAlterScenario(t, func(c *databaseCreateCase) {
				c.create("da_source", "OWNER da_owner", false)
				c.remember("da_go")
				tx, err := c.conn.BeginTx(c.p.Ctx, nil)
				if err != nil {
					c.t.Fatal(err)
				}
				defer tx.Rollback()
				for _, q := range []string{"ALTER DATABASE da_source RENAME TO da_go", "ALTER DATABASE da_go IS_TEMPLATE true CONNECTION LIMIT 2", "ALTER DATABASE da_go OWNER TO da_next"} {
					databaseAlterRun(c, tx, c.backend.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
				}
				databaseAlterRun(c, tx, c.backend.Recorder, fixture.Select("SELECT r.rolname,d.datistemplate,d.datconnlimit FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='da_go'", []string{"rolname", "datistemplate", "datconnlimit"}, []string{"NAME", "BOOL", "INT4"}, [][]any{{"da_next", true, int64(2)}}))
				if commit {
					if err := tx.Commit(); err != nil {
						c.t.Fatal(err)
					}
					c.absent("da_source")
					databaseAlterFlags(c, "da_go", true, true, 2)
				} else {
					if err := tx.Rollback(); err != nil {
						c.t.Fatal(err)
					}
					c.absent("da_go")
					databaseAlterFlags(c, "da_source", false, true, -1)
				}
			})
		})
	}
}

func TestReferenceDatabaseAlterOwner(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		c.create("da_other", "OWNER da_owner", false)
		c.run(fixture.Exec("GRANT CREATE ON DATABASE da_source TO da_plain", 0))
		c.run(fixture.Exec("GRANT da_next TO da_owner WITH INHERIT FALSE, SET TRUE", 0))
		c.run(fixture.Exec("SET ROLE da_owner", 0))
		c.run(fixture.Prepared(fixture.Exec("ALTER DATABASE da_source OWNER TO da_next", 0)))
		c.error("ALTER DATABASE da_source IS_TEMPLATE true", "42501", "must be owner of database da_source")
		c.run(fixture.Exec("RESET ROLE", 0))
		db, raw, rec := c.pool("da_source", "")
		defer databaseAlterClose(c, db, raw)
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{"SET ROLE da_next", "CREATE TABLE da_owned(v integer)", "CREATE SCHEMA da_owned_schema", "RESET ROLE"} {
			databaseAlterRun(c, conn, rec, fixture.Exec(q, 0))
		}
		databaseAlterRun(c, conn, rec, fixture.Exec("SET ROLE da_owner", 0))
		denied := databaseCreateDiagnostic("42501", "permission denied for schema public")
		denied.Position = databaseCreatePosition("CREATE TABLE da_denied(v integer)", "da_denied", false)
		databaseAlterRun(c, conn, rec, fixture.ErrorDiagnostic("CREATE TABLE da_denied(v integer)", denied))
		databaseAlterRun(c, conn, rec, fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Select("SELECT r.rolname,has_database_privilege('da_next',d.oid,'CREATE') AS successor,has_database_privilege('da_plain',d.oid,'CREATE') AS grantee FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='da_source'", []string{"rolname", "successor", "grantee"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"da_next", true, true}}))
		databaseAlterRun(c, conn, rec, fixture.Select("SELECT pg_has_role('da_next','pg_database_owner','USAGE') AS successor,pg_has_role('da_owner','pg_database_owner','USAGE') AS former", []string{"successor", "former"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		c.run(fixture.Select("SELECT pg_has_role('da_next','pg_database_owner','USAGE') AS unrelated", []string{"unrelated"}, []string{"BOOL"}, [][]any{{false}}))
		c.run(fixture.Select("SELECT count(*) AS old_owner_entries FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE d.datname='da_source' AND r.rolname='da_owner'", []string{"old_owner_entries"}, []string{"INT8"}, [][]any{{int64(0)}}))
		c.run(fixture.Select("SELECT r.rolname,d.datacl IS NULL AS default_acl FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='da_other'", []string{"rolname", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{"da_owner", true}}))
		c.run(fixture.Exec("ALTER DATABASE da_other OWNER TO da_next", 0))
		c.run(fixture.Select("SELECT datacl IS NULL AS default_acl FROM pg_database WHERE datname='da_other'", []string{"default_acl"}, []string{"BOOL"}, [][]any{{true}}))
		c.run(fixture.Exec("SET ROLE da_next", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source OWNER TO da_next", 0))
		c.run(fixture.Exec("RESET ROLE", 0))
	})
}

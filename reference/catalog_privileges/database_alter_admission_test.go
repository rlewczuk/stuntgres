package reference_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterOptions(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		for _, options := range []string{"WITH IS_TEMPLATE = true ALLOW_CONNECTIONS = on CONNECTION LIMIT = 2", "IS_TEMPLATE 1 ALLOW_CONNECTIONS 'TRUE' CONNECTION LIMIT 2", "WITH IS_TEMPLATE 'On' ALLOW_CONNECTIONS 1 CONNECTION LIMIT 2"} {
			c.run(fixture.Prepared(fixture.Exec("ALTER DATABASE da_source "+options, 0)))
			databaseAlterFlags(c, "da_source", true, true, 2)
		}
		c.run(fixture.Exec("ALTER DATABASE da_source WITH IS_TEMPLATE DEFAULT ALLOW_CONNECTIONS DEFAULT CONNECTION LIMIT DEFAULT", 0))
		databaseAlterFlags(c, "da_source", false, true, -1)
		for _, options := range []string{"IS_TEMPLATE false ALLOW_CONNECTIONS off CONNECTION LIMIT 0", "IS_TEMPLATE 0 ALLOW_CONNECTIONS 'false' CONNECTION LIMIT 0"} {
			c.run(fixture.Exec("ALTER DATABASE da_source "+options, 0))
			databaseAlterFlags(c, "da_source", false, false, 0)
		}
		c.run(fixture.Exec("ALTER DATABASE da_source ALLOW_CONNECTIONS true CONNECTION LIMIT -1", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source", 0))
		c.run(fixture.Prepared(fixture.Exec("ALTER DATABASE da_source WITH", 0)))
		db, raw, rec := c.pool("da_source", "")
		defer databaseAlterClose(c, db, raw)
		databaseAlterRun(c, db, rec, fixture.Select("SELECT datistemplate,datallowconn,datconnlimit FROM pg_database WHERE datname='da_source'", []string{"datistemplate", "datallowconn", "datconnlimit"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{false, true, int64(-1)}}))
		for _, value := range []string{"yes", "no", "'1'", "2"} {
			for _, flag := range []string{"IS_TEMPLATE", "ALLOW_CONNECTIONS"} {
				message := "is_template requires a Boolean value"
				if flag == "ALLOW_CONNECTIONS" {
					message = "allow_connections requires a Boolean value"
				}
				c.error("ALTER DATABASE da_source "+flag+" "+value, "42601", message)
				databaseAlterFlags(c, "da_source", false, true, -1)
			}
		}
		for _, value := range []string{"'1'", "1.0", "2147483648", "-2147483649"} {
			c.error("ALTER DATABASE da_source CONNECTION LIMIT "+value, "42601", "connection_limit requires an integer value")
		}
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 2147483647", 0))
		databaseAlterFlags(c, "da_source", false, true, 2147483647)
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT DEFAULT", 0))
		c.error("ALTER DATABASE da_source CONNECTION LIMIT -2", "22023", "invalid connection limit: -2")
		c.error("ALTER DATABASE da_source ALLOW_CONNECTIONS yes IS_TEMPLATE no CONNECTION LIMIT -2", "42601", "is_template requires a Boolean value")
		databaseAlterFlags(c, "da_source", false, true, -1)
	})
}

func TestReferenceDatabaseAlterAdmission(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		db, raw, rec := c.pool("da_source", "da_plain")
		defer databaseAlterClose(c, db, raw)
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer conn.Close()
		owner, ownerRaw, ownerRec := c.pool("da_source", "")
		databaseAlterRun(c, owner, ownerRec, fixture.Exec("GRANT CREATE ON SCHEMA public TO da_plain", 0))
		databaseAlterClose(c, owner, ownerRaw)
		databaseAlterRun(c, conn, rec, fixture.Exec("CREATE TABLE da_live(v integer)", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source ALLOW_CONNECTIONS false", 0))
		databaseAlterRun(c, conn, rec, fixture.Select("SELECT 42 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		databaseAlterRun(c, conn, rec, fixture.Exec("INSERT INTO da_live VALUES (42)", 1))
		databaseAlterRun(c, conn, rec, fixture.Select("SELECT v FROM da_live", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		for _, role := range []string{"da_plain", ""} {
			databaseAlterStartup(c, "da_source", role, "55000", "database \"da_source\" is not currently accepting connections", "")
		}
		databaseAlterFlags(c, "da_source", false, false, -1)
		c.run(fixture.Exec("ALTER DATABASE da_source ALLOW_CONNECTIONS true", 0))
		databaseAlterStartup(c, "da_source", "da_plain", "", "", "")
		c.run(fixture.Exec("REVOKE CONNECT ON DATABASE da_source FROM PUBLIC", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 0", 0))
		databaseAlterStartup(c, "da_source", "da_plain", "42501", "permission denied for database \"da_source\"", "User does not have CONNECT privilege.")
		c.run(fixture.Exec("ALTER DATABASE da_source ALLOW_CONNECTIONS false", 0))
		databaseAlterStartup(c, "da_source", "da_plain", "55000", "database \"da_source\" is not currently accepting connections", "")
		c.run(fixture.Exec("ALTER DATABASE da_source ALLOW_CONNECTIONS true CONNECTION LIMIT -1", 0))
		c.run(fixture.Exec("GRANT CONNECT ON DATABASE da_source TO PUBLIC", 0))
		c.run(fixture.Exec("ALTER ROLE da_owner NOCREATEDB", 0))
		c.run(fixture.Exec("SET ROLE da_owner", 0))
		c.run(fixture.Prepared(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE true", 0)))
		c.run(fixture.Exec("RESET ROLE", 0))
		current := c.scalar("SELECT current_database()")
		c.error("ALTER DATABASE "+pq.QuoteIdentifier(current)+" ALLOW_CONNECTIONS false", "22023", "cannot disallow connections for current database")
	})
}

func TestReferenceDatabaseAlterLimits(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		c.create("da_other", "", false)
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 0", 0))
		for _, role := range []string{"da_plain", "da_owner"} {
			databaseAlterStartup(c, "da_source", role, "53300", "too many connections for database \"da_source\"", "")
		}
		databaseAlterStartup(c, "da_source", "", "", "", "")
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 1", 0))
		db, raw, rec := c.pool("da_source", "")
		conn, err := db.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer conn.Close()
		databaseAlterStartup(c, "da_source", "da_plain", "53300", "too many connections for database \"da_source\"", "")
		databaseAlterRun(c, conn, rec, fixture.Exec("SET ROLE da_plain", 0))
		databaseAlterStartup(c, "da_source", "da_owner", "53300", "too many connections for database \"da_source\"", "")
		databaseAlterStartup(c, "da_other", "da_plain", "", "", "")
		if err := conn.Close(); err != nil {
			c.t.Fatal(err)
		}
		databaseAlterClose(c, db, raw)
		databaseAlterStartup(c, "da_source", "da_plain", "", "", "")
		first, firstRaw, _ := c.pool("da_source", "da_plain")
		fixture.State(c.t, first.PingContext(c.p.Ctx), "")
		databaseAlterStartup(c, "da_source", "da_owner", "53300", "too many connections for database \"da_source\"", "")
		databaseAlterClose(c, first, firstRaw)
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT -1", 0))
		c.run(fixture.Exec("ALTER ROLE da_plain CONNECTION LIMIT 1", 0))
		first, firstRaw, _ = c.pool("da_source", "da_plain")
		fixture.State(c.t, first.PingContext(c.p.Ctx), "")
		databaseAlterStartup(c, "da_other", "da_plain", "53300", "too many connections for role \"da_plain\"", "")
		databaseAlterClose(c, first, firstRaw)
		databaseAlterStartup(c, "da_other", "da_plain", "", "", "")
		c.run(fixture.Exec("ALTER ROLE da_plain CONNECTION LIMIT -1", 0))
		canceled, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		fail, failRaw, _ := c.pool("da_source", "da_plain")
		if err := fail.PingContext(canceled); !errors.Is(err, context.Canceled) {
			c.t.Fatalf("pre-canceled startup: %v", err)
		}
		databaseAlterClose(c, fail, failRaw)
		databaseAlterStartup(c, "da_source", "da_plain", "", "", "")
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 1", 0))
		databaseAlterStartup(c, "da_source", "da_plain", "", "", "")
	})
}
